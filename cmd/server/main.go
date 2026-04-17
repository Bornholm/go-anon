package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	goanon "github.com/bornholm/go-anon"
	"github.com/bornholm/go-anon/pkg/ner"
)

//go:embed index.html
var htmlContent embed.FS

type Server struct {
	models map[string]*ner.Model
	mu     sync.RWMutex
}

type AnonymizeRequest struct {
	Text          string              `json:"text"`
	Language      string              `json:"language"`
	Strategy      string              `json:"strategy"`
	MinConfidence float64             `json:"minConfidence"`
	MaxTokens     int                 `json:"maxTokens"`
	Blocklist     map[string][]string `json:"blocklist"`
	SkipTypes     []string            `json:"skipTypes"`
}

type Entity struct {
	Type       string  `json:"type"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

type AnonymizeResponse struct {
	Text     string            `json:"text"`
	Mapping  map[string]string `json:"mapping"`
	Entities []Entity          `json:"entities"`
}

type DeanonymizeRequest struct {
	Text    string            `json:"text"`
	Mapping map[string]string `json:"mapping"`
}

type DeanonymizeResponse struct {
	Text string `json:"text"`
}

func main() {
	modelsFlag := flag.String("models", "", "comma-separated list of lang:path pairs (e.g., fr:model_fr.crf.gz,en:model_en.crf.gz)")
	port := flag.String("port", "8080", "server port")
	flag.Parse()

	if *modelsFlag == "" {
		log.Fatal("error: -models flag is required (format: lang:path,lang:path)")
	}

	srv := &Server{
		models: make(map[string]*ner.Model),
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
	result, err := anon.Anonymize(req.Text)
	if err != nil {
		http.Error(w, "anonymization failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	entities := make([]Entity, len(result.Entities))
	for i, e := range result.Entities {
		entities[i] = Entity{
			Type:       string(e.Type),
			Text:       e.Text,
			Confidence: e.Confidence,
		}
	}

	resp := AnonymizeResponse{
		Text:     result.Text,
		Mapping:  result.Mapping,
		Entities: entities,
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

	text := req.Text
	for placeholder, original := range req.Mapping {
		text = strings.ReplaceAll(text, placeholder, original)
	}

	resp := DeanonymizeResponse{Text: text}
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
	var result []goanon.EntityType
	for _, t := range []goanon.EntityType{goanon.TypePER, goanon.TypeLOC, goanon.TypeORG, goanon.TypeMISC} {
		if !skip[t] {
			result = append(result, t)
		}
	}
	return result
}
