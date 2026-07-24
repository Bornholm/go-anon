package mappingstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/ner"
)

var (
	testKey  = Key(bytes.Repeat([]byte{0x2a}, KeyLen))
	otherKey = Key(bytes.Repeat([]byte{0x5b}, KeyLen))
)

// surnameProbe est la forme de surface recherchée dans les fichiers bruts : sa
// présence en clair prouverait que le chiffrement ne couvre pas le mapping.
const surnameProbe = "Jean Dupont"

func newTestSession(t *testing.T) *anonymizer.Session {
	t.Helper()

	rec := &fixedRecognizer{entities: []ner.Entity{
		{Text: surnameProbe, Type: ner.TypePER, Start: 0, End: len(surnameProbe), Confidence: 1.0},
	}}
	anon := anonymizer.New(rec, anonymizer.Config{Strategy: anonymizer.TagReplace, ConsistentMap: true})

	session := anonymizer.NewSession()
	if _, err := anon.Anonymize(surnameProbe+" habite à Paris.", anonymizer.WithSession(session)); err != nil {
		t.Fatalf("préparation de la session : %v", err)
	}
	if len(session.Mapping) == 0 {
		t.Fatal("session de test vide")
	}
	return session
}

type fixedRecognizer struct{ entities []ner.Entity }

func (r *fixedRecognizer) Recognize(string) ([]ner.Entity, error) { return r.entities, nil }

func TestFileStore_RoundTrip(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}

	ctx := context.Background()
	original := newTestSession(t)
	if err := store.Save(ctx, "dossier-42", original); err != nil {
		t.Fatalf("Save : %v", err)
	}

	loaded, err := store.Load(ctx, "dossier-42")
	if err != nil {
		t.Fatalf("Load : %v", err)
	}

	if loaded.Nonce() != original.Nonce() {
		t.Errorf("nonce perdu : %q ≠ %q", loaded.Nonce(), original.Nonce())
	}
	if len(loaded.Mapping) != len(original.Mapping) {
		t.Fatalf("mapping tronqué : %d ≠ %d", len(loaded.Mapping), len(original.Mapping))
	}
	for k, v := range original.Mapping {
		if loaded.Mapping[k] != v {
			t.Errorf("mapping[%q] = %q, attendu %q", k, loaded.Mapping[k], v)
		}
	}

	// Les compteurs doivent reprendre là où ils s'étaient arrêtés, sinon un
	// second document réattribuerait PERSON_1 à quelqu'un d'autre.
	if got := loaded.State().Counters[ner.TypePER]; got != original.State().Counters[ner.TypePER] {
		t.Errorf("compteur PER = %d, attendu %d", got, original.State().Counters[ner.TypePER])
	}
}

func TestGuarantee_StoreHoldsNoCleartextPII(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}

	session := newTestSession(t)
	if err := store.Save(context.Background(), "dossier-42", session); err != nil {
		t.Fatalf("Save : %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "dossier-42"+fileExt))
	if err != nil {
		t.Fatalf("lecture du fichier : %v", err)
	}

	// Toutes les formes de surface du mapping, plus les placeholders : rien de
	// tout cela ne doit apparaître en clair dans le fichier.
	for placeholder, surface := range session.Mapping {
		if bytes.Contains(raw, []byte(surface)) {
			t.Errorf("forme de surface en clair dans le fichier : %q", surface)
		}
		if bytes.Contains(raw, []byte(placeholder)) {
			t.Errorf("placeholder en clair dans le fichier : %q", placeholder)
		}
	}
	if bytes.Contains(raw, []byte(surnameProbe)) {
		t.Error("le nom de test apparaît en clair dans le fichier")
	}
}

func TestFileStore_WrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}
	if err := store.Save(context.Background(), "doc", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}

	wrong, err := NewFileStore(dir, otherKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}

	session, err := wrong.Load(context.Background(), "doc")
	if err == nil {
		t.Fatal("Load avec une mauvaise clé aurait dû échouer")
	}
	if session != nil {
		t.Fatal("aucune session ne doit être retournée en cas d'échec")
	}
	if !errors.Is(err, ErrKeyMismatch) && !errors.Is(err, ErrDecrypt) {
		t.Fatalf("erreur inattendue : %v", err)
	}
}

