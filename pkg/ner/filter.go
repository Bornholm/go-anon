package ner

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/features"
)

// EntityFilter est une fonction de post-traitement appliquée sur la liste
// d'entités détectées. Elle reçoit le texte original de la reconnaissance et
// peut supprimer, modifier ou réordonner les entités.
// Les filtres sont chaînés dans l'ordre fourni à WithPostFilters.
//
// Le texte est passé en argument (et non capturé depuis le Recognizer) : c'est
// ce qui rend le Recognizer sans état après construction, donc partageable entre
// goroutines sans course ni contamination inter-requêtes (cf. Recognize).
type EntityFilter func(text string, entities []Entity) []Entity

// WithPostFilters enregistre des filtres appliqués après la reconnaissance NER,
// dans l'ordre fourni. Les filtres sont ajoutés après ceux déjà configurés
// (ex: WithMergePass, WithNameCompletionPass).
func WithPostFilters(filters ...EntityFilter) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.postFilters = append(rec.postFilters, filters...)
		return nil
	}
}

// MinConfidenceFilter supprime les entités dont le score de confiance est
// strictement inférieur à threshold. Utile pour éliminer les prédictions
// hésitantes (ex. titres de poste mal étiquetés PER).
func MinConfidenceFilter(threshold float64) EntityFilter {
	return func(_ string, entities []Entity) []Entity {
		out := entities[:0]
		for _, e := range entities {
			if e.Confidence >= threshold {
				out = append(out, e)
			}
		}
		return out
	}
}

// MaxTokensFilter supprime les entités dont le nombre de tokens (mots séparés
// par des espaces) dépasse max. Réduit les spans anormalement longs résultant
// d'erreurs de segmentation ou d'un contexte ambiguë.
func MaxTokensFilter(max int) EntityFilter {
	return func(_ string, entities []Entity) []Entity {
		out := entities[:0]
		for _, e := range entities {
			if countTokens(e.Text) <= max {
				out = append(out, e)
			}
		}
		return out
	}
}

// BlocklistFilter supprime les entités du type entityType dont tous les tokens
// figurent dans la liste de mots interdits (comparaison insensible à la casse).
// Permet d'éviter que des titres de poste ou termes génériques soient confondus
// avec des entités nommées (ex. "Ingénieur Logiciels Libres" → PER).
func BlocklistFilter(entityType EntityType, words ...string) EntityFilter {
	blocklist := make(map[string]bool, len(words))
	for _, w := range words {
		blocklist[strings.ToLower(w)] = true
	}
	return func(_ string, entities []Entity) []Entity {
		out := entities[:0]
		for _, e := range entities {
			if e.Type == entityType && allTokensBlocked(e.Text, blocklist) {
				continue
			}
			out = append(out, e)
		}
		return out
	}
}

// countTokens compte le nombre de mots dans s (séparés par des espaces).
func countTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return strings.Count(s, " ") + 1
}

// allTokensBlocked retourne true si chaque token de text figure dans blocklist.
func allTokensBlocked(text string, blocklist map[string]bool) bool {
	for _, tok := range strings.Fields(text) {
		if !blocklist[strings.ToLower(tok)] {
			return false
		}
	}
	return true
}

// FirstNameReclassifyFilter reclasse en PER les entités LOC d'un seul token
// dont le texte figure dans le gazetteer de prénoms. Utile quand le modèle
// confond un prénom (ex. "Alice", "Marie") avec un lieu.
// Si firstNames est nil, le filtre est inopérant.
func FirstNameReclassifyFilter(firstNames *features.Gazetteer) EntityFilter {
	return func(_ string, entities []Entity) []Entity {
		if firstNames == nil {
			return entities
		}
		result := make([]Entity, len(entities))
		for i, e := range entities {
			if e.Type == TypeLOC && countTokens(e.Text) == 1 && firstNames.Contains(e.Text) {
				e.Type = TypePER
			}
			result[i] = e
		}
		return result
	}
}

