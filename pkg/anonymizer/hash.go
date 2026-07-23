package anonymizer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/bornholm/go-anon/pkg/ner"
)

// HashKey est la clé secrète (pepper) de la stratégie Hash.
//
// Un SHA-256 non salé sur des noms propres est cassable par dictionnaire en
// quelques secondes : l'espace des prénoms et patronymes est minuscule. L'EDPB
// (avis 05/2014, lignes directrices pseudonymisation 01/2025) et la CNIL
// considèrent le hachage sans clé comme une mesure insuffisante. La stratégie
// Hash dérive donc ses pseudonymes par HMAC-SHA-256.
type HashKey []byte

// MinHashKeyLen est la longueur minimale imposée à une HashKey.
const MinHashKeyLen = 32

// HashKeyEnvVar nomme la variable d'environnement portant la clé (hex ou base64).
// La clé ne doit jamais transiter par un flag CLI : les arguments de processus
// sont visibles dans `ps`, l'historique du shell et les logs d'orchestrateur.
const HashKeyEnvVar = "GOANON_HASH_KEY"

var (
	// ErrHashKeyRequired est retournée quand la stratégie Hash est utilisée sans
	// clé. Le comportement est fail-closed : pas de dégradation silencieuse vers
	// un SHA-256 nu (voir WithInsecureHash pour l'opt-in explicite).
	ErrHashKeyRequired = errors.New("anonymizer: la stratégie Hash requiert une clé (WithHashKey) ou WithInsecureHash")

	// ErrHashKeyTooShort signale une clé de longueur insuffisante.
	ErrHashKeyTooShort = fmt.Errorf("anonymizer: clé de hachage trop courte (minimum %d octets)", MinHashKeyLen)

	// ErrHashKeyNotSet signale l'absence de la variable d'environnement.
	ErrHashKeyNotSet = fmt.Errorf("anonymizer: variable d'environnement %s non définie", HashKeyEnvVar)

	// ErrHashKeyFormat signale une clé illisible (ni hexadécimal ni base64).
	ErrHashKeyFormat = errors.New("anonymizer: format de clé invalide (hexadécimal ou base64 attendu)")
)

// Validate vérifie que la clé respecte la longueur minimale.
func (k HashKey) Validate() error {
	if len(k) < MinHashKeyLen {
		return ErrHashKeyTooShort
	}
	return nil
}

// ParseHashKey décode une clé encodée en hexadécimal ou en base64 (standard ou
// URL-safe) et valide sa longueur. Les erreurs ne reproduisent jamais l'entrée.
func ParseHashKey(s string) (HashKey, error) {
	decoders := []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	for _, decode := range decoders {
		if raw, err := decode(s); err == nil && len(raw) > 0 {
			key := HashKey(raw)
			if err := key.Validate(); err != nil {
				return nil, err
			}
			return key, nil
		}
	}
	return nil, ErrHashKeyFormat
}

// HashKeyFromEnv charge et valide la clé depuis GOANON_HASH_KEY.
func HashKeyFromEnv() (HashKey, error) {
	raw, ok := os.LookupEnv(HashKeyEnvVar)
	if !ok || raw == "" {
		return nil, ErrHashKeyNotSet
	}
	return ParseHashKey(raw)
}

// hashEntity dérive le pseudonyme d'une entité par HMAC-SHA-256.
//
// Le scope et le type sont préfixés au message, chacun suivi d'un octet nul :
// cette séparation de domaine garantit que ("PER", "Ax") et ("PERA", "x") ne
// produisent pas le même digest. Sans clé, vérifier l'hypothèse « ce digest
// correspond-il à Dupont ? » est impossible — c'est la propriété structurelle du
// HMAC qui remplace le SHA-256 nu.
func hashEntity(key HashKey, scope string, t ner.EntityType, text string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(scope))
	mac.Write([]byte{0x00})
	mac.Write([]byte(t))
	mac.Write([]byte{0x00})
	mac.Write([]byte(text))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// insecureHashEntity reproduit l'ancien SHA-256 non salé, uniquement accessible
// via WithInsecureHash. Vulnérable au dictionnaire : à ne pas déployer.
func insecureHashEntity(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:6]
}
