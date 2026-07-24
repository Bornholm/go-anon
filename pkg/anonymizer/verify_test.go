package anonymizer

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

// leakyPass simule une passe de post-traitement défectueuse : elle « oublie »
// un remplacement et réinjecte la forme de surface dans la sortie. C'est
// exactement le mode de défaillance historique de SurnameCompletionPass.
func leakyPass(surface string) AnonymizePass {
	return func(original string, result *Result) string {
		placeholder := result.OriginalToPlaceholder[surface]
		if placeholder == "" {
			return result.Text
		}
		return strings.Replace(result.Text, placeholder, surface, 1)
	}
}

func TestGuarantee_StrictVerificationProducesNoOutput(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Jean Dupont", Type: ner.TypePER, Start: 0, End: 11, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{
		Strategy: TagReplace,
		Passes:   []AnonymizePass{leakyPass("Jean Dupont")},
	})

	res, err := anon.Anonymize("Jean Dupont habite ici.", WithStrictVerification())
	if err == nil {
		t.Fatalf("attendu une erreur, obtenu %q", res.Text)
	}
	if res != nil {
		t.Fatalf("le mode strict ne doit produire aucun Result, obtenu %q", res.Text)
	}
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("erreur inattendue : %v", err)
	}

	// L'erreur ne doit porter que des offsets et des comptes.
	if strings.Contains(err.Error(), "Jean") || strings.Contains(err.Error(), "Dupont") {
		t.Fatalf("l'erreur expose du contenu : %q", err.Error())
	}

	var verr *VerificationError
	if !errors.As(err, &verr) {
		t.Fatal("l'erreur devrait exposer le rapport via VerificationError")
	}
	if verr.Report.OK() {
		t.Fatal("le rapport devrait contenir au moins une fuite")
	}
}

func TestVerify_ReportModeKeepsOutputAndOffsets(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Jean Dupont", Type: ner.TypePER, Start: 8, End: 19, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{
		Strategy: TagReplace,
		Passes:   []AnonymizePass{leakyPass("Jean Dupont")},
	})

	const text = "Bonjour Jean Dupont."
	res, err := anon.Anonymize(text, WithVerification())
	if err != nil {
		t.Fatalf("mode rapport : erreur inattendue %v", err)
	}
	if res.Verification.OK() {
		t.Fatal("la fuite injectée aurait dû être signalée")
	}

	var found bool
	for _, leak := range res.Verification.Leaks {
		if leak.Kind != LeakKnownEntity {
			continue
		}
		found = true
		if got := res.Text[leak.Start:leak.End]; !strings.EqualFold(got, "Jean Dupont") {
			t.Errorf("offsets incorrects : [%d:%d] = %q", leak.Start, leak.End, got)
		}
		if leak.Type != ner.TypePER {
			t.Errorf("type attendu PER, obtenu %s", leak.Type)
		}
	}
	if !found {
		t.Fatalf("aucune fuite LeakKnownEntity dans %v", res.Verification.Leaks)
	}
}

// locate construit les entités en cherchant leurs formes de surface dans le
// texte. Des offsets écrits à la main dérivent à la moindre retouche d'une
// fixture accentuée, et le test validerait alors une absence de remplacement.
func locate(t *testing.T, text string, surfaces map[string]ner.EntityType) []ner.Entity {
	t.Helper()

	entities := make([]ner.Entity, 0, len(surfaces))
	for surface, typ := range surfaces {
		idx := strings.Index(text, surface)
		if idx < 0 {
			t.Fatalf("fixture : %q absent de %q", surface, text)
		}
		entities = append(entities, ner.Entity{
			Text: surface, Type: typ, Start: idx, End: idx + len(surface), Confidence: 1.0,
		})
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i].Start < entities[j].Start })
	return entities
}

func TestVerify_NoFalsePositiveOnCleanOutput(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		surfaces map[string]ner.EntityType
		strategy Strategy
	}{
		{
			name:     "fr/tag",
			text:     "Jean Dupont habite à Paris depuis 2019.",
			surfaces: map[string]ner.EntityType{"Jean Dupont": ner.TypePER, "Paris": ner.TypeLOC},
			strategy: TagReplace,
		},
		{
			name:     "en/redact",
			text:     "John Smith works at Acme Corp in London.",
			surfaces: map[string]ner.EntityType{"John Smith": ner.TypePER, "London": ner.TypeLOC},
			strategy: Redact,
		},
		{
			name:     "es/hash",
			text:     "María García vive en Madrid.",
			surfaces: map[string]ner.EntityType{"María García": ner.TypePER, "Madrid": ner.TypeLOC},
			strategy: Hash,
		},
		{
			name: "identifiants structurés",
			text: "Contact : jean@example.com — IBAN FR7630006000011234567890189 — 192.168.1.42",
			surfaces: map[string]ner.EntityType{
				"jean@example.com":            ner.TypeEMAIL,
				"FR7630006000011234567890189": ner.TypeIBAN,
				"192.168.1.42":                ner.TypeIPV4,
			},
			strategy: TagReplace,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &mockRecognizer{entities: locate(t, tc.text, tc.surfaces)}
			anon := New(rec, Config{Strategy: tc.strategy, ConsistentMap: true})

			res, err := anon.Anonymize(tc.text, WithStrictVerification(), WithHashKey(testHashKey))
			if err != nil {
				t.Fatalf("faux positif du mode strict : %v", err)
			}
			if !res.Verification.OK() {
				t.Fatalf("rapport non vide : %v", res.Verification.Leaks)
			}
		})
	}
}

