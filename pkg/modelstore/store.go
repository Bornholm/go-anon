package modelstore

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Store struct {
	mu           sync.RWMutex
	cacheDir     string
	manifest     *Manifest
	manifestMeta *manifestMeta
	discoverer   Discoverer
	manifestURL  string
	manifestTTL  time.Duration
	httpClient   *http.Client
	offline      bool
	progressFn   func(lang string, done, total int64)
	trustedKey   *PublicKey
	skipVerify   bool
}

type Option func(*Store)

func WithCacheDir(dir string) Option {
	return func(s *Store) {
		s.cacheDir = dir
	}
}

func WithManifestURL(url string) Option {
	return func(s *Store) {
		s.manifestURL = url
	}
}

func WithDiscoverer(d Discoverer) Option {
	return func(s *Store) {
		s.discoverer = d
	}
}

func WithManifestTTL(d time.Duration) Option {
	return func(s *Store) {
		s.manifestTTL = d
	}
}

func WithProgress(fn func(lang string, done, total int64)) Option {
	return func(s *Store) {
		s.progressFn = fn
	}
}

func WithHTTPClient(c *http.Client) Option {
	return func(s *Store) {
		s.httpClient = c
	}
}

func WithOfflineMode(b bool) Option {
	return func(s *Store) {
		s.offline = b
	}
}

// WithTrustedKey impose la vérification de la signature minisign du manifest
// avec la clé publique fournie. Surcharge la clé embarquée (DefaultTrustedKey).
func WithTrustedKey(pk *PublicKey) Option {
	return func(s *Store) {
		s.trustedKey = pk
	}
}

// WithInsecureSkipVerify désactive la vérification de signature du manifest.
// Réservé aux manifests custom non signés ; émet un avertissement au runtime.
func WithInsecureSkipVerify(b bool) Option {
	return func(s *Store) {
		s.skipVerify = b
	}
}

func New(opts ...Option) (*Store, error) {
	s := &Store{
		manifestTTL: DefaultManifestTTL,
		httpClient: &http.Client{
			Timeout: DefaultDownloadTimeout,
		},
		trustedKey: DefaultTrustedKey(),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.cacheDir == "" {
		dir, err := cacheDir()
		if err != nil {
			return nil, err
		}
		s.cacheDir = dir
	}

	if s.discoverer == nil {
		url := s.manifestURL
		if url == "" {
			url = DefaultManifestURL
		}
		disc := NewStaticDiscoverer(url)
		disc.HTTPClient = s.httpClient
		disc.TrustedKey = s.trustedKey
		disc.SkipVerify = s.skipVerify
		s.discoverer = disc
	}

	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return nil, err
	}

	log.Printf("modelstore: cache directory: %s", s.cacheDir)

	return s, nil
}

func (s *Store) Get(ctx context.Context, lang string) (string, error) {
	if err := validateLang(lang); err != nil {
		return "", err
	}

	s.mu.RLock()
	manifest := s.manifest
	s.mu.RUnlock()

	if manifest == nil {
		if err := s.ensureManifest(ctx); err != nil {
			return "", err
		}
		s.mu.RLock()
		manifest = s.manifest
		s.mu.RUnlock()
	}

	entry, ok := manifest.Models[lang]
	if !ok {
		return "", ErrLanguageNotFound
	}

	if isModelCached(s.cacheDir, lang) {
		valid, err := validateCachedSHA(s.cacheDir, lang, entry.SHA256)
		if err == nil && valid {
			return modelCachePath(s.cacheDir, lang), nil
		}
	}

	if s.offline {
		return "", ErrOffline
	}

	lockPath := filepath.Join(s.cacheDir, ".lock-"+lang)
	lock, err := acquireLock(lockPath)
	if err != nil {
		return "", err
	}
	defer lock.Release()

	if isModelCached(s.cacheDir, lang) {
		valid, err := validateCachedSHA(s.cacheDir, lang, entry.SHA256)
		if err == nil && valid {
			return modelCachePath(s.cacheDir, lang), nil
		}
	}

	return downloadModel(ctx, s.httpClient, entry.URL, entry.SHA256, s.cacheDir, lang, s.progressFn)
}

func (s *Store) GetAll(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	manifest := s.manifest
	s.mu.RUnlock()

	if manifest == nil {
		if err := s.ensureManifest(ctx); err != nil {
			return nil, err
		}
		s.mu.RLock()
		manifest = s.manifest
		s.mu.RUnlock()
	}

	result := make(map[string]string)
	for lang := range manifest.Models {
		path, err := s.Get(ctx, lang)
		if err != nil {
			return result, err
		}
		result[lang] = path
	}
	return result, nil
}

func (s *Store) Refresh(ctx context.Context) error {
	if s.offline {
		return ErrOffline
	}

	m, source, err := s.discoverer.Discover(ctx)
	if err != nil {
		return err
	}

	if err := saveManifest(s.cacheDir, m, source); err != nil {
		return err
	}

	s.mu.Lock()
	s.manifest = m
	s.manifestMeta = &manifestMeta{
		FetchedAt: time.Now().UTC(),
		Source:    source,
	}
	s.mu.Unlock()

	return nil
}

