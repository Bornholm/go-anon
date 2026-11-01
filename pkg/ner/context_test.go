package ner

import (
	"context"
	"errors"
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/tokenizer"
)

// countdownContext se termine après n consultations de Err : il annule un
// appel au milieu de son travail, sans dépendre du temps.
type countdownContext struct {
	context.Context
	n int
}

func (c *countdownContext) Err() error {
	if c.n <= 0 {
		return context.Canceled
	}
	c.n--
	return nil
}

// Recognize s'arrête à la ligne qui suit l'annulation.
func TestRecognizer_StopsBetweenLines(t *testing.T) {
	rec := &Recognizer{
		tok:       &tokenizer.UnicodeTokenizer{},
		extractor: &features.FeatureExtractor{WindowSize: 2},
		crf:       newTestCRF(),
	}

	ctx := &countdownContext{Context: context.Background(), n: 1}
	if _, err := rec.Recognize(ctx, "première ligne\ndeuxième ligne\ntroisième ligne"); !errors.Is(err, context.Canceled) {
		t.Errorf("Recognize = %v, want context.Canceled", err)
	}

	ctx = &countdownContext{Context: context.Background(), n: 3}
	if _, err := rec.Recognize(ctx, "première ligne\ndeuxième ligne\ntroisième ligne"); err != nil {
		t.Errorf("Recognize with time for every line = %v", err)
	}
}

func TestEvaluate_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &mockEvalRecognizer{}
	sentences := []corpus.Sentence{{{Word: "Jean", Tag: "B-PER"}}}
	if _, err := Evaluate(ctx, rec, sentences); !errors.Is(err, context.Canceled) {
		t.Errorf("Evaluate = %v, want context.Canceled", err)
	}
}

// cancellingRecognizer annule son contexte pendant la reconnaissance, et
// renvoie l'erreur du contexte comme un recognizer qui s'arrête.
type cancellingRecognizer struct{ cancel context.CancelFunc }

func (r cancellingRecognizer) Recognize(ctx context.Context, text string) ([]Entity, error) {
	r.cancel()
	return nil, ctx.Err()
}

// Une annulation pendant la dernière phrase fait échouer Evaluate : des
// métriques sans cette phrase passeraient pour complètes.
func TestEvaluate_CancelledDuringTheLastSentence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sentences := []corpus.Sentence{{{Word: "Jean", Tag: "B-PER"}}}
	if m, err := Evaluate(ctx, cancellingRecognizer{cancel}, sentences); !errors.Is(err, context.Canceled) {
		t.Errorf("Evaluate = %v, %v; want context.Canceled", m, err)
	}
}

func TestRecognizer_EmptyTextWithEndedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &Recognizer{tok: &tokenizer.UnicodeTokenizer{}, extractor: &features.FeatureExtractor{WindowSize: 2}, crf: newTestCRF()}
	if _, err := rec.Recognize(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("Recognize = %v, want context.Canceled", err)
	}
}
