package modelstore

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

// testKey génère une clé de test et son PublicKey de confiance (keyID fixe).
func testKey(t *testing.T) (ed25519.PrivateKey, *PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("génération clé: %v", err)
	}
	pk := &PublicKey{key: pub}
	copy(pk.keyID[:], []byte("KEYID_01"))
	return priv, pk
}

// makeSig fabrique un fichier .minisig valide pour message avec priv/keyID.
// prehashed choisit l'algorithme "ED" (BLAKE2b-512) ou "Ed" (message brut).
func makeSig(priv ed25519.PrivateKey, keyID [8]byte, message []byte, prehashed bool, trusted string) []byte {
	var algo string
	var signed []byte
	if prehashed {
		algo = sigAlgHashedED
		h := blake2b.Sum512(message)
		signed = h[:]
	} else {
		algo = sigAlgLegacy
		signed = message
	}

	sig := ed25519.Sign(priv, signed)

	blob := make([]byte, 0, 2+8+len(sig))
	blob = append(blob, algo...)
	blob = append(blob, keyID[:]...)
	blob = append(blob, sig...)

	global := ed25519.Sign(priv, append(append([]byte{}, sig...), []byte(trusted)...))

	return fmt.Appendf(nil,
		"untrusted comment: signature\n%s\ntrusted comment: %s\n%s\n",
		base64.StdEncoding.EncodeToString(blob),
		trusted,
		base64.StdEncoding.EncodeToString(global),
	)
}

func TestMinisignVerify_ValidPrehashed(t *testing.T) {
	priv, pk := testKey(t)
	msg := []byte(`{"schema_version":1}`)
	sig := makeSig(priv, pk.keyID, msg, true, "release v1.2.3")

	if err := pk.Verify(msg, sig); err != nil {
		t.Fatalf("signature valide rejetée: %v", err)
	}
}

func TestMinisignVerify_ValidLegacy(t *testing.T) {
	priv, pk := testKey(t)
	msg := []byte("contenu brut")
	sig := makeSig(priv, pk.keyID, msg, false, "legacy")

	if err := pk.Verify(msg, sig); err != nil {
		t.Fatalf("signature legacy valide rejetée: %v", err)
	}
}

func TestMinisignVerify_TamperedMessage(t *testing.T) {
	priv, pk := testKey(t)
	msg := []byte("original")
	sig := makeSig(priv, pk.keyID, msg, true, "tc")

	if err := pk.Verify([]byte("altéré"), sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("attendu ErrSignatureInvalid sur message altéré, got %v", err)
	}
}

func TestMinisignVerify_TamperedTrustedComment(t *testing.T) {
	priv, pk := testKey(t)
	msg := []byte("m")
	sig := makeSig(priv, pk.keyID, msg, true, "bon commentaire")

	// Remplacer le trusted comment sans re-signer la signature globale.
	altered := strings.Replace(string(sig), "bon commentaire", "commentaire pirate", 1)
	if err := pk.Verify(msg, []byte(altered)); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("attendu ErrSignatureInvalid sur trusted comment altéré, got %v", err)
	}
}

func TestMinisignVerify_WrongKey(t *testing.T) {
	priv, _ := testKey(t)
	_, otherPub := testKey(t) // clé de confiance différente, même keyID
	msg := []byte("m")
	sig := makeSig(priv, otherPub.keyID, msg, true, "tc")

	if err := otherPub.Verify(msg, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("attendu ErrSignatureInvalid avec mauvaise clé, got %v", err)
	}
}

func TestMinisignVerify_WrongKeyID(t *testing.T) {
	priv, pk := testKey(t)
	var otherID [8]byte
	copy(otherID[:], []byte("KEYID_99"))
	msg := []byte("m")
	sig := makeSig(priv, otherID, msg, true, "tc")

	if err := pk.Verify(msg, sig); !errors.Is(err, ErrSignatureKeyMismatch) {
		t.Fatalf("attendu ErrSignatureKeyMismatch, got %v", err)
	}
}

func TestMinisignVerify_MalformedSignature(t *testing.T) {
	_, pk := testKey(t)
	if err := pk.Verify([]byte("m"), []byte("pas un fichier minisig")); !errors.Is(err, ErrSignatureFormat) {
		t.Fatalf("attendu ErrSignatureFormat, got %v", err)
	}
}

func TestParsePublicKey_RoundTrip(t *testing.T) {
	_, pk := testKey(t)

	// Reconstituer une ligne base64 de clé publique et la reparser.
	blob := make([]byte, 0, 42)
	blob = append(blob, sigAlgLegacy...)
	blob = append(blob, pk.keyID[:]...)
	blob = append(blob, pk.key...)
	line := "untrusted comment: minisign public key\n" + base64.StdEncoding.EncodeToString(blob)

	parsed, err := ParsePublicKey(line)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	if parsed.keyID != pk.keyID {
		t.Errorf("keyID: got %x, want %x", parsed.keyID, pk.keyID)
	}
	if !parsed.key.Equal(pk.key) {
		t.Error("clé publique reparsée différente")
	}
}

func TestParsePublicKey_Malformed(t *testing.T) {
	if _, err := ParsePublicKey("untrusted comment: vide\n"); !errors.Is(err, ErrSignatureFormat) {
		t.Fatalf("attendu ErrSignatureFormat sur clé vide, got %v", err)
	}
	if _, err := ParsePublicKey("!!!pas du base64!!!"); !errors.Is(err, ErrSignatureFormat) {
		t.Fatalf("attendu ErrSignatureFormat sur base64 invalide, got %v", err)
	}
}
