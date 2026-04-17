package corpus

// TagPrefix retourne le préfixe d'un tag BIO/BIOES : "B", "I", "E", "S" ou "O".
// Retourne le tag tel quel si le format n'est pas reconnu.
func TagPrefix(tag string) string {
	if len(tag) >= 2 && tag[1] == '-' {
		return tag[:1]
	}
	return tag // "O" ou tag inconnu
}

// TagEntity retourne le type d'entité d'un tag (ex : "PER", "LOC", "ORG").
// Retourne "" pour les tags sans entité ("O") ou les tags mal formés.
func TagEntity(tag string) string {
	if len(tag) >= 3 && tag[1] == '-' {
		return tag[2:]
	}
	return ""
}

// ConvertBIOtoBIOES convertit un corpus annoté en schéma BIO vers le schéma BIOES.
//
// Règles de conversion :
//   - B-X seul (non suivi de I-X du même type) → S-X  (entité singleton)
//   - B-X suivi de I-X (même type)             → B-X  (début, inchangé)
//   - I-X en milieu de séquence I-X (même type)→ I-X  (intérieur, inchangé)
//   - I-X en fin de séquence (dernier I-X)     → E-X  (fin de séquence)
//   - O                                        → O    (hors entité, inchangé)
//
// Cette conversion améliore les performances des CRF en rendant les
// frontières d'entité explicites dans le schéma de labels.
func ConvertBIOtoBIOES(sentences []Sentence) []Sentence {
	result := make([]Sentence, len(sentences))
	for i, sent := range sentences {
		result[i] = convertSentenceBIOtoBIOES(sent)
	}
	return result
}

func convertSentenceBIOtoBIOES(sent Sentence) Sentence {
	if len(sent) == 0 {
		return Sentence{}
	}

	out := make(Sentence, len(sent))

	for j, tok := range sent {
		prefix := TagPrefix(tok.Tag)
		entity := TagEntity(tok.Tag)

		switch prefix {
		case "B":
			// Vérifier si le token suivant est un I-X du même type.
			if nextIsI(sent, j, entity) {
				out[j] = tok // B-X → B-X (début de séquence multi-tokens)
			} else {
				out[j] = AnnotatedToken{Word: tok.Word, Tag: "S-" + entity} // singleton
			}

		case "I":
			// Vérifier si le token suivant est un I-X du même type.
			if nextIsI(sent, j, entity) {
				out[j] = tok // I-X → I-X (milieu de séquence)
			} else {
				out[j] = AnnotatedToken{Word: tok.Word, Tag: "E-" + entity} // fin de séquence
			}

		default:
			// "O", "E", "S" ou tout autre préfixe : inchangé.
			out[j] = tok
		}
	}

	return out
}

// nextIsI retourne true si le token suivant la position j est un "I-<entity>".
func nextIsI(sent Sentence, j int, entity string) bool {
	if j+1 >= len(sent) {
		return false
	}
	next := sent[j+1]
	return TagPrefix(next.Tag) == "I" && TagEntity(next.Tag) == entity
}
