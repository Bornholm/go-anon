package modelstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const validManifestJSON = `{
	"schema_version": 1,
	"version": "models-v1",
	"published_at": "2025-01-15T10:00:00Z",
	"models": {
		"fr": {"url": "https://example.com/fr.crf.gz", "sha256": "abc123", "size_bytes": 1000}
	}
}`

// signedManifestServer sert le manifest sur /manifest.json et sa signature sur
// /manifest.json.minisig. sigOverride, s'il est non nil, remplace la signature
// servie (pour simuler une altération).
func signedManifestServer(t *testing.T, manifest, sig []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifest)
	})
	mux.HandleFunc("/manifest.json.minisig", func(w http.ResponseWriter, r *http.Request) {
		if sig == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(sig)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSignedDiscoverer_VerifiesValidSignature(t *testing.T) {
	priv, pk := testKey(t)
	manifest := []byte(validManifestJSON)
	sig := makeSig(priv, pk.keyID, manifest, true, "models-v1")

	srv := signedManifestServer(t, manifest, sig)

	disc := NewStaticDiscoverer(srv.URL + "/manifest.json")
	disc.HTTPClient = srv.Client()
	disc.TrustedKey = pk

	m, _, err := disc.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover avec signature valide: %v", err)
	}
	if m.Version != "models-v1" {
		t.Errorf("version inattendue: %s", m.Version)
	}
}

func TestSignedDiscoverer_RejectsTamperedManifest(t *testing.T) {
	priv, pk := testKey(t)
	original := []byte(validManifestJSON)
	sig := makeSig(priv, pk.keyID, original, true, "models-v1")

	// On sert un manifest modifié mais l'ancienne signature.
	tampered := []byte(validManifestJSON + " ")
	srv := signedManifestServer(t, tampered, sig)

	disc := NewStaticDiscoverer(srv.URL + "/manifest.json")
	disc.HTTPClient = srv.Client()
	disc.TrustedKey = pk

	_, _, err := disc.Discover(context.Background())
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("attendu ErrSignatureInvalid sur manifest altéré, got %v", err)
	}
}

func TestSignedDiscoverer_FailsOnMissingSignature(t *testing.T) {
	_, pk := testKey(t)
	srv := signedManifestServer(t, []byte(validManifestJSON), nil) // pas de signature servie

	disc := NewStaticDiscoverer(srv.URL + "/manifest.json")
	disc.HTTPClient = srv.Client()
	disc.TrustedKey = pk

	if _, _, err := disc.Discover(context.Background()); err == nil {
		t.Fatal("attendu une erreur quand la signature est absente et une clé de confiance imposée")
	}
}

func TestSignedDiscoverer_SkipVerifyBypasses(t *testing.T) {
	_, pk := testKey(t)
	srv := signedManifestServer(t, []byte(validManifestJSON), nil)

	disc := NewStaticDiscoverer(srv.URL + "/manifest.json")
	disc.HTTPClient = srv.Client()
	disc.TrustedKey = pk
	disc.SkipVerify = true // court-circuite la vérification

	if _, _, err := disc.Discover(context.Background()); err != nil {
		t.Fatalf("SkipVerify devrait ignorer la signature: %v", err)
	}
}

func TestSignedDiscoverer_NoKeyProceedsWithWarning(t *testing.T) {
	srv := signedManifestServer(t, []byte(validManifestJSON), nil)

	disc := NewStaticDiscoverer(srv.URL + "/manifest.json")
	disc.HTTPClient = srv.Client()
	// TrustedKey nil : rétrocompatibilité, avertissement + poursuite.

	if _, _, err := disc.Discover(context.Background()); err != nil {
		t.Fatalf("sans clé de confiance, Discover devrait réussir (avec avertissement): %v", err)
	}
}

func TestValidateLang(t *testing.T) {
	valid := []string{"fr", "en", "es"}
	for _, l := range valid {
		if err := validateLang(l); err != nil {
			t.Errorf("validateLang(%q) inattendu: %v", l, err)
		}
	}
	invalid := []string{"", "f", "fra", "FR", "f1", "../", "e/", "..", "e\x00"}
	for _, l := range invalid {
		if err := validateLang(l); !errors.Is(err, ErrInvalidLang) {
			t.Errorf("validateLang(%q) = %v, attendu ErrInvalidLang", l, err)
		}
	}
}

func TestStoreGet_RejectsInvalidLang(t *testing.T) {
	s := &Store{cacheDir: t.TempDir()}
	if _, err := s.Get(context.Background(), "../etc"); !errors.Is(err, ErrInvalidLang) {
		t.Fatalf("Get avec langue traversante = %v, attendu ErrInvalidLang", err)
	}
}
