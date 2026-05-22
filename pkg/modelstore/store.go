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

func New(opts ...Option) (*Store, error) {
	s := &Store{
		manifestTTL: DefaultManifestTTL,
		httpClient: &http.Client{
			Timeout: DefaultDownloadTimeout,
		},
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
		s.discoverer = disc
	}

	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return nil, err
	}

	log.Printf("modelstore: cache directory: %s", s.cacheDir)

	return s, nil
}

func (s *Store) Get(ctx context.Context, lang string) (string, error) {
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
	for name, entry := range gazetteers {
		if !stringSliceContains(entry.Languages, lang) {
			continue
		}
		existing, ok := candidates[entry.Type]
		if !ok || entry.SizeBytes > existing.SizeBytes {
			candidates[entry.Type] = entry
			if ok {
				_ = name
			}
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
