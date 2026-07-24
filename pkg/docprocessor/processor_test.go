package docprocessor

import (
	"errors"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/ner"
)

// sliceWalker est un Walker de test qui émet des segments prédéfinis.
type sliceWalker struct {
	segments []string
	walked   int
	replaced []string
}

func (w *sliceWalker) Walk(fn func(Segment) error) error {
	for _, s := range w.segments {
		w.walked++
		if err := fn(Segment{Text: s, Replace: func(out string) {
			w.replaced = append(w.replaced, out)
		}}); err != nil {
			return err
		}
	}
	return nil
}

// emailRecognizer détecte les adresses e-mail par regex — suffisant pour tester
// l'orchestration sans charger de modèle CRF.
type emailRecognizer struct{}

func (emailRecognizer) Recognize(text string) ([]ner.Entity, error) {
	return ner.RegexEntityFilter(func() string { return text }, ner.BuiltinRegexPatterns)(nil), nil
}

func TestProcessWithReport_AggregatesLeaksPerSegment(t *testing.T) {
	// Passe défectueuse : elle restaure le texte source, donc l'e-mail.
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace,
		Passes: []anonymizer.AnonymizePass{func(original string, r *anonymizer.Result) string {
			return original
		}},
	})

	w := &sliceWalker{segments: []string{"rien à signaler", "écrire à jean@example.com"}}
	_, report, err := New(anon).ProcessWithReport(w, anonymizer.WithVerification())
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if report.Segments != 2 {
		t.Errorf("segments = %d, attendu 2", report.Segments)
	}
	if len(report.Leaks) == 0 {
		t.Fatal("la fuite du second segment aurait dû être rapportée")
	}
	for _, leak := range report.Leaks {
		if leak.Segment != 1 {
			t.Errorf("fuite attribuée au segment %d, attendu 1", leak.Segment)
		}
	}
}

// En mode strict, le parcours s'interrompt à la première fuite et aucun segment
// n'est réécrit après elle : l'appelant ne doit pas produire de document.
func TestProcessWithReport_StrictStopsBeforeWriting(t *testing.T) {
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace,
		Passes: []anonymizer.AnonymizePass{func(original string, r *anonymizer.Result) string {
			return original
		}},
	})

	w := &sliceWalker{segments: []string{"écrire à jean@example.com", "second segment"}}
	_, _, err := New(anon).ProcessWithReport(w, anonymizer.WithStrictVerification())
	if err == nil {
		t.Fatal("le mode strict aurait dû échouer")
	}
	if !errors.Is(err, anonymizer.ErrVerificationFailed) {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(err.Error(), "segment 0") {
		t.Errorf("l'erreur devrait localiser le segment fautif : %q", err.Error())
	}
	if w.walked != 1 {
		t.Errorf("segments parcourus = %d, attendu 1 (arrêt immédiat)", w.walked)
	}
	if len(w.replaced) != 0 {
		t.Errorf("aucun segment ne doit être réécrit, %d l'ont été", len(w.replaced))
	}
}

func TestSampleTextConcatenates(t *testing.T) {
	w := &sliceWalker{segments: []string{"Bonjour", "le", "monde"}}
	got, err := SampleText(w, 1000)
	if err != nil {
		t.Fatalf("SampleText: %v", err)
	}
	want := "Bonjour\nle\nmonde"
	if got != want {
		t.Errorf("texte = %q, attendu %q", got, want)
	}
}

func TestSampleTextStopsEarly(t *testing.T) {
	w := &sliceWalker{segments: []string{"aaaaa", "bbbbb", "ccccc", "ddddd"}}
	got, err := SampleText(w, 8)
	if err != nil {
		t.Fatalf("SampleText: %v", err)
	}
	// Après "aaaaa\nbbbbb" (11 octets) le seuil de 8 est atteint : on s'arrête.
	if want := "aaaaa\nbbbbb"; got != want {
		t.Errorf("texte = %q, attendu %q", got, want)
	}
	if w.walked != 2 {
		t.Errorf("segments parcourus = %d, attendu 2 (arrêt anticipé)", w.walked)
	}
}
