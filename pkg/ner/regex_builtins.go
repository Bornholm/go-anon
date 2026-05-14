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

// SecretPatterns retourne les patterns prédéfinis pour les jetons d'authentification,
// clés d'API et mots de passe en format clé-valeur.
// L'ordre est significatif : patterns vendeurs spécifiques en premier, puis KV générique en dernier.
func SecretPatterns() []RegexPattern {
	return []RegexPattern{
		{Re: reJWT, EntityType: TypeJWT, Confidence: 0.99},
		{Re: reOpenAIKey, EntityType: TypeAPIKey, Confidence: 0.99},
		{Re: reAWSKeyID, EntityType: TypeAPIKey, Confidence: 0.99},
		{Re: reGitHubToken, EntityType: TypeAPIKey, Confidence: 0.99},
		{Re: reSlackToken, EntityType: TypeAPIKey, Confidence: 0.99},
		{Re: reStripeKey, EntityType: TypeAPIKey, Confidence: 0.99},
		// Bearer : Submatch=1 pour n'anonymiser que le jeton, pas le mot "Bearer".
		{Re: reBearerToken, EntityType: TypeAPIKey, Confidence: 0.99, Submatch: 1},
		// Mots de passe et secrets en format KV : ancré en début de ligne, Submatch=1
		// pour n'anonymiser que la valeur, jamais le nom de la variable.
		{Re: reSecretKV, EntityType: TypeSecret, Confidence: 0.95, Submatch: 1},
	}
}

// WithBuiltinSecretPatterns active la détection des jetons d'authentification
// et clés d'API courants (JWT, OpenAI, AWS, GitHub, Slack, Stripe, Bearer).
func WithBuiltinSecretPatterns() RecognizerOption {
	return WithRegexPatterns(SecretPatterns()...)
}

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
	// reJWT couvre les JSON Web Tokens (3 segments base64url séparés par des points).
	// Le header JWT est toujours du JSON débuté par "{", dont la forme base64url commence par "eyJ".
	reJWT = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]*`)

	// reOpenAIKey couvre les clés secrètes OpenAI (sk-, sk-proj-, etc.).
	reOpenAIKey = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]{20,}\b`)

	// reAWSKeyID couvre les identifiants de clé d'accès AWS IAM.
	reAWSKeyID = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)

	// reGitHubToken couvre les Personal Access Tokens classiques (ghp_) et fine-grained (github_pat_).
	reGitHubToken = regexp.MustCompile(`\b(?:ghp_[a-zA-Z0-9]{36}|github_pat_[a-zA-Z0-9_]{82})\b`)

	// reSlackToken couvre les tokens bot, user, app et webhook Slack (xoxb-, xoxp-, xoxa-, xoxr-, xoxs-).
	reSlackToken = regexp.MustCompile(`\bxox[baprs]-[0-9a-zA-Z-]{20,}\b`)

	// reStripeKey couvre les clés secrètes et restreintes Stripe (live et test).
	reStripeKey = regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[a-zA-Z0-9]{24,}\b`)

	// reBearerToken couvre les jetons Bearer dans les en-têtes HTTP Authorization.
	// Submatch=1 permet d'anonymiser uniquement le jeton, sans toucher au mot "Bearer".
	reBearerToken = regexp.MustCompile(`(?i)Bearer\s+([A-Za-z0-9_\-.+/=]{20,})`)

	// reSecretKV détecte les valeurs de variables dont le nom contient un fragment sensible.
	// Fragments couverts (ordre : plus long avant plus court pour éviter les matches partiels) :
	//   password · passphrase · passwd · pass
	//   credential · cred
	//   private · secret · token
	// Exemples : DB_PASSWORD, JWT_SECRET, ACCESS_TOKEN, PRIVATE_KEY, DB_CRED, REDIS_PASS...
	// Ancré en début de ligne ((?m)^) pour éviter les faux positifs en milieu de phrase.
	// Submatch=1 capture uniquement la valeur ; le nom de variable reste intact.
	reSecretKV = regexp.MustCompile(`(?im)^(?:[a-zA-Z_]\w*)?(?:password|passphrase|passwd|pass|credential|cred|private|secret|token)\w*\s*[=:]\s*(\S{4,})`)

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
