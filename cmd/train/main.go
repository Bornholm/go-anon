// Commande train — entraîne un modèle CRF-NER sur un corpus annoté.
//
// Usage :
//
//	train -train corpus.conll -lang en -output model.crf.gz
//
// Formats supportés : "conll" (CoNLL-2002/2003) et "wikiner".
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/lang"
	"github.com/bornholm/go-anon/pkg/model"
)

func main() {
	trainPath := flag.String("train", "", "corpus d'entraînement (obligatoire)")
	devPath := flag.String("dev", "", "corpus de validation pour early stopping (optionnel)")
	langCode := flag.String("lang", "en", `langue : "fr", "en" ou "es"`)
	outputPath := flag.String("output", "model.crf.gz", "chemin de sortie .crf.gz")
	format := flag.String("format", "conll", `format corpus : "conll" ou "wikiner"`)
	tagCol := flag.Int("tag-col", -1, "colonne NER dans CoNLL (-1 = dernière)")
	epochs := flag.Int("epochs", 20, "nombre d'époques")
	lr := flag.Float64("lr", 0.1, "learning rate SGD initial")
	lrDecay := flag.Float64("lr-decay", 0.0, "decay multiplicatif du LR par époque (0=désactivé, ex: 0.95)")
	l2 := flag.Float64("l2", 0.01, "coefficient de régularisation L2")
	window := flag.Int("window", 3, "demi-fenêtre de contexte")
	workers := flag.Int("workers", runtime.NumCPU(), "nombre de goroutines parallèles")
	earlyStop := flag.Int("early-stop", 5, "arrêt anticipé après N epochs sans amélioration (0 = désactivé)")
	batchSize := flag.Int("batch-size", 1, "taille du mini-batch (1 = SGD classique, plus = plus stable)")
	dropout := flag.Float64("dropout", 0.0, "taux de dropout des features (0 = désactivé, ex: 0.1)")
	bioes := flag.Bool("bioes", false, "convertir BIO → BIOES avant entraînement")
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à inclure : "nom:fichier.txt,nom:fichier.txt"`)
	clustersPath := flag.String("clusters", "", "fichier Brown clusters (optionnel)")
	embeddingsPath := flag.String("embeddings", "", "fichier word embeddings GloVe (optionnel)")
	optimizer := flag.String("optimizer", "sgd", `optimiseur : "sgd", "momentum", ou "adam"`)
	momentum := flag.Float64("momentum", 0.9, "coefficient de momentum pour optimizer momentum")
	pruneThreshold := flag.Float64("prune-threshold", 0.0, "seuil d'élagage des poids avant sauvegarde (0=désactivé, ex: 0.001)")

	flag.Parse()

	if *trainPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -train est obligatoire")
		flag.Usage()
		os.Exit(1)
	}

	// --- Chargement du corpus d'entraînement ---
	log.Printf("chargement du corpus : %s (format %s)", *trainPath, *format)
	trainSents, err := loadCorpus(*trainPath, *format, *tagCol)
	if err != nil {
		log.Fatalf("chargement corpus train : %v", err)
	}
	log.Printf("corpus train : %d phrases", len(trainSents))

	var devSents []corpus.Sentence
	if *devPath != "" {
		log.Printf("chargement dev : %s", *devPath)
		devSents, err = loadCorpus(*devPath, *format, *tagCol)
		if err != nil {
			log.Fatalf("chargement corpus dev : %v", err)
		}
		log.Printf("corpus dev : %d phrases", len(devSents))
	}

	// --- Conversion de schéma ---
	if *bioes {
		log.Println("conversion BIO → BIOES")
		trainSents = corpus.ConvertBIOtoBIOES(trainSents)
		if devSents != nil {
			devSents = corpus.ConvertBIOtoBIOES(devSents)
		}
	}

	// --- Chargement des gazetteers ---
	gazetteers, err := cmdutil.ParseGazetteers(*gazetteerFlag)
	if err != nil {
		log.Fatalf("chargement gazetteers : %v", err)
	}
	if len(gazetteers) > 0 {
		log.Printf("gazetteers chargés : %v", cmdutil.GazetteerNames(gazetteers))
	}

	// --- Chargement des Brown clusters ---
	var clusters *features.BrownClusters
	if *clustersPath != "" {
		f, err := os.Open(*clustersPath)
		if err != nil {
			log.Fatalf("chargement clusters : %v", err)
		}
		clusters, err = features.LoadBrownClusters(f)
		f.Close()
		if err != nil {
			log.Fatalf("chargement clusters : %v", err)
		}
		log.Printf("Brown clusters chargés : %s", *clustersPath)
	}

	// --- Chargement des embeddings ---
	var embeddings *features.Embeddings
	if *embeddingsPath != "" {
		log.Printf("chargement embeddings : %s", *embeddingsPath)
		embeddings, err = features.LoadEmbeddings(*embeddingsPath)
		if err != nil {
			log.Fatalf("chargement embeddings : %v", err)
		}
		log.Printf("embeddings chargés : dim=%d", embeddings.Dim())
	}

	// --- Configuration du FeatureExtractor ---
	extractor := &features.FeatureExtractor{
		WindowSize:  *window,
		LangProfile: langProfile(*langCode),
		Gazetteers:  gazetteers,
		Clusters:    clusters,
		Embeddings:  embeddings,
	}

	// --- Entraînement ---
	config := model.TrainConfig{
		Epochs:       *epochs,
		LearningRate: *lr,
		LRDecay:      *lrDecay,
		L2Lambda:     *l2,
		Shuffle:      true,
		EarlyStop:    *earlyStop,
		NumWorkers:   *workers,
		BatchSize:    *batchSize,
		DropoutRate:  *dropout,
		Optimizer:    *optimizer,
		Momentum:     *momentum,
	}

	trainer := &model.Trainer{
		Config:    config,
		Extractor: extractor,
	}

	log.Printf("démarrage entraînement : %d époques, lr=%.4f, lr-decay=%.4f, l2=%.4f, workers=%d",
		*epochs, *lr, *lrDecay, *l2, *workers)

	crf, err := trainer.Train(trainSents, devSents)
	if err != nil {
		log.Fatalf("entraînement : %v", err)
	}

	// --- Élagage des poids (optionnel) ---
	if *pruneThreshold > 0 {
		removed := crf.Weights.Prune(*pruneThreshold)
		total := crf.Weights.Len()
		log.Printf("élagage (seuil=%.4f) : %d entrées supprimées, %d restantes", *pruneThreshold, removed, total)
	}

	// --- Remplir FeatureCfg avant sauvegarde ---
	crf.FeatureCfg = model.FeatureConfig{
		WindowSize:     *window,
		GazetteerNames: cmdutil.GazetteerNames(gazetteers),
		LangCode:       *langCode,
		HasClusters:    clusters != nil,
		HasEmbeddings:  embeddings != nil,
	}

	// --- Sauvegarde ---
	f, err := os.Create(*outputPath)
	if err != nil {
		log.Fatalf("création fichier modèle : %v", err)
	}
	defer f.Close()

	if err := crf.Save(f); err != nil {
		log.Fatalf("sauvegarde modèle : %v", err)
	}

	log.Printf("modèle sauvegardé : %s (%d labels, gazetteers : %v)",
		*outputPath, len(crf.Labels), cmdutil.GazetteerNames(gazetteers))
}

// loadCorpus ouvre et parse un fichier corpus selon le format spécifié.
func loadCorpus(path, format string, tagCol int) ([]corpus.Sentence, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ouverture %q : %w", path, err)
	}
	defer f.Close()

	switch format {
	case "conll":
		r := &corpus.ConLLReader{WordColumn: 0, TagColumn: tagCol}
		return r.Read(f)
	case "wikiner":
		r := &corpus.WikiNERReader{}
		return r.Read(f)
	default:
		return nil, fmt.Errorf("format inconnu %q (utiliser \"conll\" ou \"wikiner\")", format)
	}
}

// langProfile retourne le LangProfile correspondant au code langue.
// Retourne nil si le code est inconnu (pas de profil linguistique).
func langProfile(code string) *lang.LangProfile {
	switch code {
	case "fr":
		return lang.NewFrenchProfile()
	case "en":
		return lang.NewEnglishProfile()
	case "es":
		return lang.NewSpanishProfile()
	default:
		log.Printf("avertissement : langue %q inconnue, pas de profil linguistique", code)
		return nil
	}
}
