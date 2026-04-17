package ner

import (
	"fmt"
	"io"

	"github.com/bornholm/go-anon/pkg/model"
)

// Model encapsule un modèle CRF chargé et prêt pour l'inférence.
type Model struct {
	crf *model.CRF
}

// LoadModel charge un modèle CRF sérialisé depuis r (format gob+gzip).
func LoadModel(r io.Reader) (*Model, error) {
	crf, err := model.LoadModel(r)
	if err != nil {
		return nil, fmt.Errorf("ner: LoadModel: %w", err)
	}
	return &Model{crf: crf}, nil
}
