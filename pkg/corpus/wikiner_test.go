package corpus_test

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
)

// --- Lecture de base ---

func TestWikiNERBasic(t *testing.T) {
	// Une phrase avec 4 tokens : FORME|POS|NER séparés par espaces.
	input := "Il|PRO:PER|O assure|VER:pres|O de|PRP|I-PER Saussure|NAM|I-PER\n"
	r := &corpus.WikiNERReader{}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// normalizeBIO convertit I-PER → B-PER pour le premier token entité
	// (précédé ici de tokens O), conforme au schéma BIO standard.
	assertSentences(t, sentences, []corpus.Sentence{
		{
			at("Il", "O"),
			at("assure", "O"),
			at("de", "B-PER"),
			at("Saussure", "I-PER"),
		},
	})
}

// --- Plusieurs phrases (plusieurs lignes non-vides) ---

func TestWikiNERMultipleSentences(t *testing.T) {
	input := "Jean|NAM|B-PER Dupont|NAM|I-PER habite|VER:pres|O à|PRP|O Paris|NAM|B-LOC .|SENT|O\n" +
		"Il|PRO:PER|O travaille|VER:pres|O chez|PRP|O Airbus|NAM|B-ORG .|SENT|O\n"
	r := &corpus.WikiNERReader{}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{
			at("Jean", "B-PER"), at("Dupont", "I-PER"),
			at("habite", "O"), at("à", "O"),
			at("Paris", "B-LOC"), at(".", "O"),
		},
		{
			at("Il", "O"), at("travaille", "O"),
			at("chez", "O"), at("Airbus", "B-ORG"), at(".", "O"),
		},
	})
}

// --- Lignes vides ignorées ---

func TestWikiNERBlankLinesIgnored(t *testing.T) {
	input := "\nJean|NAM|B-PER Dupont|NAM|I-PER .|SENT|O\n\n"
	r := &corpus.WikiNERReader{}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("Jean", "B-PER"), at("Dupont", "I-PER"), at(".", "O")},
	})
}

// --- Fichier vide ---

func TestWikiNEREmpty(t *testing.T) {
	r := &corpus.WikiNERReader{}
	sentences, err := r.Read(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(sentences) != 0 {
		t.Fatalf("attendu 0 phrase pour fichier vide, got %d", len(sentences))
	}
}

// --- Séparateurs personnalisés ---

func TestWikiNERCustomSeparators(t *testing.T) {
	// Séparateur de tokens = ";" et séparateur de champs = ":"
	input := "Jean:NAM:B-PER;Dupont:NAM:I-PER\n"
	r := &corpus.WikiNERReader{TokenSep: ";", FieldSep: ":"}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("Jean", "B-PER"), at("Dupont", "I-PER")},
	})
}

// --- Champs personnalisés (WordField / TagField) ---

func TestWikiNERCustomFields(t *testing.T) {
	// Format : NER|FORME|POS → WordField=1, TagField=0
	input := "B-PER|Jean|NAM I-PER|Dupont|NAM O|habite|VER:pres\n"
	r := &corpus.WikiNERReader{WordField: 1, TagField: 0}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertSentences(t, sentences, []corpus.Sentence{
		{at("Jean", "B-PER"), at("Dupont", "I-PER"), at("habite", "O")},
	})
}

// --- Schéma BIO complet ---

func TestWikiNERBIOTags(t *testing.T) {
	input := "Saussure|NAM|B-PER ,|PUN|O cours|NOM|O de|PRP|O Paris|NAM|B-LOC .|SENT|O\n"
	r := &corpus.WikiNERReader{}
	sentences, err := r.Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []corpus.Sentence{
		{
			at("Saussure", "B-PER"),
			at(",", "O"),
			at("cours", "O"),
			at("de", "O"),
			at("Paris", "B-LOC"),
			at(".", "O"),
		},
	}
	assertSentences(t, sentences, want)
}

// --- Erreur : champ hors limites ---

func TestWikiNERMissingFieldError(t *testing.T) {
	// Token avec seulement 2 champs alors que TagField=2 attendu.
	input := "Jean|NAM\n"
	r := &corpus.WikiNERReader{}
	_, err := r.Read(strings.NewReader(input))
	if err == nil {
		t.Fatal("attendu une erreur pour champ hors limites, got nil")
	}
}
