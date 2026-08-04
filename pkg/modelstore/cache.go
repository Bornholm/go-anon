package modelstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	manifestCacheFile = ".manifest.json"
	manifestMetaFile  = ".manifest-meta.json"
	modelFileSuffix   = ".crf.gz"
)

type manifestMeta struct {
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source"`
}

func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("modelstore: get user cache dir: %w", err)
	}
	dir := filepath.Join(base, "go-anon", "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("modelstore: create cache dir: %w", err)
	}
	return dir, nil
}

func modelCachePath(dir, lang string) string {
	return filepath.Join(dir, lang+modelFileSuffix)
}

func gazetteerCachePath(dir, name string) string {
	return filepath.Join(dir, name+".txt")
}

func isFileCached(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isModelCached(dir, lang string) bool {
	return isFileCached(modelCachePath(dir, lang))
}

func validateCachedSHAFromPath(path, expectedSHA string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("modelstore: hash cached file: %w", err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	return got == expectedSHA, nil
}

func validateCachedSHA(dir, lang, expectedSHA string) (bool, error) {
	return validateCachedSHAFromPath(modelCachePath(dir, lang), expectedSHA)
}

func saveManifest(dir string, m *Manifest, source string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("modelstore: marshal manifest: %w", err)
	}

	meta := manifestMeta{
		FetchedAt: time.Now().UTC(),
		Source:    source,
	}
	metaData, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("modelstore: marshal manifest meta: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, manifestCacheFile), data, 0o644); err != nil {
		return fmt.Errorf("modelstore: write manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestMetaFile), metaData, 0o644); err != nil {
		return fmt.Errorf("modelstore: write manifest meta: %w", err)
	}
	return nil
}

func loadManifest(dir string) (*Manifest, *manifestMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestCacheFile))
	if err != nil {
		return nil, nil, err
	}

	metaData, err := os.ReadFile(filepath.Join(dir, manifestMetaFile))
	if err != nil {
		return nil, nil, err
	}

	m, err := ParseManifest(data)
	if err != nil {
		return nil, nil, err
	}

	var meta manifestMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, nil, fmt.Errorf("modelstore: parse manifest meta: %w", err)
	}

	return m, &meta, nil
}

func isManifestFresh(dir string, ttl time.Duration) bool {
	_, meta, err := loadManifest(dir)
	if err != nil {
		return false
	}
	return time.Since(meta.FetchedAt) < ttl
}

// clusterCachePath nomme le fichier de clusters en cache. Le préfixe évite
// toute collision avec un gazetteer qui porterait un code de langue pour nom.
func clusterCachePath(dir, lang string) string {
	return filepath.Join(dir, "clusters_"+lang+".txt")
}
