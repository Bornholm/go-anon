package anonymizer

import "github.com/bornholm/go-anon/pkg/ner"

// AnonymizeOption est une option fonctionnelle pour Anonymize().
type AnonymizeOption func(*anonymizeParams)

type anonymizeParams struct {
	session *Session
}

// Session conserve l'état partagé entre plusieurs appels à Anonymize(),
// permettant une numérotation et une cohérence cross-segments (ex. paragraphes d'un document).
type Session struct {
	counters              map[ner.EntityType]int
	consistentCache       map[string]string
	Mapping               map[string]string // placeholder → original (cumulé)
	OriginalToPlaceholder map[string]string // original → placeholder (cumulé)
}

func NewSession() *Session {
	return &Session{
		counters:              make(map[ner.EntityType]int),
		consistentCache:       make(map[string]string),
		Mapping:               make(map[string]string),
		OriginalToPlaceholder: make(map[string]string),
	}
}

// WithSession injecte une session partagée dans Anonymize(), permettant
// de conserver les compteurs et le cache de cohérence entre plusieurs appels.
func WithSession(s *Session) AnonymizeOption {
	return func(p *anonymizeParams) {
		p.session = s
	}
}
