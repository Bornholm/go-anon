package ner

import (
	"regexp"
	"strings"
	"testing"
)

// --- RegexEntityFilter ---

func TestRegexEntityFilter_BasicMatch(t *testing.T) {
	text := "Code postal 75001 ici"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\d{5}`), EntityType: "ZIPCODE"},
	})
	got := f(text, nil)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	e := got[0]
	if e.Text != "75001" || e.Type != "ZIPCODE" || e.Start != 12 || e.End != 17 {
		t.Errorf("entité inattendue : %+v", e)
	}
}

func TestRegexEntityFilter_DefaultConfidence(t *testing.T) {
	text := "foo@bar.com"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL, Confidence: 0},
	})
	got := f(text, nil)
	if len(got) != 1 || got[0].Confidence != 1.0 {
		t.Errorf("confidence attendue 1.0, got %v", got)
	}
}

func TestRegexEntityFilter_CustomConfidence(t *testing.T) {
	text := "192.168.1.1"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`), EntityType: TypeIPV4, Confidence: 0.9},
	})
	got := f(text, nil)
	if len(got) != 1 || got[0].Confidence != 0.9 {
		t.Errorf("confidence attendue 0.9, got %v", got)
	}
}

func TestRegexEntityFilter_NoOverlapWithNER(t *testing.T) {
	text := "Contact foo@bar.com ok"
	nerEntity := entityWithSpan("foo@bar.com", TypePER, 8, 19, 0.95)
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
	})
	got := f(text, []Entity{nerEntity})
	if len(got) != 1 || got[0].Type != TypePER {
		t.Errorf("l'entité NER ne doit pas être écrasée : %+v", got)
	}
}

func TestRegexEntityFilter_PartialOverlapIgnored(t *testing.T) {
	text := "abc@def.com"
	// NER couvre "abc@def" (bytes 0-7)
	nerEntity := entityWithSpan("abc@def", TypePER, 0, 7, 0.9)
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
	})
	got := f(text, []Entity{nerEntity})
	if len(got) != 1 || got[0].Type != TypePER {
		t.Errorf("chevauchement partiel doit être ignoré : %+v", got)
	}
}

func TestRegexEntityFilter_FirstPatternWins(t *testing.T) {
	text := "foo@bar.com"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: "FIRST"},
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: "SECOND"},
	})
	got := f(text, nil)
	if len(got) != 1 || got[0].Type != "FIRST" {
		t.Errorf("premier pattern doit gagner : %+v", got)
	}
}

func TestRegexEntityFilter_EmptyText(t *testing.T) {
	text := ""
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\d+`), EntityType: TypeIPV4},
	})
	got := f(text, nil)
	if len(got) != 0 {
		t.Errorf("texte vide doit retourner slice vide, got %v", got)
	}
}

func TestRegexEntityFilter_NoMatch(t *testing.T) {
	text := "aucun chiffre ici"
	existing := []Entity{entityWithSpan("aucun", TypePER, 0, 5, 0.8)}
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\d+`), EntityType: TypeIPV4},
	})
	got := f(text, existing)
	if len(got) != 1 || got[0].Text != "aucun" {
		t.Errorf("entités existantes inchangées si pas de match, got %v", got)
	}
}

func TestRegexEntityFilter_MultilineOffsets(t *testing.T) {
	text := "Ligne un\nfoo@bar.com\nLigne trois"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
	})
	got := f(text, nil)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	// "foo@bar.com" commence à l'octet 9 dans "Ligne un\n"
	if got[0].Start != 9 || got[0].End != 20 {
		t.Errorf("offsets incorrects : Start=%d End=%d", got[0].Start, got[0].End)
	}
}

func TestRegexEntityFilter_SortedOutput(t *testing.T) {
	text := "IP: 1.2.3.4 email: foo@bar.com"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
		{Re: regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`), EntityType: TypeIPV4},
	})
	got := f(text, nil)
	if len(got) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(got))
	}
	if got[0].Start > got[1].Start {
		t.Errorf("résultat non trié par Start : %+v", got)
	}
}

func TestRegexEntityFilter_EmptyTextUnchanged(t *testing.T) {
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\d+`), EntityType: TypeIPV4},
	})
	existing := []Entity{entityWithSpan("Paris", TypeLOC, 0, 5, 0.9)}
	got := f("", existing)
	if len(got) != 1 {
		t.Errorf("texte vide doit retourner entités inchangées, got %v", got)
	}
}

