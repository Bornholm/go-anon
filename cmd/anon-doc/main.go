// Commande anon-doc — anonymisation de documents bureautiques (DOCX, ...).
//
// Détecte le format à partir de l'extension du fichier d'entrée et instancie
// le Walker approprié. Un flag -format permet de forcer le format.
//
// Usage :
//
//	anon-doc -model model.crf.gz -lang fr -input rapport.docx -output rapport_anon.docx
//	anon-doc -model auto -lang fr -input doc.docx -output out.docx -save-mapping mapping.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	goanon "github.com/bornholm/go-anon"
	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	"github.com/bornholm/go-anon/pkg/anonymizer"
	pkgcsv "github.com/bornholm/go-anon/pkg/csv"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	pkgdocx "github.com/bornholm/go-anon/pkg/docx"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/modelstore"
	pkgodt "github.com/bornholm/go-anon/pkg/odt"
	pkgpdf "github.com/bornholm/go-anon/pkg/pdf"
)

var version = "dev"

// walkerFactory crée un Walker à partir d'un chemin de fichier.
type walkerFactory func(path string) (docprocessor.Walker, error)

// registre des formats supportés par extension (en minuscules).
var walkerFactories = map[string]walkerFactory{
	".docx": pkgdocx.NewWalkerFromFile,
	".odt":  pkgodt.NewWalkerFromFile,
	".csv":  pkgcsv.NewWalkerFromFile,
	".tsv":  pkgcsv.NewWalkerFromFile,
	".pdf":  pkgpdf.NewWalkerFromFile,
}

