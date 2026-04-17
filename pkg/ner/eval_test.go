package ner

import (
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
)

func TestMetrics_PrecisionRecall(t *testing.T) {
	entities := []Entity{
		{Text: "John", Type: TypePER, Start: 0, End: 4, Confidence: 1.0},
	}
	predicted := []Entity{
		{Text: "John", Type: TypePER, Start: 0, End: 4, Confidence: 1.0},
		{Text: "Jane", Type: TypePER, Start: 10, End: 14, Confidence: 1.0},
	}

	metrics := computeMockMetrics(entities, predicted)

	if metrics.TotalGold != 1 {
		t.Errorf("expected TotalGold=1, got %d", metrics.TotalGold)
	}
	if metrics.TotalPred != 2 {
		t.Errorf("expected TotalPred=2, got %d", metrics.TotalPred)
	}
	if metrics.TotalMatch != 1 {
		t.Errorf("expected TotalMatch=1, got %d", metrics.TotalMatch)
	}

	expectedPrecision := 0.5
	if metrics.Precision != expectedPrecision {
		t.Errorf("expected Precision=%.3f, got %.3f", expectedPrecision, metrics.Precision)
	}

	expectedRecall := 1.0
	if metrics.Recall != expectedRecall {
		t.Errorf("expected Recall=%.3f, got %.3f", expectedRecall, metrics.Recall)
	}

	expectedF1 := 2 * 0.5 * 1.0 / (0.5 + 1.0)
	if metrics.F1 != expectedF1 {
		t.Errorf("expected F1=%.3f, got %.3f", expectedF1, metrics.F1)
	}
}

func TestMetrics_PerType(t *testing.T) {
	gold := []Entity{
		{Text: "John", Type: TypePER, Start: 0, End: 4, Confidence: 1.0},
		{Text: "Paris", Type: TypeLOC, Start: 10, End: 14, Confidence: 1.0},
	}
	pred := []Entity{
		{Text: "John", Type: TypePER, Start: 0, End: 4, Confidence: 1.0},
		{Text: "London", Type: TypeLOC, Start: 20, End: 26, Confidence: 1.0},
	}

	metrics := computeMockMetrics(gold, pred)

	if metrics.PerType == nil {
		t.Fatal("expected PerType to be initialized")
	}

	perMetrics := metrics.PerType[TypePER]
	if perMetrics == nil {
		t.Fatal("expected PER metrics")
	}
	if perMetrics.TotalMatch != 1 {
		t.Errorf("expected PER TotalMatch=1, got %d", perMetrics.TotalMatch)
	}
}

func TestEvaluate_SampleCorpus(t *testing.T) {
	sentences := []corpus.Sentence{
		{
			{Word: "John", Tag: "B-PER"},
			{Word: "Doe", Tag: "I-PER"},
			{Word: "lives", Tag: "O"},
			{Word: "in", Tag: "O"},
			{Word: "Paris", Tag: "B-LOC"},
			{Word: ".", Tag: "O"},
		},
		{
			{Word: "Peter", Tag: "B-PER"},
			{Word: "works", Tag: "O"},
			{Word: "at", Tag: "O"},
			{Word: "Airbus", Tag: "B-ORG"},
			{Word: ".", Tag: "O"},
		},
	}

	rec := &mockEvalRecognizer{
		predicted: []Entity{
			{Text: "John Doe", Type: TypePER, Start: 0, End: 7, Confidence: 1.0},
			{Text: "Paris", Type: TypeLOC, Start: 7, End: 12, Confidence: 1.0},
			{Text: "Airbus", Type: TypeORG, Start: 4, End: 10, Confidence: 1.0},
		},
	}

	metrics := Evaluate(rec, sentences)

	if metrics.TotalGold == 0 {
		t.Error("expected non-zero TotalGold")
	}
	if metrics.TotalPred == 0 {
		t.Error("expected non-zero TotalPred")
	}

	t.Logf("metrics: %s", metrics.String())
}

func TestEvaluate_NoEntities(t *testing.T) {
	sentences := []corpus.Sentence{
		{
			{Word: "John", Tag: "B-PER"},
			{Word: "lives", Tag: "O"},
		},
	}

	rec := &mockEvalRecognizer{
		predicted: []Entity{},
	}

	metrics := Evaluate(rec, sentences)

	if metrics.TotalGold != 1 {
		t.Errorf("expected TotalGold=1, got %d", metrics.TotalGold)
	}
	if metrics.TotalPred != 0 {
		t.Errorf("expected TotalPred=0, got %d", metrics.TotalPred)
	}
	if metrics.TotalMatch != 0 {
		t.Errorf("expected TotalMatch=0, got %d", metrics.TotalMatch)
	}
	if metrics.Precision != 0 {
		t.Errorf("expected Precision=0, got %.3f", metrics.Precision)
	}
	if metrics.Recall != 0 {
		t.Errorf("expected Recall=0, got %.3f", metrics.Recall)
	}
}