func (s *Store) Available(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	manifest := s.manifest
	s.mu.RUnlock()

	if manifest == nil {
		if err := s.ensureManifest(ctx); err != nil {
			return nil, err
		}
		s.mu.RLock()
		manifest = s.manifest
		s.mu.RUnlock()
	}

	langs := make([]string, 0, len(manifest.Models))
	for lang := range manifest.Models {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	return langs, nil
}

func (s *Store) IsCached(lang string) bool {
	s.mu.RLock()
	manifest := s.manifest
	s.mu.RUnlock()

	if manifest == nil {
		return false
	}

	entry, ok := manifest.Models[lang]
	if !ok {
		return false
	}

	if !isModelCached(s.cacheDir, lang) {
		return false
	}

	valid, _ := validateCachedSHA(s.cacheDir, lang, entry.SHA256)
	return valid
}

func (s *Store) GetGazetteers(ctx context.Context, lang string) (map[string]string, error) {
	if err := s.loadManifestIfNeeded(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	gazetteers := s.manifest.Gazetteers
	s.mu.RUnlock()

	if len(gazetteers) == 0 {
		return map[string]string{}, nil
	}

	candidates := make(map[string]GazetteerEntry)
	for _, entry := range gazetteers {
		if !stringSliceContains(entry.Languages, lang) {
			continue
		}
		existing, ok := candidates[entry.Type]
		if !ok || betterGazetteer(entry, existing) {
			candidates[entry.Type] = entry
		}
	}

	if len(candidates) == 0 {
		return map[string]string{}, nil
	}

	result := make(map[string]string, len(candidates))
	for gtype, entry := range candidates {
		cachePath := gazetteerCachePath(s.cacheDir, gtype)
		if isFileCached(cachePath) {
			valid, err := validateCachedSHAFromPath(cachePath, entry.SHA256)
			if err == nil && valid {
				result[gtype] = cachePath
				continue
			}
		}

		if s.offline {
			return nil, ErrOffline
		}

		path, err := downloadModel(ctx, s.httpClient, entry.URL, entry.SHA256, s.cacheDir, "gaz-"+gtype, s.progressFn)
		if err != nil {
			return nil, fmt.Errorf("download gazetteer %q: %w", gtype, err)
		}

		if err := os.Rename(path, cachePath); err != nil {
			return nil, fmt.Errorf("rename gazetteer: %w", err)
		}

		result[gtype] = cachePath
	}

	return result, nil
}

func (s *Store) loadManifestIfNeeded(ctx context.Context) error {
	s.mu.RLock()
	manifest := s.manifest
	s.mu.RUnlock()

	if manifest != nil {
		return nil
	}
	return s.ensureManifest(ctx)
}

func stringSliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func (s *Store) ensureManifest(ctx context.Context) error {
	if s.offline {
		m, meta, err := loadManifest(s.cacheDir)
		if err != nil {
			return ErrManifestExpired
		}
		s.mu.Lock()
		s.manifest = m
		s.manifestMeta = meta
		s.mu.Unlock()
		return nil
	}

	if isManifestFresh(s.cacheDir, s.manifestTTL) {
		m, meta, err := loadManifest(s.cacheDir)
		if err == nil {
			s.mu.Lock()
			s.manifest = m
			s.manifestMeta = meta
			s.mu.Unlock()
			return nil
		}
	}

	return s.Refresh(ctx)
}

// GetClusters retourne le chemin local du fichier de Brown clusters pour lang,
// en le téléchargeant au besoin. Chaîne vide si le manifeste n'en publie pas
// pour cette langue.
//
// Le manque de clusters n'est pas une erreur : les modèles publiés avant leur
// distribution restent utilisables, simplement sans cette feature. C'est à
// l'appelant de relayer l'avertissement que Recognizer.Warnings() produit
// lorsqu'un modèle entraîné avec des clusters n'en reçoit aucun.
func (s *Store) GetClusters(ctx context.Context, lang string) (string, error) {
	if err := validateLang(lang); err != nil {
		return "", err
	}
	if err := s.loadManifestIfNeeded(ctx); err != nil {
		return "", err
	}

	s.mu.RLock()
	entry, ok := s.manifest.Clusters[lang]
	s.mu.RUnlock()
	if !ok {
		return "", nil
	}

	cachePath := clusterCachePath(s.cacheDir, lang)
	if isFileCached(cachePath) {
		valid, err := validateCachedSHAFromPath(cachePath, entry.SHA256)
		if err == nil && valid {
			return cachePath, nil
		}
	}

	if s.offline {
		return "", ErrOffline
	}

	path, err := downloadModel(ctx, s.httpClient, entry.URL, entry.SHA256, s.cacheDir,
		"clusters-"+lang, s.progressFn)
	if err != nil {
		return "", fmt.Errorf("download clusters %q: %w", lang, err)
	}
	if err := os.Rename(path, cachePath); err != nil {
		return "", fmt.Errorf("rename clusters: %w", err)
	}

	return cachePath, nil
}

// betterGazetteer départage deux listes du même type pour une même langue.
//
// **La spécificité prime sur la taille.** Le volume est un mauvais indicateur de
// qualité : une liste multilingue est souvent un export brut — plus grosse,
// mais en majuscules, sans accents, parfois avec ses colonnes de comptage — là
// où une liste dédiée à une langue a été normalisée pour elle. Trancher sur la
// taille faisait ainsi préférer un dump INSEE de 209 000 prénoms non accentués
// à la liste française curée de 48 000 entrées, dans laquelle « hervé » et
// « coline » figurent bel et bien.
//
// À spécificité égale, la taille redevient un critère raisonnable : entre deux
// listes dédiées à la même langue, la plus fournie couvre davantage.
func betterGazetteer(candidate, current GazetteerEntry) bool {
	if len(candidate.Languages) != len(current.Languages) {
		return len(candidate.Languages) < len(current.Languages)
	}
	return candidate.SizeBytes > current.SizeBytes
}