// FirstNameDetectionFilter détecte les tokens majuscules correspondant à des
// prénoms du gazetteer qui ne sont pas déjà couverts par des entités existantes,
// et les ajoute comme entités PER. Le texte original est fourni par le filtre.
// stopWords est une map optionnelle de mots à exclure (en minuscules) ; si nil,
// seuls les tokens de moins de 3 caractères et ceux suivis d'une lettre minuscule
// sont filtrés.
func FirstNameDetectionFilter(firstNames *features.Gazetteer, stopWords map[string]bool) EntityFilter {
	return func(text string, entities []Entity) []Entity {
		if firstNames == nil || text == "" {
			return entities
		}

		covered := make([]bool, len(text))
		for _, e := range entities {
			for i := e.Start; i < e.End && i < len(covered); i++ {
				covered[i] = true
			}
		}

		var newEntities []Entity
		pos := 0
		for pos < len(text) {
			if covered[pos] {
				pos++
				continue
			}

			r, size := utf8.DecodeRuneInString(text[pos:])
			if !unicode.IsUpper(r) {
				pos += size
				continue
			}

			end := scanNameToken(text, pos)

			token := text[pos:end]
			if utf8.RuneCountInString(token) < 3 {
				pos = end
				continue
			}
			if stopWords != nil && stopWords[strings.ToLower(token)] {
				pos = end
				continue
			}
			if firstNames.Contains(token) {
				newEntities = append(newEntities, Entity{
					Text:       token,
					Type:       TypePER,
					Start:      pos,
					End:        end,
					Confidence: 1.0,
				})
				for i := pos; i < end; i++ {
					if i < len(covered) {
						covered[i] = true
					}
				}
			}

			pos = end
		}

		if len(newEntities) == 0 {
			return entities
		}

		result := append(entities, newEntities...)
		sort.Slice(result, func(i, j int) bool {
			if result[i].Start != result[j].Start {
				return result[i].Start < result[j].Start
			}
			return result[i].End > result[j].End
		})
		return result
	}
}

var defaultStopWords = map[string]bool{
	"le": true, "la": true, "les": true, "de": true, "du": true,
	"des": true, "et": true, "ou": true, "pour": true, "avec": true,
	"sans": true, "sur": true, "sous": true, "chez": true, "un": true,
	"une": true, "au": true, "aux": true, "par": true, "en": true,
	"dans": true, "que": true, "qui": true, "dont": true, "lors": true,
}

// isNameRune signale les caractères autorisés à l'intérieur d'un nom propre :
// lettres, apostrophes (ASCII et typographique) et traits d'union
// (ASCII et typographiques) — même ensemble que le tokenizer.
func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || r == '\'' || r == '’' ||
		r == '-' || r == '‐' || r == '‑'
}

// scanNameToken retourne l'offset de fin (exclusif) du token de type nom
// commençant à start dans text, en décodant rune par rune (UTF-8 safe).
func scanNameToken(text string, start int) int {
	end := start
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !isNameRune(r) {
			break
		}
		end += size
	}
	return end
}

func isUppercaseToken(s string) bool {
	if len(s) == 0 {
		return false
	}
	runes := []rune(s)
	return unicode.IsUpper(runes[0])
}

func isStopWordOrKnownSurname(s string, known map[string]bool) bool {
	sLower := strings.ToLower(s)
	if defaultStopWords[sLower] {
		return true
	}
	if known[sLower] {
		return true
	}
	return false
}

