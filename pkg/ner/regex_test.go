package ner

import (
	"regexp"
	"testing"
)

func makeText(s string) func() string { return func() string { return s } }

// --- RegexEntityFilter ---

func TestRegexEntityFilter_BasicMatch(t *testing.T) {
	text := "Code postal 75001 ici"
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\d{5}`), EntityType: "ZIPCODE"},
	})
	got := f(nil)
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
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL, Confidence: 0},
	})
	got := f(nil)
	if len(got) != 1 || got[0].Confidence != 1.0 {
		t.Errorf("confidence attendue 1.0, got %v", got)
	}
}

func TestRegexEntityFilter_CustomConfidence(t *testing.T) {
	text := "192.168.1.1"
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`), EntityType: TypeIPV4, Confidence: 0.9},
	})
	got := f(nil)
	if len(got) != 1 || got[0].Confidence != 0.9 {
		t.Errorf("confidence attendue 0.9, got %v", got)
	}
}

func TestRegexEntityFilter_NoOverlapWithNER(t *testing.T) {
	text := "Contact foo@bar.com ok"
	nerEntity := entityWithSpan("foo@bar.com", TypePER, 8, 19, 0.95)
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
	})
	got := f([]Entity{nerEntity})
	if len(got) != 1 || got[0].Type != TypePER {
		t.Errorf("l'entité NER ne doit pas être écrasée : %+v", got)
	}
}

func TestRegexEntityFilter_PartialOverlapIgnored(t *testing.T) {
	text := "abc@def.com"
	// NER couvre "abc@def" (bytes 0-7)
	nerEntity := entityWithSpan("abc@def", TypePER, 0, 7, 0.9)
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
	})
	got := f([]Entity{nerEntity})
	if len(got) != 1 || got[0].Type != TypePER {
		t.Errorf("chevauchement partiel doit être ignoré : %+v", got)
	}
}

func TestRegexEntityFilter_FirstPatternWins(t *testing.T) {
	text := "foo@bar.com"
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: "FIRST"},
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: "SECOND"},
	})
	got := f(nil)
	if len(got) != 1 || got[0].Type != "FIRST" {
		t.Errorf("premier pattern doit gagner : %+v", got)
	}
}

func TestRegexEntityFilter_EmptyText(t *testing.T) {
	f := RegexEntityFilter(makeText(""), []RegexPattern{
		{Re: regexp.MustCompile(`\d+`), EntityType: TypeIPV4},
	})
	got := f(nil)
	if len(got) != 0 {
		t.Errorf("texte vide doit retourner slice vide, got %v", got)
	}
}

func TestRegexEntityFilter_NoMatch(t *testing.T) {
	text := "aucun chiffre ici"
	existing := []Entity{entityWithSpan("aucun", TypePER, 0, 5, 0.8)}
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\d+`), EntityType: TypeIPV4},
	})
	got := f(existing)
	if len(got) != 1 || got[0].Text != "aucun" {
		t.Errorf("entités existantes inchangées si pas de match, got %v", got)
	}
}

func TestRegexEntityFilter_MultilineOffsets(t *testing.T) {
	text := "Ligne un\nfoo@bar.com\nLigne trois"
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
	})
	got := f(nil)
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
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\S+@\S+`), EntityType: TypeEMAIL},
		{Re: regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`), EntityType: TypeIPV4},
	})
	got := f(nil)
	if len(got) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(got))
	}
	if got[0].Start > got[1].Start {
		t.Errorf("résultat non trié par Start : %+v", got)
	}
}

func TestRegexEntityFilter_NilGetText(t *testing.T) {
	f := RegexEntityFilter(nil, []RegexPattern{
		{Re: regexp.MustCompile(`\d+`), EntityType: TypeIPV4},
	})
	existing := []Entity{entityWithSpan("Paris", TypeLOC, 0, 5, 0.9)}
	got := f(existing)
	if len(got) != 1 {
		t.Errorf("getText nil doit retourner entités inchangées, got %v", got)
	}
}

func TestRegexEntityFilter_NilEntities(t *testing.T) {
	text := "75001"
	f := RegexEntityFilter(makeText(text), []RegexPattern{
		{Re: regexp.MustCompile(`\d{5}`), EntityType: "ZIPCODE"},
	})
	got := f(nil)
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

// TestSIRETBeforeSIREN vérifie que dans les builtins, SIRET est détecté
// et empêche SIREN de matcher sur les mêmes bytes.
func TestSIRETBeforeSIREN(t *testing.T) {
	text := "SIRET: 12345678900042"
	f := RegexEntityFilter(makeText(text), BuiltinRegexPatterns)
	got := f(nil)
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
