package model

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io"
)

const modelVersion = "1"

// FeatureConfig décrit la configuration du FeatureExtractor utilisé à l'entraînement.
// Elle est sérialisée avec le modèle pour permettre de reproduire le même pipeline
// d'extraction lors de l'inférence.
type FeatureConfig struct {
	WindowSize     int
	GazetteerNames []string // noms des gazetteers chargés
	LangCode       string   // "fr", "en", "" = pas de profil langue
}

// SerializableModel est la représentation sérialisable d'un CRF.
// Elle ne contient que des types primitifs Go (compatibles gob).
type SerializableModel struct {
	Version    string
	Lang       string
	Labels     []string
	Weights    map[uint64]float64
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

// toSerializable crée une représentation sérialisable (copie profonde).
func (crf *CRF) toSerializable() *SerializableModel {
	L := len(crf.Labels)

	labels := make([]string, L)
	copy(labels, crf.Labels)

	trans := make([][]float64, L)
	for i := range trans {
		trans[i] = make([]float64, L)
		copy(trans[i], crf.Transition[i])
	}

	crf.Weights.mu.RLock()
	w := make(map[uint64]float64, len(crf.Weights.W))
	for k, v := range crf.Weights.W {
		w[k] = v
	}
	crf.Weights.mu.RUnlock()

	return &SerializableModel{
		Version:    modelVersion,
		Labels:     labels,
		Weights:    w,
		Transition: trans,
		Features:   crf.FeatureCfg,
	}
}

// LoadModel décode un CRF depuis r et compacte les poids en mémoire (lecture seule).
// À utiliser pour l'inférence. Retourne une erreur si le format est invalide.
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

	return &CRF{
		Labels:     sm.Labels,
		LabelIndex: labelIndex,
		Weights:    &SparseWeights{W: sm.Weights},
		Transition: trans,
		FeatureCfg: sm.Features,
	}
}

// toCRF reconstruit un CRF depuis sa représentation sérialisée.
// Les poids sont compactés en tableaux triés (float32) pour réduire l'empreinte mémoire.
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
		Weights:    &SparseWeights{W: sm.Weights},
		Transition: trans,
		FeatureCfg: sm.Features,
	}
	crf.Weights.Compact()
	return crf
}
