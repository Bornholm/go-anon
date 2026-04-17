package model

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
	"sync"

	"github.com/bornholm/go-anon/pkg/corpus"
)

// CRF implémente un Conditional Random Field linéaire.
// Il associe une séquence de vecteurs de features à une séquence de labels NER.
type CRF struct {
	Labels     []string       // labels triés : ["B-LOC", "B-PER", "O", ...]
	LabelIndex map[string]int // lookup inverse

	Weights      *SparseWeights // poids d'émission (feature × label)
	Transition   [][]float64    // poids de transition [prev][next]
	transitionMu sync.Mutex     // protège Transition lors des mises à jour parallèles

	L2Lambda   float64       // coefficient de régularisation L2 (copié depuis TrainConfig)
	FeatureCfg FeatureConfig // configuration du pipeline d'extraction (remplie avant Save)
}

// SparseWeights stocke les poids d'émission de façon sparse.
// Deux représentations internes selon le mode :
//   - Mode training (W != nil) : map[uint64]float64, O(1) R/W.
//   - Mode inférence (W == nil) : Keys []uint64 (trié) + Vals []float32, O(log N) RO, ~12 octets/entrée.
//
// Le RWMutex protège les accès concurrents.
type SparseWeights struct {
	mu   sync.RWMutex
	W    map[uint64]float64 // nil après Compact()
	Keys []uint64           // trié, peuplé après Compact()
	Vals []float32          // parallèle à Keys
}

// Score retourne le score d'émission pour un ensemble de features et un label donné.
// Utilise la map (mode training) ou la recherche binaire (mode inférence).
func (sw *SparseWeights) Score(features map[string]float64, labelIdx int) float64 {
	sw.mu.RLock()
	defer sw.mu.RUnlock()

	var score float64
	if sw.W != nil {
		for feat, val := range features {
			key := hashFeatureLabel(feat, labelIdx)
			if w, ok := sw.W[key]; ok {
				score += w * val
			}
		}
	} else {
		n := len(sw.Keys)
		for feat, val := range features {
			key := hashFeatureLabel(feat, labelIdx)
			i := sort.Search(n, func(j int) bool { return sw.Keys[j] >= key })
			if i < n && sw.Keys[i] == key {
				score += float64(sw.Vals[i]) * val
			}
		}
	}
	return score
}

// Prune supprime les entrées dont la valeur absolue est inférieure à threshold.
// Doit être appelé en mode training (W != nil), avant Save() ou Compact().
// Retourne le nombre d'entrées supprimées.
func (sw *SparseWeights) Prune(threshold float64) int {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	removed := 0
	for k, v := range sw.W {
		if v < threshold && v > -threshold {
			delete(sw.W, k)
			removed++
		}
	}
	return removed
}

// Len retourne le nombre d'entrées dans les poids, quelle que soit la représentation.
func (sw *SparseWeights) Len() int {
	sw.mu.RLock()
	defer sw.mu.RUnlock()
	if sw.W != nil {
		return len(sw.W)
	}
	return len(sw.Keys)
}

// Compact convertit la représentation map en tableaux triés (Keys + Vals float32),
// puis libère la map. Après appel, le modèle est en lecture seule.
// Réduit l'empreinte mémoire de ~40 octets/entrée → ~12 octets/entrée.
func (sw *SparseWeights) Compact() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.W == nil {
		return
	}
	n := len(sw.W)
	keys := make([]uint64, 0, n)
	for k := range sw.W {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	vals := make([]float32, n)
	for i, k := range keys {
		vals[i] = float32(sw.W[k])
	}
	sw.Keys = keys
	sw.Vals = vals
	sw.W = nil
}

// hashFeatureLabel calcule une clé uint64 pour la paire (feature, labelIdx)
// via FNV-1a. Séparateur 0xFF pour éviter les collisions entre ("ab", 1) et ("a", 21).
func hashFeatureLabel(feat string, labelIdx int) uint64 {
	h := fnv.New64a()
	h.Write([]byte(feat))
	h.Write([]byte{0xFF})
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(labelIdx))
	h.Write(b[:])
	return h.Sum64()
}

// newCRF crée un CRF avec les labels donnés.
// Les poids sont initialisés à zéro, et les transitions BIO/BIOES invalides
// sont initialisées à -1e9 (≈ -∞ en log-espace) pour forcer le Viterbi
// à ne jamais produire de séquences structurellement incorrectes.
func newCRF(labels []string) *CRF {
	L := len(labels)
	labelIndex := make(map[string]int, L)
	for i, l := range labels {
		labelIndex[l] = i
	}

	transition := make([][]float64, L)
	for i := range transition {
		transition[i] = make([]float64, L)
	}

	crf := &CRF{
		Labels:     labels,
		LabelIndex: labelIndex,
		Weights:    &SparseWeights{W: make(map[uint64]float64)},
		Transition: transition,
	}
	applyBIOConstraints(crf)
	return crf
}

