package model_test

import (
	"math"
	"testing"

	"github.com/bornholm/go-anon/pkg/model"
)

const eps = 1e-9

func TestLogSumExpLeftInf(t *testing.T) {
	got := model.LogSumExpForTest(math.Inf(-1), 3.0)
	if got != 3.0 {
		t.Errorf("logSumExp(-Inf, 3) = %v, want 3.0", got)
	}
}

func TestLogSumExpRightInf(t *testing.T) {
	got := model.LogSumExpForTest(5.0, math.Inf(-1))
	if got != 5.0 {
		t.Errorf("logSumExp(5, -Inf) = %v, want 5.0", got)
	}
}

func TestLogSumExpEqual(t *testing.T) {
	// logSumExp(0, 0) = log(2)
	got := model.LogSumExpForTest(0.0, 0.0)
	want := math.Log(2)
	if math.Abs(got-want) > eps {
		t.Errorf("logSumExp(0, 0) = %v, want %v", got, want)
	}
}

func TestLogSumExpDirect(t *testing.T) {
	// Vérification directe sur petites valeurs (pas d'overflow)
	a, b := 1.0, 2.0
	got := model.LogSumExpForTest(a, b)
	want := math.Log(math.Exp(a) + math.Exp(b))
	if math.Abs(got-want) > eps {
		t.Errorf("logSumExp(%v, %v) = %v, want %v", a, b, got, want)
	}
}

func TestLogSumExpSymmetric(t *testing.T) {
	a, b := 1.5, 2.5
	if model.LogSumExpForTest(a, b) != model.LogSumExpForTest(b, a) {
		t.Error("logSumExp non symétrique")
	}
}

func TestLogSumExpSliceEmpty(t *testing.T) {
	got := model.LogSumExpSliceForTest(nil)
	if !math.IsInf(got, -1) {
		t.Errorf("logSumExpSlice([]) = %v, want -Inf", got)
	}
}

func TestLogSumExpSliceAllInf(t *testing.T) {
	vals := []float64{math.Inf(-1), math.Inf(-1)}
	got := model.LogSumExpSliceForTest(vals)
	if !math.IsInf(got, -1) {
		t.Errorf("logSumExpSlice([-Inf, -Inf]) = %v, want -Inf", got)
	}
}

func TestLogSumExpSliceNumericalStability(t *testing.T) {
	// 1000 valeurs de -500 : sans le max-trick, exp(-500)=0 → sum=0 → log(0)=-Inf
	// Avec max-trick : max=-500, sum=1000*exp(0)=1000, result=-500+log(1000)
	vals := make([]float64, 1000)
	for i := range vals {
		vals[i] = -500.0
	}
	got := model.LogSumExpSliceForTest(vals)
	want := -500.0 + math.Log(1000)
	if math.Abs(got-want) > eps {
		t.Errorf("logSumExpSlice numerique = %v, want %v", got, want)
	}
}

func TestLogSumExpSliceConsistentWithBinary(t *testing.T) {
	// logSumExpSlice([a, b]) doit correspondre à logSumExp(a, b)
	a, b := 1.2, 3.4
	s := model.LogSumExpSliceForTest([]float64{a, b})
	d := model.LogSumExpForTest(a, b)
	if math.Abs(s-d) > eps {
		t.Errorf("incohérence slice vs binary : %v vs %v", s, d)
	}
}
