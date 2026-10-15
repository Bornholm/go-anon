package docprocessor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/ner"
)

// StreamWalker parcourt un document au fil de l'eau, par lots de segments :
// fn reçoit un lot, dont les segments se réécrivent par Replace, et le lot est
// écrit en sortie dès que fn rend la main. Le document n'est jamais tenu en
// entier en mémoire, ce qui distingue ce parcours de Walker : un Walker se
// parcourt plusieurs fois (échantillonnage, vues, vérification) puis s'écrit
// d'un bloc ; un StreamWalker ne se parcourt qu'une fois.
//
// Une erreur retournée par fn interrompt le parcours : le lot en cours n'est
// pas écrit, et la sortie déjà produite est incomplète. L'appelant doit alors
// la détruire.
type StreamWalker interface {
	WalkBatches(fn func(batch []Segment) error) error
}

// SampleStream lit le début d'un StreamWalker comme SampleText lit un Walker,
// pour détecter la langue. Le parcours est interrompu une fois maxChars
// caractères accumulés : le StreamWalker est consommé, il faut en ouvrir un
// autre pour le traitement.
func SampleStream(walker StreamWalker, maxChars int) (string, error) {
	var b strings.Builder
	err := walker.WalkBatches(func(batch []Segment) error {
		for _, seg := range batch {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(seg.Text)
			if b.Len() >= maxChars {
				return errSampleDone
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSampleDone) {
		return "", err
	}
	return b.String(), nil
}

// ProcessStream anonymise un document au fil de l'eau, avec une session
// partagée par tous les lots : la numérotation et la cohérence des
// pseudonymes valent pour tout le document, comme avec ProcessWithReport.
// Les rangs de segment du rapport comptent depuis le début du document ; les
// offsets d'une DocumentLeak, eux, sont relatifs à la recomposition de son lot.
//
// Les contrôles qui recomposent le document opèrent ici à l'échelle du lot :
// WithMultiViewDetection et WithDocumentVerification recomposent chaque lot,
// pas le document entier. Une entité coupée entre deux lots leur échappe ; la
// taille des lots règle ce compromis entre mémoire et portée du contrôle.
//
// En mode strict (WithStrictVerification, WithStrictDocumentVerification), la
// première fuite interrompt le parcours : les lots précédents sont déjà
// écrits, et l'appelant doit détruire la sortie.
func (p *Processor) ProcessStream(walker StreamWalker, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, *Report, error) {
	session := anonymizer.NewSession()
	allOpts := append([]anonymizer.AnonymizeOption{anonymizer.WithSession(session)}, opts...)
	report := &Report{}

	err := walker.WalkBatches(func(batch []Segment) error {
		base := report.Segments
		report.Segments += len(batch)

		var extras map[int][]ner.Entity
		if p.multiView {
			texts := make([]string, len(batch))
			for i, seg := range batch {
				texts[i] = seg.Text
			}
			found, err := p.detectViews(nil, texts)
			if err != nil {
				return err
			}
			extras = found
		}

		var outputs, replacements []string
		for i, seg := range batch {
			out, err := p.anonymizeSegment(base+i, seg, extras[i], allOpts, report)
			if err != nil {
				return err
			}
			if p.verifyDoc {
				outputs = append(outputs, out.Text)
				for _, replacement := range out.OriginalToPlaceholder {
					replacements = append(replacements, replacement)
				}
			}
		}
		if !p.verifyDoc {
			return nil
		}
		leaks, err := p.verifyDocument(outputs, replacements)
		if err != nil {
			return err
		}
		for _, leak := range leaks {
			for j := range leak.Segments {
				leak.Segments[j] += base
			}
			report.DocumentLeaks = append(report.DocumentLeaks, leak)
		}
		if p.strictDoc && len(leaks) > 0 {
			return fmt.Errorf("document : %w : %d entité(s) redétectée(s) après recomposition du lot",
				anonymizer.ErrVerificationFailed, len(leaks))
		}
		return nil
	})
	return session, report, err
}
