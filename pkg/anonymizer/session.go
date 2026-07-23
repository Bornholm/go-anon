package anonymizer

import "github.com/bornholm/go-anon/pkg/ner"

// AnonymizeOption est une option fonctionnelle pour Anonymize().
type AnonymizeOption func(*anonymizeParams)

type anonymizeParams struct {
	session *Session

	// nonce du format de placeholder, dérivé de la session ou tiré par appel.
	nonce string

	legacyPlaceholders bool
	escapeCollisions   bool
	exposeSecrets      bool

	hashKey      HashKey
	hashScope    string
	insecureHash bool
}

// Session conserve l'état partagé entre plusieurs appels à Anonymize(),
// permettant une numérotation et une cohérence cross-segments (ex. paragraphes d'un document).
type Session struct {
	counters              map[ner.EntityType]int
	consistentCache       map[string]string
	nonce                 string
	Mapping               map[string]string // placeholder → original (cumulé)
	OriginalToPlaceholder map[string]string // original → placeholder (cumulé)
}

func NewSession() *Session {
	return &Session{
		counters:              make(map[ner.EntityType]int),
		consistentCache:       make(map[string]string),
		nonce:                 newNonce(),
		Mapping:               make(map[string]string),
		OriginalToPlaceholder: make(map[string]string),
	}
}

// Nonce retourne le nonce de session inclus dans les placeholders. Tous les
// segments d'un même document partagent ce nonce, ce qui rend leurs placeholders
// mutuellement cohérents et imprédictibles depuis l'extérieur.
func (s *Session) Nonce() string {
	if s.nonce == "" {
		s.nonce = newNonce()
	}
	return s.nonce
}

// WithSession injecte une session partagée dans Anonymize(), permettant
// de conserver les compteurs et le cache de cohérence entre plusieurs appels.
func WithSession(s *Session) AnonymizeOption {
	return func(p *anonymizeParams) {
		p.session = s
	}
}

// WithLegacyPlaceholders rétablit l'ancien format `[TYPE_N]`, sans délimiteurs
// improbables ni nonce.
//
// Déprécié : ce format entre en collision avec les crochets courants des textes
// techniques, ce qui corrompt le round-trip, et il est prédictible donc
// injectable. Réservé à la compatibilité des consommateurs qui parsent `PER_1`.
func WithLegacyPlaceholders() AnonymizeOption {
	return func(p *anonymizeParams) {
		p.legacyPlaceholders = true
	}
}

// WithEscapeCollisions échappe les délimiteurs de placeholder présents dans le
// texte source au lieu de retourner ErrPlaceholderCollision. Le texte anonymisé
// n'est alors plus bit-à-bit restaurable : ⟦ et ⟧ deviennent [[ et ]].
// Indisponible en mode legacy.
func WithEscapeCollisions() AnonymizeOption {
	return func(p *anonymizeParams) {
		p.escapeCollisions = true
	}
}

// WithExposeSecrets rétablit l'exposition de la forme de surface des entités de
// type secret dans Result.Entities. Défaut : false — le texte est remplacé par
// un résumé sans contenu. À n'activer que pour du débogage local.
func WithExposeSecrets(expose bool) AnonymizeOption {
	return func(p *anonymizeParams) {
		p.exposeSecrets = expose
	}
}

// WithHashKey fournit la clé secrète de la stratégie Hash. Charger la clé depuis
// HashKeyFromEnv() plutôt que depuis un flag CLI.
func WithHashKey(k HashKey) AnonymizeOption {
	return func(p *anonymizeParams) {
		p.hashKey = k
	}
}

// WithHashScope compartimente les pseudonymes de la stratégie Hash.
//
// Par défaut (scope vide), une même clé produit le même pseudonyme partout : les
// occurrences d'une personne sont corrélables entre documents, ce qui est
// parfois voulu pour l'analyse et parfois interdit. Un scope par dossier ou par
// client casse cette linkability inter-scopes.
func WithHashScope(scope string) AnonymizeOption {
	return func(p *anonymizeParams) {
		p.hashScope = scope
	}
}

// WithInsecureHash autorise la stratégie Hash sans clé, en repassant au SHA-256
// non salé historique.
//
// Cassable par dictionnaire sur des noms propres : réservé aux jeux de tests et
// aux démonstrations. Un avertissement est émis sur la sortie de log standard.
func WithInsecureHash() AnonymizeOption {
	return func(p *anonymizeParams) {
		p.insecureHash = true
	}
}
