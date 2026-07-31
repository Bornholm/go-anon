package model

import (
	"bytes"
	"math/rand"
	"slices"
	"testing"
)

// buildTestCRF construit un CRF groupé (v3) déterministe de nBases features.
func buildTestCRF(t *testing.T, nBases int) *CRF {
	t.Helper()

	labels := []string{"B-LOC", "B-PER", "I-LOC", "I-PER", "O"}
	L := len(labels)

	crf := newCRF(labels)
	crf.FeatureCfg = FeatureConfig{
		WindowSize:     3,
		GazetteerNames: []string{"fr_prenoms"},
		LangCode:       "fr",
		FeatureSchema:  1,
	}

	rng := rand.New(rand.NewSource(42))

	bases := make([]uint64, nBases)
	blockVals := make([]float32, nBases*L)
	for i := range bases {
		bases[i] = uint64(i)*7919 + 13
		for l := 0; l < L; l++ {
			blockVals[i*L+l] = float32(rng.NormFloat64())
		}
	}

	crf.Weights = &SparseWeights{
		BaseKeys:  bases,
		BlockVals: blockVals,
		BlockL:    L,
	}
	crf.featureBases = bases

	for i := range crf.Transition {
		for j := range crf.Transition[i] {
			crf.Transition[i][j] = float64(i*L + j)
		}
	}

	return crf
}

func assertSameCRF(t *testing.T, want, got *CRF) {
	t.Helper()

	if len(got.Labels) != len(want.Labels) {
		t.Fatalf("labels: %d, attendu %d", len(got.Labels), len(want.Labels))
	}
	for i := range want.Labels {
		if got.Labels[i] != want.Labels[i] {
			t.Errorf("label %d = %q, attendu %q", i, got.Labels[i], want.Labels[i])
		}
	}

	// FeatureConfig contient une slice : comparer champ à champ.
	if got.FeatureCfg.WindowSize != want.FeatureCfg.WindowSize ||
		got.FeatureCfg.LangCode != want.FeatureCfg.LangCode ||
		got.FeatureCfg.FeatureSchema != want.FeatureCfg.FeatureSchema ||
		!slices.Equal(got.FeatureCfg.GazetteerNames, want.FeatureCfg.GazetteerNames) {
		t.Errorf("FeatureCfg = %+v, attendu %+v", got.FeatureCfg, want.FeatureCfg)
	}

	for i := range want.Transition {
		for j := range want.Transition[i] {
			if got.Transition[i][j] != want.Transition[i][j] {
				t.Fatalf("Transition[%d][%d] = %v, attendu %v",
					i, j, got.Transition[i][j], want.Transition[i][j])
			}
		}
	}

	if len(got.Weights.BaseKeys) != len(want.Weights.BaseKeys) {
		t.Fatalf("BaseKeys: %d, attendu %d", len(got.Weights.BaseKeys), len(want.Weights.BaseKeys))
	}
	for i := range want.Weights.BaseKeys {
		if got.Weights.BaseKeys[i] != want.Weights.BaseKeys[i] {
			t.Fatalf("BaseKeys[%d] = %d, attendu %d", i, got.Weights.BaseKeys[i], want.Weights.BaseKeys[i])
		}
	}

	if len(got.Weights.BlockVals) != len(want.Weights.BlockVals) {
		t.Fatalf("BlockVals: %d, attendu %d", len(got.Weights.BlockVals), len(want.Weights.BlockVals))
	}
	for i := range want.Weights.BlockVals {
		if got.Weights.BlockVals[i] != want.Weights.BlockVals[i] {
			t.Fatalf("BlockVals[%d] = %v, attendu %v", i, got.Weights.BlockVals[i], want.Weights.BlockVals[i])
		}
	}
}

// Le format v4 doit restituer le modèle bit-à-bit : les poids sont des float32
// écrits tels quels, aucune conversion ne doit altérer une valeur.
func TestSaveStream_RoundTrip(t *testing.T) {
	want := buildTestCRF(t, 5000)

	var buf bytes.Buffer
	if err := want.SaveStream(&buf); err != nil {
		t.Fatalf("SaveStream: %v", err)
	}

	got, err := LoadModel(&buf)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	assertSameCRF(t, want, got)

	if got.Weights.BlockL != len(want.Labels) {
		t.Errorf("BlockL = %d, attendu %d", got.Weights.BlockL, len(want.Labels))
	}
}

