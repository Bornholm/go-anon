// Commande eval — évalue un modèle CRF-NER sur un corpus annoté.
//
// Calcule Precision, Recall et F1 (matching strict : type ET span)
// sur le corpus de test, avec un breakdown par type d'entité.
//
// Usage :
//
//	eval -model model.crf.gz -lang en -test corpus.conll
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/ner"
)

func main() {
	modelPath := flag.String("model", "", "chemin vers le modèle .crf.gz (obligatoire)")
	langCode := flag.String("lang", "en", `langue : "fr", "en" ou "es"`)
	testPath := flag.String("test", "", "corpus de test annoté (obligatoire)")
	format := flag.String("format", "conll", `format corpus : "conll" ou "wikiner"`)
	tagCol := flag.Int("tag-col", -1, "colonne NER dans CoNLL (-1 = dernière)")
	useBIOES := flag.Bool("bioes", false, "le corpus est en BIOES (les tags BIOES sont convertis en BIO pour l'évaluation)")
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à utiliser : "nom:fichier.txt,nom:fichier.txt" (doivent correspondre à ceux utilisés à l'entraînement)`)
	clustersPath := flag.String("clusters", "", "fichier Brown clusters (doit correspondre à celui utilisé à l'entraînement)")
	keepPunct := flag.Bool("keep-punct", true, "inclure la ponctuation dans les séquences CRF (comme à l'entraînement) ; -keep-punct=false restaure l'ancien comportement")
	boundaries := flag.String("boundaries", "", `tokens délimiteurs de phrases, séparés par des espaces (ex: ". ! ? …") ; vide = défaut`)

	flag.Parse()

	if *testPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -test est obligatoire")
		flag.Usage()
		os.Exit(1)
	}

	if *modelPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -model est obligatoire")
		flag.Usage()
		os.Exit(1)
	}

	// --- Chargement du corpus de test ---
	log.Printf("chargement corpus test : %s (format %s)", *testPath, *format)
	testSents, err := loadCorpus(*testPath, *format, *tagCol)
	if err != nil {
		log.Fatalf("chargement corpus test : %v", err)
	}
	log.Printf("corpus test : %d phrases", len(testSents))

	// Si le corpus est en BIOES, convertir en BIO pour l'évaluation.
	// Evaluate() fonctionne avec du BIO.
	if *useBIOES {
		testSents = bioesToBIO(testSents)
	}

	// --- Chargement des gazetteers ---
	gazetteers, err := cmdutil.ParseGazetteers(*gazetteerFlag)
	if err != nil {
		log.Fatalf("chargement gazetteers : %v", err)
	}

	// --- Chargement du modèle ---
	mf, err := os.Open(*modelPath)
	if err != nil {
		log.Fatalf("ouverture modèle %q : %v", *modelPath, err)
	}
	defer mf.Close()

	m, err := ner.LoadModel(mf)
	if err != nil {
		log.Fatalf("chargement modèle : %v", err)
	}

	// --- Construction du Recognizer ---
	opts := []ner.RecognizerOption{ner.WithLanguage(*langCode)}

	if len(gazetteers) > 0 {
		opts = append(opts, ner.WithGazetteers(gazetteers))
	}

	if *clustersPath != "" {
		cf, err := os.Open(*clustersPath)
		if err != nil {
			log.Fatalf("ouverture clusters %q : %v", *clustersPath, err)
		}
		clusters, err := features.LoadBrownClusters(cf)
		cf.Close()
		if err != nil {
			log.Fatalf("chargement clusters : %v", err)
		}
		opts = append(opts, ner.WithBrownClusters(clusters))
	}

	opts = append(opts, ner.WithPunctuationTokens(*keepPunct))

	if *boundaries != "" {
		opts = append(opts, ner.WithSentenceBoundaries(strings.Fields(*boundaries)...))
	}

	rec, err := ner.New(m, opts...)
	if err != nil {
		log.Fatalf("initialisation recognizer : %v", err)
	}

	for _, w := range rec.Warnings() {
		log.Printf("avertissement : %s", w)
	}

	// --- Évaluation ---
	log.Println("évaluation en cours...")
	metrics := ner.Evaluate(rec, testSents)

	// --- Affichage ---
	fmt.Printf("\nÉvaluation sur %s (%d phrases)\n\n", *testPath, len(testSents))
	fmt.Printf("Global :\n")
	fmt.Printf("  Precision : %.1f%%\n", metrics.Precision*100)
	fmt.Printf("  Recall    : %.1f%%\n", metrics.Recall*100)
	fmt.Printf("  F1        : %.1f%%\n\n", metrics.F1*100)

	if len(metrics.PerType) > 0 {
		fmt.Printf("Par type :\n")
		for _, et := range []ner.EntityType{ner.TypePER, ner.TypeLOC, ner.TypeORG, ner.TypeMISC} {
			m, ok := metrics.PerType[et]
			if !ok || (m.TotalGold == 0 && m.TotalPred == 0) {
				continue
			}
			fmt.Printf("  %-6s P=%.1f%%  R=%.1f%%  F1=%.1f%%  (gold=%-5d pred=%-5d match=%d)\n",
				et,
				m.Precision*100,
				m.Recall*100,
				m.F1*100,
				m.TotalGold,
				m.TotalPred,
				m.TotalMatch,
			)
		}
	}
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
		return nil, fmt.Errorf("format inconnu %q", format)
	}
}

// bioesToBIO convertit un corpus BIOES en BIO pour l'évaluation.
// E-X → I-X, S-X → B-X.
func bioesToBIO(sentences []corpus.Sentence) []corpus.Sentence {
	result := make([]corpus.Sentence, len(sentences))
	for i, sent := range sentences {
		converted := make(corpus.Sentence, len(sent))
		for j, tok := range sent {
			tag := tok.Tag
			prefix := corpus.TagPrefix(tag)
			entity := corpus.TagEntity(tag)
			switch prefix {
			case "E":
				if entity != "" {
					tag = "I-" + entity
				}
			case "S":
				if entity != "" {
					tag = "B-" + entity
				}
			}
			converted[j] = corpus.AnnotatedToken{Word: tok.Word, Tag: tag}
		}
		result[i] = converted
	}
	return result
}
