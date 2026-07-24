package ner

import "github.com/bornholm/go-anon/pkg/features"

// Preset nomme un compromis précision/rappel prêt à l'emploi, matérialisé par un
// jeu d'options du Recognizer. Les presets sont la source de vérité unique des
// configurations recommandées, réutilisée par les binaires (eval, demo,
// anon-doc, server) et documentée dans docs/rgpd.md.
type Preset string

const (
	// PresetBalanced correspond au comportement par défaut, optimisé F1 :
	// passes de complétion activées, aucun filtre restrictif ajouté d'office.
	// C'est le compromis adapté à l'analyse ; ce n'est PAS la recommandation
	// RGPD (cf. PresetHighRecall).
	PresetBalanced Preset = "balanced"

	// PresetHighRecall maximise le rappel au prix de la précision : pour la
	// conformité RGPD, un faux négatif (donnée personnelle en clair) est bien
	// plus grave qu'un faux positif (sur-caviardage bénin). À combiner avec le
	// mode strict de vérification (anonymizer.WithStrictVerification).
	PresetHighRecall Preset = "high-recall"
)

// Balanced retourne les options du preset par défaut (compromis F1). Les passes
// n'y font que **raffiner** les détections du CRF sans en inventer de nouvelles :
//
//   - reclassement LOC→PER d'un token via le gazetteer de prénoms
//     (FirstNameReclassify — change un type, n'ajoute pas d'entité) ;
//   - fusion des spans fragmentés (MergePass) ;
//   - complétion prénom → prénom+nom sur les PER déjà détectées
//     (NameCompletionPass).
//
// Aucun filtre restrictif n'est ajouté d'office ; l'appelant reste libre d'en
// poser (MinConfidenceFilter, etc.) via WithPostFilters pour privilégier la
// précision.
func Balanced(firstNames *features.Gazetteer) []RecognizerOption {
	return []RecognizerOption{
		WithFirstNameReclassify(firstNames),
		WithMergePass(),
		WithNameCompletionPass(firstNames),
	}
}

// HighRecall retourne les options du preset « haut rappel » : tout Balanced,
// PLUS FirstNameDetectionPass qui **injecte** en PER chaque prénom du gazetteer
// non déjà couvert par le CRF. C'est le levier de rappel : il rattrape des
// personnes manquées par le modèle, au prix de faux positifs sur les prénoms
// employés comme noms communs ou toponymes.
//
// Aucun filtre restrictif (MinConfidenceFilter, MaxTokensFilter,
// BlocklistFilter) n'est ajouté : le preset ne retranche jamais une détection.
// firstNames est le gazetteer de prénoms ; s'il est nil, les passes qui en
// dépendent sont inopérantes et le preset dégénère vers Balanced sans son levier
// de rappel.
//
// Le rappel gagné se paie en précision ; publier le couple (précision, rappel)
// de ce preset à côté du preset par défaut est l'objet de `eval -preset`.
func HighRecall(firstNames *features.Gazetteer) []RecognizerOption {
	return append(Balanced(firstNames), WithFirstNameDetectionPass(firstNames))
}

// PresetOptions retourne les options associées à un preset. Un preset inconnu
// retombe sur PresetBalanced.
func PresetOptions(p Preset, firstNames *features.Gazetteer) []RecognizerOption {
	switch p {
	case PresetHighRecall:
		return HighRecall(firstNames)
	default:
		return Balanced(firstNames)
	}
}
