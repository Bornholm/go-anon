package tokenizer

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// UnicodeTokenizer est un tokenizer basé sur les catégories Unicode.
// Il fonctionne pour le français, l'anglais et l'espagnol sans dépendance externe.
//
// Stratégie : machine à états rune-par-rune.
//   - Lettres et chiffres forment des tokens de type mot (IsWord=true).
//   - Les espaces (au sens Unicode) séparent les tokens sans être émis.
//   - Chaque symbole ou signe de ponctuation isolé devient un token (IsWord=false).
//
// Les options SplitHyphen et SplitApostrophe permettent d'adapter le
// comportement aux conventions linguistiques (FR/ES : apostrophe ; EN : trait d'union).
type UnicodeTokenizer struct {
	// SplitHyphen contrôle le traitement du trait d'union en position de mot.
	// false (défaut FR) : "peut-être" → un seul token.
	// true  (défaut EN) : "well-known" → ["well", "-", "known"].
	SplitHyphen bool

	// SplitApostrophe contrôle le traitement de l'apostrophe en position de mot.
	// true  (défaut FR) : "l'homme" → ["l'", "homme"].
	// false (défaut EN) : "don't"   → un seul token.
	SplitApostrophe bool
}

// isHyphen signale un trait d'union (ASCII ou typographique non-sécable).
func isHyphen(r rune) bool {
	return r == '-' || r == '\u2010' || r == '\u2011'
}

// isApostrophe signale une apostrophe (ASCII ou typographique U+2019).
func isApostrophe(r rune) bool {
	return r == '\'' || r == '\u2019'
}

// Tokenize segmente text en tokens avec des offsets byte-accurate.
// Les offsets respectent l'encodage UTF-8 et sont compatibles avec
// le slicing Go : text[tok.Start:tok.End] == tok.Text.
func (t *UnicodeTokenizer) Tokenize(text string) []Token {
	if text == "" {
		return nil
	}

	var tokens []Token
	var buf strings.Builder
	inWord := false
	wordStart := 0

	// flushWord émet le token mot courant avec end comme offset de fin.
	flushWord := func(end int) {
		if inWord {
			tokens = append(tokens, Token{
				Text:   buf.String(),
				Start:  wordStart,
				End:    end,
				IsWord: true,
			})
			buf.Reset()
			inWord = false
		}
	}

	for i, r := range text {
		runeEnd := i + utf8.RuneLen(r)

		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				wordStart = i
				inWord = true
			}
			buf.WriteRune(r)

		case isHyphen(r) && inWord && !t.SplitHyphen:
			// Trait d'union inclus dans le mot (mode français).
			buf.WriteRune(r)

		case isApostrophe(r) && inWord && t.SplitApostrophe:
			// Apostrophe incluse dans le token courant puis scission.
			// "l'homme" → Token{"l'", 0, 2} puis Token{"homme", 2, 7}.
			buf.WriteRune(r)
			flushWord(runeEnd)

		case isApostrophe(r) && inWord:
			// Apostrophe conservée dans le mot (contraction anglaise).
			buf.WriteRune(r)

		case unicode.IsSpace(r):
			flushWord(i)

		default:
			// Ponctuation, symbole, emoji → token individuel non-mot.
			flushWord(i)
			tokens = append(tokens, Token{
				Text:   string(r),
				Start:  i,
				End:    runeEnd,
				IsWord: false,
			})
		}
	}

	flushWord(len(text))

	return tokens
}
