package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goanon "github.com/bornholm/go-anon"
	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	pkgcsv "github.com/bornholm/go-anon/pkg/csv"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	pkgdocx "github.com/bornholm/go-anon/pkg/docx"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/modelstore"
	"github.com/bornholm/go-anon/pkg/ner"
	pkgodt "github.com/bornholm/go-anon/pkg/odt"
	pkgpdf "github.com/bornholm/go-anon/pkg/pdf"
)

var version = "dev"

type walkerFactory func(path string) (docprocessor.Walker, error)

var walkerFactories = map[string]walkerFactory{
	".docx": pkgdocx.NewWalkerFromFile,
	".odt":  pkgodt.NewWalkerFromFile,
	".csv":  pkgcsv.NewWalkerFromFile,
	".tsv":  pkgcsv.NewWalkerFromFile,
	".pdf":  pkgpdf.NewWalkerFromFile,
}

var docMimeTypes = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".odt":  "application/vnd.oasis.opendocument.text",
	".csv":  "text/csv",
	".tsv":  "text/tab-separated-values",
	".pdf":  "application/pdf",
}

//go:embed index.html
var htmlContent embed.FS

type Server struct {
	models        map[string]*ner.Model
	modelVersions map[string]string
	gazetteers    map[string]*features.Gazetteer
	// hashKey est chargée une fois au démarrage depuis GOANON_HASH_KEY. Vide, la
	// stratégie "hash" est refusée plutôt que dégradée en SHA-256 nu.
	hashKey goanon.HashKey
	// strict fait échouer une requête plutôt que de renvoyer une sortie dont la
	// vérification signale une fuite. Réglé au démarrage, jamais par requête :
	// un client ne doit pas pouvoir abaisser la garantie du service.
	strict bool
	// maxBody borne la taille du corps lu par requête (protection DoS).
	maxBody int64
	mu      sync.RWMutex
}

// limitBody enveloppe le corps de la requête pour rejeter (413) au-delà de
// s.maxBody octets, avant de charger un document client entier en mémoire.
func (s *Server) limitBody(w http.ResponseWriter, r *http.Request) {
	if s.maxBody > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBody)
	}
}

// anonymizeOptions construit les options d'anonymisation d'une requête.
// L'erreur retournée est destinée au client : elle ne contient aucun contenu.
func (s *Server) anonymizeOptions(strategy goanon.Strategy) ([]goanon.AnonymizeOption, error) {
	var opts []goanon.AnonymizeOption
	if s.strict {
		opts = append(opts, goanon.WithStrictVerification())
	} else {
		opts = append(opts, goanon.WithVerification())
	}

	if strategy != goanon.Hash {
		return opts, nil
	}
	if len(s.hashKey) == 0 {
		return nil, fmt.Errorf("stratégie \"hash\" indisponible : le serveur a été démarré sans %s",
			goanon.HashKeyEnvVar)
	}
	return append(opts, goanon.WithHashKey(s.hashKey)), nil
}

type AnonymizeRequest struct {
	Text                string              `json:"text"`
	Language            string              `json:"language"`
	Strategy            string              `json:"strategy"`
	MinConfidence       float64             `json:"minConfidence"`
	MaxTokens           int                 `json:"maxTokens"`
	Blocklist           map[string][]string `json:"blocklist"`
	SkipTypes           []string            `json:"skipTypes"`
	FirstNameReclassify bool                `json:"firstNameReclassify"`
	Merge               bool                `json:"merge"`
	NameCompletion      bool                `json:"nameCompletion"`
}

