package ner

import (
	"bytes"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/model"
)

// buildBenchRecognizer entraîne un mini-CRF déterministe sur un corpus
// synthétique puis construit le Recognizer complet (tokenizer + extracteur +
// décodage). Le but n'est pas la qualité NER mais un chemin chaud réaliste.
func buildBenchRecognizer(tb testing.TB) *Recognizer {
	tb.Helper()

	// Le Trainer journalise la collecte des bases v3 ; silencieux pendant le bench.
	prevOut := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prevOut)

	firstNames := []string{"Jean", "Marie", "Pierre", "Sophie", "Luc", "Anne"}
	lastNames := []string{"Dupont", "Martin", "Bernard", "Petit", "Durand", "Moreau"}
	cities := []string{"Paris", "Lyon", "Marseille", "Lille", "Nantes", "Rennes"}

	var sents []corpus.Sentence
	for i := 0; i < 240; i++ {
		fn := firstNames[i%len(firstNames)]
		ln := lastNames[(i/2)%len(lastNames)]
		city := cities[(i/3)%len(cities)]
		sents = append(sents, corpus.Sentence{
			{Word: fn, Tag: "B-PER"},
			{Word: ln, Tag: "I-PER"},
			{Word: "habite", Tag: "O"},
			{Word: "à", Tag: "O"},
			{Word: city, Tag: "B-LOC"},
			{Word: ".", Tag: "O"},
		})
	}

	trainer := &model.Trainer{
		Config: model.TrainConfig{
			Epochs:       3,
			LearningRate: 0.1,
			NumWorkers:   1,
			Shuffle:      false,
		},
		Extractor: &features.FeatureExtractor{WindowSize: 3},
	}
	crf, err := trainer.Train(sents, nil)
	if err != nil {
		tb.Fatalf("entraînement du CRF de benchmark : %v", err)
	}
	crf.FeatureCfg.WindowSize = 3

	// Cycle Save/Load : le benchmark mesure la représentation de production
	// (format groupé v3 émis par l'entraînement).
	var buf bytes.Buffer
	if err := crf.Save(&buf); err != nil {
		tb.Fatalf("sérialisation du modèle de benchmark : %v", err)
	}
	m, err := LoadModel(&buf)
	if err != nil {
		tb.Fatalf("chargement du modèle de benchmark : %v", err)
	}

	rec, err := New(m, WithLanguage("fr"))
	if err != nil {
		tb.Fatalf("construction du Recognizer : %v", err)
	}
	return rec
}

var benchText = strings.Repeat(
	"Jean Dupont, directeur régional, habite à Paris depuis longtemps. "+
		"Marie Curie travaille avec Pierre Durand à Lyon.\n", 8)

func BenchmarkRecognize(b *testing.B) {
	rec := buildBenchRecognizer(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rec.Recognize(benchText); err != nil {
			b.Fatal(err)
		}
	}
}
