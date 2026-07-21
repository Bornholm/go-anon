package model

import (
	"math/rand"
	"strconv"
	"testing"
)

// benchLabels reproduit le jeu de labels BIO des modèles de production.
var benchLabels = []string{"B-LOC", "B-MISC", "B-ORG", "B-PER", "I-LOC", "I-MISC", "I-ORG", "I-PER", "O"}

// newBenchCRF construit un CRF compacté déterministe : chaque feature de la
// phrase a un poids pour chaque label (100 % de hits), plus du remplissage
// aléatoire pour donner une taille de table réaliste.
func newBenchCRF(feats []map[string]float64, filler int) *CRF {
	crf := newCRF(benchLabels)
	rng := rand.New(rand.NewSource(42))

	for _, f := range feats {
		for feat := range f {
			for l := range benchLabels {
				crf.Weights.W[hashFeatureLabel(feat, l)] = rng.Float64()*2 - 1
			}
		}
	}
	for i := 0; i < filler; i++ {
		crf.Weights.W[rng.Uint64()] = rng.Float64()*2 - 1
	}
	crf.Weights.Compact()
	return crf
}

// benchFeatures simule une phrase de n tokens avec ~60 features chacun,
// avec un vocabulaire partiellement partagé entre tokens (comme le contexte réel).
func benchFeatures(n int) []map[string]float64 {
	feats := make([]map[string]float64, n)
	for t := 0; t < n; t++ {
		f := make(map[string]float64, 64)
		for i := 0; i < 40; i++ {
			f["tok"+strconv.Itoa(t)+".f"+strconv.Itoa(i)] = 1.0
		}
		for i := 0; i < 20; i++ {
			f["shared.f"+strconv.Itoa((t+i)%30)] = 1.0
		}
		feats[t] = f
	}
	return feats
}

// BenchmarkPredictAndMarginals mesure l'ancien motif d'appel du Recognizer :
// Predict puis PredictMarginals, chacun recalculant les émissions.
func BenchmarkPredictAndMarginals(b *testing.B) {
	feats := benchFeatures(25)
	crf := newBenchCRF(feats, 500_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		labels := crf.Predict(feats)
		marginals := crf.PredictMarginals(feats)
		_, _ = labels, marginals
	}
}

// BenchmarkPredictWithMarginals mesure le motif actuel du Recognizer :
// émissions calculées une seule fois pour le Viterbi et les marginales.
func BenchmarkPredictWithMarginals(b *testing.B) {
	feats := benchFeatures(25)
	crf := newBenchCRF(feats, 500_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		labels, marginals := crf.PredictWithMarginals(feats)
		_, _ = labels, marginals
	}
}
