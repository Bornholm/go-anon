package features

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/lang"
)

// Préfixes de fenêtre précalculés : "w[-2]." … "w[+2]." et "wshape[-2]=" …
// Évite un fmt.Sprintf par (token × delta) dans le chemin chaud. Les chaînes
// produites doivent rester identiques aux formats "w[%+d]." et "wshape[%+d]="
// (les features sont hachées dans les modèles sérialisés).
const maxCachedDelta = 8

var (
	ctxPrefixTable   [2*maxCachedDelta + 1]string
	shapePrefixTable [2*maxCachedDelta + 1]string
)

func init() {
	for d := -maxCachedDelta; d <= maxCachedDelta; d++ {
		if d == 0 {
			continue
		}
		ctxPrefixTable[d+maxCachedDelta] = fmt.Sprintf("w[%+d].", d)
		shapePrefixTable[d+maxCachedDelta] = fmt.Sprintf("wshape[%+d]=", d)
	}
}

// ctxPrefix retourne "w[%+d]." sans allocation pour |delta| ≤ maxCachedDelta.
func ctxPrefix(delta int) string {
	if delta >= -maxCachedDelta && delta <= maxCachedDelta {
		return ctxPrefixTable[delta+maxCachedDelta]
	}
	return fmt.Sprintf("w[%+d].", delta)
}

// shapePrefix retourne "wshape[%+d]=" sans allocation pour |delta| ≤ maxCachedDelta.
func shapePrefix(delta int) string {
	if delta >= -maxCachedDelta && delta <= maxCachedDelta {
		return shapePrefixTable[delta+maxCachedDelta]
	}
	return fmt.Sprintf("wshape[%+d]=", delta)
}

// Schémas de features. Les chaînes de features sont hachées dans les modèles
// sérialisés : toute modification doit être versionnée ici et gated par
// FeatureExtractor.Schema, sinon les modèles existants se dégradent en silence.
//
//	Schéma 0 (historique, gelé) : word.len via itos (bogué pour ≥ 10),
//	  gazseq marquant uniquement le premier token d'un span multi-mots.
//	Schéma 1 : word.len via strconv.Itoa, spans gazetteers multi-mots (2-3
//	  tokens) marqués gazseq.<nom>.B sur le premier token et .I sur les autres.
const (
	SchemaLegacy = 0
	SchemaV1     = 1

	// LatestSchema est le schéma utilisé pour les nouveaux entraînements.
	LatestSchema = SchemaV1
)