// MergePass fusionne les entités fragmentées par le NER.
// Le texte original est fourni par le filtre.
// Il traite deux cas :
//   - PER + LOC adjacents (séparés par un espace ou contigus) : le LOC est
//     considéré comme un faux positif (nom de famille pris pour un lieu) et
//     absorbé dans la PER.
//   - PER + PER adjacents : fusion en une seule entité PER.
func MergePass() EntityFilter {
	return func(text string, entities []Entity) []Entity {
		if len(entities) == 0 {
			return entities
		}

		for {
			sort.Slice(entities, func(i, j int) bool {
				return entities[i].Start < entities[j].Start
			})

			merged := make([]Entity, 0, len(entities))
			i := 0
			changed := false

			for i < len(entities) {
				e := entities[i]
				if i+1 < len(entities) {
					next := entities[i+1]

					if e.End == next.Start && (e.Type == next.Type || next.Type == TypeLOC) {
						mergedType := e.Type
						// On conserve la confiance de l'entité dominante (PER de tête) :
						// quand on absorbe un second token incertain, l'ancre est la première entité.
						mergedConf := e.Confidence
						var mergedText string
						if text != "" && e.End >= 0 && next.End <= len(text) && next.End >= e.Start {
							mergedText = text[e.Start:next.End]
						} else {
							mergedText = e.Text + " " + next.Text
						}
						merged = append(merged, Entity{
							Text:       mergedText,
							Type:       mergedType,
							Start:      e.Start,
							End:        next.End,
							Confidence: mergedConf,
						})
						i += 2
						changed = true
						continue
					}
				}
				merged = append(merged, e)
				i++
			}

			entities = merged
			if !changed {
				break
			}
		}

		return entities
	}
}

// NameCompletionPass complète les entités PER d'un seul token (prénom seul) en
// cherchant un nom de famille adjacent dans le texte original.
// firstNames est un gazetteer de prénoms connus ; si nil, la passe est inopérante.
// Le texte original est fourni par le filtre.
func NameCompletionPass(firstNames *features.Gazetteer) EntityFilter {
	return func(text string, entities []Entity) []Entity {
		if len(entities) == 0 || firstNames == nil || text == "" {
			return entities
		}

		known := make(map[string]bool)
		for _, e := range entities {
			if e.Type == TypeLOC || e.Type == TypeORG {
				for _, tok := range strings.Fields(e.Text) {
					known[strings.ToLower(tok)] = true
				}
			}
		}

		result := make([]Entity, 0, len(entities))

		for _, e := range entities {
			if e.Type != TypePER {
				result = append(result, e)
				continue
			}

			if countTokens(e.Text) != 1 {
				result = append(result, e)
				continue
			}

			if !firstNames.Contains(e.Text) {
				result = append(result, e)
				continue
			}

			if e.End >= len(text) {
				result = append(result, e)
				continue
			}

			candidateStart := e.End
			if e.End < len(text) && (text[e.End] == ' ' || text[e.End] == '\n' || text[e.End] == '\r') {
				candidateStart = e.End + 1
			}

			if candidateStart >= len(text) || text[candidateStart] == ' ' {
				result = append(result, e)
				continue
			}

			if candidateStart < len(text) && (text[candidateStart] == '\n' || text[candidateStart] == '\r') {
				result = append(result, e)
				continue
			}

			candidateEnd := scanNameToken(text, candidateStart)

			candidate := text[candidateStart:candidateEnd]
			if candidateEnd <= candidateStart || utf8.RuneCountInString(candidate) < 2 {
				result = append(result, e)
				continue
			}

			if !isUppercaseToken(candidate) {
				result = append(result, e)
				continue
			}

			if isStopWordOrKnownSurname(candidate, known) {
				result = append(result, e)
				continue
			}

			alreadyCovered := false
			for _, other := range entities {
				if other.Start <= candidateEnd && other.End >= candidateStart && other.Start != e.Start {
					alreadyCovered = true
					break
				}
			}
			if alreadyCovered {
				result = append(result, e)
				continue
			}

			hasSpaceBefore := e.End < len(text) && e.End >= 0 && text[e.End] == ' '
			newEnd := candidateEnd

			var newText string
			if hasSpaceBefore {
				newText = e.Text + " " + candidate
			} else {
				newText = e.Text + candidate
			}

			result = append(result, Entity{
				Text:       newText,
				Type:       TypePER,
				Start:      e.Start,
				End:        newEnd,
				Confidence: e.Confidence,
			})
		}

		return result
	}
}