// applyBIOConstraints initialise à -1e9 toutes les transitions BIO/BIOES invalides.
// Le schéma (BIO vs BIOES) est détecté automatiquement depuis les labels.
// Les transitions invalides sont ignorées dans sgdUpdate (threshold -1e8),
// garantissant que le Viterbi ne produit jamais de séquences incohérentes.
func applyBIOConstraints(crf *CRF) {
	// Détection automatique du schéma
	isBIOES := false
	for _, l := range crf.Labels {
		p := corpus.TagPrefix(l)
		if p == "E" || p == "S" {
			isBIOES = true
			break
		}
	}

	for prev, prevLabel := range crf.Labels {
		for next, nextLabel := range crf.Labels {
			if !isBIOTransitionValid(prevLabel, nextLabel, isBIOES) {
				crf.Transition[prev][next] = -1e9
			}
		}
	}
}

// isBIOTransitionValid retourne true si la transition prev→next est valide
// selon le schéma BIO ou BIOES.
//
// BIO :
//   - O     → O, B-X          (pas I-X)
//   - B-X   → O, B-Y, I-X     (pas I-Y avec Y≠X)
//   - I-X   → O, B-Y, I-X     (pas I-Y avec Y≠X)
//
// BIOES :
//   - O     → O, B-X, S-X     (pas I-X, E-X)
//   - B-X   → I-X, E-X        (uniquement même type)
//   - I-X   → I-X, E-X        (uniquement même type)
//   - E-X   → O, B-Y, S-Y     (pas I-Y, E-Y)
//   - S-X   → O, B-Y, S-Y     (pas I-Y, E-Y)
func isBIOTransitionValid(prev, next string, isBIOES bool) bool {
	prevPrefix := corpus.TagPrefix(prev)
	prevType := corpus.TagEntity(prev)
	nextPrefix := corpus.TagPrefix(next)
	nextType := corpus.TagEntity(next)

	if isBIOES {
		switch prevPrefix {
		case "O":
			return nextPrefix == "O" || nextPrefix == "B" || nextPrefix == "S"
		case "B":
			return (nextPrefix == "I" || nextPrefix == "E") && nextType == prevType
		case "I":
			return (nextPrefix == "I" || nextPrefix == "E") && nextType == prevType
		case "E", "S":
			return nextPrefix == "O" || nextPrefix == "B" || nextPrefix == "S"
		default:
			return true
		}
	}

	// Schéma BIO
	switch prevPrefix {
	case "O":
		return nextPrefix == "O" || nextPrefix == "B"
	case "B", "I":
		return nextPrefix == "O" || nextPrefix == "B" || (nextPrefix == "I" && nextType == prevType)
	default:
		return true
	}
}

// LabelsWithIndex retourne la liste des labels et leur indice.
func (crf *CRF) LabelsWithIndex() ([]string, map[string]int) {
	return crf.Labels, crf.LabelIndex
}

// PredictMarginals retourne les scores de confiance (probabilités marginales)
// pour chaque token et chaque label.
// scores[t][l] = P(y_t = l | x) ≈ exp(alpha[t][l] + beta[t][l] - Z)
func (crf *CRF) PredictMarginals(feats []map[string]float64) [][]float64 {
	emissions := computeEmissions(crf, feats)
	alpha, beta, Z := forwardBackward(crf, emissions)

	n := len(feats)
	L := len(crf.Labels)
	scores := make([][]float64, n)

	for t := 0; t < n; t++ {
		scores[t] = make([]float64, L)
		for l := 0; l < L; l++ {
			// P(y_t = l | x) = exp(alpha + beta - Z) avec log-space
			// => = exp(alpha + beta - Z) = exp(alpha - Z + beta) = exp(logprob)
			logProb := alpha[t][l] + beta[t][l] - Z
			scores[t][l] = math.Exp(logProb)
		}
	}
	return scores
}

// NewCRFForTestAPI expose newCRF pour les tests d'intégration ner.
func NewCRFForTestAPI(labels []string) *CRF {
	return newCRF(labels)
}

// collectLabels extrait et trie tous les tags NER uniques d'un corpus.
func collectLabels(sentences []corpus.Sentence) []string {
	seen := make(map[string]struct{})
	for _, sent := range sentences {
		for _, tok := range sent {
			seen[tok.Tag] = struct{}{}
		}
	}
	labels := make([]string, 0, len(seen))
	for l := range seen {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	return labels
}
