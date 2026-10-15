package docprocessor

import (
	"errors"
	"testing"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// batchWalker est un StreamWalker de test : des lots prédéfinis, et ce qui a
// été « écrit », lot après lot.
type batchWalker struct {
	batches [][]string
	written [][]string
}

func (w *batchWalker) WalkBatches(fn func([]Segment) error) error {
	for _, texts := range w.batches {
		out := append([]string(nil), texts...)
		segs := make([]Segment, len(texts))
		for i, t := range texts {
			segs[i] = Segment{Text: t, Replace: func(s string) { out[i] = s }}
		}
		if err := fn(segs); err != nil {
			return err
		}
		w.written = append(w.written, out)
	}
	return nil
}

func TestProcessStream_RanksAcrossBatches(t *testing.T) {
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace, ConsistentMap: true,
		Passes: []anonymizer.AnonymizePass{func(original string, r *anonymizer.Result) string { return original }},
	})
	w := &batchWalker{batches: [][]string{{"rien", "à jean@example.com"}, {"rien", "à jean@example.com"}}}
	_, report, err := New(anon).ProcessStream(w, anonymizer.WithVerification())
	if err != nil {
		t.Fatal(err)
	}
	if report.Segments != 4 || len(w.written) != 2 {
		t.Fatalf("segments %d, lots écrits %d", report.Segments, len(w.written))
	}
	ranks := map[int]bool{}
	for _, l := range report.Leaks {
		ranks[l.Segment] = true
	}
	if !ranks[1] || !ranks[3] || len(ranks) != 2 {
		t.Errorf("rangs des fuites %v, attendus 1 et 3", ranks)
	}
}

func TestProcessStream_ConsistentAcrossBatches(t *testing.T) {
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{Strategy: anonymizer.TagReplace, ConsistentMap: true})
	w := &batchWalker{batches: [][]string{{"jean@example.com"}, {"jean@example.com"}}}
	session, _, err := New(anon).ProcessStream(w)
	if err != nil {
		t.Fatal(err)
	}
	if w.written[0][0] != w.written[1][0] || w.written[0][0] == "jean@example.com" || len(session.Mapping) != 1 {
		t.Errorf("sortie %v, mapping %d", w.written, len(session.Mapping))
	}
}

// En mode strict, le lot fautif n'est pas écrit ; les précédents le sont, et
// c'est à l'appelant de détruire la sortie.
func TestProcessStream_StrictStopsAtTheBatch(t *testing.T) {
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace,
		Passes:   []anonymizer.AnonymizePass{func(original string, r *anonymizer.Result) string { return original }},
	})
	w := &batchWalker{batches: [][]string{{"rien"}, {"à jean@example.com"}, {"rien"}}}
	_, _, err := New(anon).ProcessStream(w, anonymizer.WithStrictVerification())
	var verr *anonymizer.VerificationError
	if !errors.As(err, &verr) || len(w.written) != 1 {
		t.Errorf("err %v, lots écrits %d", err, len(w.written))
	}
}
