package model_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
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

// Sans époque ni phrase, aucune boucle ne consulte le contexte : l'annulation
// est tout de même constatée.
func TestTrain_CancelledOnAnEmptyCorpus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr := newDefaultTrainer()
	tr.Config.Epochs = 0 // aucune époque : seul le contrôle final le voit
	if crf, err := tr.Train(ctx, nil, nil); !errors.Is(err, context.Canceled) || crf != nil {
		t.Errorf("Train = %v, %v; want no model and context.Canceled", crf, err)
	}
}

// countdownContext se termine après n consultations de Err.
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

// Une annulation pendant l'évaluation sur dev arrête l'entraînement sans
// modèle. Une époque consulte le contexte une fois, puis une fois par phrase
// d'entraînement : il se termine sur la première phrase de dev.
func TestTrain_CancelledDuringDevEvaluation(t *testing.T) {
	train := toyCorpus()
	tr := newDefaultTrainer()
	tr.Config.Epochs = 1

	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	ctx := &countdownContext{Context: context.Background(), n: 1 + len(train)}
	if crf, err := tr.Train(ctx, train, train); !errors.Is(err, context.Canceled) || crf != nil {
		t.Errorf("Train = %v, %v; want no model and context.Canceled", crf, err)
	}
	// L'évaluation s'arrête avant d'aller au bout : elle ne rapporte pas de F1.
	if strings.Contains(logs.String(), "F1=") {
		t.Errorf("the dev evaluation ran to its end: %s", logs.String())
	}

	ctx = &countdownContext{Context: context.Background(), n: 1 << 20}
	if crf, err := tr.Train(ctx, train, train); err != nil || crf == nil {
		t.Errorf("Train with time for everything = %v, %v", crf, err)
	}
}
