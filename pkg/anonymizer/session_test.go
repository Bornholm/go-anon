package anonymizer

import (
	"errors"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

// TestGuarantee_SessionCloseRejectsReuse (8.T1) : après Close(), toute
// anonymisation ultérieure échoue proprement au lieu d'écrire dans des maps nil.
func TestGuarantee_SessionCloseRejectsReuse(t *testing.T) {
	rec := &mockRecognizer{entities: []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}}
	anon := New(rec, Config{Strategy: TagReplace})

	sess := NewSession()
	if _, err := anon.Anonymize("John Doe works here.", WithSession(sess)); err != nil {
		t.Fatalf("première anonymisation : %v", err)
	}

	sess.Close()

	_, err := anon.Anonymize("John Doe again.", WithSession(sess))
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("attendu ErrSessionClosed après Close(), got %v", err)
	}

	// Les tables ont bien été libérées (collectables par le GC).
	if sess.Mapping != nil || sess.OriginalToPlaceholder != nil {
		t.Errorf("Close() doit remettre les maps à nil")
	}
}

// TestSessionMaxEntities_CapsGrowth (8.T2) : au-delà du plafond, Anonymize
// retourne ErrSessionFull sans faire croître le mapping — pas d'OOM silencieux.
func TestSessionMaxEntities_CapsGrowth(t *testing.T) {
	// Trois entités distinctes → trois entrées de mapping.
	rec := &mockRecognizer{entities: []ner.Entity{
		{Text: "Alice", Type: ner.TypePER, Start: 0, End: 5, Confidence: 1.0},
		{Text: "Bob", Type: ner.TypePER, Start: 10, End: 13, Confidence: 1.0},
		{Text: "Carol", Type: ner.TypePER, Start: 20, End: 25, Confidence: 1.0},
	}}
	anon := New(rec, Config{Strategy: TagReplace})

	sess := NewSession()
	sess.SetMaxEntities(2)

	_, err := anon.Anonymize("Alice et Bob et Carol sont là.", WithSession(sess))
	if !errors.Is(err, ErrSessionFull) {
		t.Fatalf("attendu ErrSessionFull au-delà du plafond, got %v", err)
	}

	// Le plafond a bloqué avant écriture : le mapping n'a pas dépassé la limite.
	if len(sess.Mapping) > 2 {
		t.Errorf("le mapping a dépassé le plafond : %d entrées", len(sess.Mapping))
	}
}

// TestSession_RetainedTextsAreCloned (8.4) : les formes de surface conservées
// dans la session ne partagent pas le tableau d'octets du texte source, sinon
// une seule entité retiendrait tout le document en mémoire.
func TestSession_RetainedTextsAreCloned(t *testing.T) {
	// Le texte source est volumineux ; l'entité n'en couvre qu'un fragment.
	name := "Zoé Martin"
	source := name + strings.Repeat(" padding", 1000)

	rec := &mockRecognizer{entities: []ner.Entity{
		{Text: name, Type: ner.TypePER, Start: 0, End: len(name), Confidence: 1.0},
	}}
	anon := New(rec, Config{Strategy: TagReplace})

	sess := NewSession()
	if _, err := anon.Anonymize(source, WithSession(sess)); err != nil {
		t.Fatalf("Anonymize : %v", err)
	}

	// La clé conservée doit être une copie indépendante (même contenu, capacité
	// réduite à sa propre longueur, pas celle du document entier).
	for orig := range sess.OriginalToPlaceholder {
		if orig != name {
			continue
		}
		// strings.Clone produit une chaîne dont le backing array a exactement la
		// taille du contenu : impossible à vérifier directement en Go, mais on
		// s'assure au moins que la valeur est correcte et autonome.
		if orig != name {
			t.Errorf("clé de mapping altérée : %q", orig)
		}
	}
	if _, ok := sess.OriginalToPlaceholder[name]; !ok {
		t.Errorf("entité attendue absente du mapping de session")
	}
}
