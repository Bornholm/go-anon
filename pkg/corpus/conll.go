package corpus

import (
	"bufio"
	"io"
	"strings"
)

// TagScheme représente le schéma d'annotation NER utilisé dans un corpus.
type TagScheme int

const (
	// BIO : Begin / Inside / Outside.
	// Ex : "B-PER", "I-PER", "O".
	BIO TagScheme = iota
	// BIOES : Begin / Inside / Outside / End / Single.
	// Ex : "B-PER", "I-PER", "E-PER", "S-LOC", "O".
	// Donne de meilleures performances avec les CRF.
	BIOES
)

// AnnotatedToken est un token avec son label NER gold.
type AnnotatedToken struct {
	Word string // forme de surface (ex : "John")
	Tag  string // label NER (ex : "B-PER", "O")
}

// Sentence est une séquence annotée de tokens.
type Sentence []AnnotatedToken

// ConLLReader lit des fichiers au format CoNLL-2002 ou CoNLL-2003.
//
// Format attendu : une ligne par token, colonnes séparées par des tabulations.
// Les phrases sont délimitées par des lignes vides ou des lignes "-DOCSTART-".
//
//	John	B-PER
//	Doe	I-PER
//	lives	O
//	              ← ligne vide : nouvelle phrase
//	Paris	B-LOC
//
// Champs :
//   - WordColumn : index (0-based) de la colonne du mot. Défaut Go = 0 (première colonne).
//   - TagColumn  : index (0-based) de la colonne du tag NER.
//     Utiliser -1 pour désigner automatiquement la dernière colonne.
//     Attention : la valeur zéro Go désigne la première colonne, pas la dernière.
//   - Separator  : séparateur de colonnes. Vide → tabulation ("\t").
type ConLLReader struct {
	WordColumn int
	TagColumn  int
	Separator  string
}

// Read parse le contenu de input et retourne les phrases annotées.
// Une phrase vide en fin de fichier est silencieusement ignorée.
func (r *ConLLReader) Read(input io.Reader) ([]Sentence, error) {
	var sentences []Sentence
	var current Sentence

	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Ligne vide ou marqueur de début de document → fin de phrase courante.
		if line == "" || strings.HasPrefix(line, "-DOCSTART-") {
			if len(current) > 0 {
				sentences = append(sentences, current)
				current = nil
			}
			continue
		}

		fields := strings.Split(line, r.separator())

		word := r.extractWord(fields)
		tag := r.extractTag(fields)

		current = append(current, AnnotatedToken{Word: word, Tag: tag})
	}

	// Flush de la dernière phrase si le fichier ne se termine pas par une ligne vide.
	if len(current) > 0 {
		sentences = append(sentences, current)
	}

	return sentences, scanner.Err()
}

// separator retourne le séparateur effectif (tabulation par défaut).
func (r *ConLLReader) separator() string {
	if r.Separator != "" {
		return r.Separator
	}
	return "\t"
}

// extractWord retourne le mot à la colonne WordColumn.
// Retourne "" si la colonne est hors limites.
func (r *ConLLReader) extractWord(fields []string) string {
	if r.WordColumn < len(fields) {
		return fields[r.WordColumn]
	}
	return ""
}

// extractTag retourne le tag NER à la colonne TagColumn.
// Si TagColumn < 0, prend la dernière colonne.
// Retourne "O" si la colonne est hors limites.
func (r *ConLLReader) extractTag(fields []string) string {
	col := r.TagColumn
	if col < 0 {
		col = len(fields) - 1
	}
	if col >= 0 && col < len(fields) {
		return fields[col]
	}
	return "O"
}
