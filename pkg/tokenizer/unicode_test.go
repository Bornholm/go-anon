package tokenizer_test

import (
	"testing"

	"github.com/bornholm/go-anon/pkg/tokenizer"
)

// tokenizer par défaut : SplitHyphen=false, SplitApostrophe=false.
var defaultTok = &tokenizer.UnicodeTokenizer{}

// tokenizer français : SplitApostrophe=true, SplitHyphen=false.
var frTok = &tokenizer.UnicodeTokenizer{SplitApostrophe: true}

// tokenizer anglais : SplitHyphen=true, SplitApostrophe=false.
var enTok = &tokenizer.UnicodeTokenizer{SplitHyphen: true}

// tok est un helper de construction de Token attendu.
func tok(text string, start, end int, isWord bool) tokenizer.Token {
	return tokenizer.Token{Text: text, Start: start, End: end, IsWord: isWord}
}

func assertTokens(t *testing.T, got []tokenizer.Token, want []tokenizer.Token) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("nombre de tokens : got %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}

	for i, w := range want {
		g := got[i]
		if g != w {
			t.Errorf("token[%d]: got %+v, want %+v", i, g, w)
		}
	}
}

// assertOffsets vérifie que text[tok.Start:tok.End] == tok.Text pour chaque token.
func assertOffsets(t *testing.T, text string, tokens []tokenizer.Token) {
	t.Helper()
	for i, tok := range tokens {
		slice := text[tok.Start:tok.End]
		if slice != tok.Text {
			t.Errorf("token[%d]: text[%d:%d]=%q ≠ tok.Text=%q", i, tok.Start, tok.End, slice, tok.Text)
		}
	}
}

// --- Cas de base ---

func TestEmpty(t *testing.T) {
	got := defaultTok.Tokenize("")
	if len(got) != 0 {
		t.Fatalf("chaîne vide : attendu 0 token, got %d", len(got))
	}
}

func TestSpacesOnly(t *testing.T) {
	got := defaultTok.Tokenize("   ")
	if len(got) != 0 {
		t.Fatalf("espaces seuls : attendu 0 token, got %d", len(got))
	}
}

func TestSingleWord(t *testing.T) {
	got := defaultTok.Tokenize("hello")
	assertTokens(t, got, []tokenizer.Token{
		tok("hello", 0, 5, true),
	})
	assertOffsets(t, "hello", got)
}

// --- Anglais basique ---

func TestEnglishBasic(t *testing.T) {
	text := "Hello, world!"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("Hello", 0, 5, true),
		tok(",", 5, 6, false),
		tok("world", 7, 12, true),
		tok("!", 12, 13, false),
	})
	assertOffsets(t, text, got)
}

func TestEnglishContraction(t *testing.T) {
	// Sans SplitApostrophe : "don't" reste un seul token.
	text := "don't cry"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("don't", 0, 5, true),
		tok("cry", 6, 9, true),
	})
	assertOffsets(t, text, got)
}

func TestEnglishHyphenSplit(t *testing.T) {
	// SplitHyphen=true : "well-known" → trois tokens.
	text := "well-known"
	got := enTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("well", 0, 4, true),
		tok("-", 4, 5, false),
		tok("known", 5, 10, true),
	})
	assertOffsets(t, text, got)
}

// --- Français ---

func TestFrenchApostrophe(t *testing.T) {
	// SplitApostrophe=true : "l'homme" → ["l'", "homme"].
	text := "l'homme"
	got := frTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("l'", 0, 2, true),
		tok("homme", 2, 7, true),
	})
	assertOffsets(t, text, got)
}

func TestFrenchApostropheTypographic(t *testing.T) {
	// Apostrophe typographique U+2019 (3 bytes en UTF-8).
	text := "l\u2019homme"
	got := frTok.Tokenize(text)
	// "l" = 1 byte, U+2019 = 3 bytes → "l'" = [0:4], "homme" = [4:9]
	assertTokens(t, got, []tokenizer.Token{
		tok("l\u2019", 0, 4, true),
		tok("homme", 4, 9, true),
	})
	assertOffsets(t, text, got)
}

func TestFrenchHyphenKept(t *testing.T) {
	// SplitHyphen=false (défaut) : "peut-être" reste un seul token.
	text := "peut-être"
	got := frTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("peut-être", 0, len(text), true),
	})
	assertOffsets(t, text, got)
}