// FeatureExtractor génère les features pour chaque token d'une phrase.
// Il produit ~40–60 features par token réparties en 6 catégories :
// morphologiques, orthographiques, shape, contexte, gazetteers et langue.
type FeatureExtractor struct {
	// WindowSize est la demi-taille de la fenêtre de contexte (valeur typique : 2).
	// Avec WindowSize=2, les tokens à [-2, -1, +1, +2] sont pris en compte.
	WindowSize int

	// Schema sélectionne la version des chaînes de features (cf. SchemaLegacy,
	// SchemaV1). Doit valoir exactement la valeur utilisée à l'entraînement du
	// modèle (FeatureConfig.FeatureSchema) — propagé par ner.New.
	Schema int

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
	// ~60-100 entrées par token selon les ressources chargées :
	// pré-dimensionner évite les réallocations de la map dans le chemin chaud.
	f := make(map[string]float64, 96)
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
	runeLen := utf8.RuneCountInString(word)
	if fe.Schema >= SchemaV1 {
		f["word.len="+strconv.Itoa(runeLen)] = 1.0
	} else {
		f["word.len="+itos(runeLen)] = 1.0
	}
	// Bucket de longueur (plus robuste que la valeur exacte)
	switch {
	case runeLen <= 2:
		f["word.lenBucket=short"] = 1.0
	case runeLen <= 5:
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
		ctxKey := ctxPrefix(delta)

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
		// freq couvre aussi Contains (freq > 0) et IsUnique (freq == 1) :
		// un seul lookup au lieu de trois.
		freq := gaz.FrequencyLower(wordLower)
		if freq > 0 {
			f["gaz."+name] = 1.0

			// Feature de confiance basée sur la fréquence.
			// Entries très fréquentes (prénoms populaires) = moins spécifiques
			if freq > 10000 {
				f["gaz."+name+".common"] = 1.0
			} else if freq > 1000 {
				f["gaz."+name+".medium"] = 1.0
			} else {
				f["gaz."+name+".rare"] = 1.0
			}

			// Entry unique = très spécifique (ex: nom de ville rare)
			if freq == 1 {
				f["gaz."+name+".unique"] = 1.0
			}
		}
		// Multi-word gazetteer lookup.
		if fe.Schema >= SchemaV1 {
			// Schéma 1 : chaque token d'un span multi-mots (2-3 tokens) du
			// gazetteer est marqué — .B sur le premier token, .I sur les
			// suivants. (Le schéma 0 ne marquait que le token de départ :
			// « York » dans « New York » n'avait aucune feature gazetteer.)
			for start := max(0, idx-2); start <= idx; start++ {
				endMin := max(start+2, idx+1)
				endMax := min(len(tokens), start+3)
				for end := endMin; end <= endMax; end++ {
					if gaz.ContainsSequenceLower(lowerTokens, start, end) {
						if start == idx {
							f["gazseq."+name+".B"] = 1.0
						} else {
							f["gazseq."+name+".I"] = 1.0
						}
					}
				}
			}
		} else {
			// Schéma 0 (gelé) : séquences démarrant à idx uniquement —
			// longueurs 1..3 si un token précède (ancienne première boucle),
			// sinon longueurs 2..3 (ancienne seconde boucle ; les deux se
			// recouvraient pour idx > 0).
			seqStart := idx + 1
			if idx == 0 {
				seqStart = idx + 2
			}
			for end := seqStart; end <= len(tokens) && end-idx <= 3; end++ {
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
			if gaz.ContainsLower(wordLower) {
				currentInGaz = true
			}
			if gaz.ContainsLower(lowerTokens[idx-1]) {
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
		ctxKey := shapePrefix(delta)

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
					f[ctxPrefix(delta)+k+"="+v] = 1.0
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
				dim := strconv.Itoa(d)
				// Seuils plus nuancés pour capturer plus d'information
				if val > 1.0 {
					f["emb"+dim+"xl"] = 1.0
				} else if val > 0.5 {
					f["emb"+dim+"lg"] = 1.0
				} else if val > 0.0 {
					f["emb"+dim+"sm"] = 1.0
				} else if val > -0.5 {
					f["emb"+dim+"nl"] = 1.0
				} else {
					f["emb"+dim+"xn"] = 1.0
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
							f[ctxPrefix(delta)+"emb"+strconv.Itoa(d)+"lg"] = 1.0
						} else if val < -0.5 {
							f[ctxPrefix(delta)+"emb"+strconv.Itoa(d)+"xn"] = 1.0
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

// suffix retourne les n dernières runes de s (UTF-8 safe) sans allocation.
// Si s contient au plus n runes, retourne s en entier.
func suffix(s string, n int) string {
	i := len(s)
	for c := 0; c < n; c++ {
		if i == 0 {
			return s
		}
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

// prefix retourne les n premières runes de s (UTF-8 safe) sans allocation.
// Si s contient au plus n runes, retourne s en entier.
func prefix(s string, n int) string {
	i := 0
	for c := 0; c < n; c++ {
		if i >= len(s) {
			return s
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	if i >= len(s) {
		return s
	}
	return s[:i]
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
// (pour distinguer "Paris" de "PARIS"). Sans allocation []rune.
func isTitleCase(s string) bool {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 || !unicode.IsUpper(first) {
		return false
	}
	for _, r := range s[size:] {
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

// itos convertit un entier en un caractère unique ('0'+i).
// ATTENTION : faux pour i ≥ 10 (produit ':', ';', …) mais utilisé de façon
// identique à l'entraînement et à l'inférence — les chaînes de features sont
// hachées dans les modèles sérialisés, donc NE PAS remplacer par strconv.Itoa
// sans ré-entraîner tous les modèles.
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
