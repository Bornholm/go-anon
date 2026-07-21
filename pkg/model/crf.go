package model

import (
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

	// featureBases contient les hachés de base (hashFeatureBase) de toutes les
	// features vues à l'entraînement. Rempli par le Trainer (ou au chargement
	// mutable d'un modèle v3), il permet à Save d'émettre le format groupé v3.
	// Vide pour un modèle v1/v2 chargé : les clés sont des hachés à sens unique.
	featureBases []uint64
}

// SparseWeights stocke les poids d'émission de façon sparse.
// Trois représentations internes selon le mode :
//   - Mode training (W != nil) : map[uint64]float64, O(1) R/W, sous RWMutex.
//   - Mode inférence plat (Keys != nil) : Keys []uint64 (trié) + Vals []float32,
//     une entrée par paire (feature, label) — modèles v1/v2.
//   - Mode inférence groupé (BaseKeys != nil) : une entrée par feature, bloc
//     contigu de BlockL poids (un par label) — modèles v3. Une seule recherche
//     binaire par feature au lieu de L.
//
// Les représentations d'inférence sont immuables et lues sans verrou :
// leur construction (Compact ou chargement) doit précéder toute lecture concurrente.
type SparseWeights struct {
	mu   sync.RWMutex
	W    map[uint64]float64 // nil après Compact()
	Keys []uint64           // trié, peuplé après Compact()
	Vals []float32          // parallèle à Keys

	BaseKeys  []uint64  // v3 : hachés de base des features, triés
	BlockVals []float32 // v3 : blocs de BlockL poids, parallèles à BaseKeys
	BlockL    int       // v3 : taille de bloc = nombre de labels
}

// Score retourne le score d'émission pour un ensemble de features et un label donné.
// Utilise la map (mode training, sous RLock) ou la recherche binaire
// (mode inférence, sans verrou — cf. ScoreAll).
func (sw *SparseWeights) Score(features map[string]float64, labelIdx int) float64 {
	var score float64
	if sw.W != nil {
		sw.mu.RLock()
		defer sw.mu.RUnlock()
		for feat, val := range features {
			key := hashFeatureLabel(feat, labelIdx)
			if w, ok := sw.W[key]; ok {
				score += w * val
			}
		}
		return score
	}
	if sw.BaseKeys != nil {
		keys, blocks, L := sw.BaseKeys, sw.BlockVals, sw.BlockL
		n := len(keys)
		for feat, val := range features {
			base := hashFeatureBase(feat)
			i := sort.Search(n, func(j int) bool { return keys[j] >= base })
			if i < n && keys[i] == base {
				score += float64(blocks[i*L+labelIdx]) * val
			}
		}
		return score
	}
	keys, vals := sw.Keys, sw.Vals
	n := len(keys)
	for feat, val := range features {
		key := hashFeatureLabel(feat, labelIdx)
		i := sort.Search(n, func(j int) bool { return keys[j] >= key })
		if i < n && keys[i] == key {
			score += float64(vals[i]) * val
		}
	}
	return score
}

