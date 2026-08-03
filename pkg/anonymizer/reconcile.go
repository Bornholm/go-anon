package anonymizer

import (
	"sort"

	"github.com/bornholm/go-anon/pkg/ner"
)

// reconcileEntities unione les entités du recognizer et celles fournies par
// l'appelant (WithAdditionalEntities), puis résout les chevauchements.
//
// Sans extra, la fonction est un no-op strict : la liste du recognizer est
// rendue telle quelle, référence comprise. Le chemin nominal est donc
// rigoureusement inchangé.
//
// Règle de résolution : **le span le plus large gagne**. C'est la traduction de
// la posture de conformité — entre deux lectures concurrentes d'une même zone,
// sur-anonymiser coûte un mot caviardé à tort, sous-anonymiser coûte une
// violation. À largeur égale, l'entité du recognizer l'emporte (elle porte le
// score de confiance du modèle), puis l'ordre des offsets tranche pour rendre
// le résultat déterministe.
//
// La résolution n'est pas cosmétique : la passe de remplacement d'Anonymize
// exige des spans disjoints. Elle réécrit de droite à gauche et vérifie
// `text[start:end] == ent.Text` avant chaque substitution ; deux entités qui se
// chevauchent feraient échouer silencieusement la seconde, laissant une portion
// de la zone en clair.
func reconcileEntities(base, extra []ner.Entity, text string) []ner.Entity {
	if len(extra) == 0 {
		return base
	}

	type candidate struct {
		ent       ner.Entity
		fromModel bool
		order     int
	}

	candidates := make([]candidate, 0, len(base)+len(extra))
	for i, ent := range base {
		candidates = append(candidates, candidate{ent: ent, fromModel: true, order: i})
	}
	for i, ent := range extra {
		// Les entités fournies viennent d'une autre recomposition : leur champ
		// Text décrit une autre chaîne. Seuls les offsets font foi, et la forme
		// de surface est reprise du texte courant — sans quoi la substitution
		// serait rejetée par le contrôle d'égalité d'Anonymize.
		if ent.Start < 0 || ent.End > len(text) || ent.Start >= ent.End {
			continue
		}
		ent.Text = text[ent.Start:ent.End]
		candidates = append(candidates, candidate{ent: ent, fromModel: false, order: len(base) + i})
	}

	// Le plus large d'abord ; à largeur égale, le modèle avant l'appelant ; puis
	// l'ordre d'origine, pour un résultat stable.
	sort.SliceStable(candidates, func(i, j int) bool {
		wi := candidates[i].ent.End - candidates[i].ent.Start
		wj := candidates[j].ent.End - candidates[j].ent.Start
		if wi != wj {
			return wi > wj
		}
		if candidates[i].fromModel != candidates[j].fromModel {
			return candidates[i].fromModel
		}
		return candidates[i].order < candidates[j].order
	})

	var kept []ner.Entity
	for _, c := range candidates {
		if overlapsAny(kept, c.ent) {
			continue
		}
		kept = append(kept, c.ent)
	}

	sort.Slice(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// overlapsAny rapporte si ent recouvre l'une des entités déjà retenues.
func overlapsAny(kept []ner.Entity, ent ner.Entity) bool {
	for _, k := range kept {
		if ent.Start < k.End && ent.End > k.Start {
			return true
		}
	}
	return false
}
