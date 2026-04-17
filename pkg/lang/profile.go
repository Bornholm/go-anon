package lang

import "strings"

// LangProfile contient les ressources linguistiques spécifiques à une langue.
// Ces informations alimentent le FeatureExtractor pour générer des features
// pertinentes lors de la reconnaissance d'entités nommées.
type LangProfile struct {
	Code           string              // code de langue ISO 639-1 : "fr", "en"
	StopWords      map[string]struct{} // mots vides (en minuscules)
	CommonPrefixes []string            // particules nominales : "de", "von", "van"
	Abbreviations  map[string]struct{} // abréviations avec ponctuation : "M.", "Dr."
}

// Features retourne les features linguistiques pour un mot donné.
// Les clés retournées sont préfixées par "lang.".
//
//   - "lang.isStopWord"        : mot vide (articles, prépositions, etc.)
//   - "lang.isNominalParticle" : particule nominale ("de", "von", "van")
//   - "lang.isAbbreviation"    : abréviation connue ("M.", "Dr.", "Mr.")
func (lp *LangProfile) Features(word string) map[string]float64 {
	f := make(map[string]float64)
	lower := strings.ToLower(word)

	if _, ok := lp.StopWords[lower]; ok {
		f["lang.isStopWord"] = 1.0
	}

	for _, p := range lp.CommonPrefixes {
		if strings.ToLower(p) == lower {
			f["lang.isNominalParticle"] = 1.0
			break
		}
	}

	if _, ok := lp.Abbreviations[word]; ok {
		f["lang.isAbbreviation"] = 1.0
	}

	return f
}

// makeSet construit une map set depuis une liste de mots.
func makeSet(words []string) map[string]struct{} {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}
