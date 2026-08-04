package modelstore

import (
	"testing"
	"time"
)

func TestParseManifestValid(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.SchemaVersion != 1 {
		t.Errorf("expected schema version 1, got %d", m.SchemaVersion)
	}
	if m.Version != "models-v1" {
		t.Errorf("expected version models-v1, got %s", m.Version)
	}
	if len(m.Models) != 1 {
		t.Errorf("expected 1 model, got %d", len(m.Models))
	}
	entry, ok := m.Models["fr"]
	if !ok {
		t.Fatal("expected fr model")
	}
	if entry.URL != "https://example.com/fr.crf.gz" {
		t.Errorf("unexpected URL: %s", entry.URL)
	}
	if entry.SHA256 != "abc123" {
		t.Errorf("unexpected SHA256: %s", entry.SHA256)
	}
	if entry.SizeBytes != 1000 {
		t.Errorf("unexpected size: %d", entry.SizeBytes)
	}
}

func TestParseManifestInvalidJSON(t *testing.T) {
	_, err := ParseManifest([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidateSchemaVersion(t *testing.T) {
	data := []byte(`{
		"schema_version": 99,
		"version": "models-v99",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for unsupported schema version")
	}
}

func TestValidateMissingVersion(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestValidateMissingPublishedAt(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for missing published_at")
	}
}

func TestValidateNoModels(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for empty models")
	}
}

func TestValidateMissingURL(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"sha256": "abc123",
				"size_bytes": 1000
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestValidateMissingSHA256(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"size_bytes": 1000
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for missing SHA256")
	}
}

func TestValidateInvalidSize(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": -1
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
}

func TestManifestPublishedAtParsing(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	if !m.PublishedAt.Equal(expected) {
		t.Errorf("expected published_at %v, got %v", expected, m.PublishedAt)
	}
}

func TestParseManifestWithGazetteers(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		},
		"gazetteers": {
			"fr_prenoms": {
				"url": "https://example.com/fr_prenoms.txt",
				"sha256": "def456",
				"size_bytes": 500,
				"languages": ["fr"],
				"type": "firstnames"
			},
			"fr_en_es_prenoms": {
				"url": "https://example.com/fr_en_es_prenoms.txt",
				"sha256": "ghi789",
				"size_bytes": 2000,
				"languages": ["fr", "en", "es"],
				"type": "firstnames"
			}
		}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(m.Gazetteers) != 2 {
		t.Errorf("expected 2 gazetteers, got %d", len(m.Gazetteers))
	}

	fr, ok := m.Gazetteers["fr_prenoms"]
	if !ok {
		t.Fatal("expected fr_prenoms gazetteer")
	}
	if fr.Type != "firstnames" {
		t.Errorf("expected type firstnames, got %s", fr.Type)
	}
	if len(fr.Languages) != 1 || fr.Languages[0] != "fr" {
		t.Errorf("expected languages [fr], got %v", fr.Languages)
	}

	eu, ok := m.Gazetteers["fr_en_es_prenoms"]
	if !ok {
		t.Fatal("expected fr_en_es_prenoms gazetteer")
	}
	if len(eu.Languages) != 3 {
		t.Errorf("expected 3 languages, got %d", len(eu.Languages))
	}
}

func TestValidateGazetteerMissingLanguages(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		},
		"gazetteers": {
			"fr_prenoms": {
				"url": "https://example.com/fr_prenoms.txt",
				"sha256": "def456",
				"size_bytes": 500,
				"type": "firstnames"
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for missing languages")
	}
}

func TestValidateGazetteerMissingType(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		},
		"gazetteers": {
			"fr_prenoms": {
				"url": "https://example.com/fr_prenoms.txt",
				"sha256": "def456",
				"size_bytes": 500,
				"languages": ["fr"]
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestValidateGazetteerInvalidSize(t *testing.T) {
	data := []byte(`{
		"schema_version": 1,
		"version": "models-v1",
		"published_at": "2025-01-15T10:00:00Z",
		"models": {
			"fr": {
				"url": "https://example.com/fr.crf.gz",
				"sha256": "abc123",
				"size_bytes": 1000
			}
		},
		"gazetteers": {
			"fr_prenoms": {
				"url": "https://example.com/fr_prenoms.txt",
				"sha256": "def456",
				"size_bytes": -1,
				"languages": ["fr"],
				"type": "firstnames"
			}
		}
	}`)

	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
}

// TestParseManifest_WithClusters : contrat avec go-anon-resources. Le champ
// clusters est indexé par code de langue et validé comme tel.
func TestParseManifest_WithClusters(t *testing.T) {
	data := []byte(`{
	  "schema_version": 1,
	  "version": "v2026.8.4-abc",
	  "published_at": "2026-08-04T09:00:00Z",
	  "models": {"fr": {"url": "https://x/fr.crf.gz", "sha256": "aa", "size_bytes": 10}},
	  "clusters": {"fr": {"url": "https://x/clusters_fr.txt", "sha256": "bb", "size_bytes": 20}}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	entry, ok := m.Clusters["fr"]
	if !ok {
		t.Fatal("clusters fr absents")
	}
	if entry.URL != "https://x/clusters_fr.txt" || entry.SizeBytes != 20 {
		t.Errorf("entrée = %+v", entry)
	}
}

// TestParseManifest_WithoutClusters : le champ est facultatif, donc un manifest
// publié avant leur distribution reste valide. Sans cela, ajouter les clusters
// casserait tous les clients existants.
func TestParseManifest_WithoutClusters(t *testing.T) {
	data := []byte(`{
	  "schema_version": 1,
	  "version": "v1",
	  "published_at": "2026-08-04T09:00:00Z",
	  "models": {"fr": {"url": "https://x/fr.crf.gz", "sha256": "aa", "size_bytes": 10}}
	}`)

	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Clusters) != 0 {
		t.Errorf("clusters = %+v, attendu vide", m.Clusters)
	}
}

// TestParseManifest_RejectsBadClusterKey : la clé sert à construire un chemin
// de cache ; un code hors ^[a-z]{2}$ ouvrirait une traversée de répertoire.
func TestParseManifest_RejectsBadClusterKey(t *testing.T) {
	for _, key := range []string{"../etc", "FR", "français", ""} {
		data := []byte(`{
		  "schema_version": 1,
		  "version": "v1",
		  "published_at": "2026-08-04T09:00:00Z",
		  "models": {"fr": {"url": "https://x/fr.crf.gz", "sha256": "aa", "size_bytes": 10}},
		  "clusters": {"` + key + `": {"url": "https://x/c.txt", "sha256": "bb", "size_bytes": 20}}
		}`)
		if _, err := ParseManifest(data); err == nil {
			t.Errorf("clé %q acceptée à tort", key)
		}
	}
}
