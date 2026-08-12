package generate

import (
	"fmt"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/ner"
	"github.com/bornholm/go-anon/pkg/synth/render"
	"github.com/bornholm/go-anon/pkg/tokenizer"
)

// Tokenizer retourne le tokeniseur configuré comme à l'inférence pour une
// langue. C'est le même package que le pipeline de production : toute
// divergence introduirait un décalage systématique entre corpus et production
// (DATASET.md § 9.1).
func Tokenizer(lang string) tokenizer.Tokenizer {
	switch lang {
	case "fr", "es":
		return &tokenizer.UnicodeTokenizer{SplitApostrophe: true}
	default:
		return &tokenizer.UnicodeTokenizer{SplitHyphen: true}
	}
}

// boundaries reprend les délimiteurs de phrase par défaut du Recognizer.
var boundaries = func() map[string]bool {
	m := make(map[string]bool, len(ner.DefaultSentenceBoundaries))
	for _, t := range ner.DefaultSentenceBoundaries {
		m[t] = true
	}
	return m
}()

// ProjectBIO projette les spans caractères sur les tokens et découpe en phrases.
//
// Le découpage reproduit celui de l'inférence : d'abord par ligne, puis aux
// seules fins de phrase, ponctuation conservée dans la séquence.
func ProjectBIO(text string, spans []render.Span, lang string) ([]corpus.Sentence, error) {
	tok := Tokenizer(lang)

	// labelAt associe à chaque byte le span qui le couvre. Les spans étant
	// produits par concaténation de segments, ils ne peuvent pas se chevaucher ;
	// la vérification reste comme filet contre une régression du renderer.
	owner := make([]int, len(text))
	for i := range owner {
		owner[i] = -1
	}
	for si, s := range spans {
		if s.Start < 0 || s.End > len(text) || s.Start >= s.End {
			return nil, fmt.Errorf("span dégénéré %+v", s)
		}
		for i := s.Start; i < s.End; i++ {
			if owner[i] != -1 {
				return nil, fmt.Errorf("spans chevauchants : %+v et %+v", spans[owner[i]], s)
			}
			owner[i] = si
		}
	}

	var sentences []corpus.Sentence
	var cur corpus.Sentence
	prevSpan := -1

	flush := func() {
		if len(cur) > 0 {
			sentences = append(sentences, cur)
			cur = nil
		}
		prevSpan = -1
	}

	lineStart := 0
	for i := 0; i <= len(text); i++ {
		if i < len(text) && text[i] != '\n' {
			continue
		}
		line := text[lineStart:i]
		base := lineStart
		lineStart = i + 1

		for _, t := range tok.Tokenize(line) {
			start, end := base+t.Start, base+t.End
			si := spanOf(owner, start, end)
			tag := "O"
			if si >= 0 {
				if si == prevSpan {
					tag = "I-" + string(spans[si].Label)
				} else {
					tag = "B-" + string(spans[si].Label)
				}
			}
			prevSpan = si
			cur = append(cur, corpus.AnnotatedToken{Word: t.Text, Tag: tag})

			if !t.IsWord && boundaries[t.Text] {
				flush()
			}
		}
		flush()
	}
	return sentences, nil
}

// spanOf retourne l'index du span couvrant un token.
//
// Politique de résolution des frontières partielles : un token est attribué au
// span dès qu'il le chevauche, même partiellement. Le bruit d'espacement peut
// en effet couper un mot au milieu d'un span ; refuser l'attribution
// produirait des entités tronquées, ce qui est pire qu'une frontière élargie
// pour un usage d'anonymisation.
func spanOf(owner []int, start, end int) int {
	for i := start; i < end && i < len(owner); i++ {
		if owner[i] != -1 {
			return owner[i]
		}
	}
	return -1
}
