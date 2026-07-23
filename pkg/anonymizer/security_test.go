package anonymizer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

// testHashKey est une clé HMAC de test (32 octets). Une vraie clé provient de
// GOANON_HASH_KEY, jamais du code source.
var testHashKey = HashKey(strings.Repeat("k", MinHashKeyLen))

// fakeGitHubToken imite un Personal Access Token GitHub (ghp_ + 36 caractères).
const fakeGitHubToken = "ghp_ZzZz0011223344556677889900aabbccdd"

// allStrategies énumère les stratégies pour les tests table-driven.
var allStrategies = []struct {
	name  string
	strat Strategy
	opts  []AnonymizeOption
}{
	{"TagReplace", TagReplace, nil},
	{"Redact", Redact, nil},
	{"Hash", Hash, []AnonymizeOption{WithHashKey(testHashKey)}},
	{"Consistent", Consistent, nil},
}

// --- S1 : les secrets n'entrent jamais dans le mapping ---

// TestGuarantee_SecretsNeverEnterMapping matérialise le critère d'acceptation du
// chantier S1 : pour les quatre stratégies, le secret est introuvable dans la
// sortie anonymisée, dans le mapping sérialisé, et dans la sortie de Deanonymize.
func TestGuarantee_SecretsNeverEnterMapping(t *testing.T) {
	original := "Jean Dupont a poussé avec " + fakeGitHubToken + " hier."

	for _, tc := range allStrategies {
		t.Run(tc.name, func(t *testing.T) {
			entities := []ner.Entity{
				spanEntity(original, "Jean Dupont", ner.TypePER),
				spanEntity(original, fakeGitHubToken, ner.TypeAPIKey),
			}
			anon := New(&mockRecognizer{entities: entities}, Config{
				Strategy:      tc.strat,
				ConsistentMap: true,
			})

			result, err := anon.Anonymize(original, tc.opts...)
			if err != nil {
				t.Fatalf("Anonymize: %v", err)
			}

			if strings.Contains(result.Text, fakeGitHubToken) {
				t.Error("le secret subsiste dans le texte anonymisé")
			}

			for placeholder, value := range result.Mapping {
				if strings.Contains(placeholder, fakeGitHubToken) || strings.Contains(value, fakeGitHubToken) {
					t.Error("le secret est présent dans Mapping")
				}
			}
			for value, placeholder := range result.OriginalToPlaceholder {
				if strings.Contains(placeholder, fakeGitHubToken) || strings.Contains(value, fakeGitHubToken) {
					t.Error("le secret est présent dans OriginalToPlaceholder")
				}
			}

			serialized, err := json.Marshal(result.Mapping)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if strings.Contains(string(serialized), fakeGitHubToken) {
				t.Error("le secret est présent dans le mapping JSON sérialisé")
			}

			restored, err := Deanonymize(result.Text, result.Mapping)
			if err != nil {
				t.Fatalf("Deanonymize: %v", err)
			}
			if strings.Contains(restored, fakeGitHubToken) {
				t.Error("Deanonymize a restauré le secret")
			}
		})
	}
}

// TestSecrets_NotExposedInResultEntities vérifie que la forme de surface d'un
// secret ne ressort pas dans Result.Entities, sauf opt-in explicite.
func TestSecrets_NotExposedInResultEntities(t *testing.T) {
	original := "clé " + fakeGitHubToken + " fin."
	entities := []ner.Entity{spanEntity(original, fakeGitHubToken, ner.TypeAPIKey)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: TagReplace})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if result.Entities[0].Text == fakeGitHubToken {
		t.Error("la forme de surface du secret est exposée dans Result.Entities")
	}

	exposed, err := anon.Anonymize(original, WithExposeSecrets(true))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if exposed.Entities[0].Text != fakeGitHubToken {
		t.Error("WithExposeSecrets(true) devrait conserver la forme de surface")
	}
}

// TestSecrets_NoStablePseudonym vérifie que deux secrets distincts reçoivent le
// même marqueur : un pseudonyme stable permettrait de corréler les usages.
func TestSecrets_NoStablePseudonym(t *testing.T) {
	tokenA := fakeGitHubToken
	tokenB := "ghp_AaAa9988776655443322110099zzyyxxww"
	original := tokenA + " puis " + tokenB
	entities := []ner.Entity{
		spanEntity(original, tokenA, ner.TypeAPIKey),
		spanEntity(original, tokenB, ner.TypeAPIKey),
	}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: Hash})

	result, err := anon.Anonymize(original, WithHashKey(testHashKey))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	marker := phOpen + string(ner.TypeAPIKey) + redactedSuffix + phClose
	if got := strings.Count(result.Text, marker); got != 2 {
		t.Errorf("attendu 2 marqueurs de caviardage identiques, obtenu %d dans %q", got, result.Text)
	}
}