func TestFileStore_TamperedFileFails(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}
	if err := store.Save(context.Background(), "doc", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}

	path := filepath.Join(dir, "doc"+fileExt)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("lecture : %v", err)
	}
	raw[len(raw)-1] ^= 0xff // altération d'un octet du tag GCM
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("écriture : %v", err)
	}

	if _, err := store.Load(context.Background(), "doc"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("altération non détectée : %v", err)
	}
}

// L'identifiant entre dans les données authentifiées : renommer le fichier d'un
// mapping pour le faire passer pour un autre doit échouer.
func TestGuarantee_MappingCannotBeReplayedUnderAnotherID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}
	if err := store.Save(context.Background(), "dossier-a", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}

	if err := os.Rename(filepath.Join(dir, "dossier-a"+fileExt), filepath.Join(dir, "dossier-b"+fileExt)); err != nil {
		t.Fatalf("renommage : %v", err)
	}

	if _, err := store.Load(context.Background(), "dossier-b"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("rejeu sous un autre identifiant non détecté : %v", err)
	}
}

func TestFileStore_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("les permissions POSIX ne s'appliquent pas sous Windows")
	}

	dir := filepath.Join(t.TempDir(), "store")
	store, err := NewFileStore(dir, testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}
	if err := store.Save(context.Background(), "doc", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat du répertoire : %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("permissions du répertoire = %04o, attendu 0700", perm)
	}

	fileInfo, err := os.Stat(filepath.Join(dir, "doc"+fileExt))
	if err != nil {
		t.Fatalf("stat du fichier : %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions du fichier = %04o, attendu 0600", perm)
	}
}

func TestFileStore_DeleteThenLoad(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}

	ctx := context.Background()
	if err := store.Save(ctx, "doc", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}
	if err := store.Delete(ctx, "doc"); err != nil {
		t.Fatalf("Delete : %v", err)
	}
	if _, err := store.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
	if err := store.Delete(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete : attendu ErrNotFound, obtenu %v", err)
	}
}

func TestFileStore_ListAndPurgeExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	shortLived, err := NewFileStore(dir, testKey, WithTTL(time.Hour), withClock(clock))
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}
	permanent, err := NewFileStore(dir, testKey, withClock(clock))
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}

	ctx := context.Background()
	if err := shortLived.Save(ctx, "temporaire", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}
	if err := permanent.Save(ctx, "permanent", newTestSession(t)); err != nil {
		t.Fatalf("Save : %v", err)
	}

	entries, err := permanent.List(ctx)
	if err != nil {
		t.Fatalf("List : %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("attendu 2 entrées, obtenu %d", len(entries))
	}
	if entries[0].ID != "permanent" || !entries[0].ExpiresAt.IsZero() {
		t.Errorf("entrée permanente incorrecte : %+v", entries[0])
	}
	if entries[1].ID != "temporaire" || entries[1].ExpiresAt.IsZero() {
		t.Errorf("entrée temporaire incorrecte : %+v", entries[1])
	}
	if entries[0].KeyID != testKey.ID() {
		t.Errorf("KeyID = %d, attendu %d", entries[0].KeyID, testKey.ID())
	}

	// Avant expiration, rien ne doit être purgé.
	if n, err := shortLived.PurgeExpired(ctx); err != nil || n != 0 {
		t.Fatalf("purge prématurée : n=%d err=%v", n, err)
	}

	now = now.Add(2 * time.Hour)
	n, err := shortLived.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired : %v", err)
	}
	if n != 1 {
		t.Fatalf("attendu 1 purge, obtenu %d", n)
	}

	remaining, err := permanent.List(ctx)
	if err != nil {
		t.Fatalf("List : %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "permanent" {
		t.Fatalf("la purge a touché une entrée non expirée : %+v", remaining)
	}
}

