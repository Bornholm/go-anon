package anonymizer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
)

// Strategy définit le mode de remplacement d'une entité.
type Strategy int

const (
	TagReplace Strategy = iota // "John" → "[PERSON_1]"
	Redact                     // "John" → "████"
	Hash                       // "John" → "[PER_a1b2c3]"
	Consistent                 // même entity fuzzy → même placeholder
)

// AnonymizePass est une fonction de post-traitement appliquée après le
// remplacement principal des entités. Elle reçoit le texte original et le
// résultat courant, et retourne le texte mis à jour.
type AnonymizePass func(original string, result *Result) string

// Config configure l'anonymiseur.
type Config struct {
	Strategy        Strategy
	EntityTypes     []ner.EntityType // nil = toutes les entités
	ConsistentMap   bool
	CustomReplacers map[ner.EntityType]ReplacerFunc
	// Passes liste les passes de post-traitement appliquées dans l'ordre après
	// le remplacement des entités. nil déclenche les passes par défaut :
	// ConsistencyPass() puis SurnameCompletionPass().
	// Passer une slice vide désactive tout post-traitement.
	Passes []AnonymizePass
}

// ReplacerFunc permet un remplacement personnalisé.
// entity est l'entité détectée, index est le numéro séquentiel pour ce type.
type ReplacerFunc func(entity ner.Entity, index int) string

// Recognizer définit l'interface pour la reconnaissance d'entités.
type Recognizer interface {
	Recognize(text string) ([]ner.Entity, error)
}

// Anonymizer anonymise les entités nommées dans un texte.
type Anonymizer struct {
	recognizer Recognizer
	config     Config
}

// Result contient le résultat de l'anonymisation.
type Result struct {
	Text                  string            // texte anonymisé
	Entities              []ner.Entity      // entités détectées
	Mapping               map[string]string // "[PERSON_1]" → "Jean Dupont"
	OriginalToPlaceholder map[string]string // "Jean Dupont" → "[PERSON_1]"
}

// New crée un nouvel Anonymizer. Si config.Passes est nil, les passes par
// défaut (ConsistencyPass + SurnameCompletionPass) sont activées.
func New(recognizer Recognizer, config Config) *Anonymizer {
	if config.Passes == nil {
		config.Passes = []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}
	}
	return &Anonymizer{
		recognizer: recognizer,
		config:     config,
	}
}

// Anonymize anonymise les entités dans le texte.
// Retourne le texte anonymisé avec les mappings pour dé-anonymisation.
func (a *Anonymizer) Anonymize(text string) (*Result, error) {
	if text == "" {
		return &Result{
			Text:                  text,
			Entities:              nil,
			Mapping:               make(map[string]string),
			OriginalToPlaceholder: make(map[string]string),
		}, nil
	}

	entities, err := a.recognizer.Recognize(text)
	if err != nil {
		return nil, fmt.Errorf("recognition failed: %w", err)
	}

	if a.config.EntityTypes != nil {
		entities = filterByType(entities, a.config.EntityTypes)
	}

	sort.Slice(entities, func(i, j int) bool {
		if typePriority(entities[i].Type) != typePriority(entities[j].Type) {
			return typePriority(entities[i].Type) > typePriority(entities[j].Type)
		}
		return entities[i].Start > entities[j].Start
	})

	result := &Result{
		Text:                  text,
		Entities:              entities,
		Mapping:               make(map[string]string),
		OriginalToPlaceholder: make(map[string]string),
	}

	counters := make(map[ner.EntityType]int)
	consistentCache := make(map[string]string)

	shift := 0
	for _, ent := range entities {
		var replacement string

		if a.config.ConsistentMap {
			cacheKey := normalizeForFuzzy(ent.Text)
			if cached, ok := consistentCache[cacheKey]; ok {
				replacement = cached
			}
		}

		if replacement == "" {
			counters[ent.Type]++
			replacement = a.replace(ent, counters[ent.Type])
			if a.config.ConsistentMap {
				consistentCache[normalizeForFuzzy(ent.Text)] = replacement
			}
		}

		result.Mapping[replacement] = ent.Text
		result.OriginalToPlaceholder[ent.Text] = replacement

		adjustedStart := ent.Start + shift
		adjustedEnd := ent.End + shift

		if adjustedStart >= 0 && adjustedEnd <= len(result.Text) && result.Text[adjustedStart:adjustedEnd] == ent.Text {
			result.Text = result.Text[:adjustedStart] + replacement + result.Text[adjustedEnd:]
			shift += len(replacement) - (ent.End - ent.Start)
		} else {
			shift += len(replacement) - (ent.End - ent.Start)
		}
	}

	for _, pass := range a.config.Passes {
		result.Text = pass(text, result)
	}

	return result, nil
}

