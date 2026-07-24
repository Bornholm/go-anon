package mappingstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// Format du fichier de mapping chiffré (tous les entiers en big-endian) :
//
//	 0  10  magic "GOANONMAP1"
//	10   1  version du format
//	11   4  identifiant de clé (Key.ID)
//	15   8  date de création, secondes Unix
//	23   8  date d'expiration, secondes Unix (0 = pas d'expiration)
//	31  12  nonce GCM
//	43   n  ciphertext || tag GCM
//
// Les 31 premiers octets sont authentifiés en AAD, concaténés à l'identifiant du
// mapping : un fichier ne peut donc pas être rejoué sous un autre identifiant,
// ni voir sa date d'expiration reculée sans invalider le tag.
const (
	magic      = "GOANONMAP1"
	formatV1   = 1
	aadLen     = 31
	nonceLen   = 12
	headerLen  = aadLen + nonceLen
	keyIDOff   = 10 + 1
	createdOff = keyIDOff + 4
	expiresOff = createdOff + 8
)

var (
	ErrBadFormat = errors.New("mappingstore: format de fichier invalide")
	// ErrDecrypt couvre indistinctement mauvaise clé, altération et rejeu sous
	// un autre identifiant : GCM ne permet pas — et ne doit pas permettre — de
	// les distinguer.
	ErrDecrypt = errors.New("mappingstore: déchiffrement impossible (clé erronée ou fichier altéré)")
)

// Metadata porte les informations lisibles sans déchiffrement.
type Metadata struct {
	Version   byte
	KeyID     uint32
	CreatedAt time.Time
	ExpiresAt time.Time // zéro = pas d'expiration
}

// Seal chiffre l'état d'une session en AES-256-GCM.
//
// id entre dans les données authentifiées : le blob scellé n'est déchiffrable
// que sous cet identifiant.
func Seal(key Key, id string, state anonymizer.SessionState, createdAt, expiresAt time.Time) ([]byte, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}

	plaintext, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("mappingstore: sérialisation de la session : %w", err)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	out := make([]byte, headerLen, headerLen+len(plaintext)+aead.Overhead())
	copy(out, magic)
	out[10] = formatV1
	binary.BigEndian.PutUint32(out[keyIDOff:], key.ID())
	binary.BigEndian.PutUint64(out[createdOff:], uint64(createdAt.UTC().Unix()))
	binary.BigEndian.PutUint64(out[expiresOff:], unixOrZero(expiresAt))

	nonce := out[aadLen:headerLen]
	// crypto/rand.Read panique si l'entropie système est indisponible ; il ne
	// retourne jamais d'erreur depuis Go 1.24.
	_, _ = rand.Read(nonce)

	return aead.Seal(out, nonce, plaintext, aad(out[:aadLen], id)), nil
}

// Open déchiffre un blob produit par Seal et retourne l'état de session ainsi
// que ses métadonnées.
func Open(key Key, id string, blob []byte) (anonymizer.SessionState, Metadata, error) {
	var state anonymizer.SessionState

	hdr, err := parseHeader(blob)
	if err != nil {
		return state, hdr, err
	}
	if hdr.KeyID != key.ID() {
		return state, hdr, ErrKeyMismatch
	}

	aead, err := newAEAD(key)
	if err != nil {
		return state, hdr, err
	}

	plaintext, err := aead.Open(nil, blob[aadLen:headerLen], blob[headerLen:], aad(blob[:aadLen], id))
	if err != nil {
		// L'erreur GCM sous-jacente n'apporte rien et pourrait varier selon
		// l'implémentation : on retourne une sentinelle unique.
		return state, hdr, ErrDecrypt
	}

	if err := json.Unmarshal(plaintext, &state); err != nil {
		return state, hdr, fmt.Errorf("mappingstore: état de session illisible : %w", err)
	}
	return state, hdr, nil
}

func parseHeader(blob []byte) (Metadata, error) {
	var hdr Metadata
	if len(blob) < headerLen || string(blob[:10]) != magic {
		return hdr, ErrBadFormat
	}
	hdr.Version = blob[10]
	if hdr.Version != formatV1 {
		return hdr, fmt.Errorf("%w : version %d non supportée", ErrBadFormat, hdr.Version)
	}
	hdr.KeyID = binary.BigEndian.Uint32(blob[keyIDOff:])
	hdr.CreatedAt = time.Unix(int64(binary.BigEndian.Uint64(blob[createdOff:])), 0).UTC()
	if exp := binary.BigEndian.Uint64(blob[expiresOff:]); exp != 0 {
		hdr.ExpiresAt = time.Unix(int64(exp), 0).UTC()
	}
	return hdr, nil
}

func newAEAD(key Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("mappingstore: initialisation AES : %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("mappingstore: initialisation GCM : %w", err)
	}
	return aead, nil
}

// aad lie l'en-tête et l'identifiant du mapping. Le séparateur nul empêche
// qu'un en-tête tronqué et un id rallongé produisent la même chaîne.
func aad(head []byte, id string) []byte {
	out := make([]byte, 0, len(head)+1+len(id))
	out = append(out, head...)
	out = append(out, 0x00)
	return append(out, id...)
}

func unixOrZero(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UTC().Unix())
}
