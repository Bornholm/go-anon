package ner

import (
	"regexp"
	"sort"
)

// RegexPattern associe une expression régulière compilée à un type d'entité.
// Re doit être pré-compilé par l'appelant (regexp.MustCompile ou regexp.Compile).
// Confidence à 0 est interprété comme 1.0.
// Submatch indique quel groupe capturant utiliser comme span de l'entité :
// 0 (défaut) = match complet, 1 = premier groupe capturant, etc.
type RegexPattern struct {
	Re         *regexp.Regexp
	EntityType EntityType
	Confidence float64
	Submatch   int
}

// RegexEntityFilter retourne un EntityFilter qui injecte des entités détectées
// par expressions régulières. Les spans déjà couverts par des entités existantes
// ne sont jamais écrasés (NER prime sur regex, premier pattern regex gagne).
// Le texte original est fourni par le filtre.
func RegexEntityFilter(patterns []RegexPattern) EntityFilter {
	return func(text string, entities []Entity) []Entity {
		if len(patterns) == 0 || text == "" {
			return entities
		}

		covered := make([]bool, len(text))
		for _, e := range entities {
			for i := e.Start; i < e.End && i < len(covered); i++ {
				covered[i] = true
			}
		}

		var newEntities []Entity
		for _, p := range patterns {
			conf := p.Confidence
			if conf == 0 {
				conf = 1.0
			}
			for _, m := range p.Re.FindAllStringSubmatchIndex(text, -1) {
				var start, end int
				if p.Submatch > 0 && p.Submatch*2+1 < len(m) && m[p.Submatch*2] >= 0 {
					start, end = m[p.Submatch*2], m[p.Submatch*2+1]
				} else {
					start, end = m[0], m[1]
				}
				overlaps := false
				for i := start; i < end && i < len(covered); i++ {
					if covered[i] {
						overlaps = true
						break
					}
				}
				if overlaps {
					continue
				}
				for i := start; i < end && i < len(covered); i++ {
					covered[i] = true
				}
				newEntities = append(newEntities, Entity{
					Text:       text[start:end],
					Type:       p.EntityType,
					Start:      start,
					End:        end,
					Confidence: conf,
				})
			}
		}

		if len(newEntities) == 0 {
			return entities
		}
		result := append(entities, newEntities...)
		sort.Slice(result, func(i, j int) bool {
			return result[i].Start < result[j].Start
		})
		return result
	}
}

// WithRegexPatterns ajoute une passe de détection par expressions régulières
// après les filtres NER existants. Les patterns sont appliqués dans l'ordre fourni.
func WithRegexPatterns(patterns ...RegexPattern) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.postFilters = append(rec.postFilters, RegexEntityFilter(patterns))
		return nil
	}
}

// WithBuiltinRegexPatterns active les patterns intégrés pour les types courants :
// EMAIL, IPV4, IPV6, IBAN, SIRET, SIREN, PHONE.
func WithBuiltinRegexPatterns() RecognizerOption {
	return WithRegexPatterns(BuiltinRegexPatterns...)
}