type Entity struct {
	Type       string  `json:"type"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

type AnonymizeResponse struct {
	Text       string            `json:"text"`
	Language   string            `json:"language"`
	Mapping    map[string]string `json:"mapping"`
	Entities   []Entity          `json:"entities"`
	DurationMs float64           `json:"durationMs"`
}

// modelLanguages retourne, triés, les codes des langues actuellement chargées.
func (s *Server) modelLanguages() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	langs := make([]string, 0, len(s.models))
	for l := range s.models {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// detectLanguage détecte la langue de text en la restreignant aux langues
// chargées. Renvoie une erreur si la détection est peu fiable (le résultat est
// alors nécessairement l'une des langues disponibles).
func (s *Server) detectLanguage(text string) (string, error) {
	candidates := s.modelLanguages()
	det := goanon.NewWhatlangDetector(candidates...)
	res, err := det.Detect(text)
	if err != nil {
		return "", err
	}
	if res.Lang == "" || !res.Reliable {
		return "", fmt.Errorf("could not reliably detect language (confidence %.2f); specify one of: %s", res.Confidence, strings.Join(candidates, ", "))
	}
	return res.Lang, nil
}

type DeanonymizeRequest struct {
	Text    string            `json:"text"`
	Mapping map[string]string `json:"mapping"`
}

type DeanonymizeResponse struct {
	Text       string  `json:"text"`
	DurationMs float64 `json:"durationMs"`
}

func main() {
	modelsFlag := flag.String("models", "", `comma-separated lang:path pairs, "auto" for all, or lang:auto (e.g., fr:auto,en:model_en.crf.gz)`)
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à charger : "nom:fichier.txt,nom:fichier.txt"`)
	port := flag.String("port", "8080", "server port")
	cacheDir := flag.String("models-cache", "", "répertoire de cache pour les modèles téléchargés (optionnel)")
	refresh := flag.Bool("refresh-models", false, "forcer le rafraîchissement du manifeste des modèles")
	offline := flag.Bool("offline", false, "interdire toute requête réseau")
	strict := flag.Bool("strict", false, "mode fail-closed : refuser (422) toute réponse dont la vérification signale une fuite")
	maxBody := flag.Int64("max-body", 10<<20, "taille maximale du corps d'une requête, en octets")
	maxConcurrent := flag.Int("max-concurrent", runtime.NumCPU(), "nombre maximum d'anonymisations simultanées")
	skipVerify := flag.Bool("insecure-skip-verify", false, "désactiver la vérification de signature du manifeste de modèles (manifests custom non signés)")
	flag.Parse()

	insecureSkipModelVerify = *skipVerify

	if *maxConcurrent < 1 {
		*maxConcurrent = 1
	}

	if *modelsFlag == "" {
		log.Fatal("error: -models flag is required (format: lang:path,lang:path)")
	}

	gazetteers, err := cmdutil.ParseGazetteers(*gazetteerFlag)
	if err != nil {
		log.Fatalf("chargement gazetteers : %v", err)
	}

	srv := &Server{
		models:        make(map[string]*ner.Model),
		modelVersions: make(map[string]string),
		gazetteers:    gazetteers,
		strict:        *strict,
		maxBody:       *maxBody,
	}
	if *strict {
		log.Print("mode strict actif : une sortie dont la vérification signale une fuite ne sera pas renvoyée")
	}

	// La clé HMAC de la stratégie "hash" est optionnelle au démarrage mais
	// obligatoire à l'usage : sans elle, les requêtes hash sont refusées.
	switch key, err := goanon.HashKeyFromEnv(); {
	case err == nil:
		srv.hashKey = key
		log.Printf("clé de hachage chargée depuis %s (stratégie \"hash\" disponible)", goanon.HashKeyEnvVar)
	case errors.Is(err, goanon.ErrHashKeyNotSet):
		log.Printf("%s non définie : la stratégie \"hash\" sera refusée", goanon.HashKeyEnvVar)
	default:
		log.Fatalf("chargement de %s : %v", goanon.HashKeyEnvVar, err)
	}

	// Détection du mode auto global
	if *modelsFlag == "auto" {
		loadAllAutoModels(srv, *cacheDir, *refresh, *offline)
	} else {
		loadModelsFromPairs(srv, *modelsFlag, *cacheDir, *refresh, *offline)
	}

	if len(srv.models) == 0 {
		log.Fatal("error: no models loaded")
	}

	// Auto-download gazetteers si demandé ou si des modèles auto sont chargés
	gzLang, isAutoGz := parseGazetteersAuto(*gazetteerFlag)

	hasAutoModels := false
	for _, v := range srv.modelVersions {
		if v == "auto" {
			hasAutoModels = true
			break
		}
	}

	shouldAutoGz := isAutoGz || (hasAutoModels && len(srv.gazetteers) == 0)
	if shouldAutoGz {
		if !isAutoGz {
			gzLang = ""
		}
		autoGz := loadAutoGazetteers(gzLang, srv.models, *cacheDir, *refresh, *offline)
		for k, v := range autoGz {
			if _, exists := srv.gazetteers[k]; !exists {
				srv.gazetteers[k] = v
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.serveHTML)
	mux.HandleFunc("/api/pseudonymize", srv.handlePseudonymize)
	mux.HandleFunc("/api/depseudonymize", srv.handleDepseudonymize)
	mux.HandleFunc("/api/languages", srv.handleLanguages)
	mux.HandleFunc("/api/doc-formats", srv.handleDocFormats)
	mux.HandleFunc("/api/pseudonymize-doc", srv.handlePseudonymizeDoc)

	// Sémaphore d'anonymisations concurrentes : borne la mémoire et le CPU.
	sem := make(chan struct{}, *maxConcurrent)
	handler := chain(mux,
		withRequestID,
		recoverPanic,
		accessLog,
		noStore,
		func(next http.Handler) http.Handler {
			return limitConcurrency(sem, 5*time.Second, next)
		},
	)

	addr := ":" + *port
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Limites de ressources : sans elles, un client lent (Slowloris) ou un
		// gros en-tête suffit à immobiliser une connexion indéfiniment.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// ErrorLog silencieux : les erreurs de transport (handshakes, resets)
		// sont verbeuses et sans valeur ici ; surtout, ne pas leur donner un
		// canal de log distinct de la politique « métadonnées seulement ».
		ErrorLog: log.New(io.Discard, "", 0),
	}
	log.Printf("starting server on %s (max-body=%d o, max-concurrent=%d, strict=%v)",
		addr, *maxBody, *maxConcurrent, *strict)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func (s *Server) serveHTML(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := htmlContent.ReadFile("index.html")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) handlePseudonymize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.limitBody(w, r)
	defer r.Body.Close()

	var req AnonymizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Message générique : une erreur de décodage JSON peut refléter un
		// fragment du corps (caractère fautif). On ne renvoie que le statut.
		if maxBytesExceeded(err) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	lang := strings.ToLower(req.Language)
	if lang == "" || lang == "auto" {
		detected, err := s.detectLanguage(req.Text)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		lang = detected
	}
	s.mu.RLock()
	model, ok := s.models[lang]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, fmt.Sprintf("language %q not supported", req.Language), http.StatusBadRequest)
		return
	}

	var recognizerOpts []goanon.RecognizerOption
	recognizerOpts = append(recognizerOpts, goanon.WithLanguage(lang))
	if req.FirstNameReclassify {
		recognizerOpts = append(recognizerOpts, goanon.WithFirstNameReclassify(s.gazetteers["firstnames"]))
	}
	if req.Merge {
		recognizerOpts = append(recognizerOpts, goanon.WithMergePass())
	}
	if req.NameCompletion {
		recognizerOpts = append(recognizerOpts, goanon.WithNameCompletionPass(s.gazetteers["firstnames"]))
	}
	if req.FirstNameReclassify {
		// Détecte les prénoms du gazetteer non couverts par les entités NER (ex: prénom
		// seul en milieu de phrase). À placer après les autres passes NER.
		recognizerOpts = append(recognizerOpts, goanon.WithFirstNameDetectionPass(s.gazetteers["firstnames"]))
	}
	if req.MinConfidence > 0 || req.MaxTokens > 0 || len(req.Blocklist) > 0 {
		var filters []goanon.EntityFilter
		if req.MinConfidence > 0 {
			filters = append(filters, goanon.MinConfidenceFilter(req.MinConfidence))
		}
		if req.MaxTokens > 0 {
			filters = append(filters, goanon.MaxTokensFilter(req.MaxTokens))
		}
		for et, words := range req.Blocklist {
			if len(words) > 0 {
				filters = append(filters, goanon.BlocklistFilter(goanon.EntityType(et), words...))
			}
		}
		recognizerOpts = append(recognizerOpts, goanon.WithPostFilters(filters...))
	}
	recognizerOpts = append(recognizerOpts, goanon.WithBuiltinRegexPatterns())
	recognizerOpts = append(recognizerOpts, goanon.WithBuiltinSecretPatterns())

	rec, err := goanon.NewRecognizer(model, recognizerOpts...)
	if err != nil {
		http.Error(w, "creating recognizer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	strat := parseStrategy(req.Strategy)
	cfg := goanon.Config{
		Strategy:      strat,
		ConsistentMap: true,
		EntityTypes:   parseSkipTypes(req.SkipTypes),
	}

	anonOpts, err := s.anonymizeOptions(strat)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	anon := goanon.NewAnonymizer(rec, cfg)
	anonStart := time.Now()
	result, err := anon.Anonymize(req.Text, anonOpts...)
	if err != nil {
		var verr *goanon.VerificationError
		if errors.As(err, &verr) {
			// Mode strict : aucun texte n'est renvoyé. Le rapport ne porte que
			// des offsets et des types, il peut donc être logué tel quel.
			http.Error(w, "anonymization refused: output verification failed", http.StatusUnprocessableEntity)
			log.Printf("anonymize: refus strict, %d fuite(s) %v", len(verr.Report.Leaks), verr.Report.CountByKind())
			return
		}
		http.Error(w, "anonymization failed", http.StatusInternalServerError)
		log.Printf("anonymize: échec : %v", err)
		return
	}
	// Politique de logs « métadonnées seulement » : comptes par type, jamais de
	// forme de surface ni de fragment du texte source.
	countsByType := make(map[goanon.EntityType]int, len(result.Entities))
	for _, e := range result.Entities {
		countsByType[e.Type]++
	}
	log.Printf("anonymize: %d chars, %d entities %v, verification %v, took %s",
		len(req.Text), len(result.Entities), countsByType,
		result.Verification.CountByKind(), time.Since(anonStart))

	entities := make([]Entity, len(result.Entities))
	for i, e := range result.Entities {
		entities[i] = Entity{
			Type:       string(e.Type),
			Text:       e.Text,
			Confidence: e.Confidence,
		}
	}

	sortedMapping := make([]string, 0, len(result.Mapping))
	for placeholder := range result.Mapping {
		sortedMapping = append(sortedMapping, placeholder)
	}
	sort.Strings(sortedMapping)

	mapping := make(map[string]string, len(result.Mapping))
	for _, placeholder := range sortedMapping {
		mapping[placeholder] = result.Mapping[placeholder]
	}

	resp := AnonymizeResponse{
		Text:       result.Text,
		Language:   lang,
		Mapping:    mapping,
		Entities:   entities,
		DurationMs: float64(time.Since(anonStart).Microseconds()) / 1000.0,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDepseudonymize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.limitBody(w, r)
	defer r.Body.Close()

	var req DeanonymizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if maxBytesExceeded(err) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	deanonStart := time.Now()
	text, err := goanon.Deanonymize(req.Text, req.Mapping)
	if err != nil {
		http.Error(w, "deanonymization failed", http.StatusBadRequest)
		log.Printf("deanonymize: échec : %v", err)
		return
	}
	log.Printf("deanonymize: %d chars, %d mappings, took %s", len(req.Text), len(req.Mapping), time.Since(deanonStart))

	resp := DeanonymizeResponse{
		Text:       text,
		DurationMs: float64(time.Since(deanonStart).Microseconds()) / 1000.0,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	langs := make([]string, 0, len(s.models))
	for lang := range s.models {
		langs = append(langs, lang)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"languages": langs,
	})
}

func (s *Server) handleDocFormats(w http.ResponseWriter, r *http.Request) {
	formats := make([]string, 0, len(walkerFactories))
	for ext := range walkerFactories {
		formats = append(formats, ext)
	}
	sort.Strings(formats)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"formats": formats})
}

