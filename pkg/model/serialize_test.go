package model_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/model"
)

func trainedCRF(t *testing.T) *model.CRF {
	t.Helper()
	tr := &model.Trainer{
		Config: model.TrainConfig{
			Epochs:       20,
			LearningRate: 0.1,
			L2Lambda:     0.001,
		},
		Extractor: &features.FeatureExtractor{WindowSize: 1},
	}
	crf, err := tr.Train(toyCorpus(), nil)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	return crf
}

func predictAll(crf *model.CRF, fe *features.FeatureExtractor) [][]string {
	sentences := toyCorpus()
	results := make([][]string, len(sentences))
	for i, sent := range sentences {
		words := make([]string, len(sent))
		for j, tok := range sent {
			words[j] = tok.Word
		}
		feats := make([]map[string]float64, len(sent))
		for j := range sent {
			feats[j] = fe.Features(words, j)
		}
		results[i] = crf.Predict(feats)
	}
	return results
}

func TestSerializeRoundTrip(t *testing.T) {
	fe := &features.FeatureExtractor{WindowSize: 1}
	crf := trainedCRF(t)

	// Prédictions avant sauvegarde.
	before := predictAll(crf, fe)

	// Sauvegarder.
	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Charger.
	loaded, err := model.LoadModel(&buf)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	// Prédictions après chargement : doivent être identiques.
	after := predictAll(loaded, fe)

	for i, bRow := range before {
		for j, b := range bRow {
			if j >= len(after[i]) || after[i][j] != b {
				t.Errorf("sent[%d] token[%d] : before=%q, after=%q", i, j, b, after[i][j])
			}
		}
	}
}

func TestSerializeSizeNotZero(t *testing.T) {
	crf := trainedCRF(t)
	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("fichier sérialisé vide")
	}
}

func TestLoadModelInvalidData(t *testing.T) {
	// Données non-gzip : doit retourner une erreur.
	r := strings.NewReader("not gzip data")
	_, err := model.LoadModel(r)
	if err == nil {
		t.Error("LoadModel avec données invalides : attendu une erreur, got nil")
	}
}

func TestSerializePreservesLabels(t *testing.T) {
	crf := trainedCRF(t)
	originalLabels := make([]string, len(crf.Labels))
	copy(originalLabels, crf.Labels)

	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := model.LoadModel(&buf)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	if len(loaded.Labels) != len(originalLabels) {
		t.Fatalf("len(Labels) : got %d, want %d", len(loaded.Labels), len(originalLabels))
	}
	for i, l := range originalLabels {
		if loaded.Labels[i] != l {
			t.Errorf("Labels[%d] : got %q, want %q", i, loaded.Labels[i], l)
		}
	}
}