func main() {
	modelFlag := flag.String("model", "", `chemin local ou "auto"/"auto:fr" pour téléchargement automatique`)
	langCode := flag.String("lang", "auto", `langue : "auto" (détection automatique), "fr", "en" ou "es"`)
	inputPath := flag.String("input", "", "fichier d'entrée à anonymiser (obligatoire)")
	outputPath := flag.String("output", "", "fichier de sortie (obligatoire)")
	format := flag.String("format", "", `format du document : "docx" (auto-détecté si absent)`)
	strategy := flag.String("strategy", "tag", `stratégie : "tag", "redact" ou "hash"`)
	saveMappingPath := flag.String("save-mapping", "", "chemin JSON pour sauvegarder le mapping (optionnel)")
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à utiliser : "nom:fichier.txt,..."`)
	clustersPath := flag.String("clusters", "", "fichier Brown clusters (optionnel)")
	cacheDir := flag.String("models-cache", "", "répertoire de cache pour les modèles téléchargés (optionnel)")
	refresh := flag.Bool("refresh-models", false, "forcer le rafraîchissement du manifeste des modèles")
	offline := flag.Bool("offline", false, "interdire toute requête réseau")

	flag.Parse()

	if *modelFlag == "" || *inputPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -model, -input et -output sont obligatoires")
		flag.Usage()
		os.Exit(1)
	}

	autoLang, isAuto := resolveAutoMode(*modelFlag)

	// --- Résolution du format ---
	ext := strings.ToLower(*format)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(*inputPath))
	} else if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	factory, ok := walkerFactories[ext]
	if !ok {
		fmt.Fprintf(os.Stderr, "erreur : format %q non supporté (formats disponibles : %s)\n", ext, supportedFormats())
		os.Exit(1)
	}

	// --- Détection automatique de la langue ---
	lang := *langCode
	if lang == "auto" {
		sampleWalker, err := factory(*inputPath)
		if err != nil {
			log.Fatalf("ouverture %q : %v", *inputPath, err)
		}
		sample, err := docprocessor.SampleText(sampleWalker, 4000)
		if err != nil {
			log.Fatalf("échantillonnage du document : %v", err)
		}
		lang, err = cmdutil.DetectLanguage(sample, goanon.SupportedLanguages())
		if err != nil {
			log.Fatalf("détection de langue : %v", err)
		}
		log.Printf("langue détectée : %s", lang)
	}

	// --- Chargement du modèle ---
	var modelPath string
	if isAuto {
		modelLang := autoLang
		if modelLang == "" {
			modelLang = lang
		}
		modelPath = resolveAutoModel(modelLang, *cacheDir, *refresh, *offline)
	} else {
		modelPath = *modelFlag
	}

	mf, err := os.Open(modelPath)
	if err != nil {
		log.Fatalf("ouverture modèle %q : %v", modelPath, err)
	}
	defer mf.Close()

	m, err := goanon.LoadModel(mf)
	if err != nil {
		log.Fatalf("chargement modèle : %v", err)
	}

	// --- Gazetteers ---
	gazetteers, err := cmdutil.ParseGazetteers(*gazetteerFlag)
	if err != nil {
		log.Fatalf("chargement gazetteers : %v", err)
	}

	if gzLang, isAutoGz := resolveAutoMode(*gazetteerFlag); isAutoGz {
		gzModelLang := gzLang
		if gzModelLang == "" {
			gzModelLang = lang
		}
		autoGz := resolveAutoGazetteers(gzModelLang, *cacheDir, *refresh, *offline)
		if gazetteers == nil {
			gazetteers = autoGz
		} else {
			for k, v := range autoGz {
				gazetteers[k] = v
			}
		}
	}

	// --- Recognizer ---
	recOpts := []goanon.RecognizerOption{goanon.WithLanguage(lang)}
	if len(gazetteers) > 0 {
		recOpts = append(recOpts, goanon.WithGazetteers(gazetteers))
	}
	if *clustersPath != "" {
		f, err := os.Open(*clustersPath)
		if err != nil {
			log.Fatalf("chargement clusters : %v", err)
		}
		clusters, err := goanon.LoadBrownClusters(f)
		f.Close()
		if err != nil {
			log.Fatalf("chargement clusters : %v", err)
		}
		recOpts = append(recOpts, goanon.WithBrownClusters(clusters))
	}

	rec, err := goanon.NewRecognizer(m, recOpts...)
	if err != nil {
		log.Fatalf("initialisation recognizer : %v", err)
	}

	// --- Anonymizer + Processor ---
	cfg := anonymizer.Config{
		Strategy:      parseStrategy(*strategy),
		ConsistentMap: true,
	}
	anon := anonymizer.New(rec, cfg)
	proc := docprocessor.New(anon)

	// --- Walker ---
	walker, err := factory(*inputPath)
	if err != nil {
		log.Fatalf("ouverture %q : %v", *inputPath, err)
	}

	session, err := proc.Process(walker)
	if err != nil {
		log.Fatalf("anonymisation : %v", err)
	}

	// --- Sauvegarde du document ---
	if saver, ok := walker.(interface{ SaveTo(string) error }); ok {
		if err := saver.SaveTo(*outputPath); err != nil {
			log.Fatalf("sauvegarde %q : %v", *outputPath, err)
		}
	} else {
		log.Fatalf("le walker ne supporte pas SaveTo — implémentation manquante pour ce format")
	}

	fmt.Printf("Document anonymisé : %s\n", *outputPath)
	fmt.Printf("Entités remplacées : %d\n", len(session.Mapping))

	// --- Mapping JSON ---
	if *saveMappingPath != "" {
		data, err := json.MarshalIndent(session.Mapping, "", "  ")
		if err != nil {
			log.Fatalf("sérialisation mapping : %v", err)
		}
		if err := os.WriteFile(*saveMappingPath, data, 0o644); err != nil {
			log.Fatalf("écriture mapping %q : %v", *saveMappingPath, err)
		}
		fmt.Printf("Mapping sauvegardé : %s\n", *saveMappingPath)
	}
}

func parseStrategy(s string) anonymizer.Strategy {
	switch strings.ToLower(s) {
	case "redact":
		return anonymizer.Redact
	case "hash":
		return anonymizer.Hash
	default:
		return anonymizer.TagReplace
	}
}

func supportedFormats() string {
	var exts []string
	for ext := range walkerFactories {
		exts = append(exts, ext)
	}
	return strings.Join(exts, ", ")
}

func resolveAutoMode(modelFlag string) (lang string, auto bool) {
	if modelFlag == "auto" {
		return "", true
	}
	if strings.HasPrefix(modelFlag, "auto:") {
		return strings.TrimPrefix(modelFlag, "auto:"), true
	}
	return "", false
}

func resolveAutoModel(lang, cacheDir string, refresh, offline bool) string {
	opts := []modelstore.Option{
		modelstore.WithProgress(func(l string, done, total int64) {}),
		modelstore.WithOfflineMode(offline),
	}
	if cacheDir != "" {
		opts = append(opts, modelstore.WithCacheDir(cacheDir))
	}

	store, err := modelstore.New(opts...)
	if err != nil {
		log.Fatalf("initialisation store modèles : %v", err)
	}

	ctx := context.Background()
	if refresh {
		if err := store.Refresh(ctx); err != nil {
			log.Fatalf("rafraîchissement manifeste : %v", err)
		}
	}

	path, err := store.Get(ctx, lang)
	if err != nil {
		log.Fatalf("téléchargement modèle %q : %v", lang, err)
	}

	log.Printf("modèle chargé : %s", path)
	return path
}

func resolveAutoGazetteers(lang, cacheDir string, refresh, offline bool) map[string]*features.Gazetteer {
	opts := []modelstore.Option{
		modelstore.WithOfflineMode(offline),
	}
	if cacheDir != "" {
		opts = append(opts, modelstore.WithCacheDir(cacheDir))
	}

	store, err := modelstore.New(opts...)
	if err != nil {
		log.Fatalf("initialisation store gazetteers : %v", err)
	}

	ctx := context.Background()
	if refresh {
		if err := store.Refresh(ctx); err != nil {
			log.Fatalf("rafraîchissement manifeste : %v", err)
		}
	}

	paths, err := store.GetGazetteers(ctx, lang)
	if err != nil {
		log.Fatalf("téléchargement gazetteers %q : %v", lang, err)
	}

	result := make(map[string]*features.Gazetteer, len(paths))
	for gtype, gpath := range paths {
		f, err := os.Open(gpath)
		if err != nil {
			log.Fatalf("ouverture gazetteer %q : %v", gtype, err)
		}

		gaz, err := features.LoadGazetteer(gtype, f)
		f.Close()
		if err != nil {
			log.Fatalf("chargement gazetteer %q : %v", gtype, err)
		}

		result[gtype] = gaz
		log.Printf("gazetteer chargé : %s (%s)", gtype, gpath)
	}

	return result
}
