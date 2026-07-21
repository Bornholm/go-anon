package model

import "math"

// computeEmissions pré-calcule les scores d'émission pour toute la phrase.
// emissions[t][l] = Score(feats[t], l).
// Factoriser ce calcul évite de le répéter dans forwardBackward et sgdUpdate.
func computeEmissions(crf *CRF, feats []map[string]float64) [][]float64 {
	n := len(feats)
	L := len(crf.Labels)
	emissions := make([][]float64, n)
	for t := range emissions {
		emissions[t] = make([]float64, L)
		crf.Weights.ScoreAll(feats[t], emissions[t])
	}
	return emissions
}

// forwardBackward calcule les probabilités forward (alpha) et backward (beta)
// en log-space, ainsi que la fonction de partition Z = log P(x).
//
// Définitions :
//   alpha[t][l] = log P(x[0..t], y_t = l)
//   beta[t][l]  = log P(x[t+1..T-1] | y_t = l)
//   Z           = logSumExp(alpha[n-1][:]) = log P(x)
//
// Les probabilités marginales se calculent ensuite via :
//   P(y_t = l | x) = exp(alpha[t][l] + beta[t][l] - Z)
//
// Travailler en log-space est **obligatoire** pour éviter les underflows
// numériques sur les longues phrases (exp de scores négatifs → 0 en float64).
func forwardBackward(crf *CRF, emissions [][]float64) (alpha, beta [][]float64, Z float64) {
	n := len(emissions)
	L := len(crf.Labels)

	// --- Forward ---
	alpha = make([][]float64, n)
	for t := range alpha {
		alpha[t] = make([]float64, L)
	}

	// t=0 : émission seulement (pas de transition initiale).
	for l := 0; l < L; l++ {
		alpha[0][l] = emissions[0][l]
	}

	// t=1..n-1 :
	// alpha[t][l] = emissions[t][l] + logSumExp_{prev}(alpha[t-1][prev] + Transition[prev][l])
	for t := 1; t < n; t++ {
		for l := 0; l < L; l++ {
			acc := math.Inf(-1)
			for prev := 0; prev < L; prev++ {
				v := alpha[t-1][prev] + crf.Transition[prev][l]
				acc = logSumExp(acc, v)
			}
			alpha[t][l] = acc + emissions[t][l]
		}
	}

	// --- Backward ---
	beta = make([][]float64, n)
	for t := range beta {
		beta[t] = make([]float64, L)
		// Initialisé à 0.0 = log(1) ✓ (valeur neutre pour la récurrence backward)
	}

	// t=n-2..0 :
	// beta[t][l] = logSumExp_{next}(beta[t+1][next] + emissions[t+1][next] + Transition[l][next])
	for t := n - 2; t >= 0; t-- {
		for l := 0; l < L; l++ {
			acc := math.Inf(-1)
			for next := 0; next < L; next++ {
				v := beta[t+1][next] + emissions[t+1][next] + crf.Transition[l][next]
				acc = logSumExp(acc, v)
			}
			beta[t][l] = acc
		}
	}

	// Z = log P(x) = logSumExp sur tous les labels au dernier token.
	Z = logSumExpSlice(alpha[n-1])
	return
}
