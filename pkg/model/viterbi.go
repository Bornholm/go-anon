package model

import "math"

// Predict retourne la séquence de labels optimale pour featureSequence
// en utilisant l'algorithme de Viterbi (programmation dynamique).
//
// Complexité : O(n × L²) où n = longueur de la phrase, L = nombre de labels.
// Retourne nil si featureSequence est vide.
func (crf *CRF) Predict(featureSequence []map[string]float64) []string {
	n := len(featureSequence)
	if n == 0 {
		return nil
	}
	L := len(crf.Labels)

	// dp[t][l] = meilleur score log pour atteindre le label l au temps t.
	// bp[t][l] = index du label précédent optimal (backpointer).
	dp := make([][]float64, n)
	bp := make([][]int, n)
	for t := range dp {
		dp[t] = make([]float64, L)
		bp[t] = make([]int, L)
	}

	// Initialisation t=0 : uniquement score d'émission (pas de transition initiale).
	for l := 0; l < L; l++ {
		dp[0][l] = crf.Weights.Score(featureSequence[0], l)
	}

	// Récurrence t=1..n-1.
	for t := 1; t < n; t++ {
		for l := 0; l < L; l++ {
			emission := crf.Weights.Score(featureSequence[t], l)
			bestScore := math.Inf(-1)
			bestPrev := 0

			for p := 0; p < L; p++ {
				score := dp[t-1][p] + crf.Transition[p][l]
				if score > bestScore {
					bestScore = score
					bestPrev = p
				}
			}

			dp[t][l] = bestScore + emission
			bp[t][l] = bestPrev
		}
	}

	// Backtracking depuis le meilleur dernier label.
	labels := make([]string, n)
	bestLast := argmax(dp[n-1])
	labels[n-1] = crf.Labels[bestLast]

	for t := n - 2; t >= 0; t-- {
		bestLast = bp[t+1][bestLast]
		labels[t] = crf.Labels[bestLast]
	}

	return labels
}

// argmax retourne l'index de la valeur maximale dans vals.
// En cas d'égalité, retourne le premier index.
func argmax(vals []float64) int {
	best := 0
	for i := 1; i < len(vals); i++ {
		if vals[i] > vals[best] {
			best = i
		}
	}
	return best
}
