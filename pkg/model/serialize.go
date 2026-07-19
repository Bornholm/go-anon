package model

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io"
	"sort"
)

// modelVersion "2" stores weights as WeightKeys []uint64 + WeightVals []float32,
// eliminating the intermediate map[uint64]float64 and reducing peak loading memory
// from ~1.4 GB to ~420 MB for large models.
const modelVersion = "2"

// FeatureConfig décrit la configuration du FeatureExtractor utilisé à l'entraînement.
// Elle est sérialisée avec le modèle pour permettre de reproduire le même pipeline
// d'extraction lors de l'inférence.
type FeatureConfig struct {
	WindowSize     int
	GazetteerNames []string // noms des gazetteers chargés
	LangCode       string   // "fr", "en", "" = pas de profil langue
	HasClusters    bool     // Brown clusters utilisés à l'entraînement
	HasEmbeddings  bool     // word embeddings utilisés à l'entraînement
}

// SerializableModel est la représentation sérialisable d'un CRF.
// Version "1" : poids dans Weights map[uint64]float64 (héritage).
// Version "2" : poids dans WeightKeys []uint64 + WeightVals []float32 (format compact).
type SerializableModel struct {
	Version    string
	Lang       string
	Labels     []string
	Weights    map[uint64]float64 // v1 uniquement, conservé pour rétrocompatibilité
	WeightKeys []uint64           // v2 : clés triées
	WeightVals []float32          // v2 : valeurs parallèles à WeightKeys
	Transition [][]float64
	Features   FeatureConfig
}

// Save sérialise le CRF dans w au format gob+gzip.
// Le résultat peut être rechargé via LoadModel.
func (crf *CRF) Save(w io.Writer) error {
	gz := gzip.NewWriter(w)
	if err := gob.NewEncoder(gz).Encode(crf.toSerializable()); err != nil {
		return fmt.Errorf("encode model: %w", err)
	}
	return gz.Close()
}

// toSerializable crée une représentation sérialisable v2 (copie profonde).
func (crf *CRF) toSerializable() *SerializableModel {
	L := len(crf.Labels)

	labels := make([]string, L)
	copy(labels, crf.Labels)

	trans := make([][]float64, L)
	for i := range trans {
		trans[i] = make([]float64, L)
		copy(trans[i], crf.Transition[i])
	}

	sm := &SerializableModel{
		Version:    modelVersion,
		Labels:     labels,
		Transition: trans,
		Features:   crf.FeatureCfg,
	}

	crf.Weights.mu.RLock()
	defer crf.Weights.mu.RUnlock()

	if crf.Weights.W != nil {
		// Mode training/mutable : convertir map → clés triées + vals float32.
		n := len(crf.Weights.W)
		keys := make([]uint64, 0, n)
		for k := range crf.Weights.W {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		vals := make([]float32, n)
		for i, k := range keys {
			vals[i] = float32(crf.Weights.W[k])
		}
		sm.WeightKeys = keys
		sm.WeightVals = vals
	} else {
		// Mode compact : copier les tranches directement.
		sm.WeightKeys = crf.Weights.Keys
		sm.WeightVals = crf.Weights.Vals
	}

	return sm
}

// LoadModel décode un CRF depuis r en mode lecture seule (inférence).
// Retourne une erreur si le format est invalide.
func LoadModel(r io.Reader) (*CRF, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	var sm SerializableModel
	if err := gob.NewDecoder(gz).Decode(&sm); err != nil {
		return nil, fmt.Errorf("decode model: %w", err)
	}
	return sm.toCRF(), nil
}

// LoadModelMutable décode un CRF depuis r sans compacter les poids.
// À utiliser quand on veut modifier le modèle (ex : Prune) avant de le resauvegarder.
func LoadModelMutable(r io.Reader) (*CRF, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	var sm SerializableModel
	if err := gob.NewDecoder(gz).Decode(&sm); err != nil {
		return nil, fmt.Errorf("decode model: %w", err)
	}
	return sm.toCRFMutable(), nil
}

// toCRFMutable reconstruit un CRF sans compacter les poids (mode mutable).
func (sm *SerializableModel) toCRFMutable() *CRF {
	L := len(sm.Labels)

	labelIndex := make(map[string]int, L)
	for i, l := range sm.Labels {
		labelIndex[l] = i
	}

	trans := make([][]float64, L)
	for i := range trans {
		trans[i] = make([]float64, L)
		if i < len(sm.Transition) {
			copy(trans[i], sm.Transition[i])
		}
	}

	var weights map[uint64]float64
	if sm.Version == "2" {
		// v2 : reconvertir float32 → map float64 pour permettre les mutations.
		weights = make(map[uint64]float64, len(sm.WeightKeys))
		for i, k := range sm.WeightKeys {
			weights[k] = float64(sm.WeightVals[i])
		}
	} else {
		weights = sm.Weights
	}

	return &CRF{
		Labels:     sm.Labels,
		LabelIndex: labelIndex,
		Weights:    &SparseWeights{W: weights},
		Transition: trans,
		FeatureCfg: sm.Features,
	}
}

// toCRF reconstruit un CRF depuis sa représentation sérialisée.
// En v2, les poids sont assignés directement ; en v1, Compact() est appelé.
func (sm *SerializableModel) toCRF() *CRF {
	L := len(sm.Labels)

	labelIndex := make(map[string]int, L)
	for i, l := range sm.Labels {
		labelIndex[l] = i
	}

	trans := make([][]float64, L)
	for i := range trans {
		trans[i] = make([]float64, L)
		if i < len(sm.Transition) {
			copy(trans[i], sm.Transition[i])
		}
	}

	crf := &CRF{
		Labels:     sm.Labels,
		LabelIndex: labelIndex,
		Weights:    &SparseWeights{},
		Transition: trans,
		FeatureCfg: sm.Features,
	}

	if sm.Version == "2" {
		// v2 : poids déjà au format compact, assignation directe.
		crf.Weights.Keys = sm.WeightKeys
		crf.Weights.Vals = sm.WeightVals
	} else {
		// v1 : poids dans la map, compacter pour réduire l'empreinte mémoire.
		crf.Weights.W = sm.Weights
		crf.Weights.Compact()
	}

	return crf
}