func TestRegexEntityFilter_NilEntities(t *testing.T) {
	text := "75001"
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`\d{5}`), EntityType: "ZIPCODE"},
	})
	got := f(text, nil)
	if len(got) != 1 || got[0].Text != "75001" {
		t.Errorf("nil entities doit fonctionner, got %v", got)
	}
}

// --- Patterns intégrés ---

func TestBuiltinPatterns_Email(t *testing.T) {
	cases := []string{"foo@bar.com", "jean.dupont+tag@example.co.uk", "user@sub.domain.org"}
	for _, c := range cases {
		if !reEmail.MatchString(c) {
			t.Errorf("reEmail ne matche pas %q", c)
		}
	}
}

func TestBuiltinPatterns_IPv4(t *testing.T) {
	valid := []string{"192.168.1.1", "10.0.0.0", "255.255.255.255", "0.0.0.0"}
	for _, c := range valid {
		if !reIPv4.MatchString(c) {
			t.Errorf("reIPv4 ne matche pas %q", c)
		}
	}
	invalid := []string{"256.1.1.1", "192.168.1", "999.0.0.1"}
	for _, c := range invalid {
		if reIPv4.MatchString(c) {
			t.Errorf("reIPv4 ne doit pas matcher %q", c)
		}
	}
}

func TestBuiltinPatterns_IPv6(t *testing.T) {
	cases := []string{
		"2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		"::1",
		"fe80::1",
		"2001:db8::1",
		"::ffff:192.0.2.1",
	}
	for _, c := range cases {
		if !reIPv6.MatchString(c) {
			t.Errorf("reIPv6 ne matche pas %q", c)
		}
	}
}

func TestBuiltinPatterns_IBAN(t *testing.T) {
	cases := []string{
		"FR7630006000011234567890189",
		"DE89370400440532013000",
		"GB29NWBK60161331926819",
	}
	for _, c := range cases {
		if !reIBAN.MatchString(c) {
			t.Errorf("reIBAN ne matche pas %q", c)
		}
	}
}

func TestBuiltinPatterns_SIRET(t *testing.T) {
	cases := []string{"12345678900042", "123 456 789 00042"}
	for _, c := range cases {
		if !reSIRET.MatchString(c) {
			t.Errorf("reSIRET ne matche pas %q", c)
		}
	}
}

func TestBuiltinPatterns_SIREN(t *testing.T) {
	if !reSIREN.MatchString("123456789") {
		t.Error("reSIREN ne matche pas un SIREN valide")
	}
}

func TestBuiltinPatterns_PhoneIntl(t *testing.T) {
	cases := []string{"+33612345678", "+1 555 123 4567", "+44 7911 123456", "+49 30 12345678"}
	for _, c := range cases {
		if !rePhoneIntl.MatchString(c) {
			t.Errorf("rePhoneIntl ne matche pas %q", c)
		}
	}
}

func TestBuiltinPatterns_PhoneFR(t *testing.T) {
	cases := []string{"0612345678", "06 12 34 56 78", "06.12.34.56.78", "06-12-34-56-78"}
	for _, c := range cases {
		if !rePhoneFR.MatchString(c) {
			t.Errorf("rePhoneFR ne matche pas %q", c)
		}
	}
}

// --- Support Submatch ---

func TestRegexEntityFilter_Submatch(t *testing.T) {
	text := "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.signature123"
	f := RegexEntityFilter([]RegexPattern{
		{Re: reBearerToken, EntityType: TypeAPIKey, Confidence: 0.99, Submatch: 1},
	})
	got := f(text, nil)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d: %+v", len(got), got)
	}
	e := got[0]
	// Doit contenir uniquement le jeton, pas "Bearer "
	if e.Text == "" || e.Text == text {
		t.Errorf("entité inattendue : %+v", e)
	}
	if len(e.Text) < 20 {
		t.Errorf("jeton trop court : %q", e.Text)
	}
	// Le texte de l'entité ne doit pas commencer par "Bearer"
	if len(e.Text) >= 6 && e.Text[:6] == "Bearer" {
		t.Errorf("l'entité ne doit pas inclure 'Bearer' : %q", e.Text)
	}
}

