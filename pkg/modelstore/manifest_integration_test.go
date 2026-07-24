package modelstore

import (
	"context"
	"net/http"
	"testing"
)

func TestIntegrationRealManifestParsing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	client := &http.Client{}
	disc := NewStaticDiscoverer(DefaultManifestURL)
	disc.HTTPClient = client

	m, source, err := disc.Discover(context.Background())
	if err != nil {
		t.Fatalf("fetch manifest from %s: %v", DefaultManifestURL, err)
	}

	if source != DefaultManifestURL {
		t.Errorf("expected source %q, got %q", DefaultManifestURL, source)
	}

	if m.SchemaVersion != SupportedSchemaVersion {
		t.Errorf("expected schema_version %d, got %d", SupportedSchemaVersion, m.SchemaVersion)
	}

	if m.Version == "" {
		t.Error("expected non-empty version")
	}

	if m.PublishedAt.IsZero() {
		t.Error("expected non-zero published_at")
	}

	expectedLangs := []string{"en", "es", "fr"}
	for _, lang := range expectedLangs {
		entry, ok := m.Models[lang]
		if !ok {
			t.Errorf("expected model for language %q", lang)
			continue
		}
		if entry.URL == "" {
			t.Errorf("model %q: empty URL", lang)
		}
		if entry.SHA256 == "" {
			t.Errorf("model %q: empty SHA256", lang)
		}
		if entry.SizeBytes <= 0 {
			t.Errorf("model %q: invalid size_bytes: %d", lang, entry.SizeBytes)
		}
	}

	if len(m.Gazetteers) == 0 {
		t.Error("expected at least one gazetteer")
	}

	for name, g := range m.Gazetteers {
		if g.URL == "" {
			t.Errorf("gazetteer %q: empty URL", name)
		}
		if g.SHA256 == "" {
			t.Errorf("gazetteer %q: empty SHA256", name)
		}
		if g.SizeBytes <= 0 {
			t.Errorf("gazetteer %q: invalid size_bytes", name)
		}
		if len(g.Languages) == 0 {
			t.Errorf("gazetteer %q: no languages", name)
		}
		if g.Type == "" {
			t.Errorf("gazetteer %q: empty type", name)
		}
	}
}

func TestIntegrationRealStoreAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	scope := t.TempDir()
	store, err := New(
		WithCacheDir(scope),
		WithManifestURL(DefaultManifestURL),
		WithManifestTTL(0),
		// Ce test vérifie la disponibilité et le parsing du manifest réel, pas la
		// signature : on la court-circuite pour rester indépendant du déploiement
		// de manifest.json.minisig côté go-anon-resources.
		WithInsecureSkipVerify(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	langs, err := store.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}

	if len(langs) == 0 {
		t.Fatal("expected at least one language, got empty")
	}

	expected := []string{"en", "es", "fr"}
	for i, lang := range langs {
		if lang != expected[i] {
			t.Errorf("langs[%d] = %q, want %q", i, lang, expected[i])
		}
	}
}
