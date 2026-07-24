package anonymizer

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
)

// regexRecognizer est un Recognizer dont la sortie dépend réellement du texte —
// contrairement à mockRecognizer, qui retourne des entités figées. Le fuzzing
// exige cette dépendance : c'est elle qui fait varier les chemins de code.
// Il n'utilise aucun modèle CRF, ce qui garde les campagnes rapides.
type regexRecognizer struct{ patterns []ner.RegexPattern }

func newRegexRecognizer() *regexRecognizer {
	patterns := append([]ner.RegexPattern{}, ner.BuiltinRegexPatterns...)
	return &regexRecognizer{patterns: append(patterns, ner.SecretPatterns()...)}
}

func (r *regexRecognizer) Recognize(text string) ([]ner.Entity, error) {
	return ner.RegexEntityFilter(r.patterns)(text, nil), nil
}

// fuzzSeeds couvre les familles d'entrées qui ont historiquement cassé le
// pipeline : accents, délimiteurs de placeholder, secrets, UTF-8 tronqué.
var fuzzSeeds = []string{
	"",
	"Jean Dupont habite à Paris.",
	"José Müller — Kraków, Ćwikła",
	"Contact : jean@example.com, tél. 01 23 45 67 89",
	"IBAN FR7630006000011234567890189 et SIRET 732 829 320 00074",
	"⟦PERSON_1_abcdef⟧ est un placeholder pré-injecté",
	"[PERSON_1] ancien format [PERSON_10] préfixe ambigu",
	"DB_PASSWORD=s3cr3t-tr3s-l0ng\nAPI_KEY=ghp_ZzZz0011223344556677889900aabbccddef",
	"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc",
	"\xff\xfe tronqué \xc3",
	"a\x00b\nc\r\nd",
	strings.Repeat("Élodie ", 40),
}

// containsIsolated signale une occurrence de needle délimitée par des
// non-lettres/non-chiffres — c'est-à-dire une occurrence qui aurait dû être
// remplacée, par opposition à un fragment d'un mot plus long.
func containsIsolated(haystack, needle string) bool {
	for pos := 0; ; {
		idx := strings.Index(haystack[pos:], needle)
		if idx < 0 {
			return false
		}
		start := idx + pos
		if isWordBoundary(haystack, start, start+len(needle)) {
			return true
		}
		pos = start + 1
	}
}

// FuzzAnonymizeInvariants vérifie les invariants structurels du pipeline sur des
// entrées arbitraires.
func FuzzAnonymizeInvariants(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}

	rec := newRegexRecognizer()
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true})

	f.Fuzz(func(t *testing.T, input string) {
		res, err := anon.Anonymize(input, WithStrictVerification())
		if err != nil {
			// Le fail-closed est un résultat acceptable : c'est même l'issue
			// attendue sur une entrée qui contient déjà un placeholder.
			return
		}

		// Invariant 1 : l'anonymisation ne dégrade jamais un encodage valide.
		// Un remplacement au milieu d'une séquence multi-octets se manifeste ici.
		if utf8.ValidString(input) && !utf8.ValidString(res.Text) {
			t.Fatal("sortie UTF-8 invalide pour une entrée valide")
		}

		// Invariant 2 : aucune occurrence isolée d'une forme de surface du
		// mapping ne subsiste. Contrôle indépendant de Verify : scan direct,
		// sans zones sûres, sans normalisation de casse.
		//
		// La frontière de mot est nécessaire, pas cosmétique : « 000000000 »
		// détecté comme SIREN reste un sous-mot de « 0000000000 » resté en
		// clair, et ce n'est pas une fuite de l'entité.
		for placeholder, surface := range res.Mapping {
			if surface == "" || strings.Contains(placeholder, surface) {
				continue
			}
			if containsIsolated(res.Text, surface) {
				t.Fatalf("forme de surface résiduelle (%d octets)", len(surface))
			}
		}

		// Invariant 3 : les secrets ne sont jamais consignés.
		for _, e := range res.Entities {
			if ner.IsSecretType(e.Type) {
				if _, ok := res.OriginalToPlaceholder[e.Text]; ok {
					t.Fatalf("secret présent dans le mapping, type=%s", e.Type)
				}
			}
		}

		// Invariant 4 : Deanonymize ne panique jamais et ne laisse aucun
		// placeholder réversible derrière lui.
		back, err := Deanonymize(res.Text, res.Mapping)
		if err != nil {
			return
		}
		if idx := findResidualPlaceholder(back); idx >= 0 {
			t.Fatalf("placeholder résiduel après Deanonymize (offset %d)", idx)
		}
	})
}