func TestRegexEntityFilter_SubmatchInvalid(t *testing.T) {
	text := "test 12345"
	// Submatch=2 mais le pattern n'a qu'un groupe → doit utiliser le match complet
	f := RegexEntityFilter([]RegexPattern{
		{Re: regexp.MustCompile(`(\d+)`), EntityType: "NUM", Submatch: 2},
	})
	got := f(text, nil)
	// Le comportement de repli doit retourner le match complet (groupe 0)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
}

// --- Patterns secrets ---

func TestSecretPatterns_JWT(t *testing.T) {
	valid := []string{
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0.abc123def456",
	}
	for _, c := range valid {
		if !reJWT.MatchString(c) {
			t.Errorf("reJWT ne matche pas %q", c)
		}
	}
}

func TestSecretPatterns_OpenAI(t *testing.T) {
	valid := []string{
		"sk-abcdefghijklmnopqrstuvwxyz123456",
		"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890ABCD",
	}
	for _, c := range valid {
		if !reOpenAIKey.MatchString(c) {
			t.Errorf("reOpenAIKey ne matche pas %q", c)
		}
	}
	invalid := []string{"sk-abc", "sk-1234"}
	for _, c := range invalid {
		if reOpenAIKey.MatchString(c) {
			t.Errorf("reOpenAIKey ne doit pas matcher %q", c)
		}
	}
}

func TestSecretPatterns_AWS(t *testing.T) {
	valid := []string{"AKIAIOSFODNN7EXAMPLE", "AKIAI44QH8DHBEXAMPLE"}
	for _, c := range valid {
		if !reAWSKeyID.MatchString(c) {
			t.Errorf("reAWSKeyID ne matche pas %q", c)
		}
	}
	invalid := []string{"AKIA123", "akiaIOSFODNN7EXAMPLE"}
	for _, c := range invalid {
		if reAWSKeyID.MatchString(c) {
			t.Errorf("reAWSKeyID ne doit pas matcher %q", c)
		}
	}
}

func TestSecretPatterns_GitHub(t *testing.T) {
	ghp := "ghp_" + strings.Repeat("a", 36)
	gpat := "github_pat_" + strings.Repeat("a", 82)
	for _, c := range []string{ghp, gpat} {
		if !reGitHubToken.MatchString(c) {
			t.Errorf("reGitHubToken ne matche pas %q", c)
		}
	}
}

func TestSecretPatterns_Slack(t *testing.T) {
	valid := []string{
		"xoxb-123456789012-123456789012-abcdefghijklmnopqrstuvwx",
		"xoxp-123456789012-123456789012-123456789012-abcdefghij",
	}
	for _, c := range valid {
		if !reSlackToken.MatchString(c) {
			t.Errorf("reSlackToken ne matche pas %q", c)
		}
	}
}

func TestSecretPatterns_Stripe(t *testing.T) {
	valid := []string{
		"sk_live_abcdefghijklmnopqrstuvwx",
		"sk_test_abcdefghijklmnopqrstuvwx",
		"rk_live_abcdefghijklmnopqrstuvwx",
	}
	for _, c := range valid {
		if !reStripeKey.MatchString(c) {
			t.Errorf("reStripeKey ne matche pas %q", c)
		}
	}
}

func TestSecretPatterns_Bearer(t *testing.T) {
	text := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1In0.sig12345678901234567"
	f := RegexEntityFilter(SecretPatterns())
	got := f(text, nil)
	found := false
	for _, e := range got {
		if e.Type == TypeJWT || e.Type == TypeAPIKey {
			found = true
			// Vérifie que l'entité ne contient pas "Bearer "
			if len(e.Text) >= 7 && e.Text[:7] == "Bearer " {
				t.Errorf("l'entité ne doit pas inclure le préfixe Bearer : %q", e.Text)
			}
		}
	}
	if !found {
		t.Errorf("aucun JWT ou API_KEY détecté dans : %q", text)
	}
}

func TestSecretPatterns_JWTBeforeBearer(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	text := "Authorization: Bearer " + jwt
	f := RegexEntityFilter(SecretPatterns())
	got := f(text, nil)
	// Le JWT doit être détecté en priorité
	for _, e := range got {
		if e.Type == TypeJWT {
			return
		}
	}
	t.Errorf("JWT doit être détecté en priorité sur Bearer générique : %+v", got)
}

