package features

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/bornholm/go-anon/pkg/lang"
)

// FeatureExtractor génère les features pour chaque token d'une phrase.
// Il produit ~40–60 features par token réparties en 6 catégories :
// morphologiques, orthographiques, shape, contexte, gazetteers et langue.
type FeatureExtractor struct {
	// WindowSize est la demi-taille de la fenêtre de contexte (valeur typique : 2).
	// Avec WindowSize=2, les tokens à [-2, -1, +1, +2] sont pris en compte.
	WindowSize int

	// Gazetteers est un ensemble nommé de dictionnaires de lookup.
	// Chaque hit génère une feature "gaz.<name>"=1.0.
	Gazetteers map[string]*Gazetteer

	// LangProfile fournit des features linguistiques spécifiques à la langue.
	// nil = pas de features langue.
	LangProfile *lang.LangProfile

	// Clusters contient les Brown clusters pour les features de similarité distributionnelle.
	Clusters *BrownClusters

	// Embeddings contient les word embeddings pré-entraînés (GloVe, etc.)
	Embeddings *Embeddings
}

// Features retourne la map de features pour le token à la position idx dans tokens.
// tokens est la séquence de mots de la phrase (formes de surface).
func (fe *FeatureExtractor) Features(tokens []string, idx int) map[string]float64 {
	lowerTokens := make([]string, len(tokens))
	for i, t := range tokens {
		lowerTokens[i] = strings.ToLower(t)
	}
	return fe.FeaturesEx(tokens, lowerTokens, idx)
}

