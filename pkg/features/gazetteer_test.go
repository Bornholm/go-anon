package features_test

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/features"
)

func loadTestGazetteer(t *testing.T, name, content string) *features.Gazetteer {
	t.Helper()
	g, err := features.LoadGazetteer(name, strings.NewReader(content))
	if err != nil {
		t.Fatalf("LoadGazetteer: %v", err)
	}
	return g
}

func TestLoadGazetteer(t *testing.T) {
	content := "Paris\nLyon\nMarseille\n"
	g := loadTestGazetteer(t, "cities", content)
	if g == nil {
		t.Fatal("gazetteer nil")
	}
}

func TestContainsHit(t *testing.T) {
	g := loadTestGazetteer(t, "cities", "Paris\nLyon\n")
	if !g.Contains("Paris") {
		t.Error("Contains(Paris) = false, want true")
	}
}

func TestContainsMiss(t *testing.T) {
	g := loadTestGazetteer(t, "cities", "Paris\nLyon\n")
	if g.Contains("Berlin") {
		t.Error("Contains(Berlin) = true, want false")
	}
}

func TestContainsCaseInsensitive(t *testing.T) {
	// Entrées stockées en minuscules → lookup insensible à la casse.
	g := loadTestGazetteer(t, "cities", "Paris\n")
	if !g.Contains("paris") {
		t.Error("Contains(paris) = false (entrée Paris), want true")
	}
	if !g.Contains("PARIS") {
		t.Error("Contains(PARIS) = false (entrée Paris), want true")
	}
}

func TestContainsIgnoresComments(t *testing.T) {
	content := "# commentaire\nParis\n"
	g := loadTestGazetteer(t, "cities", content)
	if g.Contains("#") || g.Contains("commentaire") {
		t.Error("les lignes commentaires ne doivent pas être indexées")
	}
	if !g.Contains("Paris") {
		t.Error("Paris doit être présent")
	}
}

func TestContainsIgnoresBlankLines(t *testing.T) {
	content := "\nParis\n\nLyon\n"
	g := loadTestGazetteer(t, "cities", content)
	if g.Contains("") {
		t.Error("ligne vide ne doit pas être indexée")
	}
	if !g.Contains("Paris") || !g.Contains("Lyon") {
		t.Error("Paris et Lyon doivent être présents")
	}
}

func TestContainsSequenceHit(t *testing.T) {
	// "New York" → true si l'entrée "new york" est dans le gazetteer.
	g := loadTestGazetteer(t, "cities", "New York\nParis\n")
	if !g.ContainsSequence([]string{"lives", "in", "New", "York", "."}, 2, 4) {
		t.Error("ContainsSequence([New York]) = false, want true")
	}
}

func TestContainsSequenceMiss(t *testing.T) {
	g := loadTestGazetteer(t, "cities", "New York\n")
	if g.ContainsSequence([]string{"New", "Jersey"}, 0, 2) {
		t.Error("ContainsSequence([New Jersey]) = true, want false")
	}
}

func TestContainsSequenceInvalidRange(t *testing.T) {
	g := loadTestGazetteer(t, "cities", "Paris\n")
	// start >= end
	if g.ContainsSequence([]string{"Paris"}, 0, 0) {
		t.Error("start == end doit retourner false")
	}
	// start < 0
	if g.ContainsSequence([]string{"Paris"}, -1, 1) {
		t.Error("start < 0 doit retourner false")
	}
	// end > len(words)
	if g.ContainsSequence([]string{"Paris"}, 0, 5) {
		t.Error("end > len doit retourner false")
	}
}

func TestName(t *testing.T) {
	g := loadTestGazetteer(t, "firstnames_fr", "Jean\nMarie\n")
	if g.Name() != "firstnames_fr" {
		t.Errorf("Name() = %q, want %q", g.Name(), "firstnames_fr")
	}
}

// TestLoadGazetteer_CSVEntries : les listes de référence circulent souvent en
// CSV — le fichier des prénoms de l'INSEE est fait de lignes « PRENOM,effectif ».
// Prendre la ligne entière produisait des clés introuvables : le gazetteer se
// chargeait sans erreur, annonçait ses centaines de milliers d'entrées, et n'en
// reconnaissait aucune. Panne d'autant plus coûteuse qu'elle était silencieuse.
func TestLoadGazetteer_CSVEntries(t *testing.T) {
	g := loadTestGazetteer(t, "firstnames",
		"prenom,sum\nHERVE,34897\nCOLINE,1204\nMARIE,900000\n")

	for _, name := range []string{"Herve", "coline", "MARIE"} {
		if !g.Contains(name) {
			t.Errorf("%q devrait être reconnu", name)
		}
	}
	if g.Contains("prenom,sum") {
		t.Error("l'entrée brute de l'en-tête ne devrait pas être retenue")
	}
}

// TestLoadGazetteer_PlainEntries : non-régression sur le format simple, un terme
// par ligne, qu'utilisent les autres listes distribuées.
func TestLoadGazetteer_PlainEntries(t *testing.T) {
	g := loadTestGazetteer(t, "locations",
		"# commentaire\nParis\nSaint-Étienne\n\nLyon\n")

	for _, name := range []string{"paris", "Saint-Étienne", "LYON"} {
		if !g.Contains(name) {
			t.Errorf("%q devrait être reconnu", name)
		}
	}
	if g.Contains("# commentaire") {
		t.Error("les commentaires ne devraient pas être retenus")
	}
}