func TestSecretPatterns_SecretKV(t *testing.T) {
	cases := []struct {
		text  string
		value string
	}{
		// password / passwd / passphrase
		{"DB_PASSWORD=Tr0ub4dor&3", "Tr0ub4dor&3"},
		{"SMTP_PASSWORD=key-abcdef1234567890", "key-abcdef1234567890"},
		{"passphrase=correct-horse-battery-staple", "correct-horse-battery-staple"},
		// pass (fragment court)
		{"DB_PASS=s3cr3t!", "s3cr3t!"},
		{"REDIS_PASS=r3d1s_p@ss", "r3d1s_p@ss"},
		// secret
		{"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		{"client_secret=mysecretvalue123", "mysecretvalue123"},
		{"SECRET_KEY=abcdefgh", "abcdefgh"},
		// token
		{"ACCESS_TOKEN=ya29.a0AfH6SMBxxx", "ya29.a0AfH6SMBxxx"},
		{"REFRESH_TOKEN=1//0gLhxxxxxxxxxx", "1//0gLhxxxxxxxxxx"},
		// credential / cred
		{"DB_CREDENTIALS=user:p@ssw0rd", "user:p@ssw0rd"},
		{"API_CRED=mycredential123", "mycredential123"},
		// private
		{"PRIVATE_KEY=-----BEGIN-RSA", "-----BEGIN-RSA"},
	}
	for _, c := range cases {
		f := RegexEntityFilter([]RegexPattern{
			{Re: reSecretKV, EntityType: TypeSecret, Confidence: 0.95, Submatch: 1},
		})
		got := f(c.text, nil)
		if len(got) != 1 {
			t.Errorf("[%s] attendu 1 entité, got %d: %+v", c.text, len(got), got)
			continue
		}
		if got[0].Text != c.value {
			t.Errorf("[%s] valeur attendue %q, got %q", c.text, c.value, got[0].Text)
		}
		if got[0].Type != TypeSecret {
			t.Errorf("[%s] type attendu SECRET, got %s", c.text, got[0].Type)
		}
	}
}

func TestSecretPatterns_SecretKV_NoMatchMidSentence(t *testing.T) {
	// Le pattern est ancré ^ : ne doit pas matcher en milieu de phrase.
	text := "Le mot de passe secret est password mais ce n'est pas un secret=valeur"
	f := RegexEntityFilter([]RegexPattern{
		{Re: reSecretKV, EntityType: TypeSecret, Confidence: 0.95, Submatch: 1},
	})
	got := f(text, nil)
	if len(got) != 0 {
		t.Errorf("ne doit pas matcher en milieu de phrase, got %+v", got)
	}
}

func TestSecretPatterns_SecretKV_ShortValueIgnored(t *testing.T) {
	// Valeurs < 4 chars ignorées (évite les faux positifs sur secret=ok, etc.)
	text := "DB_PASSWORD=abc"
	f := RegexEntityFilter([]RegexPattern{
		{Re: reSecretKV, EntityType: TypeSecret, Confidence: 0.95, Submatch: 1},
	})
	got := f(text, nil)
	if len(got) != 0 {
		t.Errorf("valeur trop courte ne doit pas être détectée, got %+v", got)
	}
}

func TestSecretPatterns_JWTBeforeSecretKV(t *testing.T) {
	// JWT_SECRET= : le JWT est détecté en priorité par reJWT, reSecretKV ne doit pas doubler.
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	text := "JWT_SECRET=" + jwt
	f := RegexEntityFilter(SecretPatterns())
	got := f(text, nil)
	jwtCount := 0
	for _, e := range got {
		if e.Type == TypeJWT {
			jwtCount++
		}
	}
	if jwtCount != 1 {
		t.Errorf("le JWT doit être détecté exactement une fois, got %d JWT dans %+v", jwtCount, got)
	}
}

// TestSIRETBeforeSIREN vérifie que dans les builtins, SIRET est détecté
// et empêche SIREN de matcher sur les mêmes bytes.
func TestSIRETBeforeSIREN(t *testing.T) {
	// SIRET dont la clé de Luhn est valide, comme le SIREN qu'il contient :
	// sans cela le test ne distinguerait pas la priorité des patterns d'un
	// simple rejet par la clé de contrôle.
	text := "SIRET: 32145678200002"
	f := RegexEntityFilter(BuiltinRegexPatterns)
	got := f(text, nil)
	for _, e := range got {
		if e.Type == TypeSIREN {
			t.Errorf("SIREN ne doit pas matcher à l'intérieur d'un SIRET : %+v", e)
		}
	}
	siretFound := false
	for _, e := range got {
		if e.Type == TypeSIRET {
			siretFound = true
		}
	}
	if !siretFound {
		t.Error("SIRET doit être détecté")
	}
}

// TestValidateRejectsMatch vérifie que le hook Validate écarte un match sans
// bloquer les patterns suivants sur la même zone.
func TestValidateRejectsMatch(t *testing.T) {
	patterns := []RegexPattern{
		{
			Re:         regexp.MustCompile(`\d{4}`),
			EntityType: "REJETE",
			Validate:   func(string) bool { return false },
		},
		{
			Re:         regexp.MustCompile(`\d{4}`),
			EntityType: "RETENU",
		},
	}
	got := RegexEntityFilter(patterns)("code 1234 fin", nil)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d : %+v", len(got), got)
	}
	if got[0].Type != "RETENU" {
		t.Errorf("le pattern rejeté ne doit pas réserver la zone, got %+v", got[0])
	}
}

