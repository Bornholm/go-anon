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
	"sort"
	"strings"

	goanon "github.com/bornholm/go-anon"
	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	"github.com/bornholm/go-anon/pkg/anonymizer"
	pkgcsv "github.com/bornholm/go-anon/pkg/csv"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	pkgdocx "github.com/bornholm/go-anon/pkg/docx"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/modelstore"
	"github.com/bornholm/go-anon/pkg/ocr"
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
	preset := flag.String("preset", "", `passes de post-traitement : "" (défaut, aucune), "balanced" ou "high-recall". Voir l'avertissement dans docs/rgpd.md avant d'activer`)
	hashScope := flag.String("hash-scope", "", `scope de la stratégie "hash" : casse la corrélation des pseudonymes entre scopes`)
	insecureHash := flag.Bool("insecure-hash", false, `autoriser la stratégie "hash" sans clé (SHA-256 non salé, hors production)`)
	strict := flag.Bool("strict", false, "mode fail-closed : échouer sans produire de document si la vérification détecte une fuite")
	verifyDocument := flag.Bool("verify-document", true, "recomposer la sortie et y relancer une détection, pour repérer les entités coupées par la segmentation (une passe de détection supplémentaire)")
	multiView := flag.Bool("multi-view", true, "détecter sur plusieurs recompositions du document et unioner les résultats : rattrape les entités coupées par la segmentation (environ trois passes de détection supplémentaires)")
	ocrMode := flag.String("ocr", "auto", `reconnaissance optique du contenu bitmap : "auto" (si les outils sont présents), "on" (échouer sinon) ou "off"`)
	ocrLang := flag.String("ocr-lang", "", "langue de l'OCR (défaut : celle du document)")
	ocrDPI := flag.Int("ocr-dpi", pkgpdf.DefaultDPI, "résolution de rastérisation pour l'OCR")
	ocrMinConf := flag.Float64("ocr-min-confidence", 0, "seuil de confiance des mots reconnus (0 = tout conserver)")
	sanitize := flag.Bool("sanitize", true, "purger les métadonnées et surfaces cachées du document (auteur, commentaires, révisions)")
	saveMappingID := flag.String("save-mapping", "", "identifiant sous lequel enregistrer le mapping chiffré dans le store")
	mappingDir := flag.String("mapping-store", "mappings", "répertoire du store de mappings chiffrés")
	mappingTTL := flag.Duration("mapping-ttl", 0, "durée de rétention du mapping (ex. 720h) ; 0 = illimité")
	saveMappingInsecure := flag.String("save-mapping-insecure", "", "chemin JSON pour écrire le mapping EN CLAIR (déconseillé)")
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à utiliser : "nom:fichier.txt,..."`)
	clustersPath := flag.String("clusters", "", `fichier Brown clusters, ou "auto"/"auto:fr" (défaut : suit -model)`)
	cacheDir := flag.String("models-cache", "", "répertoire de cache pour les modèles téléchargés (optionnel)")
	refresh := flag.Bool("refresh-models", false, "forcer le rafraîchissement du manifeste des modèles")
	offline := flag.Bool("offline", false, "interdire toute requête réseau")
	skipVerify := flag.Bool("insecure-skip-verify", false, "désactiver la vérification de signature du manifeste de modèles (manifests custom non signés)")

	flag.Parse()

	insecureSkipModelVerify = *skipVerify

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
	// Le mode auto doit être reconnu **avant** l'analyse : « auto » n'est pas
	// une paire « nom:chemin » et ParseGazetteers le rejetterait, rendant la
	// branche automatique inatteignable.
	gzFlag := *gazetteerFlag
	if gzFlag == "" && isAuto {
		// Un modèle téléchargé arrive avec ses gazetteers, et il a été entraîné
		// avec eux : ne pas les charger prive l'inférence de features que le
		// modèle attend, sans que rien n'échoue. Le défaut suit donc -model.
		gzFlag = *modelFlag
	}

	var gazetteers map[string]*features.Gazetteer
	if gzLang, isAutoGz := resolveAutoMode(gzFlag); isAutoGz {
		gzModelLang := gzLang
		if gzModelLang == "" {
			gzModelLang = lang
		}
		gazetteers = resolveAutoGazetteers(gzModelLang, *cacheDir, *refresh, *offline)
	} else if gzFlag != "" {
		gazetteers, err = cmdutil.ParseGazetteers(gzFlag)
		if err != nil {
			log.Fatalf("chargement gazetteers : %v", err)
		}
	}

	// --- Recognizer ---
	recOpts := []goanon.RecognizerOption{goanon.WithLanguage(lang)}
	if len(gazetteers) > 0 {
		recOpts = append(recOpts, goanon.WithGazetteers(gazetteers))
	}
	// Les clusters suivent la même règle que les gazetteers : ce sont des
	// features du modèle, et un modèle téléchargé arrive avec les siennes.
	clFlag := *clustersPath
	if clFlag == "" && isAuto {
		clFlag = *modelFlag
	}
	if clLang, isAutoCl := resolveAutoMode(clFlag); isAutoCl {
		clModelLang := clLang
		if clModelLang == "" {
			clModelLang = lang
		}
		clFlag = resolveAutoClusters(clModelLang, *cacheDir, *refresh, *offline)
	}
	if clFlag != "" {
		f, err := os.Open(clFlag)
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

	// Aucun preset par défaut — mesuré, pas supposé.
	//
	// `eval -preset` sur WikiNER fr, avec le gazetteer de prénoms réparé :
	//
	//   aucun       P=97,3  R=97,4  F1=97,3
	//   balanced    P=85,4  R=85,6  F1=85,5
	//   high-recall P=83,1  R=85,6  F1=84,3
	//
	// Les deux presets dégradent le rappel autant que la précision. Le détail
	// par type explique pourquoi : FirstNameReclassify relabellise ~310 LOC
	// correctes en PER (prédictions PER 805→1188 pour UNE correspondance de
	// plus), le gazetteer INSEE comptant 209 000 prénoms dont beaucoup sont
	// aussi des toponymes français.
	//
	// Ces passes n'avaient jamais été exercées : jusqu'à la correction du
	// chargement des gazetteers (format CSV), elles s'appuyaient sur une liste
	// silencieusement vide. Les réactiver demande de les régler contre un
	// gazetteer qui fonctionne, ce qui reste à faire.
	if *preset != "" {
		switch *preset {
		case string(goanon.PresetBalanced), string(goanon.PresetHighRecall):
			recOpts = append(recOpts, goanon.PresetOptions(goanon.Preset(*preset), gazetteers["firstnames"])...)
			log.Printf("avertissement : le preset %q dégrade actuellement précision ET rappel "+
				"sur WikiNER fr (cf. commentaire dans cmd/anon-doc)", *preset)
		default:
			log.Fatalf("preset inconnu %q (attendu %q, %q ou vide)",
				*preset, goanon.PresetBalanced, goanon.PresetHighRecall)
		}
	}

	rec, err := goanon.NewRecognizer(m, recOpts...)
	if err != nil {
		log.Fatalf("initialisation recognizer : %v", err)
	}
	for _, w := range rec.Warnings() {
		log.Printf("avertissement : %s", w)
	}

	// --- Anonymizer + Processor ---
	cfg := anonymizer.Config{
		Strategy:      parseStrategy(*strategy),
		ConsistentMap: true,
	}
	anonOpts, err := cmdutil.HashOptions(cfg.Strategy, *hashScope, *insecureHash)
	if err != nil {
		log.Fatalf("anonymisation : %v", err)
	}
	if *strict {
		anonOpts = append(anonOpts, goanon.WithStrictVerification())
	} else {
		anonOpts = append(anonOpts, goanon.WithVerification())
	}
	anon := anonymizer.New(rec, cfg)
	// La vérification document recompose la sortie et y relance une détection :
	// c'est le seul contrôle capable de voir une entité coupée par la
	// segmentation du format. Elle coûte une passe de détection de plus sur
	// l'intégralité du document, d'où le flag.
	var procOpts []docprocessor.Option
	if *multiView {
		procOpts = append(procOpts, docprocessor.WithMultiViewDetection())
	}
	if *verifyDocument {
		if *strict {
			procOpts = append(procOpts, docprocessor.WithStrictDocumentVerification())
		} else {
			procOpts = append(procOpts, docprocessor.WithDocumentVerification())
		}
	}
	proc := docprocessor.New(anon, procOpts...)

	// --- Walker ---
	walker, err := factory(*inputPath)
	if err != nil {
		log.Fatalf("ouverture %q : %v", *inputPath, err)
	}

	// --- Reconnaissance optique ---
	// Doit précéder le traitement : le texte reconnu alimente le rapport des
	// portions que le pipeline sait lire sans pouvoir les réécrire.
	ocrLanguage := *ocrLang
	if ocrLanguage == "" {
		ocrLanguage = lang
	}
	if err := runOCR(walker, *ocrMode, ocrLanguage, *ocrDPI, *ocrMinConf); err != nil {
		log.Fatalf("OCR : %v", err)
	}

	session, report, err := proc.ProcessWithReport(walker, anonOpts...)
	if err != nil {
		// En mode strict, l'échec doit précéder toute écriture : le document de
		// sortie n'est jamais créé, pas même partiellement anonymisé.
		log.Fatalf("anonymisation : %v", err)
	}
	reportLeaks(report)

	// --- Sanitisation des surfaces cachées (métadonnées, commentaires, révisions) ---
	// Doit précéder SaveTo : en mode strict, une surface non traitée interrompt
	// avant toute écriture, comme la vérification.
	//
	// En mode strict, le contrôle des surfaces est effectué même si -sanitize=false :
	// sans cela, un PDF scanné passerait sans le moindre signalement alors que
	// l'utilisateur a explicitement demandé le fail-closed. -sanitize ne pilote
	// plus alors que la purge des métadonnées.
	if *sanitize || *strict {
		policy := docprocessor.DefaultSanitizePolicy()
		policy.Strict = *strict
		policy.StripMetadata = *sanitize
		sanReport, err := docprocessor.Sanitize(walker, policy)
		if err != nil {
			log.Fatalf("sanitisation : %v", err)
		}
		reportSanitize(sanReport)
	}

	// --- Sauvegarde du document ---
	if saver, ok := walker.(interface{ SaveTo(string) error }); ok {
		if err := saver.SaveTo(*outputPath); err != nil {
			log.Fatalf("sauvegarde %q : %v", *outputPath, err)
		}
	} else {
		log.Fatalf("le walker ne supporte pas SaveTo — implémentation manquante pour ce format")
	}

	// --- Vérification visuelle du document produit ---
	// Nécessairement après l'écriture : c'est le fichier produit qui est relu,
	// rendu puis océrisé. Le fail-closed prend donc ici la forme d'une
	// destruction — un document dont on sait qu'il expose une donnée
	// personnelle ne doit pas subsister sur le disque.
	visualLeaks, err := proc.VerifyOutput(walker, *outputPath, session)
	if err != nil {
		log.Fatalf("vérification visuelle : %v", err)
	}
	if len(visualLeaks) > 0 {
		byRegion := make(map[string]int, len(visualLeaks))
		for _, leak := range visualLeaks {
			byRegion[leak.Region]++
		}
		if *strict {
			os.Remove(*outputPath)
			log.Fatalf("vérification visuelle : %d donnée(s) restée(s) lisible(s) %v — "+
				"document détruit", len(visualLeaks), byRegion)
		}
		fmt.Fprintf(os.Stderr, "AVERTISSEMENT : vérification visuelle — %d donnée(s) "+
			"restée(s) LISIBLE(s) dans le document produit %v ; "+
			"utiliser -strict pour le refuser\n", len(visualLeaks), byRegion)
	}

	fmt.Printf("Document anonymisé : %s\n", *outputPath)
	fmt.Printf("Entités remplacées : %d (%d occurrence(s) %s)\n",
		len(session.Mapping), report.TotalEntities(), formatEntityCounts(report.Entities))
	if report.RedactedZones > 0 {
		fmt.Printf("Zones caviardées dans du contenu bitmap : %d\n", report.RedactedZones)
	}

	// --- Mapping chiffré ---
	if *saveMappingID != "" {
		store, err := cmdutil.OpenMappingStore(*mappingDir, *mappingTTL)
		if err != nil {
			log.Fatalf("%v (ou -save-mapping-insecure pour écrire en clair, en connaissance de cause)", err)
		}
		if err := store.Save(context.Background(), *saveMappingID, session); err != nil {
			log.Fatalf("sauvegarde du mapping : %v", err)
		}
		fmt.Printf("Mapping chiffré enregistré : %s (store %s)\n", *saveMappingID, *mappingDir)
		if *mappingTTL == 0 {
			fmt.Fprintln(os.Stderr, "avertissement : mapping sans date de rétention "+
				"— préciser -mapping-ttl, la conservation illimitée d'une table de "+
				"ré-identification étant difficilement justifiable")
		}
	}

	// --- Mapping en clair (dérogation explicite) ---
	if *saveMappingInsecure != "" {
		data, err := json.MarshalIndent(session.Mapping, "", "  ")
		if err != nil {
			log.Fatalf("sérialisation mapping : %v", err)
		}
		// 0600 : le mapping est la table de ré-identification, donc une donnée
		// personnelle à part entière.
		if err := os.WriteFile(*saveMappingInsecure, data, 0o600); err != nil {
			log.Fatalf("écriture mapping %q : %v", *saveMappingInsecure, err)
		}
		fmt.Printf("Mapping sauvegardé : %s\n", *saveMappingInsecure)
		fmt.Fprintf(os.Stderr, "AVERTISSEMENT : %s contient la table de ré-identification EN CLAIR "+
			"(donnée personnelle) — préférer -save-mapping, qui chiffre ; à défaut, "+
			"protéger ce fichier et le détruire après usage\n", *saveMappingInsecure)
	}

	// Hygiène mémoire (S8) : libérer la table de ré-identification une fois tous
	// ses usages terminés, plutôt que de la laisser vivre jusqu'à la fin du process.
	session.Close()
}

// ocrCapable est implémentée par les Walkers sachant océriser leur contenu
// bitmap. Seul le PDF le fait aujourd'hui.
type ocrCapable interface {
	RunOCR(pkgpdf.OCROptions) error
}

// runOCR déclenche la reconnaissance optique selon le mode demandé.
//
// En mode « auto », l'absence d'outil est un avertissement : le pipeline
// continue, et les pages bitmap restent signalées comme surfaces non traitées
// par la sanitisation. En mode « on », c'est une erreur — l'utilisateur a
// demandé la couverture, la lui donner à moitié sans le dire serait pire que
// de refuser.
func runOCR(walker docprocessor.Walker, mode, lang string, dpi int, minConf float64) error {
	if mode == "off" {
		return nil
	}
	capable, ok := walker.(ocrCapable)
	if !ok {
		if mode == "on" {
			return fmt.Errorf("le format ne sait pas océriser son contenu")
		}
		return nil
	}

	opts := pkgpdf.OCROptions{
		Engine:     ocr.NewTesseractExec(),
		Rasterizer: pkgpdf.NewPdftoppmRasterizer(),
		Lang:       lang,
		DPI:        dpi,
		// Mode épars obligatoire pour relire un document caviardé : l'analyse
		// de mise en page classerait la page comme non textuelle et ne rendrait
		// rien, donnant une vérification qui passe toujours.
		VerifyEngine:  ocr.NewTesseractExecSparse(),
		MinConfidence: minConf,
	}

	if err := capable.RunOCR(opts); err != nil {
		if mode == "on" {
			return err
		}
		fmt.Fprintf(os.Stderr, "avertissement : OCR indisponible (%v) ; "+
			"le contenu bitmap ne sera pas analysé — utiliser -ocr on pour l'exiger\n", err)
		return nil
	}
	return nil
}

// formatEntityCounts rend la répartition par type, triée pour être stable.
//
// C'est le seul retour immédiat sur la **précision** : une configuration réglée
// pour le rappel s'emballe silencieusement, et un type qui explose (des LOC
// partout, des PER sur des noms communs) se repère ici avant de rendre les
// documents inexploitables.
func formatEntityCounts(counts map[goanon.EntityType]int) string {
	if len(counts) == 0 {
		return "aucune"
	}
	types := make([]string, 0, len(counts))
	for t := range counts {
		types = append(types, string(t))
	}
	sort.Strings(types)

	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, fmt.Sprintf("%s:%d", t, counts[goanon.EntityType(t)]))
	}
	return strings.Join(parts, " ")
}

// reportLeaks résume la vérification sur stderr en métadonnées seulement :
// nombre de fuites par nature, jamais les contenus concernés.
func reportLeaks(report *docprocessor.Report) {
	if report.OK() {
		return
	}

	if len(report.Leaks) > 0 {
		byKind := make(map[goanon.LeakKind]int, len(report.Leaks))
		for _, leak := range report.Leaks {
			byKind[leak.Kind]++
		}
		fmt.Fprintf(os.Stderr, "avertissement : vérification — %d fuite(s) sur %d segment(s) %v ; "+
			"utiliser -strict pour refuser de produire un document dans ce cas\n",
			len(report.Leaks), report.Segments, byKind)
	}

	if n := len(report.DocumentLeaks); n > 0 {
		// Distinguer les entités à cheval sur plusieurs segments : ce sont celles
		// qu'aucune vérification par segment ne pouvait voir, et le signal le plus
		// actionnable du rapport.
		crossing := 0
		for _, leak := range report.DocumentLeaks {
			if len(leak.Segments) > 1 {
				crossing++
			}
		}
		fmt.Fprintf(os.Stderr, "avertissement : vérification document — %d entité(s) "+
			"redétectée(s) après recomposition, dont %d à cheval sur plusieurs segments ; "+
			"utiliser -strict pour refuser de produire un document dans ce cas\n", n, crossing)
	}

	if n := len(report.RegionLeaks); n > 0 {
		// Nature différente des précédentes : ce n'est pas un remplacement qui a
		// échoué, c'est du contenu que le pipeline ne sait pas retirer. Le
		// message doit dire quoi faire, pas seulement constater.
		byRegion := make(map[string]int, n)
		for _, leak := range report.RegionLeaks {
			byRegion[leak.Region]++
		}
		fmt.Fprintf(os.Stderr, "AVERTISSEMENT : %d entité(s) détectée(s) dans du contenu "+
			"non réécrivable %v — elles resteront LISIBLES dans le document produit ; "+
			"caviarder ces zones en amont, ou utiliser -strict pour refuser le document\n",
			n, byRegion)
	}
}

// reportSanitize résume la sanitisation sur stderr, en métadonnées seulement :
// surfaces purgées et comptes, jamais de contenu.
func reportSanitize(r docprocessor.SanitizeReport) {
	if r.MetadataStripped {
		fmt.Fprintln(os.Stderr, "sanitisation : métadonnées purgées")
	}
	if r.CommentsFound > 0 {
		fmt.Fprintf(os.Stderr, "sanitisation : %d commentaire(s) traité(s)\n", r.CommentsFound)
	}
	if r.RevisionsFound > 0 {
		fmt.Fprintf(os.Stderr, "sanitisation : %d révision(s) détectée(s)\n", r.RevisionsFound)
	}
	if !r.OK() {
		fmt.Fprintf(os.Stderr, "avertissement : surfaces non traitées : %v ; "+
			"utiliser -strict pour refuser de produire un document dans ce cas\n", r.Unprocessed)
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

// insecureSkipModelVerify reflète le flag -insecure-skip-verify : il désactive
// la vérification de signature du manifeste de modèles.
var insecureSkipModelVerify bool

func resolveAutoModel(lang, cacheDir string, refresh, offline bool) string {
	opts := []modelstore.Option{
		modelstore.WithProgress(func(l string, done, total int64) {}),
		modelstore.WithOfflineMode(offline),
		modelstore.WithInsecureSkipVerify(insecureSkipModelVerify),
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
		modelstore.WithInsecureSkipVerify(insecureSkipModelVerify),
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

// resolveAutoClusters télécharge les Brown clusters de la langue et retourne
// leur chemin local, ou une chaîne vide si le manifeste n'en publie pas.
//
// L'absence n'est pas fatale : les modèles antérieurs à la distribution des
// clusters restent utilisables. Recognizer.Warnings() signale déjà le cas où le
// modèle en attendait.
func resolveAutoClusters(lang, cacheDir string, refresh, offline bool) string {
	opts := []modelstore.Option{
		modelstore.WithOfflineMode(offline),
		modelstore.WithInsecureSkipVerify(insecureSkipModelVerify),
	}
	if cacheDir != "" {
		opts = append(opts, modelstore.WithCacheDir(cacheDir))
	}

	store, err := modelstore.New(opts...)
	if err != nil {
		log.Fatalf("initialisation store clusters : %v", err)
	}

	ctx := context.Background()
	if refresh {
		if err := store.Refresh(ctx); err != nil {
			log.Fatalf("rafraîchissement manifeste : %v", err)
		}
	}

	path, err := store.GetClusters(ctx, lang)
	if err != nil {
		log.Fatalf("téléchargement clusters %q : %v", lang, err)
	}
	if path != "" {
		log.Printf("clusters chargés : %s", path)
	}
	return path
}
