package docprocessor

import (
	"errors"
	"testing"
)

// sanitizingWalker implémente Sanitizer et rapporte des surfaces configurables.
type sanitizingWalker struct {
	sliceWalker
	report SanitizeReport
	err    error
}

func (w *sanitizingWalker) Sanitize(policy SanitizePolicy) (SanitizeReport, error) {
	return w.report, w.err
}

// TestSanitize_StrictRejectsFormatWithoutGuarantee : un walker qui n'implémente
// pas Sanitizer est refusé en mode strict (format sans garantie).
func TestSanitize_StrictRejectsFormatWithoutGuarantee(t *testing.T) {
	w := &sliceWalker{segments: []string{"a"}}

	_, err := Sanitize(w, SanitizePolicy{Strict: true})
	if !errors.Is(err, ErrNoSanitizeGuarantee) {
		t.Fatalf("attendu ErrNoSanitizeGuarantee, got %v", err)
	}

	// Hors strict, l'absence de Sanitizer est tolérée (rapport vide).
	if _, err := Sanitize(w, SanitizePolicy{}); err != nil {
		t.Fatalf("hors strict, un format sans Sanitizer ne doit pas échouer : %v", err)
	}
}

// TestSanitize_StrictRejectsUnprocessedSurfaces : des surfaces non traitées font
// échouer le mode strict avec la liste (jamais le contenu) des surfaces.
func TestSanitize_StrictRejectsUnprocessedSurfaces(t *testing.T) {
	w := &sanitizingWalker{report: SanitizeReport{Unprocessed: []string{"révisions", "annotations"}}}

	_, err := Sanitize(w, SanitizePolicy{Strict: true})
	var unproc *ErrUnsanitizedSurface
	if !errors.As(err, &unproc) {
		t.Fatalf("attendu ErrUnsanitizedSurface, got %v", err)
	}
	if len(unproc.Surfaces) != 2 {
		t.Errorf("attendu 2 surfaces, got %v", unproc.Surfaces)
	}

	// Hors strict, le rapport est renvoyé sans erreur.
	rep, err := Sanitize(w, SanitizePolicy{})
	if err != nil {
		t.Fatalf("hors strict : %v", err)
	}
	if rep.OK() {
		t.Errorf("le rapport devrait signaler des surfaces non traitées")
	}
}
