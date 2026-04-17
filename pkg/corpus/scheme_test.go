package corpus_test

import (
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
)

// makeSentence construit une Sentence depuis des paires (word, tag).
func makeSentence(pairs ...string) corpus.Sentence {
	if len(pairs)%2 != 0 {
		panic("makeSentence: nombre impair d'arguments")
	}
	s := make(corpus.Sentence, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		s[i/2] = corpus.AnnotatedToken{Word: pairs[i], Tag: pairs[i+1]}
	}
	return s
}

func assertTags(t *testing.T, got corpus.Sentence, wantTags ...string) {
	t.Helper()
	if len(got) != len(wantTags) {
		t.Fatalf("nombre de tokens : got %d, want %d\ngot:  %+v\nwant: %v", len(got), len(wantTags), got, wantTags)
	}
	for i, wt := range wantTags {
		if got[i].Tag != wt {
			t.Errorf("token[%d] %q : tag got %q, want %q", i, got[i].Word, got[i].Tag, wt)
		}
	}
}

// --- ConvertBIOtoBIOES ---

func TestBIOtoBIOES_SequenceMultiTokens(t *testing.T) {
	// B-PER I-PER I-PER O → B-PER I-PER E-PER O
	in := []corpus.Sentence{
		makeSentence("John", "B-PER", "Doe", "I-PER", "Jr", "I-PER", "lives", "O"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "B-PER", "I-PER", "E-PER", "O")
}

func TestBIOtoBIOES_Singleton(t *testing.T) {
	// B-LOC seul → S-LOC
	in := []corpus.Sentence{
		makeSentence("Paris", "B-LOC", "is", "O", "nice", "O"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "S-LOC", "O", "O")
}

func TestBIOtoBIOES_ConsecutiveSingletons(t *testing.T) {
	// B-PER B-LOC → S-PER S-LOC
	in := []corpus.Sentence{
		makeSentence("John", "B-PER", "Paris", "B-LOC"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "S-PER", "S-LOC")
}

func TestBIOtoBIOES_SequenceThenSingleton(t *testing.T) {
	// B-PER I-PER B-LOC → B-PER E-PER S-LOC
	in := []corpus.Sentence{
		makeSentence("John", "B-PER", "Doe", "I-PER", "Paris", "B-LOC"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "B-PER", "E-PER", "S-LOC")
}

func TestBIOtoBIOES_AllO(t *testing.T) {
	// Que des "O" → inchangé.
	in := []corpus.Sentence{
		makeSentence("the", "O", "cat", "O", "sat", "O"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "O", "O", "O")
}

func TestBIOtoBIOES_EmptySentence(t *testing.T) {
	in := []corpus.Sentence{{}}
	out := corpus.ConvertBIOtoBIOES(in)
	if len(out[0]) != 0 {
		t.Fatalf("phrase vide : attendu 0 token, got %d", len(out[0]))
	}
}

func TestBIOtoBIOES_PreservesWords(t *testing.T) {
	// Les mots ne doivent pas être altérés par la conversion.
	in := []corpus.Sentence{
		makeSentence("Jean", "B-PER", "Dupont", "I-PER"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	if out[0][0].Word != "Jean" || out[0][1].Word != "Dupont" {
		t.Errorf("mots altérés : got %+v", out[0])
	}
}

func TestBIOtoBIOES_EntityAtEndOfSentence(t *testing.T) {
	// B-ORG en fin de phrase → S-ORG
	in := []corpus.Sentence{
		makeSentence("works", "O", "at", "O", "Airbus", "B-ORG"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "O", "O", "S-ORG")
}

func TestBIOtoBIOES_LongSequenceAtEndOfSentence(t *testing.T) {
	// B-ORG I-ORG en fin de phrase → B-ORG E-ORG
	in := []corpus.Sentence{
		makeSentence("at", "O", "Air", "B-ORG", "France", "I-ORG"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "O", "B-ORG", "E-ORG")
}

func TestBIOtoBIOES_MultipleSentences(t *testing.T) {
	// Vérification que la conversion s'applique à toutes les phrases.
	in := []corpus.Sentence{
		makeSentence("Paris", "B-LOC"),
		makeSentence("John", "B-PER", "Doe", "I-PER"),
	}
	out := corpus.ConvertBIOtoBIOES(in)
	assertTags(t, out[0], "S-LOC")
	assertTags(t, out[1], "B-PER", "E-PER")
}

func TestBIOtoBIOES_DoesNotMutateInput(t *testing.T) {
	// La conversion ne doit pas modifier les phrases d'entrée.
	in := []corpus.Sentence{
		makeSentence("Paris", "B-LOC"),
	}
	original := in[0][0].Tag
	corpus.ConvertBIOtoBIOES(in)
	if in[0][0].Tag != original {
		t.Errorf("input muté : tag devenu %q", in[0][0].Tag)
	}
}

// --- TagPrefix et TagEntity ---

func TestTagPrefix(t *testing.T) {
	cases := []struct{ tag, want string }{
		{"B-PER", "B"},
		{"I-LOC", "I"},
		{"E-ORG", "E"},
		{"S-MISC", "S"},
		{"O", "O"},
		{"", ""},
	}
	for _, c := range cases {
		got := corpus.TagPrefix(c.tag)
		if got != c.want {
			t.Errorf("TagPrefix(%q) = %q, want %q", c.tag, got, c.want)
		}
	}
}

func TestTagEntity(t *testing.T) {
	cases := []struct{ tag, want string }{
		{"B-PER", "PER"},
		{"I-LOC", "LOC"},
		{"E-ORG", "ORG"},
		{"S-MISC", "MISC"},
		{"O", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := corpus.TagEntity(c.tag)
		if got != c.want {
			t.Errorf("TagEntity(%q) = %q, want %q", c.tag, got, c.want)
		}
	}
}
