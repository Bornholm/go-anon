package model

import "math"

// logSumExp calcule log(exp(a) + exp(b)) de manière numériquement stable.
// Évite les underflows pour les grandes valeurs négatives et overflows pour
// les grandes valeurs positives, en soustrayant le max avant l'exponentiation.
func logSumExp(a, b float64) float64 {
	if math.IsInf(a, -1) {
		return b
	}
	if math.IsInf(b, -1) {
		return a
	}
	if a > b {
		return a + math.Log1p(math.Exp(b-a))
	}
	return b + math.Log1p(math.Exp(a-b))
}

// logSumExpSlice calcule log(Σ exp(vals[i])) de manière stable via le max-trick.
// Pour vals vide ou tout à -Inf, retourne -Inf.
func logSumExpSlice(vals []float64) float64 {
	if len(vals) == 0 {
		return math.Inf(-1)
	}

	max := math.Inf(-1)
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	if math.IsInf(max, -1) {
		return math.Inf(-1)
	}

	sum := 0.0
	for _, v := range vals {
		sum += math.Exp(v - max)
	}
	return max + math.Log(sum)
}
