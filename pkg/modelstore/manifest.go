package modelstore

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	SupportedSchemaVersion = 1
	DefaultManifestURL     = "https://bornholm.github.io/go-anon-resources/manifest.json"
	DefaultManifestTTL     = 6 * time.Hour
)

type Manifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Version       string                    `json:"version"`
	PublishedAt   time.Time                 `json:"published_at"`
	Models        map[string]ModelEntry     `json:"models"`
	Gazetteers    map[string]GazetteerEntry `json:"gazetteers,omitempty"`
	// Clusters distribue les Brown clusters par langue.
	//
	// Champ facultatif ajouté sans changer schema_version : un client antérieur
	// l'ignore, un manifeste antérieur le laisse vide. Les clusters sont une
	// feature du modèle au même titre que les gazetteers — s'ils manquent à
	// l'inférence alors que l'entraînement les utilisait, le modèle perd
	// silencieusement du rappel.
	Clusters map[string]ClusterEntry `json:"clusters,omitempty"`
}

type ModelEntry struct {
	URL       string         `json:"url"`
	SHA256    string         `json:"sha256"`
	SizeBytes int64          `json:"size_bytes"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type GazetteerEntry struct {
	URL       string   `json:"url"`
	SHA256    string   `json:"sha256"`
	SizeBytes int64    `json:"size_bytes"`
	Languages []string `json:"languages"`
	Type      string   `json:"type"`
}

// ClusterEntry décrit un fichier de Brown clusters.
//
// Contrairement à GazetteerEntry, l'indexation se fait directement par langue :
// un jeu de clusters est lié au corpus qui l'a produit et n'est pas partageable
// entre langues, là où une liste de villes peut l'être.
type ClusterEntry struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Validate() error {
	if m.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrManifestSchema, m.SchemaVersion, SupportedSchemaVersion)
	}
	if m.Version == "" {
		return fmt.Errorf("parse manifest: missing version")
	}
	if m.PublishedAt.IsZero() {
		return fmt.Errorf("parse manifest: missing published_at")
	}
	if len(m.Models) == 0 {
		return fmt.Errorf("parse manifest: no models defined")
	}
	for lang, entry := range m.Models {
		if lang == "" {
			return fmt.Errorf("parse manifest: empty language key")
		}
		if entry.URL == "" {
			return fmt.Errorf("parse manifest: missing url for language %q", lang)
		}
		if entry.SHA256 == "" {
			return fmt.Errorf("parse manifest: missing sha256 for language %q", lang)
		}
		if entry.SizeBytes <= 0 {
			return fmt.Errorf("parse manifest: invalid size_bytes for language %q: %d", lang, entry.SizeBytes)
		}
	}
	for name, entry := range m.Gazetteers {
		if name == "" {
			return fmt.Errorf("parse manifest: empty gazetteer key")
		}
		if entry.URL == "" {
			return fmt.Errorf("parse manifest: missing url for gazetteer %q", name)
		}
		if entry.SHA256 == "" {
			return fmt.Errorf("parse manifest: missing sha256 for gazetteer %q", name)
		}
		if entry.SizeBytes <= 0 {
			return fmt.Errorf("parse manifest: invalid size_bytes for gazetteer %q: %d", name, entry.SizeBytes)
		}
		if len(entry.Languages) == 0 {
			return fmt.Errorf("parse manifest: missing languages for gazetteer %q", name)
		}
		if entry.Type == "" {
			return fmt.Errorf("parse manifest: missing type for gazetteer %q", name)
		}
	}
	for lang, entry := range m.Clusters {
		if err := validateLang(lang); err != nil {
			return fmt.Errorf("parse manifest: cluster key %q: %w", lang, err)
		}
		if entry.URL == "" {
			return fmt.Errorf("parse manifest: missing url for clusters %q", lang)
		}
		if entry.SHA256 == "" {
			return fmt.Errorf("parse manifest: missing sha256 for clusters %q", lang)
		}
		if entry.SizeBytes <= 0 {
			return fmt.Errorf("parse manifest: invalid size_bytes for clusters %q: %d", lang, entry.SizeBytes)
		}
	}
	return nil
}