// FeaturesEx est identique à Features mais accepte lowerTokens pré-calculés,
// évitant de recalculer strings.ToLower pour chaque token de la phrase.
func (fe *FeatureExtractor) FeaturesEx(tokens []string, lowerTokens []string, idx int) map[string]float64 {
	f := make(map[string]float64)
	word := tokens[idx]
	wordLower := lowerTokens[idx]

	// --- Features morphologiques ---
	f["bias"] = 1.0
	f["word.lower="+wordLower] = 1.0
	f["word.suffix1="+suffix(word, 1)] = 1.0
	f["word.suffix2="+suffix(word, 2)] = 1.0
	f["word.suffix3="+suffix(word, 3)] = 1.0
	f["word.suffix4="+suffix(word, 4)] = 1.0
	f["word.prefix1="+prefix(word, 1)] = 1.0
	f["word.prefix2="+prefix(word, 2)] = 1.0
	f["word.prefix3="+prefix(word, 3)] = 1.0
	f["word.prefix4="+prefix(word, 4)] = 1.0
	f["word.len="+itos(len([]rune(word)))] = 1.0
	// Bucket de longueur (plus robuste que la valeur exacte)
	switch l := len([]rune(word)); {
	case l <= 2:
		f["word.lenBucket=short"] = 1.0
	case l <= 5:
		f["word.lenBucket=medium"] = 1.0
	default:
		f["word.lenBucket=long"] = 1.0
	}

	// --- Features orthographiques ---
	f["word.isUpper"] = boolToFloat(isAllUpper(word))
	f["word.isTitle"] = boolToFloat(isTitleCase(word))
	f["word.isDigit"] = boolToFloat(isDigit(word))
	f["word.hasHyphen"] = boolToFloat(containsHyphen(word))
	f["word.hasApostrophe"] = boolToFloat(containsApostrophe(word))
	f["word.hasDigit"] = boolToFloat(containsDigit(word))
	f["word.allCapsRatio"] = allCapsRatio(word)
	// Premier token de la phrase : la capitalisation est moins informative
	f["word.isFirstToken"] = boolToFloat(idx == 0)
	// Capitalisé en milieu de phrase → signal NE fort
	f["word.isMidSentenceCapital"] = boolToFloat(isTitleCase(word) && idx > 0)

	// --- Features de forme (word shape) ---
	// "John" → "Xxxx", "US-2" → "XX-d", "42" → "dd"
	f["word.shape="+wordShape(word)] = 1.0
	f["word.shortShape="+shortShape(word)] = 1.0

	// --- Features de contexte (fenêtre glissante) ---
	for delta := -fe.WindowSize; delta <= fe.WindowSize; delta++ {
		if delta == 0 {
			continue
		}
		pos := idx + delta
		ctxKey := fmt.Sprintf("w[%+d].", delta)

		if pos < 0 {
			f[ctxKey+"BOS"] = 1.0 // Begin of Sentence
		} else if pos >= len(tokens) {
			f[ctxKey+"EOS"] = 1.0 // End of Sentence
		} else {
			ctxWord := tokens[pos]
			f[ctxKey+"lower="+lowerTokens[pos]] = 1.0
			f[ctxKey+"isTitle"] = boolToFloat(isTitleCase(ctxWord))
			// Suffixe 3 du contexte
			if len(ctxWord) >= 3 {
				f[ctxKey+"suf3="+suffix(lowerTokens[pos], 3)] = 1.0
			}
		}
	}

	// --- Features gazetteers avec niveaux de confiance ---
	for name, gaz := range fe.Gazetteers {
		if gaz.Contains(word) {
			f["gaz."+name] = 1.0

			// Feature de confiance basée sur la fréquence
			freq := gaz.Frequency(word)
			if freq > 0 {
				// Entries très fréquentes (prénoms populaires) = moins spécifiques
				if freq > 10000 {
					f["gaz."+name+".common"] = 1.0
				} else if freq > 1000 {
					f["gaz."+name+".medium"] = 1.0
				} else {
					f["gaz."+name+".rare"] = 1.0
				}
			}

			// Entry unique = très spécifique (ex: nom de ville rare)
			if gaz.IsUnique(word) {
				f["gaz."+name+".unique"] = 1.0
			}
		}
		// Multi-word gazetteer lookup
		if idx > 0 {
			for end := idx + 1; end <= len(tokens) && end-idx <= 3; end++ {
				if gaz.ContainsSequenceLower(lowerTokens, idx, end) {
					f["gazseq."+name] = 1.0
					break
				}
			}
		}
		if idx < len(tokens)-1 {
			for end := idx + 2; end <= len(tokens) && end-idx <= 3; end++ {
				if gaz.ContainsSequenceLower(lowerTokens, idx, end) {
					f["gazseq."+name] = 1.0
					break
				}
			}
		}
	}

	// --- Feature de chaîne gazetteer ---
	// Si le mot actuel ET le mot précédent sont dans UN gazetteer
	if idx > 0 && fe.Gazetteers != nil {
		currentInGaz := false
		prevInGaz := false
		for _, gaz := range fe.Gazetteers {
			if gaz.Contains(word) {
				currentInGaz = true
			}
			if gaz.Contains(tokens[idx-1]) {
				prevInGaz = true
			}
			if currentInGaz && prevInGaz {
				f["gaz.chain"] = 1.0
				break
			}
		}
	}

	// --- Bigram features ---
	if idx > 0 {
		f["bigram.w[-1]+w[0]="+lowerTokens[idx-1]+" "+wordLower] = 1.0
	}
	if idx < len(tokens)-1 {
		f["bigram.w[0]+w[+1]="+wordLower+" "+lowerTokens[idx+1]] = 1.0
	}
	// Bigrammes contextuels élargis
	if idx >= 2 {
		f["bigram.w[-2]+w[-1]="+lowerTokens[idx-2]+" "+lowerTokens[idx-1]] = 1.0
	}
	if idx < len(tokens)-2 {
		f["bigram.w[+1]+w[+2]="+lowerTokens[idx+1]+" "+lowerTokens[idx+2]] = 1.0
	}

	// --- Features positionnelles ---
	f["pos.first"] = boolToFloat(idx == 0)
	f["pos.second"] = boolToFloat(idx == 1)
	f["pos.last"] = boolToFloat(idx == len(tokens)-1)

	// --- Title Bigram features ---
	if idx > 0 && isTitleCase(tokens[idx-1]) && isTitleCase(word) {
		f["titleBigram"] = 1.0
	}
	if idx < len(tokens)-1 && isTitleCase(word) && isTitleCase(tokens[idx+1]) {
		f["titleBigramRight"] = 1.0
	}

	// --- Title Run (longueur de la séquence titlecase) ---
	titleRun := 0
	for j := idx; j < len(tokens) && isTitleCase(tokens[j]); j++ {
		titleRun++
	}
	for j := idx - 1; j >= 0 && isTitleCase(tokens[j]); j-- {
		titleRun++
	}
	if titleRun > 1 {
		if titleRun >= 5 {
			f["titleRun=5+"] = 1.0
		} else {
			f["titleRun="+itos(titleRun)] = 1.0
		}
	}
	for delta := -fe.WindowSize; delta <= fe.WindowSize; delta++ {
		if delta == 0 {
			continue
		}
		pos := idx + delta
		ctxKey := fmt.Sprintf("wshape[%+d]=", delta)

		if pos < 0 {
			f[ctxKey+"BOS"] = 1.0
		} else if pos >= len(tokens) {
			f[ctxKey+"EOS"] = 1.0
		} else {
			f[ctxKey+wordShape(tokens[pos])] = 1.0
		}
	}

	// --- Brown clusters features ---
	if fe.Clusters != nil {
		prefixes := fe.Clusters.Prefixes(word)
		for k, v := range prefixes {
			f[k+"="+v] = 1.0
		}
		// Clusters du contexte aussi
		for delta := -1; delta <= 1; delta++ {
			if delta == 0 {
				continue
			}
			pos := idx + delta
			if pos >= 0 && pos < len(tokens) {
				cp := fe.Clusters.Prefixes(tokens[pos])
				for k, v := range cp {
					f[fmt.Sprintf("w[%+d].%s=%s", delta, k, v)] = 1.0
				}
			}
		}
	}

	// --- Word embeddings features ---
	if fe.Embeddings != nil {
		vec := fe.Embeddings.Vector(word)
		if vec != nil {
			// Utiliser plus de dimensions avec seuils plus fins
			dim := fe.Embeddings.Dim()
			maxDims := 10
			if maxDims > dim {
				maxDims = dim
			}
			for d := 0; d < maxDims; d++ {
				val := vec[d]
				// Seuils plus nuancés pour capturer plus d'information
				if val > 1.0 {
					f[fmt.Sprintf("emb%dxl", d)] = 1.0
				} else if val > 0.5 {
					f[fmt.Sprintf("emb%dlg", d)] = 1.0
				} else if val > 0.0 {
					f[fmt.Sprintf("emb%dsm", d)] = 1.0
				} else if val > -0.5 {
					f[fmt.Sprintf("emb%dnl", d)] = 1.0
				} else {
					f[fmt.Sprintf("emb%dxn", d)] = 1.0
				}
			}
		}
		// Embeddings du contexte aussi
		for delta := -1; delta <= 1; delta++ {
			if delta == 0 {
				continue
			}
			pos := idx + delta
			if pos >= 0 && pos < len(tokens) {
				ctxVec := fe.Embeddings.Vector(tokens[pos])
				if ctxVec != nil {
					maxDims := 5
					dim := fe.Embeddings.Dim()
					if maxDims > dim {
						maxDims = dim
					}
					for d := 0; d < maxDims; d++ {
						val := ctxVec[d]
						if val > 0.5 {
							f[fmt.Sprintf("w[%+d].emb%dlg", delta, d)] = 1.0
						} else if val < -0.5 {
							f[fmt.Sprintf("w[%+d].emb%dxn", delta, d)] = 1.0
						}
					}
				}
			}
		}
	}

	// --- Features spécifiques langue ---
	if fe.LangProfile != nil {
		for k, v := range fe.LangProfile.Features(word) {
			f[k] = v
		}
	}

	return f
}

