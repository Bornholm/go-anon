package checksum

import (
	"strings"
	"unicode"
)

// ibanLengths donne la longueur exacte de l'IBAN par code pays. La longueur est
// fixée par pays dans le registre IBAN : la contrôler écarte les faux positifs
// que le seul mod-97 laisserait passer (le mod-97 ne dit rien de la longueur,
// et une référence bancaire interne de la bonne forme a une chance sur 97 de
// le satisfaire).
//
// Un pays absent de la table n'est pas rejeté : seul le mod-97 et les bornes
// génériques s'appliquent. Rejeter serait plus strict mais transformerait une
// table incomplète en faux négatifs — inacceptable pour de l'anonymisation.
var ibanLengths = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28,
	"BA": 20, "BE": 16, "BG": 22, "BH": 22, "BR": 29, "BY": 28,
	"CH": 21, "CR": 22, "CY": 28, "CZ": 24,
	"DE": 22, "DK": 18, "DO": 28,
	"EE": 20, "EG": 29, "ES": 24,
	"FI": 18, "FO": 18, "FR": 27,
	"GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27, "GT": 28,
	"HR": 21, "HU": 28,
	"IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27,
	"JO": 30,
	"KW": 30, "KZ": 20,
	"LB": 28, "LC": 32, "LI": 21, "LT": 20, "LU": 20, "LV": 21, "LY": 25,
	"MC": 27, "MD": 24, "ME": 22, "MK": 19, "MR": 27, "MT": 31, "MU": 30,
	"NL": 18, "NO": 15,
	"PK": 24, "PL": 28, "PS": 29, "PT": 25,
	"QA": 29,
	"RO": 24, "RS": 22,
	"SA": 24, "SC": 31, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "ST": 25, "SV": 28,
	"TL": 23, "TN": 24, "TR": 26,
	"UA": 29,
	"VA": 22, "VG": 24,
	"XK": 20,
}

// Bornes génériques du registre IBAN, appliquées aux pays hors table.
const (
	ibanMinLen = 15
	ibanMaxLen = 34
)

// IBAN vérifie un IBAN : longueur conforme au pays et clé mod-97-10 égale à 1.
// Les espaces de présentation et la casse sont tolérés.
func IBAN(s string) bool {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), r == ' ', r == '-':
			// séparateur de présentation, ignoré
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(unicode.ToUpper(r))
		default:
			return false
		}
	}
	iban := b.String()

	if len(iban) < ibanMinLen || len(iban) > ibanMaxLen {
		return false
	}
	// Les deux premiers caractères sont le code pays, les deux suivants la clé.
	if !isUpperLetter(iban[0]) || !isUpperLetter(iban[1]) {
		return false
	}
	if !isDigit(iban[2]) || !isDigit(iban[3]) {
		return false
	}
	if want, known := ibanLengths[iban[:2]]; known && len(iban) != want {
		return false
	}

	return mod97(iban[4:]+iban[:4]) == 1
}

// mod97 calcule le reste modulo 97 d'une chaîne alphanumérique selon la norme
// ISO 7064 : chaque lettre est remplacée par sa position + 9 (A=10 … Z=35),
// puis le nombre décimal ainsi formé est réduit modulo 97.
//
// La réduction est incrémentale pour éviter d'avoir à manipuler un entier de
// plusieurs centaines de bits.
func mod97(s string) int {
	rem := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case isDigit(c):
			rem = (rem*10 + int(c-'0')) % 97
		case isUpperLetter(c):
			v := int(c-'A') + 10
			rem = (rem*100 + v) % 97
		default:
			return -1
		}
	}
	return rem
}

func isDigit(c byte) bool       { return c >= '0' && c <= '9' }
func isUpperLetter(c byte) bool { return c >= 'A' && c <= 'Z' }