func (s *Server) handlePseudonymizeDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.limitBody(w, r)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		if maxBytesExceeded(err) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	lang := strings.ToLower(r.FormValue("lang"))

	ext := strings.ToLower(filepath.Ext(header.Filename))
	factory, ok := walkerFactories[ext]
	if !ok {
		http.Error(w, fmt.Sprintf("format %q not supported", ext), http.StatusBadRequest)
		return
	}

	// Parse shared config params (same as /api/anonymize)
	minConfidence, _ := strconv.ParseFloat(r.FormValue("minConfidence"), 64)
	maxTokens, _ := strconv.Atoi(r.FormValue("maxTokens"))
	firstNameReclassify := r.FormValue("firstNameReclassify") == "true"
	merge := r.FormValue("merge") == "true"
	nameCompletion := r.FormValue("nameCompletion") == "true"
	var skipTypes []string
	if st := r.FormValue("skipTypes"); st != "" {
		for _, v := range strings.Split(st, ",") {
			if v = strings.TrimSpace(v); v != "" {
				skipTypes = append(skipTypes, v)
			}
		}
	}
	var blocklist map[string][]string
	if bl := r.FormValue("blocklist"); bl != "" {
		_ = json.Unmarshal([]byte(bl), &blocklist)
	}

	tmpIn, err := os.CreateTemp("", "goanon-in-*"+ext)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpIn.Name())

	if _, err := io.Copy(tmpIn, file); err != nil {
		tmpIn.Close()
		http.Error(w, "error reading file", http.StatusInternalServerError)
		return
	}
	tmpIn.Close()

	// Détection automatique de la langue si non spécifiée (ou "auto") : on
	// échantillonne le texte du document via un walker de lecture seule.
	if lang == "" || lang == "auto" {
		sampleWalker, err := factory(tmpIn.Name())
		if err != nil {
			http.Error(w, "error reading file", http.StatusInternalServerError)
			return
		}
		sample, err := docprocessor.SampleText(sampleWalker, 4000)
		if err != nil {
			id := requestIDFrom(r)
			http.Error(w, "error sampling document (ref "+id+")", http.StatusInternalServerError)
			log.Printf("anonymize-doc req=%s: sampling: %v", id, err)
			return
		}
		detected, err := s.detectLanguage(sample)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		lang = detected
	}

	s.mu.RLock()
	model, ok := s.models[lang]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, fmt.Sprintf("language %q not supported", lang), http.StatusBadRequest)
		return
	}

	tmpOut, err := os.CreateTemp("", "goanon-out-*"+ext)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tmpOutName := tmpOut.Name()
	tmpOut.Close()
	defer os.Remove(tmpOutName)

	var recognizerOpts []goanon.RecognizerOption
	recognizerOpts = append(recognizerOpts, goanon.WithLanguage(lang))
	if firstNameReclassify {
		recognizerOpts = append(recognizerOpts, goanon.WithFirstNameReclassify(s.gazetteers["firstnames"]))
	}
	if merge {
		recognizerOpts = append(recognizerOpts, goanon.WithMergePass())
	}
	if nameCompletion {
		recognizerOpts = append(recognizerOpts, goanon.WithNameCompletionPass(s.gazetteers["firstnames"]))
	}
	if firstNameReclassify {
		recognizerOpts = append(recognizerOpts, goanon.WithFirstNameDetectionPass(s.gazetteers["firstnames"]))
	}
	if minConfidence > 0 || maxTokens > 0 || len(blocklist) > 0 {
		var filters []goanon.EntityFilter
		if minConfidence > 0 {
			filters = append(filters, goanon.MinConfidenceFilter(minConfidence))
		}
		if maxTokens > 0 {
			filters = append(filters, goanon.MaxTokensFilter(maxTokens))
		}
		for et, words := range blocklist {
			if len(words) > 0 {
				filters = append(filters, goanon.BlocklistFilter(goanon.EntityType(et), words...))
			}
		}
		recognizerOpts = append(recognizerOpts, goanon.WithPostFilters(filters...))
	}
	recognizerOpts = append(recognizerOpts, goanon.WithBuiltinRegexPatterns())
	recognizerOpts = append(recognizerOpts, goanon.WithBuiltinSecretPatterns())

	rec, err := goanon.NewRecognizer(model, recognizerOpts...)
	if err != nil {
		id := requestIDFrom(r)
		http.Error(w, "internal error (ref "+id+")", http.StatusInternalServerError)
		log.Printf("anonymize-doc req=%s: recognizer: %v", id, err)
		return
	}

	cfg := goanon.Config{
		Strategy:      parseStrategy(r.FormValue("strategy")),
		ConsistentMap: true,
		EntityTypes:   parseSkipTypes(skipTypes),
	}
	anonOpts, err := s.anonymizeOptions(cfg.Strategy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	anon := goanon.NewAnonymizer(rec, cfg)
	proc := docprocessor.New(anon)

	walker, err := factory(tmpIn.Name())
	if err != nil {
		id := requestIDFrom(r)
		http.Error(w, "internal error (ref "+id+")", http.StatusInternalServerError)
		log.Printf("anonymize-doc req=%s: ouverture document: %v", id, err)
		return
	}

	session, docReport, err := proc.ProcessWithReport(walker, anonOpts...)
	if err != nil {
		var verr *goanon.VerificationError
		if errors.As(err, &verr) {
			// Mode strict : le document de sortie n'est jamais écrit.
			http.Error(w, "anonymisation refusée : vérification de la sortie en échec", http.StatusUnprocessableEntity)
			log.Printf("anonymize-doc: refus strict, %d fuite(s) %v", len(verr.Report.Leaks), verr.Report.CountByKind())
			return
		}
		http.Error(w, "erreur anonymisation", http.StatusInternalServerError)
		log.Printf("anonymize-doc: échec : %v", err)
		return
	}
	log.Printf("pseudonymize-doc: lang=%s format=%s entities=%d", lang, ext, docReport.TotalEntities())

	// Sanitisation des surfaces cachées (métadonnées, commentaires, révisions)
	// avant écriture. En mode strict, une surface non traitée refuse le document.
	sanPolicy := docprocessor.DefaultSanitizePolicy()
	sanPolicy.Strict = s.strict
	sanReport, err := docprocessor.Sanitize(walker, sanPolicy)
	if err != nil {
		id := requestIDFrom(r)
		var unproc *docprocessor.ErrUnsanitizedSurface
		if errors.As(err, &unproc) || errors.Is(err, docprocessor.ErrNoSanitizeGuarantee) {
			http.Error(w, "anonymisation refusée : surfaces non sanitisées", http.StatusUnprocessableEntity)
			log.Printf("anonymize-doc req=%s: refus strict sanitisation : %v", id, err)
			return
		}
		http.Error(w, "internal error (ref "+id+")", http.StatusInternalServerError)
		log.Printf("anonymize-doc req=%s: sanitisation : %v", id, err)
		return
	}
	log.Printf("anonymize-doc: sanitize meta=%v comments=%d revisions=%d unprocessed=%d",
		sanReport.MetadataStripped, sanReport.CommentsFound, sanReport.RevisionsFound, len(sanReport.Unprocessed))
	// Hygiène mémoire (S8) : la session n'est plus utile après sanitisation.
	defer session.Close()

	saver, ok := walker.(interface{ SaveTo(string) error })
	if !ok {
		http.Error(w, "format non sauvegardable", http.StatusInternalServerError)
		return
	}
	if err := saver.SaveTo(tmpOutName); err != nil {
		id := requestIDFrom(r)
		http.Error(w, "internal error (ref "+id+")", http.StatusInternalServerError)
		log.Printf("anonymize-doc req=%s: sauvegarde: %v", id, err)
		return
	}

	outFile, err := os.Open(tmpOutName)
	if err != nil {
		http.Error(w, "erreur lecture résultat", http.StatusInternalServerError)
		return
	}
	defer outFile.Close()

	mime := docMimeTypes[ext]
	if mime == "" {
		mime = "application/octet-stream"
	}
	outFilename := "pseudonymized_" + filepath.Base(header.Filename)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+outFilename+`"`)
	io.Copy(w, outFile)
}

