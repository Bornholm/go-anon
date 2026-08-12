// Package checksum implémente les clés de contrôle des identifiants reconnus
// par les patterns intégrés de pkg/ner (SIREN, SIRET, IBAN, NIR).
//
// Ces identifiants ont tous une structure régulière, ce qui rend leur détection
// par expression régulière triviale — mais très bruitée. Une facture ou un
// compte-rendu contient quantité de nombres de la bonne longueur qui n'en sont
// pas : numéros FINESS, références de dossier, numéros de compte, codes-barres.
// Les anonymiser est une perte d'information, et sur un identifiant technique
// cela peut rendre le document inexploitable.
//
// Vérifier la clé de contrôle transforme une heuristique de forme en une
// décision quasi certaine : un nombre à 9 chiffres tiré au hasard n'a qu'une
// chance sur dix de satisfaire Luhn.
package checksum

import (
	"strings"
	"unicode"
)

// normalizeDigits retire les séparateurs usuels (espaces Unicode, points,
// tirets) et retourne la chaîne de chiffres. ok vaut false si un caractère
// n'est ni un chiffre ni un séparateur reconnu.
//
// Les séparateurs acceptés sont exactement ceux que tolèrent les patterns de
// pkg/ner : élargir cette liste reviendrait à valider des formes que la regex
// n'aurait jamais capturées.
func normalizeDigits(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '.', r == '-', r == ' ':
			// séparateur de présentation, ignoré
		default:
			return "", false
		}
	}
	return b.String(), true
}

// luhnSum retourne la somme de Luhn d'une chaîne de chiffres.
// Le doublement s'applique aux positions de rang pair depuis la droite
// (0-indexé : le dernier chiffre, la clé, n'est jamais doublé).
func luhnSum(digits string) int {
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum
}

// Luhn vérifie la formule de Luhn sur une chaîne de chiffres bruts.
// La chaîne doit être normalisée (chiffres uniquement) et non vide.
func Luhn(digits string) bool {
	if digits == "" {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return luhnSum(digits)%10 == 0
}

// siretLaPoste est le SIREN de La Poste. Ses établissements font exception à la
// règle de Luhn : leur SIRET est valide si la somme de ses chiffres est un
// multiple de 5. L'exception est historique et documentée par l'INSEE ; sans
// elle, tous les SIRET de La Poste seraient rejetés.
const siretLaPoste = "356000000"

// SIREN vérifie un numéro SIREN : 9 chiffres satisfaisant la formule de Luhn.
// Les séparateurs de présentation sont tolérés.
func SIREN(s string) bool {
	digits, ok := normalizeDigits(s)
	if !ok || len(digits) != 9 {
		return false
	}
	if digits == siretLaPoste {
		return true
	}
	return Luhn(digits)
}

// SIRET vérifie un numéro SIRET : 14 chiffres (SIREN 9 + NIC 5) satisfaisant la
// formule de Luhn, avec l'exception La Poste. Les séparateurs de présentation
// sont tolérés.
func SIRET(s string) bool {
	digits, ok := normalizeDigits(s)
	if !ok || len(digits) != 14 {
		return false
	}
	if strings.HasPrefix(digits, siretLaPoste) {
		sum := 0
		for i := 0; i < len(digits); i++ {
			sum += int(digits[i] - '0')
		}
		return sum%5 == 0
	}
	return Luhn(digits)
}
