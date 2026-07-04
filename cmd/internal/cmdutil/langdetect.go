package cmdutil

import (
	"fmt"
	"slices"
	"strings"

	goanon "github.com/bornholm/go-anon"
)

// DetectLanguage détecte la langue de text en restreignant la détection aux
// codes candidats fournis (typiquement les langues exécutables par le pipeline).
// Retourne une erreur explicite si la détection est jugée peu fiable ou si la
// langue détectée ne fait pas partie des candidats.
func DetectLanguage(text string, candidates []string) (string, error) {
	det := goanon.NewWhatlangDetector(candidates...)
	res, err := det.Detect(text)
	if err != nil {
		return "", fmt.Errorf("détection de langue : %w", err)
	}
	if res.Lang == "" || !res.Reliable {
		return "", fmt.Errorf("langue indéterminée (confiance %.2f) — précisez -lang %s", res.Confidence, strings.Join(candidates, "|"))
	}
	if !slices.Contains(candidates, res.Lang) {
		return "", fmt.Errorf("langue détectée %q non supportée — précisez -lang %s", res.Lang, strings.Join(candidates, "|"))
	}
	return res.Lang, nil
}