func loadModelsFromPairs(srv *Server, modelsFlag, cacheDir string, refresh, offline bool) {
	store, err := newModelStore(cacheDir, offline)
	if err != nil {
		log.Fatalf("initialisation store modèles : %v", err)
	}

	ctx := context.Background()
	if refresh {
		if err := store.Refresh(ctx); err != nil {
			log.Printf("warning: refresh manifest: %v", err)
		}
	}

	for _, pair := range strings.Split(modelsFlag, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			log.Fatalf("invalid model pair %q: expected lang:path", pair)
		}
		lang := strings.TrimSpace(parts[0])
		path := strings.TrimSpace(parts[1])

		var modelPath string
		var modelVersion string

		if path == "auto" {
			p, err := store.Get(ctx, lang)
			if err != nil {
				log.Fatalf("auto-download model %s: %v", lang, err)
			}
			modelPath = p
			modelVersion = "auto"
		} else {
			modelPath = path
			modelVersion = "local"
		}

		log.Printf("loading model for %s: %s", lang, modelPath)
		mf, err := os.Open(modelPath)
		if err != nil {
			log.Fatalf("opening model %s: %v", modelPath, err)
		}

		m, err := goanon.LoadModel(mf)
		if err != nil {
			mf.Close()
			log.Fatalf("loading model %s: %v", modelPath, err)
		}
		mf.Close()

		langKey := strings.ToLower(lang)
		srv.models[langKey] = m
		srv.modelVersions[langKey] = modelVersion
		log.Printf("loaded model for language: %s (version: %s)", lang, modelVersion)
	}
}

