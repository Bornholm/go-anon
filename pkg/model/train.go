package model

import (
	"log"
	"math"
	"math/rand"
	"sync"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
)

// TrainConfig contient les hyperparamètres d'entraînement.
type TrainConfig struct {
	Epochs       int     // nombre d'époques maximum
	LearningRate float64 // taux d'apprentissage SGD initial
	LRDecay      float64 // facteur multiplicatif par époque (0 = pas de decay, ex: 0.95)
	L2Lambda     float64 // coefficient de régularisation L2
	Shuffle      bool    // mélanger les phrases à chaque époque
	EarlyStop    int     // arrêt anticipé si pas d'amélioration après N époques (0 = désactivé)
	NumWorkers   int     // nombre de goroutines parallèles (0 ou 1 = séquentiel)
	BatchSize    int     // taille du mini-batch (1 = SGD classique)
	DropoutRate  float64 // taux de dropout des features (0 = désactivé)
	Optimizer    string  // optimizer type: "sgd", "momentum", "adam"
	Momentum     float64 // momentum coefficient (for momentum optimizer)
	Beta1        float64 // Adam beta1
	Beta2        float64 // Adam beta2
}

// Metrics contient les métriques d'évaluation du modèle.
// En Phase 4, seule l'accuracy token-level est calculée.
// La Phase 7 ajoutera le F1 entity-level (span-based).
type Metrics struct {
	Accuracy float64 // accuracy token-level
	F1       float64 // alias Accuracy pour l'early stopping
}

// Trainer entraîne un modèle CRF via SGD avec Forward-Backward.
type Trainer struct {
	Config    TrainConfig
	Extractor *features.FeatureExtractor
	rng       *rand.Rand
	rngMu     sync.Mutex // Protects rng for thread-safe access
	// Optimizer state
	optType        string
	mVelEmission   map[uint64]float64
	mVelTransition [][]float64
	vEmission      map[uint64]float64
	vTransition    [][]float64
	iter           int
}

// Train entraîne le CRF sur train, avec évaluation optionnelle sur dev.
// Si dev != nil et EarlyStop > 0, retourne le meilleur modèle selon l'accuracy dev.
// Si dev == nil, retourne le modèle après Epochs époques.
func (tr *Trainer) Train(train, dev []corpus.Sentence) (*CRF, error) {
	crf := newCRF(collectLabels(train))
	crf.L2Lambda = tr.Config.L2Lambda

	tr.rng = rand.New(rand.NewSource(12345))

	// Initialize optimizer state
	tr.optType = tr.Config.Optimizer
	if tr.optType == "" {
		tr.optType = "sgd"
	}
	L := len(crf.Labels)
	tr.mVelEmission = make(map[uint64]float64)
	tr.vEmission = make(map[uint64]float64)
	tr.mVelTransition = make([][]float64, L)
	tr.vTransition = make([][]float64, L)
	for i := 0; i < L; i++ {
		tr.mVelTransition[i] = make([]float64, L)
		tr.vTransition[i] = make([]float64, L)
	}
	tr.iter = 0

	var bestCRF *CRF
	bestF1 := -1.0
	noImprove := 0

	workers := tr.Config.NumWorkers
	if workers < 1 {
		workers = 1
	}

	batchSize := tr.Config.BatchSize
	if batchSize < 1 {
		batchSize = 1
	}

	for epoch := 0; epoch < tr.Config.Epochs; epoch++ {
		if tr.Config.Shuffle {
			shuffle(train)
		}

		// Taux d'apprentissage avec decay exponentiel optionnel
		epochLR := tr.Config.LearningRate
		if tr.Config.LRDecay > 0 {
			epochLR *= math.Pow(tr.Config.LRDecay, float64(epoch))
		}

		if workers > 1 {
			tr.trainParallel(crf, train, workers, epochLR)
		} else {
			// Mini-batch accumulation
			var batchFeats [][]map[string]float64
			var batchEmissions [][][]float64
			var batchGoldIndices [][]int

			for i, sent := range train {
				if len(sent) == 0 {
					continue
				}
				feats := tr.extractFeatures(sent)
				emissions := computeEmissions(crf, feats)
				goldIndices := goldLabelIndices(crf, sent)

				batchFeats = append(batchFeats, feats)
				batchEmissions = append(batchEmissions, emissions)
				batchGoldIndices = append(batchGoldIndices, goldIndices)

				// Appliquer les mises à jour après chaque mini-batch
				if len(batchFeats) >= batchSize || i == len(train)-1 {
					tr.miniBatchUpdate(crf, batchFeats, batchEmissions, batchGoldIndices, epochLR)
					batchFeats = nil
					batchEmissions = nil
					batchGoldIndices = nil
				}
			}
		}

		if dev != nil {
			m := tr.evaluate(crf, dev)
			improved := ""
			if m.F1 > bestF1 {
				bestF1 = m.F1
				bestCRF = copyCRF(crf)
				noImprove = 0
				improved = " ✓ meilleur"
			} else {
				noImprove++
			}
			log.Printf("époque %d/%d : F1=%.4f (lr=%.6f, noImprove=%d)%s",
				epoch+1, tr.Config.Epochs, m.F1, epochLR, noImprove, improved)
			if tr.Config.EarlyStop > 0 && noImprove >= tr.Config.EarlyStop {
				log.Printf("early stopping à l'époque %d (pas d'amélioration depuis %d époques)", epoch+1, tr.Config.EarlyStop)
				break
			}
		}
	}

	if bestCRF != nil {
		return bestCRF, nil
	}
	return crf, nil
}

