package model_test

import (
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/model"
)

func TestSparseWeightsScoreEmpty(t *testing.T) {
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	f := map[string]float64{"bias": 1.0}
	score := crf.Weights.Score(f, 0)
	if score != 0.0 {
		t.Errorf("Score avec poids vides = %v, want 0.0", score)
	}
}

func TestSparseWeightsScoreKnown(t *testing.T) {
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	// Injecter un poids manuellement
	model.SetWeight(crf, "word.lower=john", 1, 2.5)

	f := map[string]float64{"word.lower=john": 1.0}
	score := crf.Weights.Score(f, 1) // label index 1 = "B-PER"
	if score != 2.5 {
		t.Errorf("Score = %v, want 2.5", score)
	}
}

func TestSparseWeightsScoreMultipleFeatures(t *testing.T) {
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	model.SetWeight(crf, "bias", 0, 1.0)
	model.SetWeight(crf, "word.isTitle", 0, 0.5)

	f := map[string]float64{"bias": 1.0, "word.isTitle": 1.0}
	score := crf.Weights.Score(f, 0)
	if score != 1.5 {
		t.Errorf("Score = %v, want 1.5", score)
	}
}

func TestCollectLabels(t *testing.T) {
	sentences := []corpus.Sentence{
		{{Word: "John", Tag: "B-PER"}, {Word: "lives", Tag: "O"}},
		{{Word: "Paris", Tag: "B-LOC"}, {Word: ".", Tag: "O"}},
	}
	labels := model.CollectLabelsForTest(sentences)
	want := []string{"B-LOC", "B-PER", "O"}

	if len(labels) != len(want) {
		t.Fatalf("len(labels) = %d, want %d", len(labels), len(want))
	}
	for i, l := range want {
		if labels[i] != l {
			t.Errorf("labels[%d] = %q, want %q", i, labels[i], l)
		}
	}
}

func TestCollectLabelsNoDuplicates(t *testing.T) {
	sentences := []corpus.Sentence{
		{{Word: "a", Tag: "O"}, {Word: "b", Tag: "O"}},
		{{Word: "c", Tag: "O"}},
	}
	labels := model.CollectLabelsForTest(sentences)
	if len(labels) != 1 || labels[0] != "O" {
		t.Errorf("labels = %v, want [O]", labels)
	}
}

func TestHashFeatureLabelConsistent(t *testing.T) {
	h1 := model.HashFeatureLabelForTest("word.lower=hello", 2)
	h2 := model.HashFeatureLabelForTest("word.lower=hello", 2)
	if h1 != h2 {
		t.Error("hashFeatureLabel non déterministe")
	}
}

func TestSparseWeightsCompact(t *testing.T) {
	crf := model.NewCRFForTest([]string{"O", "B-PER", "I-PER"})
	model.SetWeight(crf, "word=paris", 1, 1.5)
	model.SetWeight(crf, "word=paris", 2, 0.3)
	model.SetWeight(crf, "bias", 0, -0.7)

	f := map[string]float64{"word=paris": 1.0, "bias": 1.0}

	// Scores avant compaction
	s0Before := crf.Weights.Score(f, 0)
	s1Before := crf.Weights.Score(f, 1)
	s2Before := crf.Weights.Score(f, 2)

	model.CompactWeights(crf)

	// Scores après compaction : doivent être identiques à 1e-4 près (float32)
	s0After := crf.Weights.Score(f, 0)
	s1After := crf.Weights.Score(f, 1)
	s2After := crf.Weights.Score(f, 2)

	const tol = 1e-4
	if diff := s0After - s0Before; diff > tol || diff < -tol {
		t.Errorf("Score label=0 avant=%.6f après=%.6f (diff=%.6f)", s0Before, s0After, diff)
	}
	if diff := s1After - s1Before; diff > tol || diff < -tol {
		t.Errorf("Score label=1 avant=%.6f après=%.6f (diff=%.6f)", s1Before, s1After, diff)
	}
	if diff := s2After - s2Before; diff > tol || diff < -tol {
		t.Errorf("Score label=2 avant=%.6f après=%.6f (diff=%.6f)", s2Before, s2After, diff)
	}
}

func TestSparseWeightsPrune(t *testing.T) {
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	model.SetWeight(crf, "important", 1, 2.0)
	model.SetWeight(crf, "near-zero", 0, 0.0005)
	model.SetWeight(crf, "also-small", 1, -0.0003)

	removed := model.PruneWeights(crf, 0.001)
	if removed != 2 {
		t.Errorf("Prune a supprimé %d entrées, attendu 2", removed)
	}

	// Le poids important doit être préservé
	f := map[string]float64{"important": 1.0}
	score := crf.Weights.Score(f, 1)
	if score != 2.0 {
		t.Errorf("Score après prune = %.4f, attendu 2.0", score)
	}

	// Les poids élagués doivent retourner 0
	fSmall := map[string]float64{"near-zero": 1.0}
	if s := crf.Weights.Score(fSmall, 0); s != 0.0 {
		t.Errorf("Score near-zero après prune = %.4f, attendu 0.0", s)
	}
}

func TestHashFeatureLabelDistinct(t *testing.T) {
	h1 := model.HashFeatureLabelForTest("word.lower=hello", 0)
	h2 := model.HashFeatureLabelForTest("word.lower=hello", 1)
	h3 := model.HashFeatureLabelForTest("word.lower=world", 0)
	if h1 == h2 {
		t.Error("hash identique pour labels différents")
	}
	if h1 == h3 {
		t.Error("hash identique pour features différentes")
	}
}