// TestBuiltinChecksumFalsePositives couvre les formes qui trompent reIBAN —
// numéro de TVA intracommunautaire, référence de dossier — et que la clé
// mod-97 écarte. Ces formes ont été relevées sur des documents réels ; les
// valeurs ci-dessous sont synthétiques, seule la forme est reprise.
func TestBuiltinChecksumFalsePositives(t *testing.T) {
	tests := []struct {
		name string
		text string
		typ  EntityType
		want bool
	}{
		{"TVA intracom FR", "no TVA Intracommunautaire : FR12321456782", TypeIBAN, false},
		{"TVA intracom FR", "N° TVA intracom : FR34407123454", TypeIBAN, false},
		{"référence de dossier", "Référence CS4185039772", TypeIBAN, false},
		{"IBAN valide", "RIB N° : FR76 3000 1007 9412 3456 7890 185", TypeIBAN, true},
		{"IBAN valide", "IBAN: FR75 1273 9000 5012 3456 7890 143", TypeIBAN, true},
		{"SIRET valide", "SIRET : 321 456 782 00002 APE: 803Z", TypeSIRET, true},
		{"SIRET valide", "SIRET 407 123 454 00008", TypeSIRET, true},
		{"SIRET à clé fausse", "SIRET : 321 456 782 00003", TypeSIRET, false},
	}
	f := RegexEntityFilter(BuiltinRegexPatterns)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, e := range f(tt.text, nil) {
				if e.Type == tt.typ {
					found = true
				}
			}
			if found != tt.want {
				t.Errorf("détection %s dans %q = %v, want %v", tt.typ, tt.text, found, tt.want)
			}
		})
	}
}

// TestSIRENContextualPattern vérifie la variante contextuelle sur les formes
// rencontrées en pratique, et son rejet d'un identifiant sectoriel qui
// satisfait Luhn par coïncidence — cas d'un numéro FINESS, que seul le
// contexte permet d'écarter. Valeurs synthétiques.
func TestSIRENContextualPattern(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"RCS avec ville", "RCS : Dijon 321456782", true},
		{"SIREN explicite", "SIREN 321456782", true},
		{"R.C.S. pointé", "R.C.S. PARIS 407123454", true},
		{"identifiant sectoriel (faux positif du pattern nu)", "FINESS ET 602453193", false},
		{"numéro nu sans marqueur", "Référence 321456782", false},
		{"marqueur mais clé fausse", "SIREN 321456783", false},
	}
	f := RegexEntityFilter([]RegexPattern{SIRENContextualPattern})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := f(tt.text, nil)
			if (len(got) > 0) != tt.want {
				t.Errorf("détection SIREN dans %q = %v, want %v (%+v)", tt.text, len(got) > 0, tt.want, got)
			}
		})
	}
}
