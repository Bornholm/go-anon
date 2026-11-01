package docprocessor

import (
	"context"
	"errors"
	"testing"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// Un traitement annulé s'arrête sans réécrire de segment : l'appelant ne doit
// pas écrire de document de sortie.
func TestProcess_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &sliceWalker{segments: []string{"Écrire à jean@example.com", "puis à marie@example.com"}}
	proc := New(anonymizer.New(emailRecognizer{}, anonymizer.Config{Strategy: anonymizer.TagReplace}))

	if _, err := proc.Process(ctx, w); !errors.Is(err, context.Canceled) {
		t.Errorf("Process = %v, want context.Canceled", err)
	}
	if len(w.replaced) != 0 || w.walked > 1 {
		t.Errorf("a cancelled process walked %d segments and rewrote %v", w.walked, w.replaced)
	}
}