// sgdUpdate effectue une mise à jour SGD sur une phrase.
// Retourne la log-vraisemblance négative (loss) calculée AVANT la mise à jour.
//
// Gradient pour les poids d'émission (feature feat, label l) :
//
//	∂L/∂w = observed(feat,l) - expected(feat,l) - λ·w
//
// Gradient pour les poids de transition (prev→next) :
//
//	∂L/∂T[prev][next] = observed(prev,next) - expected_joint(prev,next) - λ·T[prev][next]
//
// Les transitions initialisées à -1e9 (contraintes BIO invalides) sont ignorées
// (threshold -1e8) pour ne pas dériver vers 0 sous l'effet de la régularisation L2.
func (tr *Trainer) sgdUpdate(
	crf *CRF,
	feats []map[string]float64,
	emissions [][]float64,
	goldIndices []int,
	alpha, beta [][]float64,
	Z float64,
	lr float64,
) float64 {
	n := len(feats)
	L := len(crf.Labels)
	l2 := tr.Config.L2Lambda

	// Loss pré-update : Z - gold_score.
	goldScore := 0.0
	for t := 0; t < n; t++ {
		goldScore += emissions[t][goldIndices[t]]
	}
	for t := 1; t < n; t++ {
		goldScore += crf.Transition[goldIndices[t-1]][goldIndices[t]]
	}
	loss := Z - goldScore

	// Probabilités marginales unigramme : P(y_t=l|x) = exp(alpha[t][l] + beta[t][l] - Z).
	marginals := make([][]float64, n)
	for t := 0; t < n; t++ {
		marginals[t] = make([]float64, L)
		for l := 0; l < L; l++ {
			marginals[t][l] = math.Exp(alpha[t][l] + beta[t][l] - Z)
		}
	}

	// --- Mise à jour des poids d'émission ---
	// Verrouillage en écriture pour toute la durée de la mise à jour de la phrase.
	crf.Weights.mu.Lock()
	for t := 0; t < n; t++ {
		goldL := goldIndices[t]
		for feat, val := range feats[t] {
			for l := 0; l < L; l++ {
				observed := 0.0
				if l == goldL {
					observed = val
				}
				expected := marginals[t][l] * val
				key := hashFeatureLabel(feat, l)
				w := crf.Weights.W[key]
				w += lr * (observed - expected - l2*w)
				if w == 0 {
					delete(crf.Weights.W, key)
				} else {
					crf.Weights.W[key] = w
				}
			}
		}
	}
	crf.Weights.mu.Unlock()

	// --- Mise à jour des poids de transition ---
	// Marginals bigramme : P(y_{t-1}=prev, y_t=next|x)
	// = exp(alpha[t-1][prev] + Transition[prev][next] + emissions[t][next] + beta[t][next] - Z)
	// Les transitions contraintes (≤ -1e8) sont ignorées pour ne pas dériver vers 0.
	crf.transitionMu.Lock()
	for t := 1; t < n; t++ {
		for prev := 0; prev < L; prev++ {
			for next := 0; next < L; next++ {
				if crf.Transition[prev][next] <= -1e8 {
					continue // transition BIO invalide : ne pas modifier
				}
				joint := math.Exp(
					alpha[t-1][prev] + crf.Transition[prev][next] +
						emissions[t][next] + beta[t][next] - Z,
				)
				observed := 0.0
				if goldIndices[t-1] == prev && goldIndices[t] == next {
					observed = 1.0
				}
				crf.Transition[prev][next] += lr * (observed - joint - l2*crf.Transition[prev][next])
			}
		}
	}
	crf.transitionMu.Unlock()

	return loss
}