// FuzzRoundTrip isole la propriété d'identité du round-trip.
//
// Les passes de post-traitement en sont exclues : ConsistencyPass remplace des
// occurrences non détectées en normalisant la casse, ce qui rend la restauration
// légitimement non bit-à-bit. L'identité n'est un invariant que sur le
// remplacement principal, aux offsets exacts.
func FuzzRoundTrip(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}

	rec := newRegexRecognizer()
	anon := New(rec, Config{
		Strategy: TagReplace,
		Passes:   []AnonymizePass{},
	})

	f.Fuzz(func(t *testing.T, input string) {
		res, err := anon.Anonymize(input)
		if err != nil {
			return
		}

		// Les secrets sont irréversibles par conception : leur présence exclut
		// l'identité du round-trip.
		for _, e := range res.Entities {
			if ner.IsSecretType(e.Type) {
				return
			}
		}

		back, err := Deanonymize(res.Text, res.Mapping)
		if err != nil {
			t.Fatalf("Deanonymize d'un mapping complet a échoué : %v", err)
		}
		if back != input {
			t.Fatalf("round-trip non identitaire (%d octets en entrée, %d en sortie, %d entités)",
				len(input), len(back), len(res.Entities))
		}
	})
}

// FuzzDeanonymize soumet des mappings adversariaux : préfixes ambigus,
// placeholders imbriqués, clés vides, valeurs se re-matchant elles-mêmes.
func FuzzDeanonymize(f *testing.F) {
	f.Add("⟦PERSON_1_aaaaaa⟧ et ⟦PERSON_10_aaaaaa⟧", "⟦PERSON_1_aaaaaa⟧\tAlice\n⟦PERSON_10_aaaaaa⟧\tBob")
	f.Add("[PER_1][PER_10]", "[PER_1]\tX\n[PER_10]\tY")
	f.Add("texte sans placeholder", "")
	f.Add("⟦A_1_bbbbbb⟧", "⟦A_1_bbbbbb⟧\t⟦A_1_bbbbbb⟧")
	f.Add("⟦A_1_bbbbbb⟧", "\t")

	f.Fuzz(func(t *testing.T, text, rawMapping string) {
		mapping := make(map[string]string)
		for _, line := range strings.Split(rawMapping, "\n") {
			key, value, ok := strings.Cut(line, "\t")
			if !ok || key == "" {
				continue
			}
			mapping[key] = value
		}

		first, err1 := Deanonymize(text, mapping)
		second, err2 := Deanonymize(text, mapping)

		// Invariant : déterminisme. L'ancienne implémentation itérait une map Go,
		// ce qui rendait la sortie dépendante de l'ordre de parcours.
		if (err1 == nil) != (err2 == nil) || first != second {
			t.Fatal("Deanonymize non déterministe")
		}
	})
}

// FuzzVerify garantit que la vérification ne panique jamais, quelles que soient
// les incohérences entre le texte et le mapping qu'on lui présente.
func FuzzVerify(f *testing.F) {
	f.Add("Jean Dupont", "⟦PERSON_1_aaaaaa⟧ habite ici", "Jean Dupont")
	f.Add("", "", "")
	f.Add("\xff\xfe", "\xff\xfe", "\xff")
	f.Add("jean@example.com", "jean@example.com", "jean@example.com")

	f.Fuzz(func(t *testing.T, original, output, surface string) {
		res := &Result{
			Text:                  output,
			Mapping:               map[string]string{"⟦PERSON_1_aaaaaa⟧": surface},
			OriginalToPlaceholder: map[string]string{surface: "⟦PERSON_1_aaaaaa⟧"},
		}

		report := Verify(original, res)

		// Les offsets rapportés doivent rester indexables dans la sortie : un
		// rapport qui panique à la lecture serait inexploitable.
		for _, leak := range report.Leaks {
			if leak.Kind == LeakResidualPlaceholderSource {
				if leak.Start < 0 || leak.End > len(original) || leak.Start > leak.End {
					t.Fatalf("offsets source hors bornes : %v", leak)
				}
				continue
			}
			if leak.Start < 0 || leak.End > len(output) || leak.Start > leak.End {
				t.Fatalf("offsets de sortie hors bornes : %v", leak)
			}
		}
	})
}