// ScoreAll calcule le score d'émission de features pour chaque label et
// l'écrit dans out (longueur = nombre de labels).
// Deux optimisations par rapport à des appels Score répétés :
//   - le hash FNV de la feature n'est calculé qu'une fois, puis complété par
//     label (hashFeatureLabelFromBase) — au lieu d'un hash complet par label ;
//   - en mode compacté (W == nil), aucun verrou : Keys/Vals sont immuables
//     après Compact(), qui doit précéder toute lecture concurrente.
func (sw *SparseWeights) ScoreAll(features map[string]float64, out []float64) {
	for l := range out {
		out[l] = 0
	}
	L := len(out)

	if sw.W != nil {
		sw.mu.RLock()
		defer sw.mu.RUnlock()
		for feat, val := range features {
			base := hashFeatureBase(feat)
			for l := 0; l < L; l++ {
				if w, ok := sw.W[hashFeatureLabelFromBase(base, l)]; ok {
					out[l] += w * val
				}
			}
		}
		return
	}

	if sw.BaseKeys != nil {
		// Mode groupé (v3) : une seule recherche binaire par feature,
		// le bloc de L poids est contigu en mémoire.
		keys, blocks := sw.BaseKeys, sw.BlockVals
		n := len(keys)
		for feat, val := range features {
			base := hashFeatureBase(feat)
			i := sort.Search(n, func(j int) bool { return keys[j] >= base })
			if i < n && keys[i] == base {
				block := blocks[i*L : i*L+L]
				for l, w := range block {
					out[l] += float64(w) * val
				}
			}
		}
		return
	}

	keys, vals := sw.Keys, sw.Vals
	n := len(keys)
	for feat, val := range features {
		base := hashFeatureBase(feat)
		for l := 0; l < L; l++ {
			key := hashFeatureLabelFromBase(base, l)
			i := sort.Search(n, func(j int) bool { return keys[j] >= key })
			if i < n && keys[i] == key {
				out[l] += float64(vals[i]) * val
			}
		}
	}
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

// Len retourne le nombre d'entrées dans les poids, quelle que soit la
// représentation. En mode groupé (v3), compte les valeurs stockées
// (features × labels, zéros de bloc compris).
func (sw *SparseWeights) Len() int {
	sw.mu.RLock()
	defer sw.mu.RUnlock()
	if sw.W != nil {
		return len(sw.W)
	}
	if sw.BaseKeys != nil {
		return len(sw.BlockVals)
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

// Constantes FNV-1a 64 bits (identiques à hash/fnv).
const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

// hashFeatureBase calcule le FNV-1a de feat suivi du séparateur 0xFF.
// La clé d'une paire (feature, label) s'obtient en complétant cette base via
// hashFeatureLabelFromBase — le hachage de la chaîne n'est ainsi fait qu'une
// fois pour tous les labels. L'implémentation manuelle évite l'allocation
// []byte(feat) de hash/fnv et doit rester bit-à-bit identique à celle-ci
// (les clés sont sérialisées dans les modèles — voir TestHashFeatureLabel_FNVReference).
func hashFeatureBase(feat string) uint64 {
	h := fnvOffset64
	for i := 0; i < len(feat); i++ {
		h ^= uint64(feat[i])
		h *= fnvPrime64
	}
	h ^= 0xFF
	h *= fnvPrime64
	return h
}

// hashFeatureLabelFromBase complète une base hashFeatureBase avec l'index de
// label encodé en uint32 little-endian, comme le faisait l'écriture des
// 4 octets dans hash/fnv.
func hashFeatureLabelFromBase(base uint64, labelIdx int) uint64 {
	h := base
	v := uint32(labelIdx)
	h ^= uint64(v & 0xFF)
	h *= fnvPrime64
	h ^= uint64((v >> 8) & 0xFF)
	h *= fnvPrime64
	h ^= uint64((v >> 16) & 0xFF)
	h *= fnvPrime64
	h ^= uint64(v >> 24)
	h *= fnvPrime64
	return h
}

// hashFeatureLabel calcule une clé uint64 pour la paire (feature, labelIdx)
// via FNV-1a. Séparateur 0xFF pour éviter les collisions entre ("ab", 1) et ("a", 21).
func hashFeatureLabel(feat string, labelIdx int) uint64 {
	return hashFeatureLabelFromBase(hashFeatureBase(feat), labelIdx)
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
	return crf.marginalsFromEmissions(computeEmissions(crf, feats))
}

// PredictWithMarginals retourne la séquence Viterbi et les probabilités
// marginales en ne calculant les émissions qu'une seule fois.
// Équivalent à Predict + PredictMarginals, qui recalculaient chacun tous les
// scores d'émission — le poste dominant de l'inférence.
func (crf *CRF) PredictWithMarginals(feats []map[string]float64) ([]string, [][]float64) {
	if len(feats) == 0 {
		return nil, nil
	}
	emissions := computeEmissions(crf, feats)
	return crf.viterbiFromEmissions(emissions), crf.marginalsFromEmissions(emissions)
}

// marginalsFromEmissions calcule les marginales par forward-backward à partir
// d'émissions déjà calculées.
func (crf *CRF) marginalsFromEmissions(emissions [][]float64) [][]float64 {
	alpha, beta, Z := forwardBackward(crf, emissions)

	n := len(emissions)
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