// Un pseudonyme fabriqué (faux nom) ne doit pas être confondu avec une forme de
// surface résiduelle : il occupe une zone sûre.
func TestVerify_CustomReplacerNotFlagged(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Jean Dupont", Type: ner.TypePER, Start: 0, End: 11, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{
		Strategy: Consistent,
		CustomReplacers: map[ner.EntityType]ReplacerFunc{
			ner.TypePER: func(e ner.Entity, i int) string { return "Marc Durand" },
		},
	})

	res, err := anon.Anonymize("Jean Dupont habite ici.", WithStrictVerification())
	if err != nil {
		t.Fatalf("le pseudonyme généré ne doit pas déclencher le mode strict : %v", err)
	}
	if !strings.Contains(res.Text, "Marc Durand") {
		t.Fatalf("pseudonyme absent de %q", res.Text)
	}
}

// Le digest hexadécimal de la stratégie Hash ne doit pas être re-détecté comme
// un identifiant structuré (SIREN = 9 chiffres, par exemple).
func TestVerify_HashDigestNotFlaggedAsIdentifier(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Dupont", Type: ner.TypePER, Start: 0, End: 6, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: Hash})

	// Plusieurs clés : au moins l'une produira des digests riches en chiffres.
	for _, suffix := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		key := HashKey(strings.Repeat(suffix, MinHashKeyLen))
		res, err := anon.Anonymize("Dupont est là.", WithStrictVerification(), WithHashKey(key))
		if err != nil {
			t.Fatalf("clé %q : %v", suffix, err)
		}
		if !res.Verification.OK() {
			t.Fatalf("clé %q : %v", suffix, res.Verification.Leaks)
		}
	}
}

func TestVerify_SecretRedactionSatisfiesStrictMode(t *testing.T) {
	text := "Le jeton est " + fakeGitHubToken + " voilà."
	entities := []ner.Entity{
		{Text: fakeGitHubToken, Type: ner.TypeAPIKey, Start: 13, End: 13 + len(fakeGitHubToken), Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	res, err := anon.Anonymize(text, WithStrictVerification())
	if err != nil {
		t.Fatalf("le caviardage d'un secret doit satisfaire le mode strict : %v", err)
	}
	if strings.Contains(res.Text, fakeGitHubToken) {
		t.Fatal("le jeton subsiste dans la sortie")
	}
}

// Un secret non caviardé (remplacement neutralisé) doit être rattrapé par le
// re-passage des patterns de secrets, même s'il n'entre jamais dans le mapping.
func TestGuarantee_StrictModeCatchesUnreplacedSecret(t *testing.T) {
	text := "Le jeton est " + fakeGitHubToken + " voilà."
	entities := []ner.Entity{
		{Text: fakeGitHubToken, Type: ner.TypeAPIKey, Start: 13, End: 13 + len(fakeGitHubToken), Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{
		Strategy: TagReplace,
		// Passe défectueuse : elle restaure le jeton en clair.
		Passes: []AnonymizePass{func(original string, result *Result) string {
			return original
		}},
	})

	if _, err := anon.Anonymize(text, WithStrictVerification()); err == nil {
		t.Fatal("un jeton GitHub en clair doit faire échouer le mode strict")
	}
}

func TestVerify_InvalidUTF8(t *testing.T) {
	res := &Result{
		Text:                  "abc\xff\xfedef",
		OriginalToPlaceholder: map[string]string{},
	}
	report := Verify("abcdef", res)

	var found bool
	for _, l := range report.Leaks {
		if l.Kind == LeakInvalidUTF8 {
			found = true
		}
	}
	if !found {
		t.Fatalf("corruption UTF-8 non détectée : %v", report.Leaks)
	}
}

func TestVerify_ResidualPlaceholderInSource(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Paris", Type: ner.TypeLOC, Start: 0, End: 5, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	// WithEscapeCollisions neutralise les délimiteurs plutôt que d'échouer ; la
	// vérification doit tout de même signaler l'anomalie de la source.
	src := "Paris et un ⟦PERSON_1_abcdef⟧ suspect."
	res, err := anon.Anonymize(src, WithVerification(), WithEscapeCollisions())
	if err != nil {
		t.Fatalf("Anonymize : %v", err)
	}

	var found bool
	for _, l := range res.Verification.Leaks {
		if l.Kind == LeakResidualPlaceholderSource {
			found = true
		}
	}
	if !found {
		t.Fatalf("placeholder source non signalé : %v", res.Verification.Leaks)
	}
}

func TestVerify_WithVerifyPatternsNarrowsScan(t *testing.T) {
	// Aucune entité détectée : l'e-mail reste en clair dans la sortie.
	rec := &mockRecognizer{entities: nil}
	anon := New(rec, Config{Strategy: TagReplace})

	const text = "Écrire à jean@example.com."

	if _, err := anon.Anonymize(text, WithStrictVerification()); err == nil {
		t.Fatal("un e-mail en clair doit faire échouer le mode strict par défaut")
	}

	// Restreindre les patterns aux seuls secrets rend le mode strict tolérant
	// aux identifiants que le pipeline n'a délibérément pas cherchés.
	if _, err := anon.Anonymize(text, WithStrictVerification(),
		WithVerifyPatterns(ner.SecretPatterns()...)); err != nil {
		t.Fatalf("patterns restreints : %v", err)
	}
}
