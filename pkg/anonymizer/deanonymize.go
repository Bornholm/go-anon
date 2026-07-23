package anonymizer

import (
	"fmt"
	"sort"
	"strings"
)

// Deanonymize restaure le texte original à partir du texte anonymisé et du
// mapping placeholder → texte original.
//
// Le remplacement est fait en un seul scan via strings.Replacer, qui choisit le
// match le plus à gauche et ne re-scanne jamais ses propres sorties. Cela règle
// deux défauts de l'implémentation naïve par ReplaceAll successifs : l'ordre
// d'itération d'une map Go est aléatoire (sortie non reproductible), et
// `PER_1` matche comme préfixe de `PER_10` selon cet ordre. Le tri par longueur
// décroissante reste une ceinture-bretelles pour le format legacy.
//
// Après remplacement, la présence d'un placeholder résiduel réversible déclenche
// ErrIncompleteMapping : mapping tronqué, ou mapping d'un autre document. Les
// marqueurs de caviardage des secrets (_REDACTED) sont exclus de ce contrôle,
// leur irréversibilité étant voulue.
func Deanonymize(text string, mapping map[string]string) (string, error) {
	result := text

	if len(mapping) > 0 {
		keys := make([]string, 0, len(mapping))
		for k := range mapping {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if len(keys[i]) != len(keys[j]) {
				return len(keys[i]) > len(keys[j])
			}
			return keys[i] < keys[j]
		})

		pairs := make([]string, 0, len(keys)*2)
		for _, k := range keys {
			pairs = append(pairs, k, mapping[k])
		}
		result = strings.NewReplacer(pairs...).Replace(result)
	}

	if idx := findResidualPlaceholder(result); idx >= 0 {
		return "", fmt.Errorf("%w (offset %d)", ErrIncompleteMapping, idx)
	}
	return result, nil
}

// Deanonymize restaure le texte original à partir du texte anonymisé.
// Équivalent de la fonction Deanonymize du package ; l'anonymiseur ne conserve
// aucun état de ré-identification.
func (a *Anonymizer) Deanonymize(text string, mapping map[string]string) (string, error) {
	return Deanonymize(text, mapping)
}