// applyOptimizerUpdate applique les mises à jour selon le type d'optimiseur configuré.
// Cette fonction est appelée après le calcul du gradient via forwardBackward.
func (tr *Trainer) applyOptimizerUpdate(
	crf *CRF,
	gradE map[uint64]float64,
	gradT [][]float64,
	lr float64,
) {
	l2 := tr.Config.L2Lambda

	switch tr.optType {
	case "momentum":
		tr.applyMomentumUpdate(crf, gradE, gradT, lr, l2)
	case "adam":
		tr.applyAdamUpdate(crf, gradE, gradT, lr, l2)
	default: // sgd
		tr.applySGDUpdate(crf, gradE, gradT, lr, l2)
	}
}

func (tr *Trainer) applySGDUpdate(crf *CRF, gradE map[uint64]float64, gradT [][]float64, lr, l2 float64) {
	for key, grad := range gradE {
		w := crf.Weights.W[key]
		w += lr * (grad - l2*w)
		if math.Abs(w) < 1e-10 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			if crf.Transition[i][j] <= -1e8 {
				continue
			}
			w := crf.Transition[i][j]
			w += lr * (gradT[i][j] - l2*w)
			crf.Transition[i][j] = w
		}
	}
}

func (tr *Trainer) applyMomentumUpdate(crf *CRF, gradE map[uint64]float64, gradT [][]float64, lr, l2 float64) {
	mom := tr.Config.Momentum
	if mom <= 0 {
		mom = 0.9
	}

	for key, grad := range gradE {
		w := crf.Weights.W[key]
		vel := tr.mVelEmission[key]
		vel = mom*vel - lr*(grad-l2*w)
		tr.mVelEmission[key] = vel
		w += vel
		if math.Abs(w) < 1e-10 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			if crf.Transition[i][j] <= -1e8 {
				continue
			}
			w := crf.Transition[i][j]
			vel := tr.mVelTransition[i][j]
			vel = mom*vel - lr*(gradT[i][j]-l2*w)
			tr.mVelTransition[i][j] = vel
			w += vel
			crf.Transition[i][j] = w
		}
	}
}