func TestEvaluate_EmptyCorpus(t *testing.T) {
	sentences := []corpus.Sentence{}

	rec := &mockEvalRecognizer{
		predicted: []Entity{},
	}

	metrics := Evaluate(rec, sentences)

	if metrics.TotalGold != 0 {
		t.Errorf("expected TotalGold=0, got %d", metrics.TotalGold)
	}
}

func TestSentenceToEntities(t *testing.T) {
	sent := corpus.Sentence{
		{Word: "John", Tag: "B-PER"},
		{Word: "Doe", Tag: "I-PER"},
		{Word: "lives", Tag: "O"},
		{Word: "in", Tag: "O"},
		{Word: "Paris", Tag: "B-LOC"},
		{Word: ".", Tag: "O"},
	}

	entities := sentenceToEntities(sent)

	if len(entities) != 2 {
		t.Errorf("expected 2 entities, got %d", len(entities))
	}

	if entities[0].Type != TypePER {
		t.Errorf("expected first entity PER, got %s", entities[0].Type)
	}
	if entities[0].Text != "John Doe" {
		t.Errorf("expected 'John Doe', got %s", entities[0].Text)
	}

	if entities[1].Type != TypeLOC {
		t.Errorf("expected second entity LOC, got %s", entities[1].Type)
	}
}

func TestStrictMatching(t *testing.T) {
	gold := Entity{Text: "John", Type: TypePER, Start: 0, End: 4, Confidence: 1.0}
	pred := Entity{Text: "John", Type: TypePER, Start: 0, End: 4, Confidence: 1.0}

	if !strictMatching(gold, pred) {
		t.Error("expected strict match for identical entities")
	}

	predDifferentType := Entity{Text: "John", Type: TypeLOC, Start: 0, End: 4, Confidence: 1.0}
	if strictMatching(gold, predDifferentType) {
		t.Error("expected no match for different type")
	}

	predDifferentSpan := Entity{Text: "John", Type: TypePER, Start: 1, End: 5, Confidence: 1.0}
	if strictMatching(gold, predDifferentSpan) {
		t.Error("expected no match for different span")
	}
}

type mockEvalRecognizer struct {
	predicted []Entity
}

func (m *mockEvalRecognizer) Recognize(text string) ([]Entity, error) {
	return m.predicted, nil
}

func computeMockMetrics(gold, pred []Entity) *Metrics {
	totalMatch := 0
	for _, g := range gold {
		for _, p := range pred {
			if strictMatching(g, p) {
				totalMatch++
				break
			}
		}
	}

	totalGold := len(gold)
	totalPred := len(pred)

	perType := make(map[EntityType]*Metrics)
	for _, et := range []EntityType{TypePER, TypeLOC, TypeORG, TypeMISC} {
		goldCount := countByType(gold, et)
		predCount := countByType(pred, et)
		matchCount := countStrictMatches(gold, pred, et)

		pm := &Metrics{
			TotalGold:  goldCount,
			TotalPred:  predCount,
			TotalMatch: matchCount,
		}
		if predCount > 0 {
			pm.Precision = float64(matchCount) / float64(predCount)
		}
		if goldCount > 0 {
			pm.Recall = float64(matchCount) / float64(goldCount)
		}
		if pm.Precision+pm.Recall > 0 {
			pm.F1 = 2 * pm.Precision * pm.Recall / (pm.Precision + pm.Recall)
		}
		perType[et] = pm
	}

	metrics := &Metrics{
		TotalGold:  totalGold,
		TotalPred:  totalPred,
		TotalMatch: totalMatch,
		PerType:    perType,
	}

	if totalPred > 0 {
		metrics.Precision = float64(totalMatch) / float64(totalPred)
	}
	if totalGold > 0 {
		metrics.Recall = float64(totalMatch) / float64(totalGold)
	}
	if metrics.Precision+metrics.Recall > 0 {
		metrics.F1 = 2 * metrics.Precision * metrics.Recall / (metrics.Precision + metrics.Recall)
	}

	return metrics
}
