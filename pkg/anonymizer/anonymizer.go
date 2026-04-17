package anonymizer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

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

// Config configure l'anonymiseur.
type Config struct {
	Strategy        Strategy
	EntityTypes     []ner.EntityType // nil = toutes les entités
	ConsistentMap   bool             // même texte → même placeholder
	ConsistencyPass bool             // passe post-traitement pour cohérence des occurrences
	CustomReplacers map[ner.EntityType]ReplacerFunc
}

// ReplacerFunc permet un remplacement personnalisé.
// entity est l'entité détectée, index est le numéro séquentiel pour ce type.
type ReplacerFunc func(entity ner.Entity, index int) string

// Recognizer définit l'interface pour la reconnaissance d'entités.
// Permite l'injection de mock ou implémentation réelle.
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

// New crée un nouvel Anonymizer.
func New(recognizer Recognizer, config Config) *Anonymizer {
	if !config.ConsistencyPass {
		config.ConsistencyPass = true
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

	sort.Slice(entities, func(i, j int) bool {
		if typePriority(entities[i].Type) != typePriority(entities[j].Type) {
			return typePriority(entities[i].Type) > typePriority(entities[j].Type)
		}
		return entities[i].Start > entities[j].Start
	})

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

	if a.config.ConsistencyPass {
		result.Text = a.EnsureConsistency(text, result)
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

func (a *Anonymizer) EnsureConsistency(original string, result *Result) string {
	if len(result.Entities) == 0 {
		return result.Text
	}

	canonical := make(map[string]string)
	sortedByType := make([]ner.Entity, len(result.Entities))
	copy(sortedByType, result.Entities)
	sort.Slice(sortedByType, func(i, j int) bool {
		return typePriority(sortedByType[i].Type) > typePriority(sortedByType[j].Type)
	})
	for _, ent := range sortedByType {
		cacheKey := normalizeForFuzzy(ent.Text)
		if _, exists := canonical[cacheKey]; !exists {
			canonical[cacheKey] = result.OriginalToPlaceholder[ent.Text]
		}
	}

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

		for substr, placeholder := range canonical {
			if pos+len(substr) > len(text) {
				continue
			}
			candidate := text[pos : pos+len(substr)]
			if normalizeForFuzzy(candidate) != substr {
				continue
			}

			text = text[:pos] + placeholder + text[pos+len(substr):]

			newCovered := make([]bool, pos)
			copy(newCovered, covered[:pos])
			covered = newCovered
			break
		}
	}

	return text
}
