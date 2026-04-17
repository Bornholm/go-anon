package ner

import (
	"math"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/tokenizer"
)

// decodeEntities décode une séquence BIO ou BIOES de labels en entités nommées.
// Utilise les offsets byte-précis des tokens pour calculer Start/End dans le texte.
//
// Règles BIO(ES) :
//
//	B-TYPE  : début d'une nouvelle entité de type TYPE
//	I-TYPE  : continuation du span courant (ignoré sans B précédent du même type)
//	E-TYPE  : dernier token du span courant (BIOES) — étend et ferme immédiatement
//	S-TYPE  : entité d'un seul token (BIOES) — crée et ferme immédiatement
//	O       : hors entité
//
// tokens et labels doivent avoir la même longueur.
func decodeEntities(tokens []tokenizer.Token, labels []string) []Entity {
	var entities []Entity

	// État du span courant.
	inSpan := false
	spanStart := 0
	spanEnd := 0
	spanText := ""
	spanType := EntityType("")

	flush := func() {
		if inSpan {
			entities = append(entities, Entity{
				Text:       spanText,
				Type:       spanType,
				Start:      spanStart,
				End:        spanEnd,
				Confidence: 1.0,
			})
			inSpan = false
		}
	}

	for i, label := range labels {
		if i >= len(tokens) {
			break
		}
		tok := tokens[i]
		prefix := corpus.TagPrefix(label)
		entity := corpus.TagEntity(label)

		switch prefix {
		case "B":
			flush()
			inSpan = true
			spanStart = tok.Start
			spanEnd = tok.End
			spanText = tok.Text
			spanType = EntityType(entity)

		case "I":
			if inSpan && EntityType(entity) == spanType {
				spanEnd = tok.End
				spanText += " " + tok.Text
			} else {
				// I sans B précédent du même type : clore le span éventuel.
				flush()
			}

		case "E":
			// Dernier token d'un span multi-token (BIOES).
			if inSpan && EntityType(entity) == spanType {
				spanEnd = tok.End
				spanText += " " + tok.Text
			} else {
				// E sans B/I cohérent : traiter comme singleton.
				flush()
				inSpan = true
				spanStart = tok.Start
				spanEnd = tok.End
				spanText = tok.Text
				spanType = EntityType(entity)
			}
			flush() // Fermeture immédiate : E clôt toujours le span.

		case "S":
			// Entité d'un seul token (BIOES).
			flush()
			inSpan = true
			spanStart = tok.Start
			spanEnd = tok.End
			spanText = tok.Text
			spanType = EntityType(entity)
			flush() // Fermeture immédiate.

		default:
			// "O" ou préfixe inconnu : clore le span courant.
			flush()
		}
	}

	flush()

	return entities
}

// decodeEntitiesWithScores décode une séquence BIO ou BIOES de labels en entités avec confidence scores.
// Les scores de confiance sont calculés comme la moyenne des probabilités marginales des tokens du span.
func decodeEntitiesWithScores(
	tokens []tokenizer.Token,
	labels []string,
	marginals [][]float64,
	labelIndex map[string]int,
) []Entity {
	var entities []Entity

	inSpan := false
	spanStart := 0
	spanEnd := 0
	spanText := ""
	spanType := EntityType("")
	spanConfidence := 0.0

	flush := func() {
		if inSpan {
			entities = append(entities, Entity{
				Text:       spanText,
				Type:       spanType,
				Start:      spanStart,
				End:        spanEnd,
				Confidence: spanConfidence,
			})
			inSpan = false
		}
	}

	for i, label := range labels {
		if i >= len(tokens) {
			break
		}
		tok := tokens[i]
		prefix := corpus.TagPrefix(label)
		entity := corpus.TagEntity(label)

		switch prefix {
		case "B":
			flush()
			inSpan = true
			spanStart = tok.Start
			spanEnd = tok.End
			spanText = tok.Text
			spanType = EntityType(entity)
			spanConfidence = getMarginalConfidence(marginals, i, label, labelIndex)

		case "I":
			if inSpan && EntityType(entity) == spanType {
				spanEnd = tok.End
				spanText += " " + tok.Text
				spanConfidence = (spanConfidence + getMarginalConfidence(marginals, i, label, labelIndex)) / 2
			} else {
				flush()
			}

		case "E":
			// Dernier token d'un span multi-token (BIOES).
			if inSpan && EntityType(entity) == spanType {
				spanEnd = tok.End
				spanText += " " + tok.Text
				spanConfidence = (spanConfidence + getMarginalConfidence(marginals, i, label, labelIndex)) / 2
			} else {
				flush()
				inSpan = true
				spanStart = tok.Start
				spanEnd = tok.End
				spanText = tok.Text
				spanType = EntityType(entity)
				spanConfidence = getMarginalConfidence(marginals, i, label, labelIndex)
			}
			flush()

		case "S":
			// Entité d'un seul token (BIOES).
			flush()
			inSpan = true
			spanStart = tok.Start
			spanEnd = tok.End
			spanText = tok.Text
			spanType = EntityType(entity)
			spanConfidence = getMarginalConfidence(marginals, i, label, labelIndex)
			flush()

		default:
			flush()
		}
	}

	flush()

	return entities
}

func getMarginalConfidence(
	marginals [][]float64,
	idx int,
	label string,
	labelIndex map[string]int,
) float64 {
	if idx >= len(marginals) {
		return 1.0
	}
	lIdx, ok := labelIndex[label]
	if !ok {
		return 1.0
	}
	conf := marginals[idx][lIdx]
	if math.IsNaN(conf) || conf < 0 {
		return 1.0
	}
	return conf
}