// TestConsistencyPass_SecretDoesNotBlankText couvre le garde-fou 1.5 : une
// entité sans placeholder ne doit pas produire un remplacement par chaîne vide.
func TestConsistencyPass_SecretDoesNotBlankText(t *testing.T) {
	original := "token " + fakeGitHubToken + " et encore " + fakeGitHubToken + " voilà."
	first := spanEntity(original, fakeGitHubToken, ner.TypeAPIKey)
	second := spanEntityAfter(original, fakeGitHubToken, first.End, ner.TypeAPIKey)
	anon := New(&mockRecognizer{entities: []ner.Entity{first, second}}, Config{
		Strategy:      TagReplace,
		ConsistentMap: true,
		Passes:        []AnonymizePass{ConsistencyPass()},
	})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	marker := phOpen + string(ner.TypeAPIKey) + redactedSuffix + phClose
	expected := "token " + marker + " et encore " + marker + " voilà."
	if result.Text != expected {
		t.Errorf("texte corrompu par la passe de cohérence\n  attendu : %q\n  obtenu  : %q", expected, result.Text)
	}
}

// --- S2 : HMAC-SHA-256 avec clé ---

// TestHash_DeterministicPerKeyAndScope vérifie que le digest dépend à la fois de
// la clé et du scope : un scope par dossier casse la linkability inter-scopes.
func TestHash_DeterministicPerKeyAndScope(t *testing.T) {
	otherKey := HashKey(strings.Repeat("j", MinHashKeyLen))

	base := hashEntity(testHashKey, "", ner.TypePER, "Dupont")
	if again := hashEntity(testHashKey, "", ner.TypePER, "Dupont"); again != base {
		t.Error("même clé et même scope devraient produire le même digest")
	}
	if diff := hashEntity(otherKey, "", ner.TypePER, "Dupont"); diff == base {
		t.Error("une clé différente devrait produire un digest différent")
	}
	if diff := hashEntity(testHashKey, "dossier-42", ner.TypePER, "Dupont"); diff == base {
		t.Error("un scope différent devrait produire un digest différent")
	}
}

// TestHash_DomainSeparation vérifie que ("PER", "Ax") et ("PERA", "x") ne
// collisionnent pas — d'où l'octet nul entre les champs du HMAC.
func TestHash_DomainSeparation(t *testing.T) {
	a := hashEntity(testHashKey, "", ner.EntityType("PER"), "Ax")
	b := hashEntity(testHashKey, "", ner.EntityType("PERA"), "x")
	if a == b {
		t.Error("séparation de domaine absente : (PER, Ax) et (PERA, x) collisionnent")
	}

	c := hashEntity(testHashKey, "sco", ner.EntityType("pe"), "x")
	d := hashEntity(testHashKey, "s", ner.EntityType("cope"), "x")
	if c == d {
		t.Error("séparation de domaine absente entre scope et type")
	}
}

// TestHash_FailsClosedWithoutKey vérifie qu'aucune sortie n'est produite quand
// la stratégie Hash est utilisée sans clé.
func TestHash_FailsClosedWithoutKey(t *testing.T) {
	original := "Alice est ici."
	entities := []ner.Entity{spanEntity(original, "Alice", ner.TypePER)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: Hash})

	result, err := anon.Anonymize(original)
	if !errors.Is(err, ErrHashKeyRequired) {
		t.Fatalf("attendu ErrHashKeyRequired, obtenu %v", err)
	}
	if result != nil {
		t.Error("aucune sortie ne doit être produite en cas d'échec fail-closed")
	}

	// L'opt-in explicite reste possible pour les démonstrations.
	insecure, err := anon.Anonymize(original, WithInsecureHash())
	if err != nil {
		t.Fatalf("WithInsecureHash: %v", err)
	}
	if !strings.Contains(insecure.Text, phOpen+"PER_") {
		t.Errorf("attendu un placeholder haché, obtenu %q", insecure.Text)
	}
}

// TestHash_RejectsShortKey vérifie la longueur minimale de clé.
func TestHash_RejectsShortKey(t *testing.T) {
	original := "Alice est ici."
	entities := []ner.Entity{spanEntity(original, "Alice", ner.TypePER)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: Hash})

	if _, err := anon.Anonymize(original, WithHashKey(HashKey("trop court"))); !errors.Is(err, ErrHashKeyTooShort) {
		t.Fatalf("attendu ErrHashKeyTooShort, obtenu %v", err)
	}
}

