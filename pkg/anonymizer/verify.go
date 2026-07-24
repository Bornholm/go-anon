package anonymizer

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
)

// LeakKind classe les défauts détectés par Verify sur une sortie anonymisée.
type LeakKind int

const (
	// LeakKnownEntity : une forme de surface présente dans le mapping subsiste
	// dans la sortie — le remplacement a été manqué ou défait par une passe.
	LeakKnownEntity LeakKind = iota
	// LeakRegexHit : un identifiant structuré (e-mail, IBAN, jeton…) reste
	// détectable dans la sortie, hors des zones de remplacement.
	LeakRegexHit
	// LeakInvalidUTF8 : la sortie n'est pas de l'UTF-8 valide — signe d'un
	// remplacement effectué au milieu d'une séquence multi-octets.
	LeakInvalidUTF8
	// LeakResidualPlaceholderSource : le texte source contenait déjà un
	// placeholder, ce qui rend le round-trip ambigu.
	LeakResidualPlaceholderSource
)

func (k LeakKind) String() string {
	switch k {
	case LeakKnownEntity:
		return "known-entity"
	case LeakRegexHit:
		return "regex-hit"
	case LeakInvalidUTF8:
		return "invalid-utf8"
	case LeakResidualPlaceholderSource:
		return "residual-placeholder-source"
	default:
		return "unknown"
	}
}

// Leak localise un défaut dans la sortie.
//
// Invariant absolu : un Leak porte des offsets et des types, jamais le texte
// fuité. Un rapport finit dans des logs et des réponses HTTP ; s'il transportait
// le contenu, il deviendrait lui-même la fuite.
type Leak struct {
	Kind       LeakKind
	Type       ner.EntityType // vide si non applicable
	Start, End int            // offsets en octets dans la sortie
}

func (l Leak) String() string {
	if l.Type == "" {
		return fmt.Sprintf("%s [%d:%d]", l.Kind, l.Start, l.End)
	}
	return fmt.Sprintf("%s type=%s [%d:%d]", l.Kind, l.Type, l.Start, l.End)
}

// VerificationReport agrège les défauts constatés sur une sortie.
type VerificationReport struct {
	Leaks []Leak
}

// OK indique que la sortie ne présente aucun défaut détectable.
func (r *VerificationReport) OK() bool { return r == nil || len(r.Leaks) == 0 }

// CountByKind résume le rapport sans exposer d'offsets — format adapté aux logs.
func (r *VerificationReport) CountByKind() map[LeakKind]int {
	if r == nil {
		return nil
	}
	counts := make(map[LeakKind]int, len(r.Leaks))
	for _, l := range r.Leaks {
		counts[l.Kind]++
	}
	return counts
}

// ErrVerificationFailed est la sentinelle du mode fail-closed.
var ErrVerificationFailed = errors.New("anonymizer: vérification échouée")

// VerificationError enveloppe le rapport ayant provoqué l'échec en mode strict.
// Elle ne porte aucun contenu textuel.
type VerificationError struct {
	Report *VerificationReport
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("%s : %d fuite(s) détectée(s)", ErrVerificationFailed, len(e.Report.Leaks))
}

func (e *VerificationError) Unwrap() error { return ErrVerificationFailed }

// legacyRedactedPattern reconnaît les marqueurs de caviardage au format legacy.
// Les autres remplacements legacy sont couverts exactement par les valeurs de
// OriginalToPlaceholder ; seuls les marqueurs de secret en sont absents.
var legacyRedactedPattern = regexp.MustCompile(`\[[A-Z][A-Z0-9_]*` + redactedSuffix + `\]`)

// DefaultVerifyPatterns retourne les patterns re-passés sur la sortie : tous les
// identifiants structurés et tous les jetons d'authentification intégrés.
//
// Ce jeu est indépendant de la configuration du Recognizer : si le pipeline n'a
// pas été construit avec WithBuiltinRegexPatterns, la vérification signalera les
// e-mails et IBAN restés en clair. C'est voulu — ils sont bien présents dans la
// sortie. Utiliser WithVerifyPatterns pour restreindre ce jeu en connaissance de
// cause.
func DefaultVerifyPatterns() []ner.RegexPattern {
	patterns := make([]ner.RegexPattern, 0, len(ner.BuiltinRegexPatterns)+8)
	patterns = append(patterns, ner.BuiltinRegexPatterns...)
	patterns = append(patterns, ner.SecretPatterns()...)
	return patterns
}

// Verify recontrôle une sortie anonymisée avec les patterns par défaut.
func Verify(original string, res *Result) *VerificationReport {
	return VerifyWithPatterns(original, res, DefaultVerifyPatterns())
}

