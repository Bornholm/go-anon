package modelstore

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheDir(t *testing.T) {
	dir, err := cacheDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir == "" {
		t.Fatal("expected non-empty cache dir")
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("cache dir does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("cache dir is not a directory")
	}
}

func TestModelCachePath(t *testing.T) {
	path := modelCachePath("/tmp/cache", "fr")
	expected := filepath.Join("/tmp/cache", "fr.crf.gz")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func TestIsModelCached(t *testing.T) {
	dir := t.TempDir()

	if isModelCached(dir, "fr") {
		t.Fatal("expected not cached")
	}

	f, err := os.Create(modelCachePath(dir, "fr"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if !isModelCached(dir, "fr") {
		t.Fatal("expected cached")
	}
}

func TestValidateCachedSHA(t *testing.T) {
	dir := t.TempDir()
	content := []byte("test model content")
	expectedSHA := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expectedSHA[:])

	path := modelCachePath(dir, "fr")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	valid, err := validateCachedSHA(dir, "fr", expectedHex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Fatal("expected valid SHA")
	}

	valid, err = validateCachedSHA(dir, "fr", "wrongsha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid {
		t.Fatal("expected invalid SHA")
	}
}

func TestValidateCachedSHANonExistent(t *testing.T) {
	dir := t.TempDir()

	_, err := validateCachedSHA(dir, "fr", "abc123")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestSaveAndLoadManifest(t *testing.T) {
	dir := t.TempDir()

	m := &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models: map[string]ModelEntry{
			"fr": {
				URL:       "https://example.com/fr.crf.gz",
				SHA256:    "abc123",
				SizeBytes: 1000,
			},
		},
	}

	if err := saveManifest(dir, m, "test-source"); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	loaded, meta, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	if loaded.SchemaVersion != m.SchemaVersion {
		t.Errorf("schema version mismatch")
	}
	if loaded.Version != m.Version {
		t.Errorf("version mismatch")
	}
	if len(loaded.Models) != len(m.Models) {
		t.Errorf("models count mismatch")
	}
	if meta.Source != "test-source" {
		t.Errorf("source mismatch: got %s", meta.Source)
	}
}

func TestLoadManifestNonExistent(t *testing.T) {
	dir := t.TempDir()

	_, _, err := loadManifest(dir)
	if err == nil {
		t.Fatal("expected error for non-existent manifest")
	}
}

func TestIsManifestFresh(t *testing.T) {
	dir := t.TempDir()

	m := &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models: map[string]ModelEntry{
			"fr": {
				URL:       "https://example.com/fr.crf.gz",
				SHA256:    "abc123",
				SizeBytes: 1000,
			},
		},
	}

	if err := saveManifest(dir, m, "test"); err != nil {
		t.Fatal(err)
	}

	if !isManifestFresh(dir, 1*time.Hour) {
		t.Fatal("expected fresh")
	}

	if isManifestFresh(dir, 0) {
		t.Fatal("expected not fresh with zero TTL")
	}
}

func TestIsManifestExpired(t *testing.T) {
	dir := t.TempDir()

	m := &Manifest{
		SchemaVersion: 1,
		Version:       "models-v1",
		PublishedAt:   time.Now().UTC(),
		Models: map[string]ModelEntry{
			"fr": {
				URL:       "https://example.com/fr.crf.gz",
				SHA256:    "abc123",
				SizeBytes: 1000,
			},
		},
	}

	if err := saveManifest(dir, m, "test"); err != nil {
		t.Fatal(err)
	}

	if isManifestFresh(dir, 0) {
		t.Fatal("expected not fresh")
	}
}
