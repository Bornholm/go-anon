package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	goanon "github.com/bornholm/go-anon"
	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	pkgdocx "github.com/bornholm/go-anon/pkg/docx"
	pkgodt "github.com/bornholm/go-anon/pkg/odt"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/ner"
)

type walkerFactory func(path string) (docprocessor.Walker, error)

var walkerFactories = map[string]walkerFactory{
	".docx": pkgdocx.NewWalkerFromFile,
	".odt":  pkgodt.NewWalkerFromFile,
}

var docMimeTypes = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".odt":  "application/vnd.oasis.opendocument.text",
}

//go:embed index.html
var htmlContent embed.FS

type Server struct {
	models     map[string]*ner.Model
	gazetteers map[string]*features.Gazetteer
	mu         sync.RWMutex
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
	Mapping    map[string]string `json:"mapping"`
	Entities   []Entity          `json:"entities"`
	DurationMs float64           `json:"durationMs"`
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
	modelsFlag := flag.String("models", "", "comma-separated list of lang:path pairs (e.g., fr:model_fr.crf.gz,en:model_en.crf.gz)")
	gazetteerFlag := flag.String("gazetteers", "", `gazetteers à charger : "nom:fichier.txt,nom:fichier.txt"`)
	port := flag.String("port", "8080", "server port")
	flag.Parse()

	if *modelsFlag == "" {
		log.Fatal("error: -models flag is required (format: lang:path,lang:path)")
	}

	gazetteers, err := cmdutil.ParseGazetteers(*gazetteerFlag)
	if err != nil {
		log.Fatalf("chargement gazetteers : %v", err)
	}

	srv := &Server{
		models:     make(map[string]*ner.Model),
		gazetteers: gazetteers,
	}

	for _, pair := range strings.Split(*modelsFlag, ",") {
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

		log.Printf("loading model for %s: %s", lang, path)
		mf, err := os.Open(path)
		if err != nil {
			log.Fatalf("opening model %s: %v", path, err)
		}

		m, err := goanon.LoadModel(mf)
		if err != nil {
			mf.Close()
			log.Fatalf("loading model %s: %v", path, err)
		}
		mf.Close()

		srv.models[strings.ToLower(lang)] = m
		log.Printf("loaded model for language: %s", lang)
	}

	if len(srv.models) == 0 {
		log.Fatal("error: no models loaded")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.serveHTML)
	mux.HandleFunc("/api/anonymize", srv.handleAnonymize)
	mux.HandleFunc("/api/deanonymize", srv.handleDeanonymize)
	mux.HandleFunc("/api/languages", srv.handleLanguages)
	mux.HandleFunc("/api/doc-formats", srv.handleDocFormats)
	mux.HandleFunc("/api/anonymize-doc", srv.handleAnonymizeDoc)

	addr := ":" + *port
	log.Printf("starting server on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
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

func (s *Server) handleAnonymize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnonymizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	lang := strings.ToLower(req.Language)
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

	anon := goanon.NewAnonymizer(rec, cfg)
	anonStart := time.Now()
	result, err := anon.Anonymize(req.Text)
	if err != nil {
		http.Error(w, "anonymization failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("anonymize: %d chars, %d entities, took %s", len(req.Text), len(result.Entities), time.Since(anonStart))
	for _, e := range result.Entities {
		log.Printf("  entity type=%s text=%q start=%d end=%d conf=%.2f", e.Type, e.Text, e.Start, e.End, e.Confidence)
	}

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
		Mapping:    mapping,
		Entities:   entities,
		DurationMs: float64(time.Since(anonStart).Microseconds()) / 1000.0,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDeanonymize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeanonymizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	deanonStart := time.Now()
	text := req.Text
	for placeholder, original := range req.Mapping {
		text = strings.ReplaceAll(text, placeholder, original)
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

func (s *Server) handleAnonymizeDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "fichier trop volumineux (max 2 Mo)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "fichier manquant", http.StatusBadRequest)
		return
	}
	defer file.Close()

	lang := strings.ToLower(r.FormValue("lang"))
	if lang == "" {
		lang = "fr"
	}

	s.mu.RLock()
	model, ok := s.models[lang]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, fmt.Sprintf("langue %q non supportée", lang), http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	factory, ok := walkerFactories[ext]
	if !ok {
		http.Error(w, fmt.Sprintf("format %q non supporté", ext), http.StatusBadRequest)
		return
	}

	tmpIn, err := os.CreateTemp("", "goanon-in-*"+ext)
	if err != nil {
		http.Error(w, "erreur interne", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpIn.Name())

	if _, err := io.Copy(tmpIn, file); err != nil {
		tmpIn.Close()
		http.Error(w, "erreur lecture fichier", http.StatusInternalServerError)
		return
	}
	tmpIn.Close()

	tmpOut, err := os.CreateTemp("", "goanon-out-*"+ext)
	if err != nil {
		http.Error(w, "erreur interne", http.StatusInternalServerError)
		return
	}
	tmpOutName := tmpOut.Name()
	tmpOut.Close()
	defer os.Remove(tmpOutName)

	rec, err := goanon.NewRecognizer(model,
		goanon.WithLanguage(lang),
		goanon.WithBuiltinRegexPatterns(),
	)
	if err != nil {
		http.Error(w, "erreur initialisation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cfg := goanon.Config{
		Strategy:      goanon.TagReplace,
		ConsistentMap: true,
	}
	anon := goanon.NewAnonymizer(rec, cfg)
	proc := docprocessor.New(anon)

	walker, err := factory(tmpIn.Name())
	if err != nil {
		http.Error(w, "erreur ouverture document: "+err.Error(), http.StatusInternalServerError)
		return
	}

	session, err := proc.Process(walker)
	if err != nil {
		http.Error(w, "erreur anonymisation: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("anonymize-doc: lang=%s format=%s entities=%d", lang, ext, len(session.Mapping))

	saver, ok := walker.(interface{ SaveTo(string) error })
	if !ok {
		http.Error(w, "format non sauvegardable", http.StatusInternalServerError)
		return
	}
	if err := saver.SaveTo(tmpOutName); err != nil {
		http.Error(w, "erreur sauvegarde: "+err.Error(), http.StatusInternalServerError)
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
	outFilename := "anonymized_" + filepath.Base(header.Filename)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+outFilename+`"`)
	io.Copy(w, outFile)
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
	}
	var result []goanon.EntityType
	for _, t := range allTypes {
		if !skip[t] {
			result = append(result, t)
		}
	}
	return result
}