func loadAllAutoModels(srv *Server, cacheDir string, refresh, offline bool) {
	store, err := newModelStore(cacheDir, offline)
	if err != nil {
		log.Fatalf("initialisation store modèles : %v", err)
	}

	ctx := context.Background()
	if refresh {
		if err := store.Refresh(ctx); err != nil {
			log.Printf("warning: refresh manifest: %v", err)
		}
	}

	paths, err := store.GetAll(ctx)
	if err != nil {
		log.Fatalf("auto-download models: %v", err)
	}

	for lang, modelPath := range paths {
		log.Printf("loading model for %s: %s", lang, modelPath)
		mf, err := os.Open(modelPath)
		if err != nil {
			log.Fatalf("opening model %s: %v", modelPath, err)
		}

		m, err := goanon.LoadModel(mf)
		if err != nil {
			mf.Close()
			log.Fatalf("loading model %s: %v", modelPath, err)
		}
		mf.Close()

		srv.models[lang] = m
		srv.modelVersions[lang] = "auto"
		log.Printf("loaded model for language: %s", lang)
	}
}

// insecureSkipModelVerify reflète le flag -insecure-skip-verify : il désactive
// la vérification de signature du manifeste de modèles pour toutes les
// constructions de Store du serveur.
var insecureSkipModelVerify bool