func (tr *Trainer) applyAdamUpdate(crf *CRF, gradE map[uint64]float64, gradT [][]float64, lr, l2 float64) {
	beta1 := tr.Config.Beta1
	beta2 := tr.Config.Beta2
	eps := 1e-8
	if beta1 <= 0 {
		beta1 = 0.9
	}
	if beta2 <= 0 {
		beta2 = 0.999
	}

	tr.iter++

	for key, grad := range gradE {
		w := crf.Weights.W[key]
		m := tr.mVelEmission[key]
		m = beta1*m + (1-beta1)*grad
		tr.mVelEmission[key] = m

		v := tr.vEmission[key]
		v = beta2*v + (1-beta2)*grad*grad
		tr.vEmission[key] = v

		mHat := m / (1 - math.Pow(beta1, float64(tr.iter)))
		vHat := v / (1 - math.Pow(beta2, float64(tr.iter)))

		w += lr * (mHat/(math.Sqrt(vHat)+eps) - l2*w)

		if math.Abs(w) < 1e-10 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			if crf.Transition[i][j] <= -1e8 {
				continue
			}
			w := crf.Transition[i][j]
			grad := gradT[i][j]

			m := tr.mVelTransition[i][j]
			m = beta1*m + (1-beta1)*grad
			tr.mVelTransition[i][j] = m

			v := tr.vTransition[i][j]
			v = beta2*v + (1-beta2)*grad*grad
			tr.vTransition[i][j] = v

			mHat := m / (1 - math.Pow(beta1, float64(tr.iter)))
			vHat := v / (1 - math.Pow(beta2, float64(tr.iter)))

			w += lr * (mHat/(math.Sqrt(vHat)+eps) - l2*w)
			crf.Transition[i][j] = w
		}
	}
}

// trainParallel effectue une époque d'entraînement Hogwild! :
// le corpus est partitionné en n sous-listes, chaque goroutine applique
// des mises à jour SGD indépendantes sur le modèle partagé.
// Les accès concurrents aux poids sont protégés par les mutex existants.
func (tr *Trainer) trainParallel(crf *CRF, train []corpus.Sentence, n int, lr float64) {
	size := len(train)
	if size == 0 || n <= 1 {
		return
	}

	chunkSize := (size + n - 1) / n
	var wg sync.WaitGroup

	for w := 0; w < n; w++ {
		start := w * chunkSize
		if start >= size {
			break
		}
		end := start + chunkSize
		if end > size {
			end = size
		}
		chunk := train[start:end]

		wg.Add(1)
		go func(sentences []corpus.Sentence) {
			defer wg.Done()
			for _, sent := range sentences {
				if len(sent) == 0 {
					continue
				}
				feats := tr.extractFeatures(sent)
				emissions := computeEmissions(crf, feats)
				alpha, beta, Z := forwardBackward(crf, emissions)
				goldIndices := goldLabelIndices(crf, sent)
				tr.sgdUpdate(crf, feats, emissions, goldIndices, alpha, beta, Z, lr)
			}
		}(chunk)
	}

	wg.Wait()
}

// evaluate calcule le F1 entity-level sur un ensemble de données.
// C'est l'objectif réel de l'entraînement : utilisé pour l'early stopping.
func (tr *Trainer) evaluate(crf *CRF, sentences []corpus.Sentence) Metrics {
	tp, fp, fn := 0, 0, 0
	for _, sent := range sentences {
		if len(sent) == 0 {
			continue
		}
		feats := tr.extractFeatures(sent)
		predicted := crf.Predict(feats)

		goldLabels := make([]string, len(sent))
		for i, tok := range sent {
			goldLabels[i] = tok.Tag
		}
		goldSpans := extractSpans(goldLabels)
		predSpans := extractSpans(predicted)

		for _, g := range goldSpans {
			if containsSpan(predSpans, g) {
				tp++
			} else {
				fn++
			}
		}
		for _, p := range predSpans {
			if !containsSpan(goldSpans, p) {
				fp++
			}
		}
	}

	if tp+fp == 0 && tp+fn == 0 {
		return Metrics{}
	}
	var prec, rec float64
	if tp+fp > 0 {
		prec = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		rec = float64(tp) / float64(tp+fn)
	}
	var f1 float64
	if prec+rec > 0 {
		f1 = 2 * prec * rec / (prec + rec)
	}
	return Metrics{Accuracy: prec, F1: f1}
}

