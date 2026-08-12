package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/template"
)

func templatesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../../templates")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("répertoire de templates absent : %v", err)
	}
	return dir
}

func loadAll(t *testing.T) ([]*template.Template, *gazetteer.Bundle) {
	t.Helper()
	tmpls, err := LoadTemplates(templatesDir(t), "fr")
	if err != nil {
		t.Fatalf("chargement des templates : %v", err)
	}
	bundle, err := gazetteer.LoadSeed("fr", gazetteer.DefaultOptions())
	if err != nil {
		t.Fatalf("chargement des gazetteers : %v", err)
	}
	return tmpls, bundle
}

// TestSpanTextInvariant est l'invariant central du générateur : pour tout
// document, pour tout span, le texte extrait aux offsets du span est exactement
// la valeur que le renderer a insérée — y compris après application du bruit.
//
// Sa violation produirait un corpus d'apparence correcte entraînant un modèle
// silencieusement dégradé.
func TestSpanTextInvariant(t *testing.T) {
	tmpls, bundle := loadAll(t)
	opts := DefaultOptions()
	opts.Seed = 42
	// Bruit forcé au maximum : c'est la transformation la plus susceptible de
	// désaligner les offsets.
	opts.NoiseRate = 1
	opts.NoiseIntensity = 0.5

	for _, tmpl := range tmpls {
		for i := 0; i < 50; i++ {
			doc := Document(tmpl, bundle, i, opts)
			text, spans := doc.Build()
			for _, s := range spans {
				if s.Start < 0 || s.End > len(text) {
					t.Fatalf("%s doc %d : span hors bornes %+v (len=%d)", tmpl.Name, i, s, len(text))
				}
				if got := text[s.Start:s.End]; got != s.Text {
					t.Fatalf("%s doc %d : text[%d:%d] = %q, span.Text = %q",
						tmpl.Name, i, s.Start, s.End, got, s.Text)
				}
			}
		}
	}
}

// TestSpansDisjoint vérifie que les spans ne se chevauchent pas et sont triés,
// propriété garantie par la concaténation de segments.
func TestSpansDisjoint(t *testing.T) {
	tmpls, bundle := loadAll(t)
	opts := DefaultOptions()
	opts.Seed = 7

	for _, tmpl := range tmpls {
		for i := 0; i < 30; i++ {
			doc := Document(tmpl, bundle, i, opts)
			_, spans := doc.Build()
			prev := 0
			for _, s := range spans {
				if s.Start < prev {
					t.Fatalf("%s doc %d : span %+v chevauche le précédent (fin %d)", tmpl.Name, i, s, prev)
				}
				if s.Start >= s.End {
					t.Fatalf("%s doc %d : span dégénéré %+v", tmpl.Name, i, s)
				}
				prev = s.End
			}
		}
	}
}

// TestReproducible vérifie que la même seed produit exactement le même
// document. Sans cela, aucun écart entre deux entraînements n'est imputable.
func TestReproducible(t *testing.T) {
	tmpls, bundle := loadAll(t)
	opts := DefaultOptions()
	opts.Seed = 123

	for _, tmpl := range tmpls {
		a, spansA := Document(tmpl, bundle, 5, opts).Build()
		b, spansB := Document(tmpl, bundle, 5, opts).Build()
		if a != b {
			t.Fatalf("%s : deux rendus de la même seed diffèrent", tmpl.Name)
		}
		if len(spansA) != len(spansB) {
			t.Fatalf("%s : nombre de spans instable (%d vs %d)", tmpl.Name, len(spansA), len(spansB))
		}
	}
}

// TestSeedIndependence vérifie la propriété attendue de la dérivation
// hiérarchique : changer l'index d'un document ne change pas les autres.
func TestSeedIndependence(t *testing.T) {
	s1 := DocumentSeed(1, "facture", "fr", 10)
	s2 := DocumentSeed(1, "facture", "fr", 11)
	s3 := DocumentSeed(2, "facture", "fr", 10)
	if s1 == s2 || s1 == s3 {
		t.Fatalf("seeds non distinctes : %d %d %d", s1, s2, s3)
	}
	if s1 != DocumentSeed(1, "facture", "fr", 10) {
		t.Fatal("dérivation de seed non déterministe")
	}
}

// TestProjectBIOWellFormed vérifie qu'aucun I- orphelin n'est produit et que
// les tokens sont restitués fidèlement.
func TestProjectBIOWellFormed(t *testing.T) {
	tmpls, bundle := loadAll(t)
	opts := DefaultOptions()
	opts.Seed = 99

	for _, tmpl := range tmpls {
		for i := 0; i < 20; i++ {
			doc := Document(tmpl, bundle, i, opts)
			text, spans := doc.Build()
			sentences, err := ProjectBIO(text, spans, "fr")
			if err != nil {
				t.Fatalf("%s doc %d : %v", tmpl.Name, i, err)
			}
			for _, s := range sentences {
				prev := "O"
				for _, tok := range s {
					if tok.Word == "" {
						t.Fatalf("%s doc %d : token vide", tmpl.Name, i)
					}
					if strings.ContainsAny(tok.Word, " \t\n") {
						t.Fatalf("%s doc %d : token contenant un blanc %q", tmpl.Name, i, tok.Word)
					}
					if strings.HasPrefix(tok.Tag, "I-") {
						if prev == "O" || prev[2:] != tok.Tag[2:] {
							t.Fatalf("%s doc %d : %q orphelin après %q", tmpl.Name, i, tok.Tag, prev)
						}
					}
					prev = tok.Tag
				}
			}
		}
	}
}

// TestAllSpansCoveredByTokens vérifie qu'aucun span annoté ne disparaît à la
// projection : un span dont aucun token ne porte le label serait une entité
// perdue, invisible dans le CoNLL.
func TestAllSpansCoveredByTokens(t *testing.T) {
	tmpls, bundle := loadAll(t)
	opts := DefaultOptions()
	opts.Seed = 4242

	for _, tmpl := range tmpls {
		for i := 0; i < 20; i++ {
			doc := Document(tmpl, bundle, i, opts)
			text, spans := doc.Build()
			sentences, err := ProjectBIO(text, spans, "fr")
			if err != nil {
				t.Fatal(err)
			}
			begins := 0
			for _, s := range sentences {
				for _, tok := range s {
					if strings.HasPrefix(tok.Tag, "B-") {
						begins++
					}
				}
			}
			// Un span peut être coupé par une frontière de phrase et compter
			// alors deux B-. L'inverse — moins de B- que de spans — signale une
			// entité perdue.
			if begins < len(spans) {
				t.Fatalf("%s doc %d : %d spans mais %d tags B- ; entités perdues à la projection",
					tmpl.Name, i, len(spans), begins)
			}
		}
	}
}
