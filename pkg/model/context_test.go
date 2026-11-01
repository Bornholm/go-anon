package model_test

import (
	"context"
	"errors"
	"testing"
)

// Un entraînement annulé s'arrête et ne rend pas de modèle, en séquentiel
// comme en parallèle.
func TestTrain_Cancelled(t *testing.T) {
	for _, workers := range []int{0, 4} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tr := newDefaultTrainer()
		tr.Config.NumWorkers = workers

		crf, err := tr.Train(ctx, toyCorpus(), nil)
		if !errors.Is(err, context.Canceled) || crf != nil {
			t.Errorf("workers=%d: Train = %v, %v; want no model and context.Canceled", workers, crf, err)
		}
	}
}
