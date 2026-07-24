package modelstore

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testManifest(t *testing.T, models map[string][]byte) (*Manifest, map[string]string) {
	shas := make(map[string]string)
	manifestModels := make(map[string]ModelEntry)

	for lang, content := range models {
		h := sha256.Sum256(content)
		sha := hex.EncodeToString(h[:])
		shas[lang] = sha
		manifestModels[lang] = ModelEntry{
			URL:       "https://placeholder/" + lang + ".crf.gz",
			SHA256:    sha,
			SizeBytes: int64(len(content)),
		}
	}

	m := &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models:        manifestModels,
	}

	return m, shas
}

func setupTestServer(t *testing.T, models map[string][]byte) (*httptest.Server, *http.Client, *Manifest, map[string]string) {
	m, shas := testManifest(t, models)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(m)
			return
		}

		for lang, content := range models {
			if r.URL.Path == "/"+lang+".crf.gz" {
				w.Write(content)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	for lang := range models {
		entry := m.Models[lang]
		entry.URL = server.URL + "/" + lang + ".crf.gz"
		m.Models[lang] = entry
	}

	return server, client, m, shas
}

func TestStoreGetCacheMiss(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	path, err := store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(models["fr"]) {
		t.Errorf("content mismatch")
	}
}

func TestStoreGetCacheHit(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, shas := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()

	content := models["fr"]
	if err := os.WriteFile(filepath.Join(dir, "fr.crf.gz"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	store.manifest = &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models: map[string]ModelEntry{
			"fr": {
				URL:       server.URL + "/fr.crf.gz",
				SHA256:    shas["fr"],
				SizeBytes: int64(len(content)),
			},
		},
	}

	path, err := store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(models["fr"]) {
		t.Errorf("content mismatch")
	}
}

func TestStoreGetLanguageNotFound(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Get(context.Background(), "de")
	if err != ErrLanguageNotFound {
		t.Fatalf("expected ErrLanguageNotFound, got %v", err)
	}
}

func TestStoreGetOfflineMode(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
		WithOfflineMode(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Get(context.Background(), "fr")
	if err != ErrManifestExpired {
		t.Fatalf("expected ErrManifestExpired, got %v", err)
	}
}

func TestStoreGetAll(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
		"en": []byte("english model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.GetAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result))
	}

	for lang := range models {
		if _, ok := result[lang]; !ok {
			t.Errorf("missing model for %s", lang)
		}
	}
}

func TestStoreRefresh(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Refresh(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.manifest == nil {
		t.Fatal("expected manifest after refresh")
	}
	if len(store.manifest.Models) != 1 {
		t.Errorf("expected 1 model, got %d", len(store.manifest.Models))
	}
}

func TestStoreRefreshOffline(t *testing.T) {
	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithOfflineMode(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Refresh(context.Background())
	if err != ErrOffline {
		t.Fatalf("expected ErrOffline, got %v", err)
	}
}

func TestStoreAvailable(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
		"en": []byte("english model content"),
		"es": []byte("spanish model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	langs, err := store.Available(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(langs) != 3 {
		t.Fatalf("expected 3 languages, got %d", len(langs))
	}

	expected := []string{"en", "es", "fr"}
	for i, lang := range langs {
		if lang != expected[i] {
			t.Errorf("expected %s at index %d, got %s", expected[i], i, lang)
		}
	}
}

func TestStoreIsCached(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	if store.IsCached("fr") {
		t.Fatal("expected not cached before download")
	}

	_, err = store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatal(err)
	}

	if !store.IsCached("fr") {
		t.Fatal("expected cached after download")
	}

	entry := store.manifest.Models["fr"]
	entry.SHA256 = "wrong"
	store.manifest.Models["fr"] = entry
	if store.IsCached("fr") {
		t.Fatal("expected not cached after SHA change")
	}
}

func TestStoreConcurrentGet(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Get(context.Background(), "fr")
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Get error: %v", err)
	}
}

func TestStoreManifestCaching(t *testing.T) {
	models := map[string][]byte{
		"fr": []byte("french model content"),
	}
	server, client, _, _ := setupTestServer(t, models)
	defer server.Close()

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
		WithManifestTTL(1*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatal(err)
	}

	server.Close()

	_, err = store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatalf("expected cache hit after server shutdown: %v", err)
	}
}

func TestStoreGetGazetteers(t *testing.T) {
	content := []byte("fake model")
	modelSHA := sha256.Sum256(content)
	modelHex := hex.EncodeToString(modelSHA[:])

	gazContent := []byte("prenom1\nprenom2\n")
	gazSHA := sha256.Sum256(gazContent)
	gazHex := hex.EncodeToString(gazSHA[:])

	m := &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models: map[string]ModelEntry{
			"fr": {
				URL:       "https://placeholder/fr.crf.gz",
				SHA256:    modelHex,
				SizeBytes: int64(len(content)),
			},
		},
		Gazetteers: map[string]GazetteerEntry{
			"fr_prenoms": {
				URL:       "https://placeholder/fr_prenoms.txt",
				SHA256:    gazHex,
				SizeBytes: int64(len(gazContent)),
				Languages: []string{"fr"},
				Type:      "firstnames",
			},
		},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(m)
		case "/fr.crf.gz":
			w.Write(content)
		case "/fr_prenoms.txt":
			w.Write(gazContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	m.Models["fr"] = ModelEntry{
		URL:       server.URL + "/fr.crf.gz",
		SHA256:    modelHex,
		SizeBytes: int64(len(content)),
	}
	m.Gazetteers["fr_prenoms"] = GazetteerEntry{
		URL:       server.URL + "/fr_prenoms.txt",
		SHA256:    gazHex,
		SizeBytes: int64(len(gazContent)),
		Languages: []string{"fr"},
		Type:      "firstnames",
	}

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.GetGazetteers(context.Background(), "fr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 gazetteer type, got %d", len(result))
	}

	path, ok := result["firstnames"]
	if !ok {
		t.Fatal("expected 'firstnames' in result")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(gazContent) {
		t.Errorf("gazetteer content mismatch")
	}

	resultEn, err := store.GetGazetteers(context.Background(), "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resultEn) != 0 {
		t.Errorf("expected no gazetteers for en, got %d", len(resultEn))
	}
}

func TestStoreGetGazetteersDedup(t *testing.T) {
	content := []byte("fake model")
	modelSHA := sha256.Sum256(content)
	modelHex := hex.EncodeToString(modelSHA[:])

	smallGaz := []byte("small\n")
	smallSHA := sha256.Sum256(smallGaz)
	smallHex := hex.EncodeToString(smallSHA[:])

	largeGaz := []byte("large\ncontent\nhere\n")
	largeSHA := sha256.Sum256(largeGaz)
	largeHex := hex.EncodeToString(largeSHA[:])

	m := &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models: map[string]ModelEntry{
			"fr": {
				URL:       "https://placeholder/fr.crf.gz",
				SHA256:    modelHex,
				SizeBytes: int64(len(content)),
			},
		},
		Gazetteers: map[string]GazetteerEntry{
			"fr_villes": {
				URL:       "https://placeholder/fr_villes.txt",
				SHA256:    smallHex,
				SizeBytes: int64(len(smallGaz)),
				Languages: []string{"fr"},
				Type:      "locations",
			},
			"fr_communes": {
				URL:       "https://placeholder/fr_communes.txt",
				SHA256:    largeHex,
				SizeBytes: int64(len(largeGaz)),
				Languages: []string{"fr"},
				Type:      "locations",
			},
		},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(m)
		case "/fr.crf.gz":
			w.Write(content)
		case "/fr_villes.txt":
			w.Write(smallGaz)
		case "/fr_communes.txt":
			w.Write(largeGaz)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	m.Models["fr"] = ModelEntry{
		URL:       server.URL + "/fr.crf.gz",
		SHA256:    modelHex,
		SizeBytes: int64(len(content)),
	}
	m.Gazetteers["fr_villes"] = GazetteerEntry{
		URL:       server.URL + "/fr_villes.txt",
		SHA256:    smallHex,
		SizeBytes: int64(len(smallGaz)),
		Languages: []string{"fr"},
		Type:      "locations",
	}
	m.Gazetteers["fr_communes"] = GazetteerEntry{
		URL:       server.URL + "/fr_communes.txt",
		SHA256:    largeHex,
		SizeBytes: int64(len(largeGaz)),
		Languages: []string{"fr"},
		Type:      "locations",
	}

	dir := t.TempDir()
	store, err := New(
		WithCacheDir(dir),
		WithManifestURL(server.URL+"/manifest.json"),
		WithInsecureSkipVerify(true), // ces tests ne portent pas sur la signature
		WithHTTPClient(client),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Get(context.Background(), "fr")
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.GetGazetteers(context.Background(), "fr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 type, got %d", len(result))
	}

	path := result["locations"]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(largeGaz) {
		t.Error("expected the larger gazetteer to be kept")
	}
}