// entitySpan représente un span d'entité nommée par ses indices token (start, end inclusif) et son type.
type entitySpan struct {
	start, end int
	typ        string
}

// extractSpans extrait les spans d'entités d'une séquence de labels BIO ou BIOES.
// Utilise les indices token (entiers) comme positions.
func extractSpans(labels []string) []entitySpan {
	var spans []entitySpan
	inSpan := false
	spanStart := 0
	spanType := ""

	flush := func(end int) {
		if inSpan {
			spans = append(spans, entitySpan{spanStart, end - 1, spanType})
			inSpan = false
		}
	}

	for i, label := range labels {
		prefix := corpus.TagPrefix(label)
		typ := corpus.TagEntity(label)

		switch prefix {
		case "B":
			flush(i)
			inSpan = true
			spanStart = i
			spanType = typ
		case "S":
			flush(i)
			spans = append(spans, entitySpan{i, i, typ})
		case "I":
			if !inSpan || typ != spanType {
				flush(i)
				// I sans B cohérent : ignoré
			}
		case "E":
			if inSpan && typ == spanType {
				spans = append(spans, entitySpan{spanStart, i, typ})
				inSpan = false
			} else {
				flush(i)
				spans = append(spans, entitySpan{i, i, typ})
			}
		default: // O
			flush(i)
		}
	}
	flush(len(labels))
	return spans
}

// containsSpan retourne true si target est présent dans spans.
func containsSpan(spans []entitySpan, target entitySpan) bool {
	for _, s := range spans {
		if s == target {
			return true
		}
	}
	return false
}

// --- Fonctions privées ---

// extractFeatures produit la séquence de features pour une phrase annotée.
func (tr *Trainer) extractFeatures(sent corpus.Sentence) []map[string]float64 {
	words := extractWords(sent)
	feats := make([]map[string]float64, len(sent))
	for i := range sent {
		feats[i] = tr.Extractor.Features(words, i)
	}
	if tr.Config.DropoutRate > 0 && tr.rng != nil {
		feats = applyFeatureDropout(feats, tr.Config.DropoutRate, tr.rng, &tr.rngMu)
	}
	return feats
}

// goldLabelIndices convertit les tags gold d'une phrase en indices de labels.
func extractWords(sent corpus.Sentence) []string {
	words := make([]string, len(sent))
	for i, tok := range sent {
		words[i] = tok.Word
	}
	return words
}

func applyFeatureDropout(feats []map[string]float64, rate float64, rng *rand.Rand, rngMu *sync.Mutex) []map[string]float64 {
	if rate <= 0 || rate >= 1 {
		return feats
	}
	scale := 1.0 / (1.0 - rate)
	result := make([]map[string]float64, len(feats))
	for i, f := range feats {
		dropped := make(map[string]float64, len(f))
		rngMu.Lock()
		for k, v := range f {
			if rng.Float64() >= rate {
				dropped[k] = v * scale
			}
		}
		rngMu.Unlock()
		result[i] = dropped
	}
	return result
}

// goldLabelIndices convertit les tags gold d'une phrase en indices de labels.
// Les tags inconnus sont mappés sur l'index 0.
func goldLabelIndices(crf *CRF, sent corpus.Sentence) []int {
	indices := make([]int, len(sent))
	for i, tok := range sent {
		if idx, ok := crf.LabelIndex[tok.Tag]; ok {
			indices[i] = idx
		}
		// idx reste 0 pour les labels inconnus (fallback sûr)
	}
	return indices
}

// shuffle mélange les phrases aléatoirement (Fisher-Yates).
func shuffle(sentences []corpus.Sentence) {
	rand.Shuffle(len(sentences), func(i, j int) {
		sentences[i], sentences[j] = sentences[j], sentences[i]
	})
}

