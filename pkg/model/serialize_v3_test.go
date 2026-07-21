package model

import (
	"bytes"
	"testing"
)

// newV3TestCRF construit un CRF en mode training avec featureBases remplies,
// comme à la sortie de Trainer.Train.
func newV3TestCRF(t *testing.T) (*CRF, []map[string]float64) {
	t.Helper()
	feats := benchFeatures(6)
	crf := newBenchCRF(feats, 0) // compacté v2

	// Reconstituer le mode training + bases (comme après Train).
	mutable := newCRF(benchLabels)
	seen := make(map[uint64]struct{})
	for _, f := range feats {
		for feat := range f {
			base := hashFeatureBase(feat)
			seen[base] = struct{}{}
			for l := range benchLabels {
				mutable.Weights.W[hashFeatureLabelFromBase(base, l)] = crf.Weights.Score(map[string]float64{feat: 1.0}, l)
			}
		}
	}
	for b := range seen {
		mutable.featureBases = append(mutable.featureBases, b)
	}
	mutable.Transition = crf.Transition
	mutable.FeatureCfg = FeatureConfig{WindowSize: 3, FeatureSchema: 1}
	return mutable, feats
}

func TestSaveLoadV3_RoundTrip(t *testing.T) {
	crf, feats := newV3TestCRF(t)

	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadModel(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if loaded.Weights.BaseKeys == nil {
		t.Fatal("le modèle rechargé devrait être en mode groupé (v3)")
	}
	if loaded.FeatureCfg.FeatureSchema != 1 {
		t.Errorf("FeatureSchema perdu : %d", loaded.FeatureCfg.FeatureSchema)
	}

	// Scores identiques entre l'original (map) et le rechargé (groupé),
	// à la précision float32 près (sérialisation en float32 dans les deux formats).
	L := len(crf.Labels)
	out := make([]float64, L)
	for i, f := range feats {
		loaded.Weights.ScoreAll(f, out)
		for l := 0; l < L; l++ {
			want := crf.Weights.Score(f, l)
			if diff := out[l] - want; diff > 1e-4 || diff < -1e-4 {
				t.Errorf("token %d label %d : groupé=%v, original=%v", i, l, out[l], want)
			}
		}
	}

	// Predict identique.
	origLabels := crf.Predict(feats)
	loadedLabels := loaded.Predict(feats)
	for i := range origLabels {
		if origLabels[i] != loadedLabels[i] {
			t.Errorf("token %d : label original %q ≠ rechargé %q", i, origLabels[i], loadedLabels[i])
		}
	}
}

func TestSaveLoadV3_PruneCycle(t *testing.T) {
	crf, feats := newV3TestCRF(t)

	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Cycle prune : chargement mutable → Prune → re-Save → doit rester v3.
	mutable, err := LoadModelMutable(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadModelMutable: %v", err)
	}
	if mutable.Weights.W == nil {
		t.Fatal("le chargement mutable devrait exposer la map de poids")
	}
	mutable.Weights.Prune(0.5)

	var buf2 bytes.Buffer
	if err := mutable.Save(&buf2); err != nil {
		t.Fatalf("Save après prune: %v", err)
	}
	pruned, err := LoadModel(bytes.NewReader(buf2.Bytes()))
	if err != nil {
		t.Fatalf("LoadModel après prune: %v", err)
	}
	if pruned.Weights.BaseKeys == nil {
		t.Error("un modèle v3 élagué doit rester au format groupé v3")
	}

	// Les scores élagués restent cohérents avec la map élaguée.
	L := len(mutable.Labels)
	out := make([]float64, L)
	for _, f := range feats {
		pruned.Weights.ScoreAll(f, out)
		for l := 0; l < L; l++ {
			want := mutable.Weights.Score(f, l)
			if diff := out[l] - want; diff > 1e-4 || diff < -1e-4 {
				t.Errorf("après prune, label %d : groupé=%v, map=%v", l, out[l], want)
			}
		}
	}
}

func TestSaveLoadV2_StillWorks(t *testing.T) {
	// Un CRF sans featureBases (ex: chargé depuis un v2) doit continuer à
	// s'enregistrer et se recharger au format plat.
	feats := benchFeatures(4)
	crf := newBenchCRF(feats, 0)

	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadModel(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if loaded.Weights.BaseKeys != nil {
		t.Error("un modèle sans bases doit rester au format plat v2")
	}
	for l := range crf.Labels {
		for _, f := range feats {
			if got, want := loaded.Weights.Score(f, l), crf.Weights.Score(f, l); got != want {
				t.Errorf("label %d : rechargé=%v, original=%v", l, got, want)
			}
		}
	}
}