func (a *Anonymizer) replace(ent ner.Entity, index int) string {
	if fn, ok := a.config.CustomReplacers[ent.Type]; ok {
		return fn(ent, index)
	}

	switch a.config.Strategy {
	case TagReplace:
		return fmt.Sprintf("[%s_%d]", typeToLabel(ent.Type), index)
	case Redact:
		return strings.Repeat("█", len(ent.Text))
	case Hash:
		h := sha256.Sum256([]byte(ent.Text))
		return fmt.Sprintf("[%s_%s]", ent.Type, hex.EncodeToString(h[:])[:6])
	case Consistent:
		return fmt.Sprintf("[%s_%d]", typeToLabel(ent.Type), index)
	default:
		return fmt.Sprintf("[%s_%d]", typeToLabel(ent.Type), index)
	}
}

// Deanonymize restaure le texte original à partir du texte anonymisé.
// mapping doit contenir les correspondances placeholder → texte original.
func (a *Anonymizer) Deanonymize(text string, mapping map[string]string) (string, error) {
	if len(mapping) == 0 {
		return text, nil
	}

	result := text
	for placeholder, original := range mapping {
		result = strings.ReplaceAll(result, placeholder, original)
	}
	return result, nil
}

// ConsistencyPass retourne une AnonymizePass qui remplace dans le texte anonymisé
// les occurrences résiduelles de texte d'entité connue par leur placeholder.
// Utile quand le NER n'a pas détecté toutes les occurrences d'une même entité.
func ConsistencyPass() AnonymizePass {
	return func(original string, result *Result) string {
		if len(result.Entities) == 0 {
			return result.Text
		}

		// Construire le canonical depuis les entités triées par priorité de type.
		canonicalMap := make(map[string]string)
		sortedByType := make([]ner.Entity, len(result.Entities))
		copy(sortedByType, result.Entities)
		sort.Slice(sortedByType, func(i, j int) bool {
			return typePriority(sortedByType[i].Type) > typePriority(sortedByType[j].Type)
		})
		for _, ent := range sortedByType {
			cacheKey := normalizeForFuzzy(ent.Text)
			if _, exists := canonicalMap[cacheKey]; !exists {
				canonicalMap[cacheKey] = result.OriginalToPlaceholder[ent.Text]
			}
		}

		// Pour les entités PER multi-mots, ajouter chaque token majuscule
		// séparément afin de couvrir les occurrences du prénom ou nom seul.
		// Ne pas ajouter les tokens qui apparaissent dans plusieurs entités PER
		// (évite les conflits sur les noms partagés comme "Fornerot").
		perTokenCount := make(map[string]int)
		for _, ent := range result.Entities {
			if ent.Type != ner.TypePER {
				continue
			}
			for _, tok := range strings.Fields(ent.Text) {
				runes := []rune(tok)
				if len(runes) > 0 && unicode.IsUpper(runes[0]) {
					perTokenCount[normalizeForFuzzy(tok)]++
				}
			}
		}
		for _, ent := range sortedByType {
			if ent.Type != ner.TypePER {
				continue
			}
			placeholder := result.OriginalToPlaceholder[ent.Text]
			if placeholder == "" {
				continue
			}
			tokens := strings.Fields(ent.Text)
			if len(tokens) <= 1 {
				continue
			}
			for _, tok := range tokens {
				runes := []rune(tok)
				if len(runes) == 0 || !unicode.IsUpper(runes[0]) {
					continue
				}
				norm := normalizeForFuzzy(tok)
				if perTokenCount[norm] > 1 {
					continue
				}
				if _, exists := canonicalMap[norm]; !exists {
					canonicalMap[norm] = placeholder
				}
			}
		}

		// Convertir en slice triée : matchs longs d'abord (greedy), puis
		// lexicographique pour garantir un ordre déterministe.
		type canonicalEntry struct {
			substr      string
			placeholder string
		}
		canonical := make([]canonicalEntry, 0, len(canonicalMap))
		for substr, placeholder := range canonicalMap {
			canonical = append(canonical, canonicalEntry{substr, placeholder})
		}
		sort.Slice(canonical, func(i, j int) bool {
			if len(canonical[i].substr) != len(canonical[j].substr) {
				return len(canonical[i].substr) > len(canonical[j].substr)
			}
			return canonical[i].substr < canonical[j].substr
		})

		text := result.Text

		covered := make([]bool, len(text))
		for _, ent := range result.Entities {
			placeholder := result.OriginalToPlaceholder[ent.Text]
			if placeholder == "" {
				continue
			}
			pos := 0
			for {
				idx := strings.Index(text[pos:], placeholder)
				if idx < 0 {
					break
				}
				abs := pos + idx
				for i := abs; i < abs+len(placeholder) && i < len(covered); i++ {
					covered[i] = true
				}
				pos = abs + 1
			}
		}

		for pos := len(text) - 1; pos >= 0; pos-- {
			if covered[pos] {
				continue
			}

			for _, entry := range canonical {
				end := pos + len(entry.substr)
				if end > len(text) {
					continue
				}
				candidate := text[pos:end]
				if normalizeForFuzzy(candidate) != entry.substr {
					continue
				}
				if !isWordBoundary(text, pos, end) {
					continue
				}

				text = text[:pos] + entry.placeholder + text[end:]

				newCovered := make([]bool, pos)
				copy(newCovered, covered[:pos])
				covered = newCovered
				break
			}
		}

		return text
	}
}

