package ner

import (
	"sort"
	"strings"
	"unicode"
)

// EntityFilter est une fonction de post-traitement appliquée sur la liste
// d'entités détectées. Elle peut supprimer, modifier ou réordonner les entités.
// Les filtres sont chaînés dans l'ordre fourni à WithPostFilters.
type EntityFilter func([]Entity) []Entity

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
	return func(entities []Entity) []Entity {
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
	return func(entities []Entity) []Entity {
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
	return func(entities []Entity) []Entity {
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

var commonFirstNames = map[string]bool{
	"arnaud": true, "benjamin": true, "matthieu": true, "philippe": true,
	"vincent": true, "william": true, "laetitia": true, "charles": true,
	"jean": true, "marie": true, "pierre": true, "paul": true,
	"luc": true, "anne": true, "nicolas": true, "alexandre": true,
	"thomas": true, "antoine": true, "sébastien": true, "frédéric": true,
	"olivier": true, "julien": true, "maxime": true, "lucas": true,
	"mathieu": true, "nathan": true, "hugo": true, "gabriel": true,
	"raphaël": true, "claire": true, "sophie": true, "claude": true,
	"dominique": true, "gerard": true, "bernard": true, "michel": true,
	"pierre-yves": true, "jean-marie": true,
}

var defaultStopWords = map[string]bool{
	"le": true, "la": true, "les": true, "de": true, "du": true,
	"des": true, "et": true, "ou": true, "pour": true, "avec": true,
	"sans": true, "sur": true, "sous": true, "chez": true, "un": true,
	"une": true, "au": true, "aux": true, "par": true, "en": true,
	"dans": true, "que": true, "qui": true, "dont": true, "lors": true,
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
// getText est une fonction retournant le texte original (fournie par le Recognizer).
// Il traite deux cas :
//   - PER + LOC adjacents (séparés par un espace ou contigus) : le LOC est
//     considéré comme un faux positif (nom de famille pris pour un lieu) et
//     absorbé dans la PER.
//   - PER + PER adjacents : fusion en une seule entité PER.
func MergePass(getText func() string) EntityFilter {
	return func(entities []Entity) []Entity {
		if len(entities) == 0 {
			return entities
		}

		text := getText()

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
					adjacent := e.End == next.Start ||
						(e.End < next.Start && isWhitespaceBetween(text, e.End, next.Start))

					if adjacent && (e.Type == next.Type || next.Type == TypeLOC) {
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

// isWhitespaceBetween retourne true si le gap entre end et start ne contient
// que des espaces horizontaux (pas de saut de ligne). Un saut de ligne signale
// une frontière de phrase/paragraphe et ne doit pas déclencher une fusion.
func isWhitespaceBetween(text string, end, start int) bool {
	if start <= end {
		return false
	}
	for i := end; i < start && i < len(text); i++ {
		r := rune(text[i])
		if r == '\n' || r == '\r' {
			return false
		}
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// NameCompletionPass complète les entités PER partielles (prénom seul) en détectant
// les tokens adjacents qui ressemblent à des noms de famille.
// getText est une fonction retournant le texte original (fournie par le Recognizer).
func NameCompletionPass(getText func() string) EntityFilter {
	return func(entities []Entity) []Entity {
		if len(entities) == 0 {
			return entities
		}

		text := getText()
		if text == "" {
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

			firstName := strings.ToLower(e.Text)
			if !commonFirstNames[firstName] {
				result = append(result, e)
				continue
			}

			if e.End >= len(text) {
				result = append(result, e)
				continue
			}

			candidateStart := e.End
			if text[e.End] == ' ' {
				candidateStart = e.End + 1
			}

			if candidateStart >= len(text) || text[candidateStart] == ' ' {
				result = append(result, e)
				continue
			}

			candidateEnd := candidateStart
			for candidateEnd < len(text) {
				r := rune(text[candidateEnd])
				if unicode.IsLetter(r) || r == '\'' || r == '-' {
					candidateEnd++
				} else {
					break
				}
			}

			candidate := text[candidateStart:candidateEnd]
			if candidateEnd <= candidateStart || len(candidate) < 2 {
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
