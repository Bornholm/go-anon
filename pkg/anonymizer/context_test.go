package anonymizer

import (
	"context"
	"errors"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

type ctxKey struct{}

// contextRecognizer retient le contexte reçu, et compte les appels sans
// contexte.
type contextRecognizer struct {
	entities []ner.Entity
	got      context.Context
	plain    int
}

func (r *contextRecognizer) Recognize(text string) ([]ner.Entity, error) {
	r.plain++
	return r.entities, nil
}

func (r *contextRecognizer) RecognizeContext(ctx context.Context, text string) ([]ner.Entity, error) {
	r.got = ctx
	return r.entities, nil
}

func TestAnonymize_PassesTheContextToTheRecognizer(t *testing.T) {
	rec := &contextRecognizer{entities: []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}}
	anon := New(rec, Config{Strategy: TagReplace})
	ctx := context.WithValue(context.Background(), ctxKey{}, "request")

	result, err := anon.Anonymize("John Doe works at Acme.", WithContext(ctx))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if len(result.Entities) != 1 {
		t.Fatalf("entities = %+v, want John Doe", result.Entities)
	}
	if rec.got == nil || rec.got.Value(ctxKey{}) != "request" || rec.plain != 0 {
		t.Errorf("the recognizer did not get the context of the call: %v, %d calls without", rec.got, rec.plain)
	}

	// Sans WithContext, le recognizer reçoit un contexte de fond.
	rec.got = nil
	if _, err := anon.Anonymize("John Doe works at Acme."); err != nil || rec.got == nil {
		t.Errorf("Anonymize without context: %v, context %v", err, rec.got)
	}
}

func TestDetectContext_PassesTheContext(t *testing.T) {
	rec := &contextRecognizer{}
	anon := New(rec, Config{Strategy: TagReplace})
	ctx := context.WithValue(context.Background(), ctxKey{}, "detect")

	if _, err := anon.DetectContext(ctx, "John Doe"); err != nil {
		t.Fatalf("DetectContext: %v", err)
	}
	if rec.got == nil || rec.got.Value(ctxKey{}) != "detect" {
		t.Errorf("the recognizer did not get the context of the call")
	}
	if _, err := anon.Detect("John Doe"); err != nil || rec.plain != 0 {
		t.Errorf("Detect: %v, %d calls without context", err, rec.plain)
	}
}

// Un contexte déjà terminé fait échouer l'appel avant la reconnaissance, y
// compris pour un recognizer qui ne prend pas de contexte.
func TestAnonymize_EndedContextStopsBeforeRecognition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := &mockRecognizer{}
	anon := New(rec, Config{Strategy: TagReplace})
	if _, err := anon.Anonymize("John Doe", WithContext(ctx)); !errors.Is(err, context.Canceled) {
		t.Errorf("Anonymize = %v, want context.Canceled", err)
	}
	if _, err := anon.DetectContext(ctx, "John Doe"); !errors.Is(err, context.Canceled) {
		t.Errorf("DetectContext = %v, want context.Canceled", err)
	}
}

// Un recognizer qui s'arrête sur le contexte voit son erreur remonter, reliée
// à sa cause.
func TestAnonymize_RecognizerContextErrorIsWrapped(t *testing.T) {
	anon := New(failingContextRecognizer{}, Config{Strategy: TagReplace})
	_, err := anon.Anonymize("John Doe", WithContext(context.Background()))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Anonymize = %v, want context.DeadlineExceeded wrapped", err)
	}
}

type failingContextRecognizer struct{}

func (failingContextRecognizer) Recognize(string) ([]ner.Entity, error) { return nil, nil }

func (failingContextRecognizer) RecognizeContext(context.Context, string) ([]ner.Entity, error) {
	return nil, context.DeadlineExceeded
}
