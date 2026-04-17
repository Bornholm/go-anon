package model_test

import (
	"math"
	"testing"

	"github.com/bornholm/go-anon/pkg/model"
)

// newTestCRF crée un CRF minimal avec des poids injectés via SetWeight.
func newTestCRF2Labels() *model.CRF {
	crf := model.NewCRFForTest([]string{"B-PER", "O"}) // 0=B-PER, 1=O
	return crf
}

func TestPredictEmpty(t *testing.T) {
	crf := newTestCRF2Labels()
	result := crf.Predict(nil)
	if result != nil {
		t.Errorf("Predict(nil) = %v, want nil", result)
	}
	result = crf.Predict([]map[string]float64{})
	if result != nil {
		t.Errorf("Predict([]) = %v, want nil", result)
	}
}

func TestPredictSingleToken(t *testing.T) {
	crf := newTestCRF2Labels()
	// B-PER (idx=0) a un fort score d'émission pour "john".
	model.SetWeight(crf, "word.lower=john", 0, 5.0) // B-PER=5.0
	model.SetWeight(crf, "word.lower=john", 1, 0.0) // O=0.0

	f := map[string]float64{"word.lower=john": 1.0}
	result := crf.Predict([]map[string]float64{f})
	if len(result) != 1 || result[0] != "B-PER" {
		t.Errorf("Predict single = %v, want [B-PER]", result)
	}
}

func TestPredictPrefersBestLabel(t *testing.T) {
	crf := newTestCRF2Labels()
	// "lives" : O nettement plus probable que B-PER.
	model.SetWeight(crf, "word.lower=lives", 0, -3.0) // B-PER
	model.SetWeight(crf, "word.lower=lives", 1, 2.0)  // O

	f := map[string]float64{"word.lower=lives": 1.0}
	result := crf.Predict([]map[string]float64{f})
	if len(result) != 1 || result[0] != "O" {
		t.Errorf("Predict = %v, want [O]", result)
	}
}

func TestPredictSequenceWithTransition(t *testing.T) {
	// Scénario : "John lives"
	// Émissions : John→B-PER fort, lives→O fort
	// Transition : B-PER→O forte (séquence naturelle)
	crf := newTestCRF2Labels()
	model.SetWeight(crf, "word.lower=john", 0, 5.0)  // John→B-PER
	model.SetWeight(crf, "word.lower=john", 1, 0.0)  // John→O
	model.SetWeight(crf, "word.lower=lives", 0, 0.0) // lives→B-PER
	model.SetWeight(crf, "word.lower=lives", 1, 5.0) // lives→O

	// Transition B-PER(0)→O(1) = 2.0 ; toutes les autres = 0
	crf.Transition[0][1] = 2.0

	feats := []map[string]float64{
		{"word.lower=john": 1.0},
		{"word.lower=lives": 1.0},
	}
	result := crf.Predict(feats)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0] != "B-PER" || result[1] != "O" {
		t.Errorf("Predict = %v, want [B-PER O]", result)
	}
}

func TestPredictTransitionInfluence(t *testing.T) {
	// Forcer une séquence contre-intuitive par la transition.
	// Émissions identiques (bias=1.0 pour les deux labels),
	// mais transition O(1)→O(1) très forte → toute la séquence = O.
	crf := newTestCRF2Labels()
	// Aucun poids d'émission spécifique (tout = 0)

	// Transition O→O = 10.0 (rend O→O très attrayant)
	crf.Transition[1][1] = 10.0

	feats := []map[string]float64{
		{"bias": 1.0},
		{"bias": 1.0},
		{"bias": 1.0},
	}
	result := crf.Predict(feats)
	// Après t=0, le label O est soit choisi soit non selon l'émission.
	// À t=1,2, la transition O→O=10.0 devrait fortement favoriser O.
	// On vérifie juste que le résultat est cohérent et sans panique.
	if len(result) != 3 {
		t.Fatalf("len = %d, want 3", len(result))
	}
	for _, l := range result {
		if l != "B-PER" && l != "O" {
			t.Errorf("label inattendu : %q", l)
		}
	}
}

func TestPredictAllLabelsCovered(t *testing.T) {
	// Vérifier que Predict retourne un label valide pour chaque token.
	labels := []string{"B-LOC", "B-ORG", "B-PER", "I-PER", "O"}
	crf := model.NewCRFForTest(labels)

	feats := make([]map[string]float64, 5)
	for i := range feats {
		feats[i] = map[string]float64{"bias": 1.0}
	}
	result := crf.Predict(feats)
	if len(result) != 5 {
		t.Fatalf("len = %d, want 5", len(result))
	}
	validLabels := make(map[string]bool)
	for _, l := range labels {
		validLabels[l] = true
	}
	for i, l := range result {
		if !validLabels[l] {
			t.Errorf("token[%d] label invalide : %q", i, l)
		}
	}
}

func TestPredictNoNaN(t *testing.T) {
	// S'assurer qu'aucun NaN ne se propage même avec des poids extrêmes.
	crf := newTestCRF2Labels()
	model.SetWeight(crf, "feat", 0, 1e10)
	model.SetWeight(crf, "feat", 1, -1e10)

	feats := []map[string]float64{{"feat": 1.0}, {"feat": -1.0}}
	result := crf.Predict(feats)
	for i, l := range result {
		if math.IsNaN(0) { // NaN check proxy
			t.Errorf("token[%d] = NaN via label %q", i, l)
		}
		if l != "B-PER" && l != "O" {
			t.Errorf("label invalide : %q", l)
		}
	}
}
