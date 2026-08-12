package checksum

import "strings"

// NIR vérifie un numéro de sécurité sociale français (NIR) : 13 caractères
// d'identification suivis d'une clé de contrôle à 2 chiffres.
//
// La clé vaut 97 − (n mod 97), où n est le numéro à 13 chiffres. Les
// départements corses font exception : leur code alphanumérique 2A/2B doit être
// remplacé par 19/18 avant le calcul.
//
// La structure est également contrôlée (sexe, mois, département) car le seul
// mod-97 laisserait passer une référence à 15 chiffres sur 97 : sur un bulletin
// de paie ou un document médical, où le NIR côtoie quantité de numéros de
// dossier, ce n'est pas assez discriminant.
func NIR(s string) bool {
	norm, ok := normalizeNIR(s)
	if !ok {
		return false
	}

	body, key := norm[:13], norm[13:]
	if !validNIRStructure(body) {
		return false
	}

	// Substitution corse : la lettre occupe la position 5 du corps.
	numeric := body
	switch {
	case strings.HasPrefix(body[5:7], "2A"):
		numeric = body[:5] + "19" + body[7:]
	case strings.HasPrefix(body[5:7], "2B"):
		numeric = body[:5] + "18" + body[7:]
	}
	for i := 0; i < len(numeric); i++ {
		if !isDigit(numeric[i]) {
			return false
		}
	}

	rem := 0
	for i := 0; i < len(numeric); i++ {
		rem = (rem*10 + int(numeric[i]-'0')) % 97
	}
	want := 97 - rem
	got := int(key[0]-'0')*10 + int(key[1]-'0')
	return want == got
}

// normalizeNIR retire les séparateurs de présentation et met en majuscules,
// puis vérifie que le résultat fait 15 caractères dont les 2 derniers sont la
// clé numérique.
func normalizeNIR(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == 'A', r == 'B':
			b.WriteRune(r)
		case r == 'a':
			b.WriteRune('A')
		case r == 'b':
			b.WriteRune('B')
		case r == ' ', r == '.', r == '-', r == ' ':
			// séparateur de présentation, ignoré
		default:
			return "", false
		}
	}
	norm := b.String()
	if len(norm) != 15 {
		return "", false
	}
	if !isDigit(norm[13]) || !isDigit(norm[14]) {
		return "", false
	}
	return norm, true
}

// validNIRStructure contrôle les champs signifiants du corps du NIR.
//
//	position 0      : sexe — 1 ou 2, 3/4 pour les personnes en cours
//	                  d'immatriculation, 7/8 pour les NIR provisoires
//	positions 1-2   : année de naissance (00-99, toujours valide)
//	positions 3-4   : mois — 01-12, ou 20 (mois inconnu), ou 30-42 et 50-99
//	                  pour les personnes nées à l'étranger avant 1964
//	positions 5-6   : département — 01-95, 2A/2B, 96-99 ou 970-989
func validNIRStructure(body string) bool {
	switch body[0] {
	case '1', '2', '3', '4', '7', '8':
	default:
		return false
	}

	month := int(body[3]-'0')*10 + int(body[4]-'0')
	if !isDigit(body[3]) || !isDigit(body[4]) {
		return false
	}
	switch {
	case month >= 1 && month <= 12:
	case month == 20:
	case month >= 30 && month <= 42:
	case month >= 50 && month <= 99:
	default:
		return false
	}

	dept := body[5:7]
	if dept == "2A" || dept == "2B" {
		return true
	}
	if !isDigit(dept[0]) || !isDigit(dept[1]) {
		return false
	}
	d := int(dept[0]-'0')*10 + int(dept[1]-'0')
	return d >= 1 && d <= 99
}
