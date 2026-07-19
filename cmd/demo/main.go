// Commande demo — démonstration interactive du pipeline NER et anonymisation.
//
// Sans -anonymize, affiche les entités détectées avec leur type et confiance.
// Avec -anonymize, affiche le texte après remplacement des entités.
//
// Usage :
//
//	demo -model model.crf.gz -lang fr -text "Jean Dupont habite à Paris."
//	echo "..." | demo -model model.crf.gz -lang en -anonymize
//	demo -model m.crf.gz -lang fr -min-confidence 0.7 -max-tokens 5 \
//	     -blocklist "PER:Monsieur,Madame" -blocklist "ORG:SA,SARL"
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	goanon "github.com/bornholm/go-anon"
	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
)

func main() {
	modelPath := flag.String("model", "", "chemin vers le modèle .crf.gz (obligatoire)")
	langCode := flag.String("lang", "auto", `langue : "auto" (détection automatique), "fr", "en" ou "es"`)
	text := flag.String("text", "", "texte à analyser (lit stdin si absent)")
	doAnonymize := flag.Bool("anonymize", false, "appliquer l'anonymisation au lieu d'afficher les entités")
	strategy := flag.String("strategy", "tag", `stratégie d'anonymisation : "tag", "redact" ou "hash"`)
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à utiliser : "nom:fichier.txt,nom:fichier.txt"`)
	clustersPath := flag.String("clusters", "", "fichier Brown clusters (optionnel)")

	// Post-filtres
	minConfidence := flag.Float64("min-confidence", 0, "supprimer les entités sous ce seuil de confiance (0 = désactivé)")
	maxTokens := flag.Int("max-tokens", 0, "supprimer les entités dépassant ce nombre de tokens (0 = désactivé)")
	var blocklistEntries blocklistFlag
	flag.Var(&blocklistEntries, "blocklist", `supprimer les entités dont tous les tokens sont dans la liste.
	Format : "TYPE:mot1,mot2,mot3" — répétable pour plusieurs types.
	Exemple : -blocklist "PER:Monsieur,Madame" -blocklist "ORG:SA,SARL,Inc"`)
	firstNameReclassify := flag.Bool("first-name-reclassify", false, "reclasser en PER les entités LOC figurant dans le gazetteer 'firstnames'")
	firstNameDetection := flag.Bool("first-name-detection", false, "détecter les prénoms du gazetteer non couverts par des entités existantes")
	mergePass := flag.Bool("merge", false, "activer la fusion des entités fragmentées (PER+LOC adjacents)")
	nameCompletion := flag.Bool("name-completion", false, "activer la complétion des noms incomplets (prénom seul)")

	// Anonymisation sélective
	var skipTypes entityTypeFlag
	flag.Var(&skipTypes, "skip-type", `ne pas anonymiser ce type d'entité (répétable).
	Valeurs : PER, LOC, ORG, MISC.
	Exemple : -skip-type LOC -skip-type ORG`)

	flag.Parse()

	if *modelPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -model est obligatoire")
		flag.Usage()
		os.Exit(1)
	}

	// --- Chargement du modèle ---
	mf, err := os.Open(*modelPath)
	if err != nil {
		log.Fatalf("ouverture modèle %q : %v", *modelPath, err)
	}
	defer mf.Close()

	m, err := goanon.LoadModel(mf)
	if err != nil {
		log.Fatalf("chargement modèle : %v", err)
	}

	// --- Chargement des gazetteers ---
	gazetteers, err := cmdutil.ParseGazetteers(*gazetteerFlag)
	if err != nil {
		log.Fatalf("chargement gazetteers : %v", err)
	}

	// --- Lecture du texte ---
	input := *text
	if input == "" {
		input = readStdin()
	}
	input = strings.TrimSpace(input)
	if input == "" {
		fmt.Fprintln(os.Stderr, "erreur : aucun texte fourni (utiliser -text ou stdin)")
		os.Exit(1)
	}

	// --- Détection automatique de la langue ---
	lang := *langCode
	if lang == "auto" {
		lang, err = cmdutil.DetectLanguage(input, goanon.SupportedLanguages())
		if err != nil {
			log.Fatalf("détection de langue : %v", err)
		}
		log.Printf("langue détectée : %s", lang)
	}

	// --- Construction du Recognizer ---
	opts := []goanon.RecognizerOption{goanon.WithLanguage(lang)}

	if len(gazetteers) > 0 {
		opts = append(opts, goanon.WithGazetteers(gazetteers))
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
		opts = append(opts, goanon.WithBrownClusters(clusters))
	}

	// --- Post-filtres ---
	if *firstNameReclassify {
		opts = append(opts, goanon.WithFirstNameReclassify(gazetteers["firstnames"]))
	}
	if *firstNameDetection {
		opts = append(opts, goanon.WithFirstNameDetectionPass(gazetteers["firstnames"]))
	}
	if *mergePass {
		opts = append(opts, goanon.WithMergePass())
	}
	if *nameCompletion {
		opts = append(opts, goanon.WithNameCompletionPass(gazetteers["firstnames"]))
	}

	var filters []goanon.EntityFilter

	if *minConfidence > 0 {
		filters = append(filters, goanon.MinConfidenceFilter(*minConfidence))
	}
	if *maxTokens > 0 {
		filters = append(filters, goanon.MaxTokensFilter(*maxTokens))
	}
	for _, e := range blocklistEntries {
		filters = append(filters, goanon.BlocklistFilter(e.entityType, e.words...))
	}

	if len(filters) > 0 {
		opts = append(opts, goanon.WithPostFilters(filters...))
	}

	rec, err := goanon.NewRecognizer(m, opts...)
	if err != nil {
		log.Fatalf("initialisation recognizer : %v", err)
	}
	for _, w := range rec.Warnings() {
		log.Printf("avertissement : %s", w)
	}

	if *doAnonymize {
		runAnonymize(rec, input, *strategy, skipTypes)
	} else {
		runRecognize(rec, input)
	}
}

// runRecognize affiche les entités détectées dans le texte.
func runRecognize(rec goanon.Recognizer, text string) {
	entities, err := rec.Recognize(text)
	if err != nil {
		log.Fatalf("reconnaissance : %v", err)
	}

	fmt.Printf("Texte : %s\n\n", text)

	if len(entities) == 0 {
		fmt.Println("Aucune entité détectée.")
		return
	}

	fmt.Printf("Entités détectées (%d) :\n", len(entities))
	for _, e := range entities {
		fmt.Printf("  [%d:%d]  %-6s  %-30q  (confiance: %.2f)\n",
			e.Start, e.End, e.Type, e.Text, e.Confidence)
	}
}

// allEntityTypes est la liste complète des types d'entités connus.
var allEntityTypes = []goanon.EntityType{goanon.TypePER, goanon.TypeLOC, goanon.TypeORG, goanon.TypeMISC}

// runAnonymize affiche le texte après anonymisation.
func runAnonymize(rec goanon.Recognizer, text, strategyName string, skipTypes entityTypeFlag) {
	strat := parseStrategy(strategyName)

	cfg := goanon.Config{
		Strategy:      strat,
		ConsistentMap: true,
	}

	if len(skipTypes) > 0 {
		skip := make(map[goanon.EntityType]bool, len(skipTypes))
		for _, t := range skipTypes {
			skip[t] = true
		}
		for _, t := range allEntityTypes {
			if !skip[t] {
				cfg.EntityTypes = append(cfg.EntityTypes, t)
			}
		}
	}

	anon := goanon.NewAnonymizer(rec, cfg)

	result, err := anon.Anonymize(text)
	if err != nil {
		log.Fatalf("anonymisation : %v", err)
	}

	fmt.Println(result.Text)

	if len(result.Mapping) > 0 {
		keys := make([]string, 0, len(result.Mapping))
		for placeholder := range result.Mapping {
			keys = append(keys, placeholder)
		}
		sort.Strings(keys)

		fmt.Printf("\nMapping (%d substitutions) :\n", len(result.Mapping))
		for _, placeholder := range keys {
			fmt.Printf("  %-25s → %q\n", placeholder, result.Mapping[placeholder])
		}
	}
}

// parseStrategy convertit le nom de stratégie en constante anonymizer.Strategy.
func parseStrategy(name string) goanon.Strategy {
	switch strings.ToLower(name) {
	case "redact":
		return goanon.Redact
	case "hash":
		return goanon.Hash
	default:
		return goanon.TagReplace
	}
}

// readStdin lit tout le contenu de stdin.
func readStdin() string {
	var sb strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(scanner.Text())
	}
	return sb.String()
}

// blocklistEntry représente une entrée -blocklist parsée.
type blocklistEntry struct {
	entityType goanon.EntityType
	words      []string
}

// blocklistFlag est un flag.Value répétable pour -blocklist "TYPE:mot1,mot2".
type blocklistFlag []blocklistEntry

func (b *blocklistFlag) String() string {
	parts := make([]string, len(*b))
	for i, e := range *b {
		parts[i] = string(e.entityType) + ":" + strings.Join(e.words, ",")
	}
	return strings.Join(parts, " ")
}

func (b *blocklistFlag) Set(s string) error {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 {
		return fmt.Errorf("format invalide %q : attendu TYPE:mot1,mot2", s)
	}
	entityType := goanon.EntityType(strings.ToUpper(strings.TrimSpace(s[:idx])))
	rawWords := strings.Split(s[idx+1:], ",")
	words := make([]string, 0, len(rawWords))
	for _, w := range rawWords {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return fmt.Errorf("blocklist %q : aucun mot fourni", s)
	}
	*b = append(*b, blocklistEntry{entityType: entityType, words: words})
	return nil
}

var _ flag.Value = (*blocklistFlag)(nil)

// entityTypeFlag est un flag.Value répétable pour -skip-type PER.
type entityTypeFlag []goanon.EntityType

func (e *entityTypeFlag) String() string {
	parts := make([]string, len(*e))
	for i, t := range *e {
		parts[i] = string(t)
	}
	return strings.Join(parts, ",")
}

func (e *entityTypeFlag) Set(s string) error {
	t := goanon.EntityType(strings.ToUpper(strings.TrimSpace(s)))
	*e = append(*e, t)
	return nil
}

var _ flag.Value = (*entityTypeFlag)(nil)