func TestFrenchAccents(t *testing.T) {
	text := "Ça va très bien"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("Ça", 0, 3, true),    // Ç=2 bytes, a=1 byte
		tok("va", 4, 6, true),
		tok("très", 7, 12, true), // t=1 r=1 è=2 s=1 → 5 bytes
		tok("bien", 13, 17, true),
	})
	assertOffsets(t, text, got)
}

func TestFrenchSentence(t *testing.T) {
	text := "Jean Dupont habite à Paris."
	got := frTok.Tokenize(text)
	// à = U+00E0 = 2 bytes
	// offsets : Jean[0:4] Dupont[5:11] habite[12:18] à[19:21] Paris[22:27] .[27:28]
	assertTokens(t, got, []tokenizer.Token{
		tok("Jean", 0, 4, true),
		tok("Dupont", 5, 11, true),
		tok("habite", 12, 18, true),
		tok("à", 19, 21, true),
		tok("Paris", 22, 27, true),
		tok(".", 27, 28, false),
	})
	assertOffsets(t, text, got)
}

// --- Chiffres et alphanumérique ---

func TestDigits(t *testing.T) {
	text := "42"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("42", 0, 2, true),
	})
	assertOffsets(t, text, got)
}

func TestAlphanumeric(t *testing.T) {
	// Lettres et chiffres dans le même token.
	text := "42ème"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("42ème", 0, len(text), true),
	})
	assertOffsets(t, text, got)
}

// --- Ponctuation ---

func TestPunctuationMixed(t *testing.T) {
	// "M. Dupont" → ["M", ".", "Dupont"]
	text := "M. Dupont"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("M", 0, 1, true),
		tok(".", 1, 2, false),
		tok("Dupont", 3, 9, true),
	})
	assertOffsets(t, text, got)
}

func TestConsecutivePunctuation(t *testing.T) {
	text := "..."
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok(".", 0, 1, false),
		tok(".", 1, 2, false),
		tok(".", 2, 3, false),
	})
	assertOffsets(t, text, got)
}

// --- Espaces ---

func TestMultipleSpaces(t *testing.T) {
	text := "a  b"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("a", 0, 1, true),
		tok("b", 3, 4, true),
	})
	assertOffsets(t, text, got)
}

func TestTabAndNewline(t *testing.T) {
	text := "foo\tbar\nbaz"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("foo", 0, 3, true),
		tok("bar", 4, 7, true),
		tok("baz", 8, 11, true),
	})
	assertOffsets(t, text, got)
}

// --- Offsets byte-accurate ---

func TestOffsetsASCII(t *testing.T) {
	text := "Hi there"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("Hi", 0, 2, true),
		tok("there", 3, 8, true),
	})
	assertOffsets(t, text, got)
}

func TestOffsetsUTF8Multibyte(t *testing.T) {
	// "Ça" : Ç = U+00C7 = 2 bytes, a = 1 byte → total 3 bytes.
	text := "Ça"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("Ça", 0, 3, true),
	})
	assertOffsets(t, text, got)
}

func TestOffsetsAfterMultibyteWord(t *testing.T) {
	// "Ça va" : "Ça"[0:3], space[3], "va"[4:6]
	text := "Ça va"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("Ça", 0, 3, true),
		tok("va", 4, 6, true),
	})
	assertOffsets(t, text, got)
}

// --- Emojis ---

func TestEmoji(t *testing.T) {
	// 🌍 = U+1F30D = 4 bytes en UTF-8.
	text := "Hello 🌍"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("Hello", 0, 5, true),
		tok("🌍", 6, 10, false),
	})
	assertOffsets(t, text, got)
}

// --- Trait d'union hors mot ---

func TestHyphenAlone(t *testing.T) {
	text := "a - b"
	got := defaultTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("a", 0, 1, true),
		tok("-", 2, 3, false),
		tok("b", 4, 5, true),
	})
	assertOffsets(t, text, got)
}

// --- Apostrophe hors mot ---

func TestApostropheAlone(t *testing.T) {
	text := "' test"
	got := frTok.Tokenize(text)
	assertTokens(t, got, []tokenizer.Token{
		tok("'", 0, 1, false),
		tok("test", 2, 6, true),
	})
	assertOffsets(t, text, got)
}

// --- Interface Tokenizer ---

func TestImplementsInterface(t *testing.T) {
	// Vérification statique que UnicodeTokenizer implémente Tokenizer.
	var _ tokenizer.Tokenizer = &tokenizer.UnicodeTokenizer{}
}
