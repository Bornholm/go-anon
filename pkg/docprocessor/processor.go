package docprocessor

import (
	"errors"
	"fmt"
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

// SegmentLeak localise une fuite dans un document : le rang du segment dans le
// parcours, plus les offsets dans ce segment. Comme anonymizer.Leak, il ne porte
// jamais de contenu.
type SegmentLeak struct {
	Segment int
	anonymizer.Leak
}

// Report agrège les rapports de vérification de tous les segments d'un document.
type Report struct {
	Segments int
	Leaks    []SegmentLeak
}

// OK indique qu'aucun segment ne présente de fuite.
func (r *Report) OK() bool { return r == nil || len(r.Leaks) == 0 }

// Process parcourt les segments via walker, anonymise chacun avec une session partagée,
// et appelle Segment.Replace pour chaque segment dont le texte a changé.
// Les opts additionnelles sont ajoutées après WithSession (qui est toujours injecté).
func (p *Processor) Process(walker Walker, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, error) {
	session, _, err := p.ProcessWithReport(walker, opts...)
	return session, err
}

// ProcessWithReport se comporte comme Process et agrège en plus les rapports de
// vérification par segment (WithVerification).
//
// En mode strict (WithStrictVerification), la première fuite interrompt le
// parcours et retourne une erreur : l'appelant ne doit alors pas écrire de
// document de sortie, sous peine de produire un fichier partiellement anonymisé.
func (p *Processor) ProcessWithReport(walker Walker, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, *Report, error) {
	session := anonymizer.NewSession()
	allOpts := append([]anonymizer.AnonymizeOption{anonymizer.WithSession(session)}, opts...)

	report := &Report{}
	index := 0
	err := walker.Walk(func(seg Segment) error {
		i := index
		index++
		report.Segments = index

		result, err := p.anon.Anonymize(seg.Text, allOpts...)
		if err != nil {
			var verr *anonymizer.VerificationError
			if errors.As(err, &verr) {
				return fmt.Errorf("segment %d : %w", i, err)
			}
			return err
		}
		if result.Verification != nil {
			for _, leak := range result.Verification.Leaks {
				report.Leaks = append(report.Leaks, SegmentLeak{Segment: i, Leak: leak})
			}
		}
		if result.Text != seg.Text {
			seg.Replace(result.Text)
		}
		return nil
	})
	return session, report, err
}
