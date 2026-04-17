package model_test

import (
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/model"
)

// toyCorpus retourne un petit corpus d'entraînement avec des entités distinctes.
// Les mots sont suffisamment uniques pour que le modèle puisse les mémoriser.
func toyCorpus() []corpus.Sentence {
	return []corpus.Sentence{
		{
			{Word: "John", Tag: "B-PER"},
			{Word: "lives", Tag: "O"},
			{Word: "in", Tag: "O"},
			{Word: "Paris", Tag: "B-LOC"},
			{Word: ".", Tag: "O"},
		},
		{
			{Word: "Mary", Tag: "B-PER"},
			{Word: "works", Tag: "O"},
			{Word: "at", Tag: "O"},
			{Word: "Airbus", Tag: "B-ORG"},
			{Word: ".", Tag: "O"},
		},
		{
			{Word: "Jean", Tag: "B-PER"},
			{Word: "visited", Tag: "O"},
			{Word: "London", Tag: "B-LOC"},
			{Word: ".", Tag: "O"},
		},
		{
			{Word: "Apple", Tag: "B-ORG"},
			{Word: "is", Tag: "O"},
			{Word: "based", Tag: "O"},
			{Word: "in", Tag: "O"},
			{Word: "Cupertino", Tag: "B-LOC"},
			{Word: ".", Tag: "O"},
		},
		{
			{Word: "Pierre", Tag: "B-PER"},
			{Word: "left", Tag: "O"},
			{Word: "France", Tag: "B-LOC"},
			{Word: ".", Tag: "O"},
		},
	}
}

func newDefaultTrainer() *model.Trainer {
	return &model.Trainer{
		Config: model.TrainConfig{
			Epochs:       50,
			LearningRate: 0.1,
			L2Lambda:     0.001,
		},
		Extractor: &features.FeatureExtractor{WindowSize: 1},
	}
}

// accuracy calcule l'accuracy token-level sur un ensemble de phrases.
func accuracy(crf *model.CRF, sentences []corpus.Sentence, fe *features.FeatureExtractor) float64 {
	correct := 0
	total := 0
	for _, sent := range sentences {
		words := make([]string, len(sent))
		for i, tok := range sent {
			words[i] = tok.Word
		}
		feats := make([]map[string]float64, len(sent))
		for i := range sent {
			feats[i] = fe.Features(words, i)
		}
		predicted := crf.Predict(feats)
		for i, tok := range sent {
			total++
			if i < len(predicted) && predicted[i] == tok.Tag {
				correct++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(correct) / float64(total)
}

func TestTrainConvergesOnToyCorpus(t *testing.T) {
	train := toyCorpus()
	tr := newDefaultTrainer()

	crf, err := tr.Train(train, nil)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}

	acc := accuracy(crf, train, tr.Extractor)
	if acc < 0.8 {
		t.Errorf("accuracy après entraînement = %.2f, want ≥ 0.80", acc)
	}
}

func TestTrainReturnsCorrectLabels(t *testing.T) {
	train := toyCorpus()
	tr := newDefaultTrainer()

	crf, err := tr.Train(train, nil)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}

	wantLabels := map[string]bool{"B-LOC": true, "B-ORG": true, "B-PER": true, "O": true}
	if len(crf.Labels) != len(wantLabels) {
		t.Errorf("len(Labels) = %d, want %d", len(crf.Labels), len(wantLabels))
	}
	for _, l := range crf.Labels {
		if !wantLabels[l] {
			t.Errorf("label inattendu : %q", l)
		}
	}
}

func TestTrainPredictCorrectLength(t *testing.T) {
	train := toyCorpus()
	tr := newDefaultTrainer()

	crf, err := tr.Train(train, nil)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}

	for _, sent := range train {
		words := make([]string, len(sent))
		for i, tok := range sent {
			words[i] = tok.Word
		}
		feats := make([]map[string]float64, len(sent))
		for i := range sent {
			feats[i] = tr.Extractor.Features(words, i)
		}
		predicted := crf.Predict(feats)
		if len(predicted) != len(sent) {
			t.Errorf("len(predicted) = %d, want %d", len(predicted), len(sent))
		}
	}
}

func TestTrainEarlyStop(t *testing.T) {
	train := toyCorpus()
	dev := toyCorpus()

	tr := &model.Trainer{
		Config: model.TrainConfig{
			Epochs:       100,
			LearningRate: 0.1,
			L2Lambda:     0.001,
			EarlyStop:    5,
		},
		Extractor: &features.FeatureExtractor{WindowSize: 1},
	}

	crf, err := tr.Train(train, dev)
	if err != nil {
		t.Fatalf("Train avec early stop: %v", err)
	}
	if crf == nil {
		t.Fatal("crf nil après early stop")
	}
}

func TestTrainEmptySentencesIgnored(t *testing.T) {
	train := []corpus.Sentence{
		{},
		{{Word: "John", Tag: "B-PER"}},
		{},
	}
	tr := &model.Trainer{
		Config:    model.TrainConfig{Epochs: 5, LearningRate: 0.1},
		Extractor: &features.FeatureExtractor{WindowSize: 0},
	}
	_, err := tr.Train(train, nil)
	if err != nil {
		t.Fatalf("Train avec phrases vides: %v", err)
	}
}
