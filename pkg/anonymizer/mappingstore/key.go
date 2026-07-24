package mappingstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Variables d'environnement de la clé de chiffrement du store.
//
// Jamais de flag CLI : les arguments de processus sont lisibles dans `ps`, dans
// l'historique du shell et dans les logs d'orchestrateur. KeyFileEnvVar existe
// pour les secrets montés en volume (Kubernetes, systemd credentials).
const (
	KeyEnvVar     = "GOANON_MAPPING_KEY"
	KeyFileEnvVar = "GOANON_MAPPING_KEY_FILE"
)

// KeyLen est la taille imposée de la clé : AES-256.
const KeyLen = 32

// Key est la clé de chiffrement d'un compartiment de mappings.
//
// Détruire une Key rend illisibles tous les mappings de son compartiment : c'est
// le « crypto-shredding », la forme d'effacement la plus solide à opposer à un
// DPO, puisqu'elle ne dépend pas de l'écrasement physique du support.
type Key []byte

var (
	ErrKeyNotSet   = fmt.Errorf("mappingstore: ni %s ni %s ne sont définies", KeyEnvVar, KeyFileEnvVar)
	ErrKeyLength   = fmt.Errorf("mappingstore: la clé doit faire exactement %d octets", KeyLen)
	ErrKeyFormat   = errors.New("mappingstore: format de clé invalide (hexadécimal ou base64 attendu)")
	ErrKeyMismatch = errors.New("mappingstore: le fichier a été chiffré avec une autre clé")
)

// Validate vérifie la longueur de la clé.
func (k Key) Validate() error {
	if len(k) != KeyLen {
		return ErrKeyLength
	}
	return nil
}

// ID dérive un identifiant public de 4 octets à partir de la clé. Il est écrit
// en clair dans l'en-tête des fichiers : il permet de dire « ce fichier n'est
// pas de votre compartiment » sans tenter le déchiffrement, et ne révèle rien de
// la clé (pré-image d'un SHA-256 tronqué).
func (k Key) ID() uint32 {
	h := sha256.New()
	h.Write([]byte("goanon-mapping-key-id"))
	h.Write([]byte{0x00})
	h.Write(k)
	return binary.BigEndian.Uint32(h.Sum(nil)[:4])
}

// ParseKey décode une clé depuis sa représentation textuelle (hexadécimal ou
// base64, avec ou sans padding).
func ParseKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrKeyFormat
	}

	decoders := []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	for _, decode := range decoders {
		raw, err := decode(s)
		if err != nil || len(raw) == 0 {
			continue
		}
		key := Key(raw)
		if err := key.Validate(); err != nil {
			return nil, err
		}
		return key, nil
	}
	return nil, ErrKeyFormat
}

// KeyFromEnv charge la clé depuis GOANON_MAPPING_KEY, ou depuis le fichier
// pointé par GOANON_MAPPING_KEY_FILE. Retourne ErrKeyNotSet si aucune n'est
// définie — l'appelant décide alors d'échouer (recommandé) ou de basculer
// explicitement en clair.
func KeyFromEnv() (Key, error) {
	if raw, ok := os.LookupEnv(KeyEnvVar); ok && strings.TrimSpace(raw) != "" {
		return ParseKey(raw)
	}

	path, ok := os.LookupEnv(KeyFileEnvVar)
	if !ok || strings.TrimSpace(path) == "" {
		return nil, ErrKeyNotSet
	}

	content, err := os.ReadFile(path)
	if err != nil {
		// Le chemin est fourni par l'opérateur, pas par une donnée utilisateur :
		// le citer aide au diagnostic sans exposer de PII.
		return nil, fmt.Errorf("mappingstore: lecture de %s (%s) : %w", KeyFileEnvVar, path, err)
	}
	return ParseKey(string(content))
}