// miniBatchUpdate applique les gradients cumulés pour un mini-batch.
// Accumule les gradients puis applique une seule mise à jour.
func (tr *Trainer) miniBatchUpdate(
	crf *CRF,
	batchFeats [][]map[string]float64,
	batchEmissions [][][]float64,
	batchGoldIndices [][]int,
	lr float64,
) {
	if len(batchFeats) == 0 {
		return
	}

	l2 := tr.Config.L2Lambda
	L := len(crf.Labels)

	// Accumuler les gradients pour chaque phrase du batch
	weightGradients := make(map[uint64]float64)
	transitionGradients := make(map[[2]int]float64)

	for b := 0; b < len(batchFeats); b++ {
		feats := batchFeats[b]
		emissions := batchEmissions[b]
		goldIndices := batchGoldIndices[b]

		alpha, beta, Z := forwardBackward(crf, emissions)
		n := len(feats)

		// Gradients d'émission
		for t := 0; t < n; t++ {
			goldL := goldIndices[t]
			for feat, val := range feats[t] {
				for l := 0; l < L; l++ {
					observed := 0.0
					if l == goldL {
						observed = val
					}
					expected := math.Exp(alpha[t][l]+beta[t][l]-Z) * val
					key := hashFeatureLabel(feat, l)
					weightGradients[key] += (observed - expected) / float64(len(batchFeats))
				}
			}
		}

		// Gradients de transition
		for t := 1; t < n; t++ {
			for prev := 0; prev < L; prev++ {
				for next := 0; next < L; next++ {
					if crf.Transition[prev][next] < -1e8 {
						continue
					}
					joint := math.Exp(
						alpha[t-1][prev] + crf.Transition[prev][next] +
							emissions[t][next] + beta[t][next] - Z,
					)
					observed := 0.0
					if goldIndices[t-1] == prev && goldIndices[t] == next {
						observed = 1.0
					}
					grad := (observed - joint) / float64(len(batchFeats))
					key := [2]int{prev, next}
					transitionGradients[key] += grad
				}
			}
		}
	}

	// Appliquer les gradients accumulés
	crf.Weights.mu.Lock()
	for key, grad := range weightGradients {
		w := crf.Weights.W[key]
		w += lr * (grad - l2*w)
		if w == 0 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}
	crf.Weights.mu.Unlock()

	crf.transitionMu.Lock()
	for key, grad := range transitionGradients {
		prev := key[0]
		next := key[1]
		crf.Transition[prev][next] += lr * (grad - l2*crf.Transition[prev][next])
	}
	crf.transitionMu.Unlock()
}
func copyCRF(src *CRF) *CRF {
	L := len(src.Labels)

	trans := make([][]float64, L)
	for i := range trans {
		trans[i] = make([]float64, L)
		copy(trans[i], src.Transition[i])
	}

	src.Weights.mu.RLock()
	var newWeights *SparseWeights
	if src.Weights.W != nil {
		w := make(map[uint64]float64, len(src.Weights.W))
		for k, v := range src.Weights.W {
			w[k] = v
		}
		newWeights = &SparseWeights{W: w}
	} else {
		keys := make([]uint64, len(src.Weights.Keys))
		vals := make([]float32, len(src.Weights.Vals))
		copy(keys, src.Weights.Keys)
		copy(vals, src.Weights.Vals)
		newWeights = &SparseWeights{Keys: keys, Vals: vals}
	}
	src.Weights.mu.RUnlock()

	labels := make([]string, L)
	copy(labels, src.Labels)
	labelIndex := make(map[string]int, L)
	for k, v := range src.LabelIndex {
		labelIndex[k] = v
	}

	return &CRF{
		Labels:     labels,
		LabelIndex: labelIndex,
		Weights:    newWeights,
		Transition: trans,
		L2Lambda:   src.L2Lambda,
	}
}
