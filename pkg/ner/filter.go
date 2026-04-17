package ner

import (
	"strings"
)

// EntityFilter est une fonction de post-traitement appliquée sur la liste
// d'entités détectées. Elle peut supprimer, modifier ou réordonner les entités.
// Les filtres sont chaînés dans l'ordre fourni à WithPostFilters.
type EntityFilter func([]Entity) []Entity

// WithPostFilters enregistre des filtres appliqués après la reconnaissance NER,
// dans l'ordre fourni. Chaque appel remplace les filtres précédents.
func WithPostFilters(filters ...EntityFilter) RecognizerOption {
	return func(rec *Recognizer) error {
		rec.postFilters = filters
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
