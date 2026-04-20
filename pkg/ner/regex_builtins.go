package ner

import "regexp"

// Types d'entités pour les patterns intégrés.
const (
	TypeEMAIL EntityType = "EMAIL"
	TypeIPV4  EntityType = "IPV4"
	TypeIPV6  EntityType = "IPV6"
	TypeIBAN  EntityType = "IBAN"
	TypeSIRET EntityType = "SIRET"
	TypeSIREN EntityType = "SIREN"
	TypePHONE EntityType = "PHONE"
)

// BuiltinRegexPatterns contient les patterns prédéfinis, dans l'ordre de priorité.
// L'ordre est significatif : IPV6 avant IPV4, SIRET avant SIREN (évite les recouvrements).
var BuiltinRegexPatterns = []RegexPattern{
	{Re: reEmail, EntityType: TypeEMAIL, Confidence: 1.0},
	{Re: reIPv6, EntityType: TypeIPV6, Confidence: 1.0},
	{Re: reIPv4, EntityType: TypeIPV4, Confidence: 1.0},
	{Re: reIBAN, EntityType: TypeIBAN, Confidence: 1.0},
	{Re: reSIRET, EntityType: TypeSIRET, Confidence: 1.0},
	{Re: reSIREN, EntityType: TypeSIREN, Confidence: 1.0},
	{Re: rePhoneIntl, EntityType: TypePHONE, Confidence: 1.0},
	{Re: rePhoneFR, EntityType: TypePHONE, Confidence: 1.0},
}

var (
	reEmail = regexp.MustCompile(`[\w.+\-]+@[\w\-]+(?:\.[a-zA-Z]{2,})+`)

	// reIPv4 couvre les adresses IPv4 valides (0-255 par octet).
	reIPv4 = regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|[01]?\d\d?)(?:\.(?:25[0-5]|2[0-4]\d|[01]?\d\d?)){3}\b`)

	// reIPv6 couvre toutes les formes valides d'adresses IPv6 (RFC 5952).
	reIPv6 = regexp.MustCompile(
		`(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}` +
			`|(?:[0-9a-fA-F]{1,4}:){1,7}:` +
			`|(?:[0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}` +
			`|(?:[0-9a-fA-F]{1,4}:){1,5}(?::[0-9a-fA-F]{1,4}){1,2}` +
			`|(?:[0-9a-fA-F]{1,4}:){1,4}(?::[0-9a-fA-F]{1,4}){1,3}` +
			`|(?:[0-9a-fA-F]{1,4}:){1,3}(?::[0-9a-fA-F]{1,4}){1,4}` +
			`|(?:[0-9a-fA-F]{1,4}:){1,2}(?::[0-9a-fA-F]{1,4}){1,5}` +
			`|[0-9a-fA-F]{1,4}:(?::[0-9a-fA-F]{1,4}){1,6}` +
			`|:(?::[0-9a-fA-F]{1,4}){1,7}` +
			`|::`,
	)

	// reIBAN couvre les IBAN de tous pays, avec ou sans espaces inter-groupes.
	// \p{Zs} couvre espace ordinaire, insécable (U+00A0) et autres séparateurs Unicode.
	reIBAN = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?:\p{Zs}?[A-Z0-9]{4}){2,7}(?:\p{Zs}?[A-Z0-9]{1,4})?\b`)

	// reSIRET couvre le format avec ou sans espaces (3+3+3+5 chiffres).
	reSIRET = regexp.MustCompile(`\b\d{3}\p{Zs}?\d{3}\p{Zs}?\d{3}\p{Zs}?\d{5}\b`)

	// reSIREN doit être déclaré après SIRET : le bitmap covered empêche
	// les 9 premiers chiffres d'un SIRET d'être re-détectés comme SIREN.
	reSIREN = regexp.MustCompile(`\b\d{9}\b`)

	// rePhoneIntl couvre les numéros internationaux commençant par +
	// (E.164 avec séparateurs optionnels : espaces Unicode, points, tirets, parenthèses).
	rePhoneIntl = regexp.MustCompile(`\+[1-9]\d{0,2}(?:[\p{Zs}.\-()\[\]]?\d){6,12}`)

	// rePhoneFR couvre les numéros locaux français (0X XX XX XX XX).
	rePhoneFR = regexp.MustCompile(`\b0[1-9](?:[\p{Zs}.\-]?\d{2}){4}\b`)
)
