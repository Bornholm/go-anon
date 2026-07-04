package docprocessor

import (
	"errors"
	"strings"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// errSampleDone est une sentinelle interne utilisée pour interrompre un Walk
// une fois assez de texte accumulé.
var errSampleDone = errors.New("docprocessor: sample done")

// SampleText parcourt le walker et accumule le texte des segments (séparés par
// un saut de ligne) jusqu'à atteindre maxChars caractères, puis s'arrête.
// Lecture seule : Segment.Replace n'est jamais appelé. Utile pour détecter la
// langue d'un document avant de choisir le modèle et de construire le pipeline.
func SampleText(walker Walker, maxChars int) (string, error) {
	var b strings.Builder
	err := walker.Walk(func(seg Segment) error {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(seg.Text)
		if b.Len() >= maxChars {
			return errSampleDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSampleDone) {
		return "", err
	}
	return b.String(), nil
}

// Segment représente une unité de texte extraite d'un document.
// Replace est le callback qui écrit le texte anonymisé en retour dans le document.
type Segment struct {
	Text    string
	Replace func(anonymized string)
}

// Walker parcourt les segments de texte d'un document, indépendamment de son format.
type Walker interface {
	Walk(func(Segment) error) error
}

// Processor orchestre l'anonymisation d'un document via un Walker.
type Processor struct {
	anon *anonymizer.Anonymizer
}

func New(anon *anonymizer.Anonymizer) *Processor {
	return &Processor{anon: anon}
}

// Process parcourt les segments via walker, anonymise chacun avec une session partagée,
// et appelle Segment.Replace pour chaque segment dont le texte a changé.
// Les opts additionnelles sont ajoutées après WithSession (qui est toujours injecté).
func (p *Processor) Process(walker Walker, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, error) {
	session := anonymizer.NewSession()
	allOpts := append([]anonymizer.AnonymizeOption{anonymizer.WithSession(session)}, opts...)

	err := walker.Walk(func(seg Segment) error {
		result, err := p.anon.Anonymize(seg.Text, allOpts...)
		if err != nil {
			return err
		}
		if result.Text != seg.Text {
			seg.Replace(result.Text)
		}
		return nil
	})
	return session, err
}
