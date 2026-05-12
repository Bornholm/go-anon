package docprocessor

import "github.com/bornholm/go-anon/pkg/anonymizer"

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