// Un modèle écrit au format gob historique doit rester lisible : la détection
// ne doit pas confondre un message gob avec un flux v4.
func TestLoadModel_BackwardCompatibleWithGob(t *testing.T) {
	want := buildTestCRF(t, 1000)

	var buf bytes.Buffer
	if err := want.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadModel(&buf)
	if err != nil {
		t.Fatalf("LoadModel sur format gob: %v", err)
	}

	assertSameCRF(t, want, got)
}

// Les deux formats doivent produire des modèles équivalents pour l'inférence.
func TestSaveStream_MatchesGobFormat(t *testing.T) {
	src := buildTestCRF(t, 2000)

	var gobBuf, streamBuf bytes.Buffer
	if err := src.Save(&gobBuf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := src.SaveStream(&streamBuf); err != nil {
		t.Fatalf("SaveStream: %v", err)
	}

	fromGob, err := LoadModel(&gobBuf)
	if err != nil {
		t.Fatalf("LoadModel(gob): %v", err)
	}
	fromStream, err := LoadModel(&streamBuf)
	if err != nil {
		t.Fatalf("LoadModel(stream): %v", err)
	}

	assertSameCRF(t, fromGob, fromStream)
}

// Le mode mutable doit reconstruire la map de poids à l'identique depuis un v4.
func TestLoadModelMutable_Stream(t *testing.T) {
	src := buildTestCRF(t, 500)

	var streamBuf, gobBuf bytes.Buffer
	if err := src.SaveStream(&streamBuf); err != nil {
		t.Fatalf("SaveStream: %v", err)
	}
	if err := src.Save(&gobBuf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fromStream, err := LoadModelMutable(&streamBuf)
	if err != nil {
		t.Fatalf("LoadModelMutable(stream): %v", err)
	}
	fromGob, err := LoadModelMutable(&gobBuf)
	if err != nil {
		t.Fatalf("LoadModelMutable(gob): %v", err)
	}

	if len(fromStream.Weights.W) != len(fromGob.Weights.W) {
		t.Fatalf("poids mutables: %d, attendu %d", len(fromStream.Weights.W), len(fromGob.Weights.W))
	}
	for k, v := range fromGob.Weights.W {
		if fromStream.Weights.W[k] != v {
			t.Fatalf("poids[%d] = %v, attendu %v", k, fromStream.Weights.W[k], v)
		}
	}
}

// Un flux tronqué doit échouer proprement, sans allocation démesurée ni panic.
func TestLoadModel_TruncatedStream(t *testing.T) {
	src := buildTestCRF(t, 1000)

	var buf bytes.Buffer
	if err := src.SaveStream(&buf); err != nil {
		t.Fatalf("SaveStream: %v", err)
	}

	truncated := buf.Bytes()[:buf.Len()/2]
	if _, err := LoadModel(bytes.NewReader(truncated)); err == nil {
		t.Fatal("attendu une erreur sur flux tronqué, obtenu nil")
	}
}

// Un modèle plat (v2) doit franchir le format v4 sans perdre sa représentation.
func TestSaveStream_FlatModel(t *testing.T) {
	labels := []string{"B-PER", "I-PER", "O"}
	crf := newCRF(labels)
	crf.Weights = &SparseWeights{
		Keys: []uint64{3, 17, 42, 1009},
		Vals: []float32{0.5, -1.25, 3.75, 0.125},
	}

	var buf bytes.Buffer
	if err := crf.SaveStream(&buf); err != nil {
		t.Fatalf("SaveStream: %v", err)
	}

	got, err := LoadModel(&buf)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	if len(got.Weights.Keys) != 4 {
		t.Fatalf("Keys: %d, attendu 4", len(got.Weights.Keys))
	}
	for i, want := range []float32{0.5, -1.25, 3.75, 0.125} {
		if got.Weights.Vals[i] != want {
			t.Errorf("Vals[%d] = %v, attendu %v", i, got.Weights.Vals[i], want)
		}
	}
}
