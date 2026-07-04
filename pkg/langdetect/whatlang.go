package langdetect

import "github.com/abadojack/whatlanggo"

// iso6391ToLang mappe un code ISO 639-1 vers l'énumération whatlanggo
// correspondante. Limité aux langues supportées par le pipeline ; extensible.
var iso6391ToLang = map[string]whatlanggo.Lang{
	"fr": whatlanggo.Fra,
	"en": whatlanggo.Eng,
	"es": whatlanggo.Spa,
}

// WhatlangDetector est une implémentation de Detector basée sur whatlanggo.
type WhatlangDetector struct {
	opts whatlanggo.Options
}

// NewWhatlangDetector construit un détecteur restreint aux codes ISO 639-1
// fournis (recommandé : les langues réellement exécutables par le pipeline).
// Restreindre la whitelist améliore nettement la fiabilité sur textes courts.
// Les codes inconnus sont ignorés ; une liste vide n'applique aucune restriction.
func NewWhatlangDetector(allowed ...string) *WhatlangDetector {
	d := &WhatlangDetector{}
	if len(allowed) > 0 {
		whitelist := make(map[whatlanggo.Lang]bool, len(allowed))
		for _, code := range allowed {
			if lang, ok := iso6391ToLang[code]; ok {
				whitelist[lang] = true
			}
		}
		if len(whitelist) > 0 {
			d.opts.Whitelist = whitelist
		}
	}
	return d
}

// Detect détecte la langue de text. La confiance et la fiabilité sont fournies
// par whatlanggo. Lang est vide si le code détecté n'a pas d'équivalent ISO 639-1.
func (d *WhatlangDetector) Detect(text string) (Result, error) {
	info := whatlanggo.DetectWithOptions(text, d.opts)
	return Result{
		Lang:       info.Lang.Iso6391(),
		Confidence: info.Confidence,
		Reliable:   info.IsReliable(),
	}, nil
}
