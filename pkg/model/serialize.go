package model

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"sort"
)

// Versions du format de sérialisation :
//   - "1" : poids dans Weights map[uint64]float64 (héritage).
//   - "2" : poids dans WeightKeys []uint64 + WeightVals []float32 — élimine la
//     map intermédiaire, mémoire de chargement ~1.4 GB → ~420 MB.
//   - "3" : poids groupés par feature (BaseKeys []uint64 + BlockVals, bloc de
//     L poids contigus par feature) — une seule recherche binaire par feature
//     à l'inférence au lieu de L. Ne peut être produit qu'à l'entraînement,
//     où les features en clair sont connues (les clés v1/v2 sont des hachés
//     à sens unique).
const (
	modelVersionFlat    = "2"
	modelVersionGrouped = "3"
)

// FeatureConfig décrit la configuration du FeatureExtractor utilisé à l'entraînement.
// Elle est sérialisée avec le modèle pour permettre de reproduire le même pipeline
// d'extraction lors de l'inférence.
type FeatureConfig struct {
	WindowSize     int
	GazetteerNames []string // noms des gazetteers chargés
	LangCode       string   // "fr", "en", "" = pas de profil langue
	HasClusters    bool     // Brown clusters utilisés à l'entraînement
	HasEmbeddings  bool     // word embeddings utilisés à l'entraînement
	// FeatureSchema versionne les chaînes de features produites par
	// l'extracteur (0 = schéma historique gelé, 1 = word.len corrigé +
	// gazseq B/I). L'inférence doit utiliser exactement le schéma
	// d'entraînement : il est propagé au FeatureExtractor par ner.New.
	FeatureSchema int
}

// SerializableModel est la représentation sérialisable d'un CRF.
type SerializableModel struct {
	Version    string
	Lang       string
	Labels     []string
	Weights    map[uint64]float64 // v1 uniquement, conservé pour rétrocompatibilité
	WeightKeys []uint64           // v2 : clés (feature, label) triées
	WeightVals []float32          // v2 : valeurs parallèles à WeightKeys
	BaseKeys   []uint64           // v3 : hachés de base des features, triés
	BlockVals  []float32          // v3 : blocs de len(Labels) poids par feature
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
// Le format v3 (groupé par feature) est émis si les bases de features sont
// connues : après un entraînement (featureBases collectées par le Trainer) ou
// après le chargement mutable d'un modèle v3 (cycle prune). Sinon, v2.
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
		Labels:     labels,
		Transition: trans,
		Features:   crf.FeatureCfg,
	}

	crf.Weights.mu.RLock()
	defer crf.Weights.mu.RUnlock()

	switch {
	case crf.Weights.BaseKeys != nil:
		// Mode inférence groupé (chargé depuis un v3) : copie directe.
		sm.Version = modelVersionGrouped
		sm.BaseKeys = crf.Weights.BaseKeys
		sm.BlockVals = crf.Weights.BlockVals

	case crf.Weights.W != nil && len(crf.featureBases) > 0:
		// Mode training avec bases connues : regrouper les poids par feature.
		// Les blocs entièrement nuls (features élaguées) sont omis.
		sm.Version = modelVersionGrouped
		bases := make([]uint64, len(crf.featureBases))
		copy(bases, crf.featureBases)
		sort.Slice(bases, func(i, j int) bool { return bases[i] < bases[j] })

		keptBases := make([]uint64, 0, len(bases))
		blockVals := make([]float32, 0, len(bases)*L)
		block := make([]float32, L)
		covered := 0
		for _, base := range bases {
			nonZero := false
			for l := 0; l < L; l++ {
				block[l] = 0
				if w, ok := crf.Weights.W[hashFeatureLabelFromBase(base, l)]; ok {
					block[l] = float32(w)
					nonZero = true
					covered++
				}
			}
			if nonZero {
				keptBases = append(keptBases, base)
				blockVals = append(blockVals, block...)
			}
		}
		sm.BaseKeys = keptBases
		sm.BlockVals = blockVals

		if covered < len(crf.Weights.W) {
			log.Printf("model: sérialisation v3 : %d poids sur %d non couverts par les bases de features (perdus)",
				len(crf.Weights.W)-covered, len(crf.Weights.W))
		}

	case crf.Weights.W != nil:
		// Mode training/mutable sans bases : format v2 (clés triées + float32).
		sm.Version = modelVersionFlat
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

	default:
		// Mode compact plat : copier les tranches directement.
		sm.Version = modelVersionFlat
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
	var featureBases []uint64
	switch sm.Version {
	case modelVersionGrouped:
		// v3 : reconstruire la map plate depuis les blocs. Les bases sont
		// conservées pour que Save ré-émette du v3 (cycle prune).
		weights = make(map[uint64]float64, len(sm.BlockVals))
		featureBases = sm.BaseKeys
		for i, base := range sm.BaseKeys {
			for l := 0; l < L; l++ {
				if w := sm.BlockVals[i*L+l]; w != 0 {
					weights[hashFeatureLabelFromBase(base, l)] = float64(w)
				}
			}
		}
	case modelVersionFlat:
		// v2 : reconvertir float32 → map float64 pour permettre les mutations.
		weights = make(map[uint64]float64, len(sm.WeightKeys))
		for i, k := range sm.WeightKeys {
			weights[k] = float64(sm.WeightVals[i])
		}
	default:
		weights = sm.Weights
	}

	return &CRF{
		Labels:       sm.Labels,
		LabelIndex:   labelIndex,
		Weights:      &SparseWeights{W: weights},
		Transition:   trans,
		FeatureCfg:   sm.Features,
		featureBases: featureBases,
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

	switch sm.Version {
	case modelVersionGrouped:
		// v3 : poids groupés par feature, assignation directe.
		crf.Weights.BaseKeys = sm.BaseKeys
		crf.Weights.BlockVals = sm.BlockVals
		crf.Weights.BlockL = L
		crf.featureBases = sm.BaseKeys
	case modelVersionFlat:
		// v2 : poids déjà au format compact plat, assignation directe.
		crf.Weights.Keys = sm.WeightKeys
		crf.Weights.Vals = sm.WeightVals
	default:
		// v1 : poids dans la map, compacter pour réduire l'empreinte mémoire.
		crf.Weights.W = sm.Weights
		crf.Weights.Compact()
	}

	return crf
}
