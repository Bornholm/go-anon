package anonymizer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bornholm/go-anon/pkg/ner"
)

// Délimiteurs des placeholders modernes : U+27E6 MATHEMATICAL LEFT WHITE SQUARE
// BRACKET et U+27E7. Quasi inexistants en texte naturel, ils évitent les
// collisions avec les crochets ASCII omniprésents dans les logs techniques.
const (
	phOpen  = "⟦"
	phClose = "⟧"
)

// redactedSuffix marque un placeholder volontairement irréversible (secrets).
// Il est exclu du contrôle de complétude de Deanonymize : aucun mapping ne le
// restaure, par conception.
const redactedSuffix = "_REDACTED"

var (
	// ErrPlaceholderCollision signale qu'un placeholder est déjà présent dans le
	// texte source : anonymiser tel quel corromprait le round-trip (Deanonymize
	// restaurerait du texte qui n'a jamais été anonymisé).
	ErrPlaceholderCollision = errors.New("anonymizer: le texte source contient déjà un placeholder")

	// ErrIncompleteMapping signale qu'un placeholder subsiste après
	// dé-anonymisation : mapping tronqué, ou mapping d'un autre document.
	ErrIncompleteMapping = errors.New("anonymizer: placeholder résiduel après dé-anonymisation")
)

// placeholderPattern reconnaît toutes les formes de placeholder moderne
// (indexée, hachée, caviardée), indépendamment du nonce de session.
var placeholderPattern = regexp.MustCompile(phOpen + `[A-Za-z0-9_]{1,128}` + phClose)

// legacyPlaceholderPattern reconnaît l'ancien format `[TYPE_N]`.
var legacyPlaceholderPattern = regexp.MustCompile(`\[[A-Z][A-Z0-9_]*_\d+\]`)

// newNonce tire 3 octets aléatoires (6 caractères hexadécimaux). Le nonce rend
// le format des placeholders imprédictible : sans lui, un attaquant pourrait
// pré-injecter un placeholder valide dans le texte source.
func newNonce() string {
	var b [3]byte
	// Depuis Go 1.24, crypto/rand.Read ne retourne jamais d'erreur : il panique
	// si la source d'entropie du système est indisponible.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// tagPlaceholder construit le placeholder indexé d'une entité (stratégies
// TagReplace et Consistent).
func (p *anonymizeParams) tagPlaceholder(label string, index int) string {
	if p.legacyPlaceholders {
		return fmt.Sprintf("[%s_%d]", label, index)
	}
	return fmt.Sprintf("%s%s_%d_%s%s", phOpen, label, index, p.nonce, phClose)
}

// hashPlaceholder construit le placeholder d'une entité hachée. Il ne porte pas
// de nonce : le digest est stable par construction (c'est l'objet de la stratégie).
func (p *anonymizeParams) hashPlaceholder(t ner.EntityType, digest string) string {
	if p.legacyPlaceholders {
		return fmt.Sprintf("[%s_%s]", t, digest)
	}
	return phOpen + string(t) + "_" + digest + phClose
}

// secretPlaceholder construit le marqueur de caviardage d'un secret. Il ne porte
// ni index ni digest : deux occurrences du même secret sont indiscernables, ce
// qui interdit la corrélation.
func (p *anonymizeParams) secretPlaceholder(t ner.EntityType) string {
	if p.legacyPlaceholders {
		return "[" + string(t) + redactedSuffix + "]"
	}
	return phOpen + string(t) + redactedSuffix + phClose
}

// escapePlaceholderDelimiters neutralise les délimiteurs ⟦ ⟧ du texte source.
func escapePlaceholderDelimiters(text string) string {
	return strings.NewReplacer(phOpen, "[[", phClose, "]]").Replace(text)
}

// checkPlaceholderCollisions vérifie que le texte source ne contient pas déjà de
// placeholder. Retourne le texte à anonymiser (éventuellement échappé) ou
// ErrPlaceholderCollision. Les erreurs ne portent qu'un offset, jamais de contenu.
func checkPlaceholderCollisions(text string, p *anonymizeParams) (string, error) {
	pattern := placeholderPattern
	if p.legacyPlaceholders {
		pattern = legacyPlaceholderPattern
	}

	loc := pattern.FindStringIndex(text)
	if loc == nil {
		return text, nil
	}

	if !p.escapeCollisions {
		return "", fmt.Errorf("%w (offset %d)", ErrPlaceholderCollision, loc[0])
	}
	if p.legacyPlaceholders {
		// Le format legacy `[TYPE_N]` n'est pas échappable sans ambiguïté :
		// il n'a pas de délimiteur propre à neutraliser.
		return "", fmt.Errorf("%w : échappement indisponible en mode legacy (offset %d)", ErrPlaceholderCollision, loc[0])
	}
	return escapePlaceholderDelimiters(text), nil
}

// findResidualPlaceholder retourne l'offset du premier placeholder réversible
// encore présent dans text, ou -1. Les marqueurs de caviardage (_REDACTED) sont
// ignorés : leur irréversibilité est voulue.
func findResidualPlaceholder(text string) int {
	for _, loc := range placeholderPattern.FindAllStringIndex(text, -1) {
		if strings.HasSuffix(text[loc[0]:loc[1]], redactedSuffix+phClose) {
			continue
		}
		return loc[0]
	}
	return -1
}