func newModelStore(cacheDir string, offline bool) (*modelstore.Store, error) {
	opts := []modelstore.Option{
		modelstore.WithOfflineMode(offline),
		modelstore.WithInsecureSkipVerify(insecureSkipModelVerify),
	}
	if cacheDir != "" {
		opts = append(opts, modelstore.WithCacheDir(cacheDir))
	}
	return modelstore.New(opts...)
}

func parseGazetteersAuto(flagValue string) (lang string, isAuto bool) {
	v := strings.TrimSpace(flagValue)
	if v == "auto" {
		return "", true
	}
	if strings.HasPrefix(v, "auto:") {
		return strings.TrimPrefix(v, "auto:"), true
	}
	return "", false
}

func loadAutoGazetteers(gzLang string, models map[string]*ner.Model, cacheDir string, refresh, offline bool) map[string]*features.Gazetteer {
	store, err := newModelStore(cacheDir, offline)
	if err != nil {
		log.Fatalf("initialisation store gazetteers : %v", err)
	}

	ctx := context.Background()
	if refresh {
		if err := store.Refresh(ctx); err != nil {
			log.Printf("warning: refresh manifest: %v", err)
		}
	}

	var langs []string
	if gzLang != "" {
		langs = []string{gzLang}
	} else {
		for lang := range models {
			langs = append(langs, lang)
		}
	}

	result := make(map[string]*features.Gazetteer)
	for _, lang := range langs {
		paths, err := store.GetGazetteers(ctx, lang)
		if err != nil {
			log.Printf("warning: téléchargement gazetteers %q: %v", lang, err)
			continue
		}
		for gtype, gpath := range paths {
			if _, exists := result[gtype]; exists {
				continue
			}
			f, err := os.Open(gpath)
			if err != nil {
				log.Printf("warning: ouverture gazetteer %q: %v", gtype, err)
				continue
			}
			gaz, err := features.LoadGazetteer(gtype, f)
			f.Close()
			if err != nil {
				log.Printf("warning: chargement gazetteer %q: %v", gtype, err)
				continue
			}
			result[gtype] = gaz
			log.Printf("gazetteer chargé : %s", gtype)
		}
	}

	return result
}

func parseStrategy(name string) goanon.Strategy {
	switch strings.ToLower(name) {
	case "redact":
		return goanon.Redact
	case "hash":
		return goanon.Hash
	case "consistent":
		return goanon.Consistent
	default:
		return goanon.TagReplace
	}
}

func parseSkipTypes(skipTypes []string) []goanon.EntityType {
	if len(skipTypes) == 0 {
		return nil
	}
	skip := make(map[goanon.EntityType]bool)
	for _, t := range skipTypes {
		skip[goanon.EntityType(t)] = true
	}
	allTypes := []goanon.EntityType{
		goanon.TypePER, goanon.TypeLOC, goanon.TypeORG, goanon.TypeMISC,
		goanon.TypeEMAIL, goanon.TypeIPV4, goanon.TypeIPV6,
		goanon.TypeIBAN, goanon.TypeSIRET, goanon.TypeSIREN, goanon.TypePHONE,
		goanon.TypeAPIKey, goanon.TypeJWT, goanon.TypeSecret,
	}
	var result []goanon.EntityType
	for _, t := range allTypes {
		if !skip[t] {
			result = append(result, t)
		}
	}
	return result
}
