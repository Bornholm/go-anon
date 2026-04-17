package corpus

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// WikiNERReader lit des fichiers au format WikiNER.
//
// Format attendu : une phrase par ligne, chaque token représenté par
// FORME|POS|NER séparés par des espaces.
//
//	Il|PRO:PER|O assure|VER:pres|O de|PRP|I-PER Saussure|NAM|I-PER
//
// Les lignes vides sont ignorées (séparateur entre paragraphes).
//
// Champs (valeurs par défaut) :
//   - TokenSep  : séparateur entre tokens (défaut " ")
//   - FieldSep  : séparateur entre champs d'un token (défaut "|")
//   - WordField : index (0-based) du champ FORME (défaut 0)
//   - TagField  : index (0-based) du champ NER (défaut 2)
type WikiNERReader struct {
	TokenSep  string // séparateur entre tokens (défaut " ")
	FieldSep  string // séparateur entre champs d'un token (défaut "|")
	WordField int    // index du champ FORME (défaut 0)
	TagField  int    // index du champ NER (défaut 2)
}

// Read parse le contenu de input et retourne les phrases annotées.
// Chaque ligne non-vide produit exactement une phrase.
func (r *WikiNERReader) Read(input io.Reader) ([]Sentence, error) {
	tokenSep := r.tokenSep()
	fieldSep := r.fieldSep()

	var sentences []Sentence

	scanner := bufio.NewScanner(input)
	// WikiNER peut avoir de très longues lignes (dizaines de tokens)
	const maxTokens = 512
	buf := make([]byte, 0, maxTokens*32)
	scanner.Buffer(buf, maxTokens*64)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		rawTokens := strings.Split(line, tokenSep)
		var sent Sentence

		for _, raw := range rawTokens {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			fields := strings.Split(raw, fieldSep)

			word, err := r.extractField(fields, r.WordField, raw, lineNum, "mot")
			if err != nil {
				return nil, err
			}
			tag, err := r.extractField(fields, r.tagField(), raw, lineNum, "tag NER")
			if err != nil {
				return nil, err
			}

			sent = append(sent, AnnotatedToken{Word: word, Tag: tag})
		}

		if len(sent) > 0 {
			normalizeBIO(sent)
			sentences = append(sentences, sent)
		}
	}

	return sentences, scanner.Err()
}

// tokenSep retourne le séparateur de tokens effectif.
func (r *WikiNERReader) tokenSep() string {
	if r.TokenSep != "" {
		return r.TokenSep
	}
	return " "
}

// fieldSep retourne le séparateur de champs effectif.
func (r *WikiNERReader) fieldSep() string {
	if r.FieldSep != "" {
		return r.FieldSep
	}
	return "|"
}

// tagField retourne l'index effectif du champ NER.
// La valeur zéro de Go correspond au premier champ ; on distingue
// la valeur par défaut (2) via le fait que le champ est initialisé à 0
// et qu'on utilise tagField() plutôt que r.TagField directement.
func (r *WikiNERReader) tagField() int {
	if r.TagField == 0 && r.WordField == 0 {
		// valeurs Go par défaut : on utilise le champ 2 (NER dans FORME|POS|NER)
		return 2
	}
	return r.TagField
}

// normalizeBIO convertit les labels WikiNER (tous I-X) en BIO propre :
// un token I-X qui suit O ou un token d'un type différent devient B-X.
func normalizeBIO(sent Sentence) {
	for i, tok := range sent {
		if TagPrefix(tok.Tag) == "O" {
			continue
		}
		typ := TagEntity(tok.Tag)
		if i == 0 || TagPrefix(sent[i-1].Tag) == "O" || TagEntity(sent[i-1].Tag) != typ {
			sent[i].Tag = "B-" + typ
		} else {
			sent[i].Tag = "I-" + typ
		}
	}
}

// extractField retourne le champ à l'index idx depuis fields.
// Retourne une erreur descriptive si l'index est hors limites.
func (r *WikiNERReader) extractField(fields []string, idx int, raw string, lineNum int, name string) (string, error) {
	if idx < 0 || idx >= len(fields) {
		return "", fmt.Errorf("wikiner: ligne %d: token %q: champ %s (index %d) hors limites (%d champs)", lineNum, raw, name, idx, len(fields))
	}
	return fields[idx], nil
}