// --- Helpers privés ---

// suffix retourne les n dernières runes de s (UTF-8 safe).
// Si s contient moins de n runes, retourne s en entier.
func suffix(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

// prefix retourne les n premières runes de s (UTF-8 safe).
// Si s contient moins de n runes, retourne s en entier.
func prefix(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// isAllUpper retourne true si toutes les lettres de s sont majuscules
// et que s contient au moins une lettre.
func isAllUpper(s string) bool {
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsLower(r) {
				return false
			}
		}
	}
	return hasLetter
}

// isTitleCase retourne true si la première rune est une lettre majuscule
// et que le mot contient au moins une lettre minuscule
// (pour distinguer "Paris" de "PARIS").
func isTitleCase(s string) bool {
	runes := []rune(s)
	if len(runes) == 0 {
		return false
	}
	if !unicode.IsUpper(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}

// isDigit retourne true si tous les caractères de s sont des chiffres Unicode
// et que s n'est pas vide.
func isDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// containsHyphen retourne true si s contient un trait d'union (ASCII ou typographique).
func containsHyphen(s string) bool {
	for _, r := range s {
		if r == '-' || r == '\u2010' || r == '\u2011' {
			return true
		}
	}
	return false
}

// containsApostrophe retourne true si s contient une apostrophe (ASCII ou typographique).
func containsApostrophe(s string) bool {
	for _, r := range s {
		if r == '\'' || r == '\u2019' {
			return true
		}
	}
	return false
}

// wordShape convertit s en une représentation de forme :
// lettre majuscule → 'X', lettre minuscule → 'x', chiffre → 'd', autres → conservés.
// Exemples : "John" → "Xxxx", "US-2" → "XX-d", "l'homme" → "x'xxxxx".
func wordShape(s string) string {
	var buf strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			buf.WriteRune('X')
		case unicode.IsLower(r):
			buf.WriteRune('x')
		case unicode.IsDigit(r):
			buf.WriteRune('d')
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// shortShape retourne la forme compressée de s : identique à wordShape
// mais les runs de caractères consécutifs identiques sont réduits à un seul.
// Exemples : "Xxxx" → "Xx", "dddd" → "d", "XX-d" → "XX-d".
func shortShape(s string) string {
	shape := wordShape(s)
	if shape == "" {
		return ""
	}
	var buf strings.Builder
	prev := rune(-1)
	for _, r := range shape {
		if r != prev {
			buf.WriteRune(r)
			prev = r
		}
	}
	return buf.String()
}

// boolToFloat convertit un booléen en float64 (true=1.0, false=0.0).
func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

// itos convertit un entier en string.
func itos(i int) string {
	return string(rune('0' + i))
}

// containsDigit retourne true si s contient au moins un chiffre.
func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// allCapsRatio retourne le ratio de lettres majuscules dans s (entre 0 et 1).
func allCapsRatio(s string) float64 {
	var upper, letter int
	for _, r := range s {
		if unicode.IsLetter(r) {
			letter++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letter == 0 {
		return 0.0
	}
	return float64(upper) / float64(letter)
}
