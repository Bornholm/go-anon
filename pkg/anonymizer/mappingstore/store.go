// Package mappingstore conserve les tables de ré-identification produites par
// l'anonymiseur.
//
// Un mapping est l'inverse exact de la pseudonymisation : c'est une donnée
// personnelle au sens du RGPD, et l'actif le plus sensible du système. Ce
// package le stocke chiffré (AES-256-GCM), avec une durée de rétention, et
// fournit l'effacement de l'art. 17 sous sa forme la plus défendable — la
// destruction de la clé du compartiment.
package mappingstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// Entry décrit un mapping stocké, sans rien révéler de son contenu.
type Entry struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time // zéro = pas d'expiration
	KeyID     uint32
}

// Expired indique si l'entrée a dépassé sa date de rétention.
func (e Entry) Expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt)
}

// Store conserve les sessions d'anonymisation par identifiant.
type Store interface {
	Save(ctx context.Context, id string, s *anonymizer.Session) error
	Load(ctx context.Context, id string) (*anonymizer.Session, error)
	// Delete réalise l'effacement au sens de l'art. 17 : sans table de
	// correspondance, la sortie pseudonymisée devient anonyme de facto.
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]Entry, error)
}

var (
	ErrNotFound  = errors.New("mappingstore: mapping introuvable")
	ErrInvalidID = errors.New("mappingstore: identifiant de mapping invalide")
)

// validID borne les identifiants aux caractères sûrs pour un nom de fichier.
// Toute traversée de chemin est structurellement impossible.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const fileExt = ".goanonmap"

// CheckID valide un identifiant de mapping.
func CheckID(id string) error {
	if !validID.MatchString(id) || strings.Contains(id, "..") {
		return fmt.Errorf("%w : %q", ErrInvalidID, id)
	}
	return nil
}

// FileStore stocke un mapping chiffré par fichier, dans un répertoire dédié.
type FileStore struct {
	dir string
	key Key
	ttl time.Duration
	now func() time.Time
}

// FileStoreOption configure un FileStore.
type FileStoreOption func(*FileStore)

// WithTTL fixe la durée de rétention appliquée aux mappings enregistrés.
// Zéro (défaut) = pas d'expiration ; à réserver aux cas justifiés, la rétention
// bornée étant le régime attendu par l'art. 5(1)(e).
func WithTTL(d time.Duration) FileStoreOption {
	return func(s *FileStore) { s.ttl = d }
}

// withClock injecte une horloge de test.
func withClock(now func() time.Time) FileStoreOption {
	return func(s *FileStore) { s.now = now }
}

// NewFileStore ouvre (et crée au besoin) un répertoire de stockage en 0700.
func NewFileStore(dir string, key Key, opts ...FileStoreOption) (*FileStore, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	// 0700 : le répertoire ne doit être lisible que par le compte de service.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mappingstore: création de %s : %w", dir, err)
	}

	s := &FileStore{dir: dir, key: key, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

var _ Store = (*FileStore)(nil)

func (s *FileStore) path(id string) (string, error) {
	if err := CheckID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, id+fileExt), nil
}

// Save chiffre et écrit l'état de la session. L'écriture est atomique : un
// fichier temporaire du même répertoire est renommé une fois complet, ce qui
// interdit qu'une interruption laisse un mapping tronqué mais déchiffrable.
func (s *FileStore) Save(ctx context.Context, id string, session *anonymizer.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(id)
	if err != nil {
		return err
	}

	created := s.now().UTC()
	var expires time.Time
	if s.ttl > 0 {
		expires = created.Add(s.ttl)
	}

	blob, err := Seal(s.key, id, session.State(), created, expires)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, blob)
}

// Load déchiffre le mapping et reconstruit la session.
func (s *FileStore) Load(ctx context.Context, id string) (*anonymizer.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}

	blob, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w : %q", ErrNotFound, id)
		}
		return nil, fmt.Errorf("mappingstore: lecture de %q : %w", id, err)
	}

	state, _, err := Open(s.key, id, blob)
	if err != nil {
		return nil, err
	}
	return anonymizer.NewSessionFromState(state)
}

// Delete supprime le mapping.
//
// L'écrasement préalable du fichier est fait au mieux, mais n'est **pas** une
// garantie : sur SSD (wear leveling) comme sur les systèmes de fichiers
// copy-on-write (btrfs, ZFS, APFS), les blocs d'origine peuvent survivre. La
// garantie réelle d'effacement est cryptographique : détruire la clé du
// compartiment rend illisibles tous ses mappings, où qu'en soient les octets.
func (s *FileStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(id)
	if err != nil {
		return err
	}

	overwriteBestEffort(path)

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w : %q", ErrNotFound, id)
		}
		return fmt.Errorf("mappingstore: suppression de %q : %w", id, err)
	}
	return nil
}

// List énumère les mappings présents. Les métadonnées sont lues dans l'en-tête
// en clair : aucun déchiffrement, donc aucune exposition de PII.
func (s *FileStore) List(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("mappingstore: lecture de %s : %w", s.dir, err)
	}

	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), fileExt) {
			continue
		}
		id := strings.TrimSuffix(de.Name(), fileExt)
		if CheckID(id) != nil {
			continue
		}

		head, err := readHeader(filepath.Join(s.dir, de.Name()))
		if err != nil {
			// Un fichier illisible ou étranger au format ne doit pas rendre
			// l'inventaire impossible : il est ignoré, pas fatal.
			continue
		}
		entries = append(entries, Entry{
			ID:        id,
			CreatedAt: head.CreatedAt,
			ExpiresAt: head.ExpiresAt,
			KeyID:     head.KeyID,
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// PurgeExpired supprime les mappings dont la date de rétention est dépassée et
// retourne le nombre d'entrées effacées.
func (s *FileStore) PurgeExpired(ctx context.Context) (int, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return 0, err
	}

	now := s.now().UTC()
	purged := 0
	for _, e := range entries {
		if !e.Expired(now) {
			continue
		}
		if err := s.Delete(ctx, e.ID); err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}

// readHeader ne lit que l'en-tête en clair du fichier.
func readHeader(path string) (Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer f.Close()

	buf := make([]byte, headerLen)
	if _, err := io.ReadFull(f, buf); err != nil {
		return Metadata{}, ErrBadFormat
	}
	return parseHeader(buf)
}

// writeFileAtomic écrit via un temporaire du même répertoire, en 0600 dès la
// création : le contenu n'est jamais exposé avec des permissions plus larges,
// même transitoirement.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-mapping-*")
	if err != nil {
		return fmt.Errorf("mappingstore: fichier temporaire : %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	// CreateTemp crée déjà en 0600 ; le Chmod explicite couvre les umask
	// exotiques et documente l'intention.
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrInvalid) {
		cleanup()
		return fmt.Errorf("mappingstore: permissions du temporaire : %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("mappingstore: écriture : %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("mappingstore: synchronisation : %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("mappingstore: fermeture : %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("mappingstore: publication : %w", err)
	}
	return nil
}

// overwriteBestEffort écrase le contenu du fichier avant suppression. Voir la
// mise en garde de Delete : c'est une mesure de défense en profondeur, pas une
// garantie d'effacement physique.
func overwriteBestEffort(path string) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	zeros := make([]byte, 4096)
	for remaining := info.Size(); remaining > 0; {
		n := int64(len(zeros))
		if remaining < n {
			n = remaining
		}
		if _, err := f.Write(zeros[:n]); err != nil {
			return
		}
		remaining -= n
	}
	_ = f.Sync()
}