// VerifyWithPatterns recontrôle une sortie anonymisée.
//
// La méthode raisonne par « zones sûres » : les spans occupés par un
// remplacement (placeholder, caviardage, hash) sont exclus du scan. Contrôler
// une liste de formes autorisées serait fragile ; exclure les zones que
// l'anonymiseur a lui-même écrites l'est beaucoup moins.
func VerifyWithPatterns(original string, res *Result, patterns []ner.RegexPattern) *VerificationReport {
	report := &VerificationReport{}
	if res == nil {
		return report
	}
	text := res.Text

	// 1. Intégrité d'encodage : une corruption UTF-8 trahit un remplacement
	// effectué au milieu d'une séquence multi-octets.
	if !utf8.ValidString(text) {
		report.Leaks = append(report.Leaks, Leak{Kind: LeakInvalidUTF8, Start: 0, End: len(text)})
	}

	// 2. Placeholder déjà présent dans la source. Anonymize le refuse par défaut
	// (ErrPlaceholderCollision) ; ce contrôle couvre WithEscapeCollisions.
	for _, loc := range placeholderPattern.FindAllStringIndex(original, -1) {
		report.Leaks = append(report.Leaks, Leak{
			Kind: LeakResidualPlaceholderSource, Start: loc[0], End: loc[1],
		})
	}

	safe := safeZones(res)

	// 3. Formes de surface connues encore présentes hors zones sûres.
	scanKnownEntities(text, res, safe, report)

	// 4. Identifiants structurés re-détectables hors zones sûres.
	scanPatterns(text, safe, patterns, report)

	sort.Slice(report.Leaks, func(i, j int) bool {
		if report.Leaks[i].Start != report.Leaks[j].Start {
			return report.Leaks[i].Start < report.Leaks[j].Start
		}
		return report.Leaks[i].Kind < report.Leaks[j].Kind
	})
	return report
}

// safeZones marque les octets de la sortie écrits par l'anonymiseur : toutes les
// occurrences des remplacements du mapping, plus les marqueurs de caviardage
// (absents du mapping par conception, cf. chantier S1).
func safeZones(res *Result) []bool {
	safe := make([]bool, len(res.Text))
	mark := func(start, end int) {
		for i := start; i < end && i < len(safe); i++ {
			safe[i] = true
		}
	}

	for _, replacement := range res.OriginalToPlaceholder {
		if replacement == "" {
			continue
		}
		for pos := 0; ; {
			idx := strings.Index(res.Text[pos:], replacement)
			if idx < 0 {
				break
			}
			abs := pos + idx
			mark(abs, abs+len(replacement))
			pos = abs + len(replacement)
		}
	}

	for _, pattern := range []*regexp.Regexp{placeholderPattern, legacyRedactedPattern} {
		for _, loc := range pattern.FindAllStringIndex(res.Text, -1) {
			mark(loc[0], loc[1])
		}
	}

	return safe
}

func overlapsSafe(safe []bool, start, end int) bool {
	for i := start; i < end && i < len(safe); i++ {
		if safe[i] {
			return true
		}
	}
	return false
}

// scanKnownEntities recherche les formes de surface du mapping dans la sortie.
// La recherche est insensible à la casse (même normalisation que ConsistencyPass)
// et bornée aux frontières de mot, pour ne pas signaler « Paris » dans
// « parisien » — un mode strict qui crie au loup finit désactivé.
func scanKnownEntities(text string, res *Result, safe []bool, report *VerificationReport) {
	if len(res.OriginalToPlaceholder) == 0 {
		return
	}

	// typeBySurface permet de qualifier la fuite sans relire le texte.
	typeBySurface := make(map[string]ner.EntityType, len(res.Entities))
	for _, e := range res.Entities {
		if _, ok := typeBySurface[e.Text]; !ok {
			typeBySurface[e.Text] = e.Type
		}
	}

	// La recherche insensible à la casse se fait sur une copie minusculée. Elle
	// n'est utilisable que si le passage en minuscules préserve les longueurs en
	// octets — sinon les offsets ne seraient plus transposables.
	lowerText := strings.ToLower(text)
	lowerUsable := len(lowerText) == len(text)

	surfaces := make([]string, 0, len(res.OriginalToPlaceholder))
	for surface := range res.OriginalToPlaceholder {
		surfaces = append(surfaces, surface)
	}
	sort.Strings(surfaces) // rapport déterministe

	seen := make(map[[2]int]bool)
	for _, surface := range surfaces {
		if surface == "" {
			continue
		}
		haystack, needle := text, surface
		if lower := strings.ToLower(surface); lowerUsable && len(lower) == len(surface) {
			haystack, needle = lowerText, lower
		}

		for pos := 0; ; {
			idx := strings.Index(haystack[pos:], needle)
			if idx < 0 {
				break
			}
			start := pos + idx
			end := start + len(needle)
			pos = start + 1

			if overlapsSafe(safe, start, end) || !isWordBoundary(text, start, end) {
				continue
			}
			if seen[[2]int{start, end}] {
				continue
			}
			seen[[2]int{start, end}] = true
			report.Leaks = append(report.Leaks, Leak{
				Kind:  LeakKnownEntity,
				Type:  typeBySurface[surface],
				Start: start,
				End:   end,
			})
		}
	}
}

// scanPatterns re-passe les expressions régulières sur la sortie. Les matchs
// chevauchant une zone sûre sont ignorés : c'est ce qui évite qu'un digest
// hexadécimal ou un placeholder soit compté comme un identifiant résiduel.
func scanPatterns(text string, safe []bool, patterns []ner.RegexPattern, report *VerificationReport) {
	for _, p := range patterns {
		if p.Re == nil {
			continue
		}
		for _, m := range p.Re.FindAllStringSubmatchIndex(text, -1) {
			start, end := m[0], m[1]
			// Même convention que RegexEntityFilter : avec Submatch, seul le
			// groupe capturant est anonymisé, donc seul lui doit être vérifié.
			if p.Submatch > 0 && p.Submatch*2+1 < len(m) && m[p.Submatch*2] >= 0 {
				start, end = m[p.Submatch*2], m[p.Submatch*2+1]
			}
			if start < 0 || start >= end {
				continue
			}
			if overlapsSafe(safe, start, end) {
				continue
			}
			report.Leaks = append(report.Leaks, Leak{
				Kind:  LeakRegexHit,
				Type:  p.EntityType,
				Start: start,
				End:   end,
			})
		}
	}
}
