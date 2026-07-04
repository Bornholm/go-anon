package docprocessor

import "testing"

// sliceWalker est un Walker de test qui émet des segments prédéfinis.
type sliceWalker struct {
	segments []string
	walked   int
}

func (w *sliceWalker) Walk(fn func(Segment) error) error {
	for _, s := range w.segments {
		w.walked++
		if err := fn(Segment{Text: s, Replace: func(string) {}}); err != nil {
			return err
		}
	}
	return nil
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
