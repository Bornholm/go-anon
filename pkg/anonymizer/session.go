package anonymizer

import (
	"errors"
	"fmt"
	"maps"

	"github.com/bornholm/go-anon/pkg/ner"
)

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

	verify         bool
	strictVerify   bool
	verifyPatterns []ner.RegexPattern

	// extraEntities : entités détectées hors de cet appel, à unioner avec celles
	// du recognizer (cf. WithAdditionalEntities).
	extraEntities []ner.Entity
}

// Session conserve l'état partagé entre plusieurs appels à Anonymize(),
// permettant une numérotation et une cohérence cross-segments (ex. paragraphes d'un document).
//
// Une Session accumule des PII (le mapping de ré-identification) en mémoire. Sur
// un serveur long-running, deux garde-fous limitent la rétention : maxEntities
// borne la taille du mapping (cf. SetMaxEntities), et Close() rend les maps
// collectables une fois le traitement terminé.
type Session struct {
	counters              map[ner.EntityType]int
	consistentCache       map[string]string
	nonce                 string
	Mapping               map[string]string // placeholder → original (cumulé)
	OriginalToPlaceholder map[string]string // original → placeholder (cumulé)

	// maxEntities borne le nombre d'entrées du mapping (0 = illimité). Au-delà,
	// Anonymize échoue avec ErrSessionFull plutôt que de croître sans fin.
	maxEntities int
	// closed marque une session libérée par Close() : tout usage ultérieur
	// échoue avec ErrSessionClosed.
	closed bool
}

// ErrSessionClosed signale l'usage d'une session déjà libérée par Close().
var ErrSessionClosed = errors.New("anonymizer: session fermée")

// ErrSessionFull signale que le mapping a atteint le plafond maxEntities.
var ErrSessionFull = errors.New("anonymizer: plafond d'entités de la session atteint")

// SetMaxEntities borne le nombre d'entrées du mapping accumulé. Passé ce seuil,
// Anonymize retourne ErrSessionFull sans rien produire. 0 (défaut) = illimité.
//
// Sur un serveur qui conserve des sessions nommées entre requêtes, ce plafond
// transforme une croissance mémoire non bornée (donc un OOM à terme) en erreur
// explicite et maîtrisable.
func (s *Session) SetMaxEntities(n int) {
	s.maxEntities = n
}

// Close libère les tables de la session : les maps sont remises à nil, rendant
// les chaînes qu'elles retenaient (formes de surface, pseudonymes) collectables
// par le GC. Toute utilisation ultérieure de la session échoue avec
// ErrSessionClosed.
//
// En Go, la zéroïsation mémoire n'est pas garantissable (GC potentiellement
// copiant, chaînes immuables) : Close accélère la collectabilité, il ne garantit
// pas l'effacement physique. Les mitigations d'infrastructure (swap chiffré ou
// désactivé, pas de core dumps) sont documentées dans docs/rgpd.md.
func (s *Session) Close() {
	s.counters = nil
	s.consistentCache = nil
	s.Mapping = nil
	s.OriginalToPlaceholder = nil
	s.closed = true
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

// SessionStateVersion versionne le format d'échange de SessionState.
const SessionStateVersion = 1

// SessionState est la projection sérialisable d'une Session.
//
// Le type est distinct de Session à dessein : consistentCache n'y figure pas.
// Ce cache est intégralement reconstructible depuis OriginalToPlaceholder et ne
// contiendrait que des formes de surface normalisées redondantes — autant de
// PII écrites une seconde fois sur le support de stockage.
type SessionState struct {
	Version               int                    `json:"version"`
	Nonce                 string                 `json:"nonce"`
	Counters              map[ner.EntityType]int `json:"counters"`
	Mapping               map[string]string      `json:"mapping"`
	OriginalToPlaceholder map[string]string      `json:"original_to_placeholder"`
}

// State projette la session dans sa forme sérialisable. Les maps sont copiées :
// l'état retourné ne suit pas les mutations ultérieures de la session.
func (s *Session) State() SessionState {
	st := SessionState{
		Version:               SessionStateVersion,
		Nonce:                 s.nonce,
		Counters:              make(map[ner.EntityType]int, len(s.counters)),
		Mapping:               make(map[string]string, len(s.Mapping)),
		OriginalToPlaceholder: make(map[string]string, len(s.OriginalToPlaceholder)),
	}
	maps.Copy(st.Counters, s.counters)
	maps.Copy(st.Mapping, s.Mapping)
	maps.Copy(st.OriginalToPlaceholder, s.OriginalToPlaceholder)
	return st
}

// ErrUnsupportedSessionState signale un état produit par une version ultérieure.
var ErrUnsupportedSessionState = errors.New("anonymizer: version de SessionState non supportée")

// NewSessionFromState reconstruit une Session à partir d'un état sérialisé.
// Le cache de cohérence est régénéré depuis OriginalToPlaceholder.
func NewSessionFromState(st SessionState) (*Session, error) {
	if st.Version > SessionStateVersion {
		return nil, fmt.Errorf("%w : %d > %d", ErrUnsupportedSessionState, st.Version, SessionStateVersion)
	}

	s := NewSession()
	if st.Nonce != "" {
		s.nonce = st.Nonce
	}
	maps.Copy(s.counters, st.Counters)
	maps.Copy(s.Mapping, st.Mapping)
	maps.Copy(s.OriginalToPlaceholder, st.OriginalToPlaceholder)
	for original, placeholder := range st.OriginalToPlaceholder {
		s.consistentCache[normalizeForFuzzy(original)] = placeholder
	}
	return s, nil
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

// WithVerification attache un VerificationReport au Result, sans bloquer.
// Mode observation : utile pour mesurer avant de basculer en strict.
// WithAdditionalEntities injecte des entités repérées en dehors de cet appel,
// à unioner avec celles que le recognizer trouvera sur le texte.
//
// Sert à la détection multi-vues : une entité coupée par la segmentation du
// document n'est visible que depuis une recomposition, jamais depuis le segment
// isolé qu'on anonymise ici. Les offsets sont relatifs à text, et les entités
// hors bornes ou en conflit sont écartées par la réconciliation — un appelant
// ne peut donc pas corrompre le remplacement en fournissant n'importe quoi.
func WithAdditionalEntities(entities []ner.Entity) AnonymizeOption {
	return func(p *anonymizeParams) { p.extraEntities = entities }
}

func WithVerification() AnonymizeOption {
	return func(p *anonymizeParams) {
		p.verify = true
	}
}

// WithStrictVerification active le mode fail-closed : si la vérification
// détecte la moindre fuite, Anonymize retourne une erreur et **aucun texte**.
// Un texte partiellement anonymisé ne doit pas pouvoir être exploité par
// inadvertance ; c'est la garantie centrale du mode strict.
func WithStrictVerification() AnonymizeOption {
	return func(p *anonymizeParams) {
		p.verify = true
		p.strictVerify = true
	}
}

// WithVerifyPatterns restreint (ou étend) les expressions régulières re-passées
// sur la sortie lors de la vérification. Par défaut : DefaultVerifyPatterns().
//
// À utiliser quand le pipeline n'anonymise délibérément pas certains
// identifiants structurés : sans cela, la vérification les signalera — à raison,
// mais le mode strict deviendrait inutilisable.
func WithVerifyPatterns(patterns ...ner.RegexPattern) AnonymizeOption {
	return func(p *anonymizeParams) {
		// Non-nil même vide : distingue « aucun pattern » de « valeur par défaut ».
		if patterns == nil {
			patterns = []ner.RegexPattern{}
		}
		p.verifyPatterns = patterns
	}
}
