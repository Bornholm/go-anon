// Ce fichier expose les fonctions et types internes du package model
// uniquement lors des tests (compilé exclusivement avec go test).
package model

import "github.com/bornholm/go-anon/pkg/corpus"

// NewCRFForTest expose newCRF pour les tests boîte noire.
func NewCRFForTest(labels []string) *CRF {
	return newCRF(labels)
}

// CollectLabelsForTest expose collectLabels pour les tests.
func CollectLabelsForTest(sentences []corpus.Sentence) []string {
	return collectLabels(sentences)
}

// HashFeatureLabelForTest expose hashFeatureLabel pour les tests de cohérence.
func HashFeatureLabelForTest(feat string, labelIdx int) uint64 {
	return hashFeatureLabel(feat, labelIdx)
}

// SetWeight injecte un poids d'émission dans un CRF (contournement du mutex pour les tests).
func SetWeight(crf *CRF, feat string, labelIdx int, val float64) {
	key := hashFeatureLabel(feat, labelIdx)
	crf.Weights.W[key] = val
}

// CompactWeights expose Compact() pour les tests.
func CompactWeights(crf *CRF) {
	crf.Weights.Compact()
}

// PruneWeights expose Prune() pour les tests.
func PruneWeights(crf *CRF, threshold float64) int {
	return crf.Weights.Prune(threshold)
}

// ForwardBackwardForTest expose forwardBackward pour les tests numériques.
func ForwardBackwardForTest(crf *CRF, emissions [][]float64) (alpha, beta [][]float64, Z float64) {
	return forwardBackward(crf, emissions)
}

// ComputeEmissionsForTest expose computeEmissions pour les tests.
func ComputeEmissionsForTest(crf *CRF, feats []map[string]float64) [][]float64 {
	return computeEmissions(crf, feats)
}

// LogSumExpForTest expose logSumExp pour les tests numériques.
func LogSumExpForTest(a, b float64) float64 {
	return logSumExp(a, b)
}

// LogSumExpSliceForTest expose logSumExpSlice pour les tests numériques.
func LogSumExpSliceForTest(vals []float64) float64 {
	return logSumExpSlice(vals)
}
