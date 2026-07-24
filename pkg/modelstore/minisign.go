package modelstore

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Vérification de signatures au format minisign (https://jedisct1.github.io/minisign/).
//
// minisign a été retenu (plutôt que sigstore/cosign) parce qu'il s'embarque en
// pur Go : une clé publique Ed25519 compilée dans le binaire, aucune dépendance
// externe, aucun réseau à la vérification. La chaîne de confiance devient :
//
//	signature (Ed25519, clé embarquée) → manifest → SHA-256 → modèle
//
// Sans cette signature, un attaquant qui contrôle le dépôt de releases fournit à
// la fois un modèle malveillant ET son hash : le SHA-256 ne protège que
// l'intégrité du transfert, pas l'authenticité de la source.

var (
	// ErrSignatureInvalid : la signature ne correspond pas au contenu/clé.
	ErrSignatureInvalid = errors.New("modelstore: signature invalide")
	// ErrSignatureKeyMismatch : la signature vise un autre keyID que la clé de confiance.
	ErrSignatureKeyMismatch = errors.New("modelstore: keyID de signature inconnu")
	// ErrSignatureFormat : fichier de clé ou de signature mal formé.
	ErrSignatureFormat = errors.New("modelstore: format de signature invalide")
)

const (
	sigAlgHashedED = "ED" // Ed25519 sur BLAKE2b-512(contenu) — défaut minisign moderne
	sigAlgLegacy   = "Ed" // Ed25519 sur le contenu brut (ancien format)
)

// PublicKey est une clé publique minisign de confiance (Ed25519 + keyID).
type PublicKey struct {
	keyID [8]byte
	key   ed25519.PublicKey
}

// ParsePublicKey décode une clé publique minisign. L'entrée accepte soit le
// fichier .pub complet (ligne de commentaire + ligne base64), soit uniquement la
// ligne base64. Le blob décodé fait 42 octets : 2 (algorithme) + 8 (keyID) + 32
// (clé Ed25519).
func ParsePublicKey(s string) (*PublicKey, error) {
	line := lastNonCommentLine(s)
	if line == "" {
		return nil, fmt.Errorf("%w: clé publique vide", ErrSignatureFormat)
	}

	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 clé publique: %v", ErrSignatureFormat, err)
	}
	if len(raw) != 2+8+ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: clé publique de %d octets, attendu %d", ErrSignatureFormat, len(raw), 2+8+ed25519.PublicKeySize)
	}
	if string(raw[:2]) != sigAlgLegacy {
		return nil, fmt.Errorf("%w: algorithme de clé %q non géré", ErrSignatureFormat, raw[:2])
	}

	pk := &PublicKey{key: ed25519.PublicKey(bytes.Clone(raw[10:]))}
	copy(pk.keyID[:], raw[2:10])
	return pk, nil
}

// signature est une signature minisign décodée.
type signature struct {
	algorithm      string
	keyID          [8]byte
	sig            []byte // 64 octets Ed25519
	trustedComment string
	globalSig      []byte // 64 octets Ed25519 sur sig||trustedComment
}

// parseSignature décode un fichier .minisig (4 lignes : commentaire, signature
// base64, "trusted comment: …", signature globale base64).
func parseSignature(data []byte) (*signature, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	// Ignorer les lignes vides de fin.
	var kept []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) < 4 {
		return nil, fmt.Errorf("%w: fichier de signature tronqué (%d lignes)", ErrSignatureFormat, len(kept))
	}

	rawSig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kept[1]))
	if err != nil {
		return nil, fmt.Errorf("%w: base64 signature: %v", ErrSignatureFormat, err)
	}
	if len(rawSig) != 2+8+ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature de %d octets, attendu %d", ErrSignatureFormat, len(rawSig), 2+8+ed25519.SignatureSize)
	}

	const tcPrefix = "trusted comment:"
	if !strings.HasPrefix(kept[2], tcPrefix) {
		return nil, fmt.Errorf("%w: ligne trusted comment manquante", ErrSignatureFormat)
	}
	trusted := strings.TrimSpace(strings.TrimPrefix(kept[2], tcPrefix))

	globalSig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kept[3]))
	if err != nil {
		return nil, fmt.Errorf("%w: base64 signature globale: %v", ErrSignatureFormat, err)
	}
	if len(globalSig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature globale de %d octets, attendu %d", ErrSignatureFormat, len(globalSig), ed25519.SignatureSize)
	}

	s := &signature{
		algorithm:      string(rawSig[:2]),
		sig:            bytes.Clone(rawSig[10:]),
		trustedComment: trusted,
		globalSig:      globalSig,
	}
	copy(s.keyID[:], rawSig[2:10])
	return s, nil
}

// Verify vérifie que sigData signe message avec cette clé publique. Contrôle :
//  1. le keyID de la signature correspond à la clé de confiance ;
//  2. la signature Ed25519 est valide sur le message (ou son hash BLAKE2b-512
//     en mode préhaché « ED ») ;
//  3. la signature globale couvre sig||trustedComment (intégrité du commentaire
//     de confiance, qui porte en pratique la version et la date de release).
func (pk *PublicKey) Verify(message, sigData []byte) error {
	s, err := parseSignature(sigData)
	if err != nil {
		return err
	}

	if s.keyID != pk.keyID {
		return fmt.Errorf("%w: attendu %x, obtenu %x", ErrSignatureKeyMismatch, pk.keyID, s.keyID)
	}

	var signed []byte
	switch s.algorithm {
	case sigAlgHashedED:
		h := blake2b.Sum512(message)
		signed = h[:]
	case sigAlgLegacy:
		signed = message
	default:
		return fmt.Errorf("%w: algorithme de signature %q non géré", ErrSignatureFormat, s.algorithm)
	}

	if !ed25519.Verify(pk.key, signed, s.sig) {
		return ErrSignatureInvalid
	}

	// Signature globale : Ed25519 sur (signature || trusted_comment).
	global := append(bytes.Clone(s.sig), []byte(s.trustedComment)...)
	if !ed25519.Verify(pk.key, global, s.globalSig) {
		return fmt.Errorf("%w: signature globale (trusted comment altéré)", ErrSignatureInvalid)
	}

	return nil
}

// lastNonCommentLine retourne la dernière ligne non vide qui n'est pas un
// commentaire minisign ("untrusted comment: …"). Permet d'accepter aussi bien un
// fichier .pub complet que la seule ligne base64.
func lastNonCommentLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "untrusted comment:") || strings.HasPrefix(l, "trusted comment:") {
			continue
		}
		return l
	}
	return ""
}