// SurnameCompletionPass retourne une AnonymizePass qui, pour chaque placeholder
// PER dans le texte anonymisé, vérifie si le token suivant dans le texte original
// est un nom de famille non encore anonymisé, et le remplace par le même placeholder.
func SurnameCompletionPass() AnonymizePass {
	return func(original string, result *Result) string {
		if len(result.Entities) == 0 {
			return result.Text
		}

		text := result.Text

		covered := make([]bool, len(text))
		for _, e := range result.Entities {
			for i := e.Start; i < e.End && i < len(covered); i++ {
				covered[i] = true
			}
		}

		placeholderToEntity := make(map[string]ner.Entity)
		for _, e := range result.Entities {
			placeholder := result.OriginalToPlaceholder[e.Text]
			if placeholder != "" {
				placeholderToEntity[placeholder] = e
			}
		}

		for pos := len(text) - 1; pos >= 0; pos-- {
			if pos+1 < len(text) && text[pos] == ']' {
				bracketStart := pos
				for bracketStart > 0 && text[bracketStart] != '[' {
					bracketStart--
				}
				if bracketStart > 0 && text[bracketStart] == '[' {
					placeholder := text[bracketStart : pos+1]
					ent, ok := placeholderToEntity[placeholder]
					if !ok || ent.Type != ner.TypePER {
						continue
					}

					origEnd := ent.End
					for origEnd < len(original) && original[origEnd] == ' ' {
						origEnd++
					}

					if origEnd >= len(original) {
						continue
					}

					candidateEnd := origEnd
					for candidateEnd < len(original) {
						r := rune(original[candidateEnd])
						if unicode.IsLetter(r) || r == '\'' || r == '-' {
							candidateEnd++
						} else {
							break
						}
					}

					candidate := original[origEnd:candidateEnd]
					if len(candidate) < 2 {
						continue
					}

					candidateStart := pos + 1
					if candidateStart >= len(text) {
						continue
					}

					alreadyCovered := false
					for i := candidateStart; i < candidateStart+len(candidate) && i < len(covered); i++ {
						if covered[i] {
							alreadyCovered = true
							break
						}
					}
					if alreadyCovered {
						continue
					}

					if candidateStart+len(candidate) <= len(text) {
						curr := text[candidateStart : candidateStart+len(candidate)]
						if curr == candidate {
							text = text[:candidateStart] + placeholder + text[candidateStart+len(candidate):]

							newCovered := make([]bool, candidateStart)
							copy(newCovered, covered[:candidateStart])
							covered = newCovered
						}
					}
				}
			}
		}

		return text
	}
}

// isWordBoundary retourne true si le span [start, end) dans text est délimité
// par des caractères non-lettre/non-chiffre (ou par les bords du texte).
func isWordBoundary(text string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func typeToLabel(t ner.EntityType) string {
	switch t {
	case ner.TypePER:
		return "PERSON"
	case ner.TypeLOC:
		return "LOCATION"
	case ner.TypeORG:
		return "ORGANIZATION"
	case ner.TypeMISC:
		return "MISC"
	default:
		return "ENTITY"
	}
}

func typePriority(t ner.EntityType) int {
	switch t {
	case ner.TypePER:
		return 4
	case ner.TypeLOC:
		return 3
	case ner.TypeORG:
		return 2
	case ner.TypeMISC:
		return 1
	default:
		return 0
	}
}

func normalizeForFuzzy(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func filterByType(entities []ner.Entity, types []ner.EntityType) []ner.Entity {
	typeSet := make(map[ner.EntityType]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	result := make([]ner.Entity, 0)
	for _, e := range entities {
		if typeSet[e.Type] {
			result = append(result, e)
		}
	}
	return result
}