// TestHash_NotReversibleWithoutMapping vérifie que Deanonymize sur une sortie
// Hash sans mapping échoue proprement — le HMAC n'est pas inversible.
func TestHash_NotReversibleWithoutMapping(t *testing.T) {
	original := "Alice est ici."
	entities := []ner.Entity{spanEntity(original, "Alice", ner.TypePER)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: Hash})

	result, err := anon.Anonymize(original, WithHashKey(testHashKey))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	if _, err := Deanonymize(result.Text, nil); !errors.Is(err, ErrIncompleteMapping) {
		t.Fatalf("attendu ErrIncompleteMapping, obtenu %v", err)
	}
}

func TestParseHashKey(t *testing.T) {
	raw := strings.Repeat("a", MinHashKeyLen)

	for _, encoded := range []string{
		"6161616161616161616161616161616161616161616161616161616161616161", // hex
		"YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=",                     // base64
	} {
		key, err := ParseHashKey(encoded)
		if err != nil {
			t.Fatalf("ParseHashKey(%q): %v", encoded, err)
		}
		if string(key) != raw {
			t.Errorf("clé décodée incorrecte pour %q", encoded)
		}
	}

	if _, err := ParseHashKey("00ff"); !errors.Is(err, ErrHashKeyTooShort) {
		t.Errorf("attendu ErrHashKeyTooShort, obtenu %v", err)
	}
	if _, err := ParseHashKey("pas une clé !!"); !errors.Is(err, ErrHashKeyFormat) {
		t.Errorf("attendu ErrHashKeyFormat, obtenu %v", err)
	}
}

// --- S3 : placeholders anti-collision et Deanonymize déterministe ---

// TestGuarantee_PlaceholderCollisionDetected vérifie qu'un texte source
// contenant déjà un placeholder ne peut pas corrompre silencieusement le
// round-trip : soit erreur, soit échappement explicite.
func TestGuarantee_PlaceholderCollisionDetected(t *testing.T) {
	original := "Voir " + phOpen + "PERSON_1_a3f9c2" + phClose + " puis Jean Dupont."
	entities := []ner.Entity{spanEntity(original, "Jean Dupont", ner.TypePER)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: TagReplace})

	if _, err := anon.Anonymize(original); !errors.Is(err, ErrPlaceholderCollision) {
		t.Fatalf("attendu ErrPlaceholderCollision, obtenu %v", err)
	}

	escaped, err := anon.Anonymize(original, WithEscapeCollisions())
	if err != nil {
		t.Fatalf("WithEscapeCollisions: %v", err)
	}
	if strings.Contains(escaped.Text, phOpen+"PERSON_1_a3f9c2"+phClose) {
		t.Error("le placeholder injecté n'a pas été neutralisé")
	}
	if !strings.Contains(escaped.Text, "[[PERSON_1_a3f9c2]]") {
		t.Errorf("échappement attendu, obtenu %q", escaped.Text)
	}
}

// TestDeanonymize_PrefixAmbiguity couvre 3.T2 : en mode legacy, `[PER_1]` est un
// préfixe de `[PER_10]`. Le remplacement doit rester identique à chaque exécution.
func TestDeanonymize_PrefixAmbiguity(t *testing.T) {
	mapping := map[string]string{
		"[PER_1]":   "Alice",
		"[PER_10]":  "Bob",
		"[PER_100]": "Charlie",
	}
	text := "[PER_100] a vu [PER_10] et [PER_1]."
	expected := "Charlie a vu Bob et Alice."

	for i := range 1000 {
		got, err := Deanonymize(text, mapping)
		if err != nil {
			t.Fatalf("Deanonymize: %v", err)
		}
		if got != expected {
			t.Fatalf("itération %d non déterministe\n  attendu : %q\n  obtenu  : %q", i, expected, got)
		}
	}
}

// TestDeanonymize_IncompleteMapping vérifie qu'un mapping tronqué est détecté
// plutôt que de laisser passer un placeholder en sortie.
func TestDeanonymize_IncompleteMapping(t *testing.T) {
	original := "Jean Dupont habite à Paris."
	entities := []ner.Entity{
		spanEntity(original, "Jean Dupont", ner.TypePER),
		spanEntity(original, "Paris", ner.TypeLOC),
	}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: TagReplace})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	truncated := map[string]string{
		expandPH(sess.Nonce(), "[PERSON_1]"): "Jean Dupont",
	}
	if _, err := Deanonymize(result.Text, truncated); !errors.Is(err, ErrIncompleteMapping) {
		t.Fatalf("attendu ErrIncompleteMapping, obtenu %v", err)
	}
}

