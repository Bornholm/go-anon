package anonymizer

import (
	"context"
	"errors"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

type ctxKey struct{}

// contextRecognizer retient le contexte reçu.
type contextRecognizer struct {
	entities []ner.Entity
	got      context.Context
	calls    int
	err      error
}

func (r *contextRecognizer) Recognize(ctx context.Context, text string) ([]ner.Entity, error) {
	r.got = ctx
	r.calls++
	return r.entities, r.err
}

func TestAnonymize_PassesTheContextToTheRecognizer(t *testing.T) {
	rec := &contextRecognizer{entities: []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}}
	anon := New(rec, Config{Strategy: TagReplace})
	ctx := context.WithValue(context.Background(), ctxKey{}, "anonymize")

	result, err := anon.Anonymize(ctx, "John Doe works at Acme.")
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if len(result.Entities) != 1 {
		t.Fatalf("entities = %+v, want John Doe", result.Entities)
	}
	if rec.got == nil || rec.got.Value(ctxKey{}) != "anonymize" {
		t.Errorf("the recognizer did not get the context of the call")
	}

	ctx = context.WithValue(context.Background(), ctxKey{}, "detect")
	if _, err := anon.Detect(ctx, "John Doe"); err != nil || rec.got.Value(ctxKey{}) != "detect" {
		t.Errorf("Detect: %v, the recognizer did not get the context of the call", err)
	}
}

// Un contexte déjà terminé fait échouer l'appel avant la reconnaissance.
func TestAnonymize_EndedContextStopsBeforeRecognition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := &contextRecognizer{}
	anon := New(rec, Config{Strategy: TagReplace})
	if _, err := anon.Anonymize(ctx, "John Doe"); !errors.Is(err, context.Canceled) {
		t.Errorf("Anonymize = %v, want context.Canceled", err)
	}
	if _, err := anon.Detect(ctx, "John Doe"); !errors.Is(err, context.Canceled) {
		t.Errorf("Detect = %v, want context.Canceled", err)
	}
	if rec.calls != 0 {
		t.Errorf("the recognizer was called %d times on an ended context", rec.calls)
	}
}

// Un recognizer qui s'arrête sur le contexte voit son erreur remonter, reliée
// à sa cause.
func TestAnonymize_RecognizerContextErrorIsWrapped(t *testing.T) {
	anon := New(&contextRecognizer{err: context.DeadlineExceeded}, Config{Strategy: TagReplace})
	if _, err := anon.Anonymize(context.Background(), "John Doe"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Anonymize = %v, want context.DeadlineExceeded wrapped", err)
	}
}
