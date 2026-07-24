package ner

import (
	"fmt"
	"strings"

	"github.com/bornholm/go-anon/pkg/corpus"
)

// RecognizerEvalInterface définit l'interface pour l'évaluation.
// Permite l'injection de mock pour les tests.
type RecognizerEvalInterface interface {
	Recognize(text string) ([]Entity, error)
}

// Metrics contient les métriques d'évaluation standard du NER.
//
// F2 pondère le rappel deux fois plus que la précision (β=2) : c'est la
// métrique de référence pour la conformité RGPD, où un faux négatif (donnée
// personnelle laissée en clair) est bien plus grave qu'un faux positif
// (sur-caviardage bénin). Précision et rappel sont exposés séparément, et
// détaillés par type dans PerType — le rappel PER importe plus que le rappel
// MISC.
type Metrics struct {
	Precision float64
	Recall    float64
	F1        float64
	F2        float64
	PerType   map[EntityType]*Metrics

	TotalGold  int
	TotalPred  int
	TotalMatch int
}

// fBeta calcule le F-score pondéré : β>1 favorise le rappel, β<1 la précision.
// Retourne 0 si précision et rappel sont nuls.
func fBeta(precision, recall, beta float64) float64 {
	b2 := beta * beta
	denom := b2*precision + recall
	if denom == 0 {
		return 0
	}
	return (1 + b2) * precision * recall / denom
}

// Evaluate calcule les métriques NER sur un corpus annoté.
// Reconstruit le texte depuis les phrases gold, appelle Recognize,
// puis calcule Precision, Recall et F1.
//
// Matching strict : type ET span (Start, End) doivent correspondre exactement.
func Evaluate(rec RecognizerEvalInterface, testSet []corpus.Sentence) *Metrics {
	if len(testSet) == 0 {
		return &Metrics{
			PerType: make(map[EntityType]*Metrics),
		}
	}

	var totalGold, totalPred, totalMatch int
	perTypeMetrics := make(map[EntityType]*Metrics)

	for _, sent := range testSet {
		goldEntities := sentenceToEntities(sent)
		text := reconstructText(sent)

		predictedEntities, err := rec.Recognize(text)
		if err != nil {
			continue
		}

		totalGold += len(goldEntities)
		totalPred += len(predictedEntities)

		for _, et := range []EntityType{TypePER, TypeLOC, TypeORG, TypeMISC} {
			if _, ok := perTypeMetrics[et]; !ok {
				perTypeMetrics[et] = &Metrics{}
			}
			goldCount := countByType(goldEntities, et)
			predCount := countByType(predictedEntities, et)
			matchCount := countStrictMatches(goldEntities, predictedEntities, et)

			perTypeMetrics[et].TotalGold += goldCount
			perTypeMetrics[et].TotalPred += predCount
			perTypeMetrics[et].TotalMatch += matchCount
			totalMatch += matchCount
		}
	}

	metrics := &Metrics{
		PerType: perTypeMetrics,
	}

	if totalPred > 0 {
		metrics.Precision = float64(totalMatch) / float64(totalPred)
	}
	if totalGold > 0 {
		metrics.Recall = float64(totalMatch) / float64(totalGold)
	}
	metrics.F1 = fBeta(metrics.Precision, metrics.Recall, 1)
	metrics.F2 = fBeta(metrics.Precision, metrics.Recall, 2)

	metrics.TotalGold = totalGold
	metrics.TotalPred = totalPred
	metrics.TotalMatch = totalMatch

	for et, m := range perTypeMetrics {
		if m.TotalPred > 0 {
			m.Precision = float64(m.TotalMatch) / float64(m.TotalPred)
		}
		if m.TotalGold > 0 {
			m.Recall = float64(m.TotalMatch) / float64(m.TotalGold)
		}
		m.F1 = fBeta(m.Precision, m.Recall, 1)
		m.F2 = fBeta(m.Precision, m.Recall, 2)
		perTypeMetrics[et] = m
	}

	return metrics
}

// strictMatching retourne true si gold et pred sont identiques:
// même Type, même Start, même End.
func strictMatching(gold, pred Entity) bool {
	return gold.Type == pred.Type &&
		gold.Start == pred.Start &&
		gold.End == pred.End
}

// sentenceToEntities convertit une phrase CoNLL (BIO ou BIOES) en entités.
// Les offsets sont relatifs à la phrase reconstruite via reconstructText.
func sentenceToEntities(sent corpus.Sentence) []Entity {
	var entities []Entity
	var current *Entity

	flush := func() {
		if current != nil {
			entities = append(entities, *current)
			current = nil
		}
	}

	for i, tok := range sent {
		prefix := corpus.TagPrefix(tok.Tag)
		entity := corpus.TagEntity(tok.Tag)
		start := offsetInSentence(sent, i)

		switch prefix {
		case "B":
			flush()
			current = &Entity{
				Text:       tok.Word,
				Type:       EntityType(entity),
				Start:      start,
				End:        start + len(tok.Word),
				Confidence: 1.0,
			}

		case "I":
			if current != nil && EntityType(entity) == current.Type {
				current.Text += " " + tok.Word
				current.End = start + len(tok.Word)
			} else {
				// I incohérent : clore le span éventuel.
				flush()
			}

		case "E":
			// Dernier token d'un span multi-token (BIOES).
			if current != nil && EntityType(entity) == current.Type {
				current.Text += " " + tok.Word
				current.End = start + len(tok.Word)
			} else {
				flush()
				current = &Entity{
					Text:       tok.Word,
					Type:       EntityType(entity),
					Start:      start,
					End:        start + len(tok.Word),
					Confidence: 1.0,
				}
			}
			flush()

		case "S":
			// Entité d'un seul token (BIOES).
			flush()
			entities = append(entities, Entity{
				Text:       tok.Word,
				Type:       EntityType(entity),
				Start:      start,
				End:        start + len(tok.Word),
				Confidence: 1.0,
			})

		default: // "O"
			flush()
		}
	}

	flush()

	return entities
}

// reconstructText reconstruit le texte depuis une phrase CoNLL.
func reconstructText(sent corpus.Sentence) string {
	words := make([]string, len(sent))
	for i, tok := range sent {
		words[i] = tok.Word
	}
	return strings.Join(words, " ")
}

// offsetInSentence calcule l'offset approximatif d'un token dans la phrase.
func offsetInSentence(sent corpus.Sentence, idx int) int {
	offset := 0
	for i := 0; i < idx && i < len(sent); i++ {
		offset += len(sent[i].Word) + 1
	}
	return offset
}

// countByType compte les entités d'un type donné.
func countByType(entities []Entity, et EntityType) int {
	count := 0
	for _, e := range entities {
		if e.Type == et {
			count++
		}
	}
	return count
}

// countStrictMatches compte les matches stricts pour un type donné.
func countStrictMatches(gold, pred []Entity, et EntityType) int {
	count := 0
	for _, g := range gold {
		if g.Type != et {
			continue
		}
		for _, p := range pred {
			if strictMatching(g, p) {
				count++
				break
			}
		}
	}
	return count
}

// String implémente fmt.Stringer pour les Metrics.
func (m *Metrics) String() string {
	return fmt.Sprintf("P=%.3f R=%.3f F1=%.3f F2=%.3f (gold=%d pred=%d match=%d)",
		m.Precision, m.Recall, m.F1, m.F2, m.TotalGold, m.TotalPred, m.TotalMatch)
}