func TestCheckID_RejectsPathTraversal(t *testing.T) {
	invalid := []string{
		"", ".", "..", "../secret", "a/b", `a\b`, "/etc/passwd", "doc..json",
		strings.Repeat("a", 129), "-leading-dash-ok?", "espace interdit",
	}
	for _, id := range invalid[:len(invalid)-2] {
		if err := CheckID(id); err == nil {
			t.Errorf("CheckID(%q) aurait dû échouer", id)
		}
	}
	for _, id := range []string{"doc", "dossier-42", "a.b_c-d", "A1"} {
		if err := CheckID(id); err != nil {
			t.Errorf("CheckID(%q) : %v", id, err)
		}
	}
}

func TestFileStore_RejectsInvalidID(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), testKey)
	if err != nil {
		t.Fatalf("NewFileStore : %v", err)
	}

	ctx := context.Background()
	if err := store.Save(ctx, "../evasion", newTestSession(t)); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Save : attendu ErrInvalidID, obtenu %v", err)
	}
	if _, err := store.Load(ctx, "../evasion"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Load : attendu ErrInvalidID, obtenu %v", err)
	}
}

func TestNewFileStore_RejectsShortKey(t *testing.T) {
	if _, err := NewFileStore(t.TempDir(), Key("trop courte")); !errors.Is(err, ErrKeyLength) {
		t.Fatalf("attendu ErrKeyLength, obtenu %v", err)
	}
}

func TestParseKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x11}, KeyLen)

	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "hex", input: strings.Repeat("11", KeyLen)},
		{name: "base64", input: "ERERERERERERERERERERERERERERERERERERERERERE="},
		{name: "trop courte", input: strings.Repeat("11", 16), wantErr: ErrKeyLength},
		{name: "vide", input: "", wantErr: ErrKeyFormat},
		{name: "illisible", input: "ceci n'est pas une clé", wantErr: ErrKeyFormat},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := ParseKey(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("attendu %v, obtenu %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseKey : %v", err)
			}
			if !bytes.Equal(key, raw) {
				t.Fatalf("clé décodée incorrecte : %x", key)
			}
		})
	}
}

func TestKeyFromEnv(t *testing.T) {
	t.Setenv(KeyEnvVar, "")
	t.Setenv(KeyFileEnvVar, "")
	if _, err := KeyFromEnv(); !errors.Is(err, ErrKeyNotSet) {
		t.Fatalf("attendu ErrKeyNotSet, obtenu %v", err)
	}

	t.Setenv(KeyEnvVar, strings.Repeat("ab", KeyLen))
	key, err := KeyFromEnv()
	if err != nil {
		t.Fatalf("KeyFromEnv : %v", err)
	}
	if err := key.Validate(); err != nil {
		t.Fatalf("clé invalide : %v", err)
	}

	// Le fichier prend le relais quand la variable directe est absente.
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(strings.Repeat("cd", KeyLen)+"\n"), 0o600); err != nil {
		t.Fatalf("écriture de la clé : %v", err)
	}
	t.Setenv(KeyEnvVar, "")
	t.Setenv(KeyFileEnvVar, path)
	if _, err := KeyFromEnv(); err != nil {
		t.Fatalf("KeyFromEnv depuis un fichier : %v", err)
	}
}

// Deux clés distinctes doivent produire des identifiants de compartiment
// distincts, sinon List ne pourrait pas discriminer les fichiers étrangers.
func TestKeyID_DiffersPerKey(t *testing.T) {
	if testKey.ID() == otherKey.ID() {
		t.Fatal("collision d'identifiants de clé")
	}
	if testKey.ID() != Key(bytes.Repeat([]byte{0x2a}, KeyLen)).ID() {
		t.Fatal("Key.ID n'est pas déterministe")
	}
}
