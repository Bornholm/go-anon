package corpus_test

import (
	"os"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
)

// readFixture ouvre un fichier testdata et appelle le reader dessus.
func readFixture(t *testing.T, r *corpus.ConLLReader, filename string) []corpus.Sentence {
	t.Helper()
	f, err := os.Open("testdata/" + filename)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	sentences, err := r.Read(f)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return sentences
}

// at est un helper de construction d'AnnotatedToken.
func at(word, tag string) corpus.AnnotatedToken {
	return corpus.AnnotatedToken{Word: word, Tag: tag}
}

func assertSentences(t *testing.T, got, want []corpus.Sentence) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("nombre de phrases : got %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i, ws := range want {
		gs := got[i]
		if len(gs) != len(ws) {
			t.Fatalf("phrase[%d] : %d tokens, want %d\ngot:  %+v\nwant: %+v", i, len(gs), len(ws), gs, ws)
		}
		for j, wt := range ws {
			if gs[j] != wt {
				t.Errorf("phrase[%d] token[%d]: got %+v, want %+v", i, j, gs[j], wt)
			}
		}
	}
}

// --- Lecture de base ---

func TestReadTwoPhrases(t *testing.T) {
	r := &corpus.ConLLReader{TagColumn: -1}
	sentences := readFixture(t, r, "sample_bio.conll")

	assertSentences(t, sentences, []corpus.Sentence{
		{at("John", "B-PER"), at("Doe", "I-PER"), at("lives", "O"), at("in", "O"), at("Paris", "B-LOC"), at(".", "O")},
		{at("Peter", "B-PER"), at("works", "O"), at("at", "O"), at("Airbus", "B-ORG"), at(".", "O")},
	})
}

// --- Gestion de -DOCSTART- ---

func TestDocStartIgnored(t *testing.T) {
	r := &corpus.ConLLReader{TagColumn: -1}
	sentences := readFixture(t, r, "sample_docstart.conll")

	// -DOCSTART- doit être ignoré ; 1 seule phrase attendue.
	assertSentences(t, sentences, []corpus.Sentence{
		{at("John", "B-PER"), at("lives", "O"), at("in", "O"), at("France", "B-LOC"), at(".", "O")},
	})
}

// --- Fichier vide ---

func TestReadEmpty(t *testing.T) {
	r := &corpus.ConLLReader{TagColumn: -1}
	sentences, err := r.Read(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(sentences) != 0 {
		t.Fatalf("attendu 0 phrase pour fichier vide, got %d", len(sentences))
	}
}

// --- Flush de la dernière phrase sans ligne vide finale ---

func TestReadNoTrailingNewline(t *testing.T) {
	input := "hello\tB-PER\nworld\tO"
	r := &corpus.ConLLReader{TagColumn: -1}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("hello", "B-PER"), at("world", "O")},
	})
}

// --- TagColumn = -1 (dernière colonne) ---

func TestTagColumnLastExplicit(t *testing.T) {
	// 3 colonnes : mot, pos, tag — TagColumn=-1 prend le tag.
	input := "John\tNNP\tB-PER\nDoe\tNNP\tI-PER\n"
	r := &corpus.ConLLReader{TagColumn: -1}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("John", "B-PER"), at("Doe", "I-PER")},
	})
}

// --- TagColumn = 0 (première colonne) ---

func TestTagColumnFirst(t *testing.T) {
	// Colonne 0 = tag, colonne 1 = mot (format inhabituel).
	input := "B-PER\tJohn\nI-PER\tDoe\n"
	r := &corpus.ConLLReader{WordColumn: 1, TagColumn: 0}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("John", "B-PER"), at("Doe", "I-PER")},
	})
}

// --- Séparateur personnalisé ---

func TestCustomSeparator(t *testing.T) {
	input := "John,B-PER\nDoe,I-PER\n"
	r := &corpus.ConLLReader{TagColumn: -1, Separator: ","}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("John", "B-PER"), at("Doe", "I-PER")},
	})
}

// --- Fallback "O" si champs insuffisants ---

func TestTagFallbackWhenMissingColumn(t *testing.T) {
	// TagColumn=-1 avec une ligne à 1 seule colonne : la seule colonne est le mot,
	// et est aussi utilisée comme tag (len(fields)-1 = 0 = WordColumn).
	// Pour tester le vrai fallback, on utilise un TagColumn explicite hors limites.
	input := "John\n"
	r := &corpus.ConLLReader{WordColumn: 0, TagColumn: 5}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("John", "O")},
	})
}

// --- Plusieurs phrases séparées par plusieurs lignes vides ---

func TestMultipleBlankLines(t *testing.T) {
	input := "a\tB-PER\n\n\nb\tO\n"
	r := &corpus.ConLLReader{TagColumn: -1}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("a", "B-PER")},
		{at("b", "O")},
	})
}
