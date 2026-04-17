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

		result.Text = result.Text[:ent.Start] + replacement + result.Text[ent.End:]
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