// TestRoundTrip_Property couvre 3.T3 : Deanonymize(Anonymize(x)) == x sur un
// corpus fr/en/es et des entrées adversariales.
func TestRoundTrip_Property(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		surfaces []struct {
			text string
			typ  ner.EntityType
		}
	}{
		{
			name: "fr",
			text: "Jean Dupont habite à Paris et travaille chez Acme.",
			surfaces: []struct {
				text string
				typ  ner.EntityType
			}{{"Jean Dupont", ner.TypePER}, {"Paris", ner.TypeLOC}, {"Acme", ner.TypeORG}},
		},
		{
			name: "en",
			text: "John Doe moved to London to join Globex.",
			surfaces: []struct {
				text string
				typ  ner.EntityType
			}{{"John Doe", ner.TypePER}, {"London", ner.TypeLOC}, {"Globex", ner.TypeORG}},
		},
		{
			name: "es",
			text: "María Núñez vive en Sevilla y trabaja en Iniciativas.",
			surfaces: []struct {
				text string
				typ  ner.EntityType
			}{{"María Núñez", ner.TypePER}, {"Sevilla", ner.TypeLOC}, {"Iniciativas", ner.TypeORG}},
		},
		{
			name: "adversarial-brackets",
			text: "Log: [PER_1] [PER_10] — contact Jean Dupont.",
			surfaces: []struct {
				text string
				typ  ner.EntityType
			}{{"Jean Dupont", ner.TypePER}},
		},
		{
			name: "adversarial-multibyte",
			text: "Éloïse Bérard 🎉 à Genève.",
			surfaces: []struct {
				text string
				typ  ner.EntityType
			}{{"Éloïse Bérard", ner.TypePER}, {"Genève", ner.TypeLOC}},
		},
		{
			name: "adversarial-newlines",
			text: "Jean Dupont\n\tParis\r\nAcme",
			surfaces: []struct {
				text string
				typ  ner.EntityType
			}{{"Jean Dupont", ner.TypePER}, {"Paris", ner.TypeLOC}, {"Acme", ner.TypeORG}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entities := make([]ner.Entity, 0, len(tc.surfaces))
			for _, s := range tc.surfaces {
				entities = append(entities, spanEntity(tc.text, s.text, s.typ))
			}
			anon := New(&mockRecognizer{entities: entities}, Config{
				Strategy:      TagReplace,
				ConsistentMap: true,
			})

			result, err := anon.Anonymize(tc.text)
			if err != nil {
				t.Fatalf("Anonymize: %v", err)
			}
			restored, err := Deanonymize(result.Text, result.Mapping)
			if err != nil {
				t.Fatalf("Deanonymize: %v", err)
			}
			if restored != tc.text {
				t.Errorf("round-trip non identitaire\n  original  : %q\n  anonymisé : %q\n  restauré  : %q",
					tc.text, result.Text, restored)
			}
		})
	}
}

// TestPlaceholder_NonceIsolatesSessions vérifie que deux sessions produisent des
// placeholders distincts : un mapping ne peut pas être rejoué sur un autre document.
func TestPlaceholder_NonceIsolatesSessions(t *testing.T) {
	original := "Jean Dupont est ici."
	entities := []ner.Entity{spanEntity(original, "Jean Dupont", ner.TypePER)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: TagReplace})

	a, err := anon.Anonymize(original, WithSession(NewSession()))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	b, err := anon.Anonymize(original, WithSession(NewSession()))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if a.Text == b.Text {
		t.Error("deux sessions devraient produire des nonces — donc des placeholders — distincts")
	}
}

// TestPlaceholder_LegacyFormat vérifie la compatibilité descendante.
func TestPlaceholder_LegacyFormat(t *testing.T) {
	original := "Jean Dupont est ici."
	entities := []ner.Entity{spanEntity(original, "Jean Dupont", ner.TypePER)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: TagReplace})

	result, err := anon.Anonymize(original, WithLegacyPlaceholders())
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if result.Text != "[PERSON_1] est ici." {
		t.Errorf("format legacy attendu, obtenu %q", result.Text)
	}
}

// TestErrors_CarryOffsetsNotContent vérifie la règle S1.4 : les erreurs portent
// des offsets et des types, jamais le contenu du texte traité.
func TestErrors_CarryOffsetsNotContent(t *testing.T) {
	const probe = "XZLEAKPROBE"
	original := probe + " " + phOpen + "PERSON_1_a3f9c2" + phClose + " " + fakeGitHubToken
	entities := []ner.Entity{spanEntity(original, fakeGitHubToken, ner.TypeAPIKey)}
	anon := New(&mockRecognizer{entities: entities}, Config{Strategy: TagReplace})

	_, err := anon.Anonymize(original)
	if err == nil {
		t.Fatal("collision attendue")
	}
	if strings.Contains(err.Error(), probe) || strings.Contains(err.Error(), fakeGitHubToken) {
		t.Errorf("l'erreur divulgue du contenu : %v", err)
	}
}
