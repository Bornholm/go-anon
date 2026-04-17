package model_test

import (
	"math"
	"testing"

	"github.com/bornholm/go-anon/pkg/model"
)

const fbEps = 1e-6

// marginalsFromFB calcule P(y_t=l|x) = exp(alpha[t][l] + beta[t][l] - Z).
func marginalsFromFB(alpha, beta [][]float64, Z float64) [][]float64 {
	marginals := make([][]float64, len(alpha))
	for t := range alpha {
		marginals[t] = make([]float64, len(alpha[t]))
		for l := range alpha[t] {
			marginals[t][l] = math.Exp(alpha[t][l] + beta[t][l] - Z)
		}
	}
	return marginals
}

func TestFBSingleTokenMarginalsSumToOne(t *testing.T) {
	// 1 token, 2 labels, émissions [1.0, 2.0].
	// Marginals = softmax([1.0, 2.0]).
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	model.SetWeight(crf, "feat", 0, 1.0) // O
	model.SetWeight(crf, "feat", 1, 2.0) // B-PER

	feats := []map[string]float64{{"feat": 1.0}}
	emissions := model.ComputeEmissionsForTest(crf, feats)
	alpha, beta, Z := model.ForwardBackwardForTest(crf, emissions)

	marginals := marginalsFromFB(alpha, beta, Z)

	sum := 0.0
	for _, m := range marginals[0] {
		sum += m
	}
	if math.Abs(sum-1.0) > fbEps {
		t.Errorf("marginals sum = %v, want 1.0 (± %v)", sum, fbEps)
	}
}

func TestFBZConsistentWithAlpha(t *testing.T) {
	// Z doit être égal à logSumExp(alpha[n-1][:]).
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	model.SetWeight(crf, "w1", 0, 0.5)
	model.SetWeight(crf, "w1", 1, 1.5)
	model.SetWeight(crf, "w2", 0, 0.3)
	model.SetWeight(crf, "w2", 1, -0.2)
	crf.Transition[0][1] = 0.8
	crf.Transition[1][0] = -0.5

	feats := []map[string]float64{
		{"w1": 1.0},
		{"w2": 1.0},
	}
	emissions := model.ComputeEmissionsForTest(crf, feats)
	alpha, _, Z := model.ForwardBackwardForTest(crf, emissions)

	// Recalculer Z depuis alpha[n-1]
	ZfromAlpha := model.LogSumExpSliceForTest(alpha[len(alpha)-1])
	if math.Abs(Z-ZfromAlpha) > fbEps {
		t.Errorf("Z = %v, logSumExp(alpha[n-1]) = %v", Z, ZfromAlpha)
	}
}

func TestFBMarginalsAllTokensSumToOne(t *testing.T) {
	// Pour chaque position t, Σ_l P(y_t=l|x) doit être ≈ 1.
	crf := model.NewCRFForTest([]string{"O", "B-PER", "B-LOC"})
	model.SetWeight(crf, "w", 0, 1.0)
	model.SetWeight(crf, "w", 1, 0.5)
	model.SetWeight(crf, "w", 2, 0.3)

	feats := []map[string]float64{
		{"w": 1.0},
		{"w": 1.0},
		{"w": 1.0},
	}
	emissions := model.ComputeEmissionsForTest(crf, feats)
	alpha, beta, Z := model.ForwardBackwardForTest(crf, emissions)
	marginals := marginalsFromFB(alpha, beta, Z)

	for idx, row := range marginals {
		sum := 0.0
		for _, m := range row {
			sum += m
		}
		if math.Abs(sum-1.0) > fbEps {
			t.Errorf("pos=%d : marginals sum = %v, want 1.0", idx, sum)
		}
	}
}

func TestFBNoNaN(t *testing.T) {
	// Aucun NaN/Inf dans alpha et beta même avec des poids extrêmes.
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	model.SetWeight(crf, "feat", 0, 100.0)
	model.SetWeight(crf, "feat", 1, -100.0)

	feats := make([]map[string]float64, 10)
	for i := range feats {
		feats[i] = map[string]float64{"feat": 1.0}
	}
	emissions := model.ComputeEmissionsForTest(crf, feats)
	alpha, beta, Z := model.ForwardBackwardForTest(crf, emissions)

	for pos, row := range alpha {
		for l, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 1) {
				t.Errorf("alpha[%d][%d] = %v (NaN/+Inf)", pos, l, v)
			}
		}
	}
	for pos, row := range beta {
		for l, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 1) {
				t.Errorf("beta[%d][%d] = %v (NaN/+Inf)", pos, l, v)
			}
		}
	}
	if math.IsNaN(Z) {
		t.Errorf("Z = NaN")
	}
}

func TestFBBetaLastTokenIsZero(t *testing.T) {
	// beta[T-1][l] = 0.0 = log(1) pour tout l (condition initiale backward).
	crf := model.NewCRFForTest([]string{"O", "B-PER"})
	feats := []map[string]float64{{"bias": 1.0}, {"bias": 1.0}}
	emissions := model.ComputeEmissionsForTest(crf, feats)
	_, beta, _ := model.ForwardBackwardForTest(crf, emissions)

	n := len(beta)
	for l, v := range beta[n-1] {
		if v != 0.0 {
			t.Errorf("beta[T-1][%d] = %v, want 0.0", l, v)
		}
	}
}
