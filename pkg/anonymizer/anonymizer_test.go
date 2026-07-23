package anonymizer

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
)

type mockRecognizer struct {
	entities []ner.Entity
}

func (m *mockRecognizer) Recognize(text string) ([]ner.Entity, error) {
	return m.entities, nil
}

// expandPH réécrit les placeholders legacy `[LABEL_N]` d'une chaîne attendue au
// format moderne `⟦LABEL_N_nonce⟧`, le nonce étant tiré au hasard par session.
func expandPH(nonce, s string) string {
	return legacyPlaceholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		return phOpen + m[1:len(m)-1] + "_" + nonce + phClose
	})
}

func TestAnonymize_TagReplace(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	sess := NewSession()
	result, err := anon.Anonymize("John Doe works at Acme.", WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	person := expandPH(sess.Nonce(), "[PERSON_1]")
	if !strings.Contains(result.Text, person) {
		t.Errorf("expected %s in result, got %q", person, result.Text)
	}

	if result.Mapping[person] != "John Doe" {
		t.Errorf("mapping incorrect: got %s", result.Mapping[person])
	}
}

func TestAnonymize_Redact(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John", Type: ner.TypePER, Start: 0, End: 4, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: Redact})

	result, err := anon.Anonymize("John lives here.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if !strings.Contains(result.Text, "████") {
		t.Errorf("expected ████ in result, got %q", result.Text)
	}
}

func TestAnonymize_Hash(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Alice", Type: ner.TypePER, Start: 0, End: 5, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: Hash})

	result, err := anon.Anonymize("Alice is here.", WithHashKey(testHashKey))
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if !strings.Contains(result.Text, phOpen+"PER_") {
		t.Errorf("expected ⟦PER_...⟧ in result, got %q", result.Text)
	}

	for placeholder := range result.Mapping {
		// ⟦ (3o) + "PER_" (4o) + 16 hex + ⟧ (3o)
		expectedLen := 3 + 4 + 16 + 3
		if len(placeholder) != expectedLen {
			t.Errorf("expected placeholder length %d, got %d", expectedLen, len(placeholder))
		}
	}
}

func TestAnonymize_ConsistentMap_Fuzzy(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
		{Text: "JOHN DOE", Type: ner.TypePER, Start: 20, End: 28, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: Consistent, ConsistentMap: true})

	sess := NewSession()
	result, err := anon.Anonymize("John Doe works with JOHN DOE.", WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	person := expandPH(sess.Nonce(), "[PERSON_1]")
	count := strings.Count(result.Text, person)
	if count != 2 {
		t.Errorf("expected 2 occurrences of %s, got %d", person, count)
	}

	firstOriginal, ok := result.OriginalToPlaceholder["John Doe"]
	if !ok {
		t.Error("expected OriginalToPlaceholder entry for 'John Doe'")
	}
	if firstOriginal != person {
		t.Errorf("expected %s, got %s", person, firstOriginal)
	}
}

func TestAnonymize_ConsistentMap_Exact(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
		{Text: "Jane Smith", Type: ner.TypePER, Start: 20, End: 30, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: Consistent, ConsistentMap: true})

	result, err := anon.Anonymize("John Doe and Jane Smith are here.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if len(result.Mapping) != 2 {
		t.Errorf("expected 2 mappings, got %d", len(result.Mapping))
	}

	if result.OriginalToPlaceholder["John Doe"] == "" {
		t.Error("expected OriginalToPlaceholder entry for John Doe")
	}
	if result.OriginalToPlaceholder["Jane Smith"] == "" {
		t.Error("expected OriginalToPlaceholder entry for Jane Smith")
	}
}

func TestAnonymize_FilterByType(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
		{Text: "Paris", Type: ner.TypeLOC, Start: 20, End: 25, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{
		Strategy:    TagReplace,
		EntityTypes: []ner.EntityType{ner.TypePER},
	})

	sess := NewSession()
	result, err := anon.Anonymize("John Doe lives in Paris.", WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	person := expandPH(sess.Nonce(), "[PERSON_1]")
	if !strings.Contains(result.Text, person) {
		t.Errorf("expected %s, got %q", person, result.Text)
	}

	if strings.Contains(result.Text, "LOCATION") {
		t.Error("expected LOCATION entity to be filtered out")
	}
}

func TestAnonymize_CustomReplacer(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}

	customCalled := false
	customReplacer := func(entity ner.Entity, index int) string {
		customCalled = true
		return "[CUSTOM_" + string(entity.Type) + "_" + itoa(index) + "]"
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{
		Strategy: TagReplace,
		CustomReplacers: map[ner.EntityType]ReplacerFunc{
			ner.TypePER: customReplacer,
		},
	})

	result, err := anon.Anonymize("John Doe is here.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if !customCalled {
		t.Error("custom replacer was not called")
	}

	if !strings.Contains(result.Text, "[CUSTOM_PER_1]") {
		t.Errorf("expected [CUSTOM_PER_1], got %q", result.Text)
	}
}

func TestAnonymize_EmptyText(t *testing.T) {
	rec := &mockRecognizer{entities: nil}
	anon := New(rec, Config{Strategy: TagReplace})

	result, err := anon.Anonymize("")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if result.Text != "" {
		t.Errorf("expected empty text, got %q", result.Text)
	}

	if len(result.Mapping) != 0 {
		t.Errorf("expected empty mapping, got %d entries", len(result.Mapping))
	}
}

func TestDeanonymize(t *testing.T) {
	rec := &mockRecognizer{entities: nil}
	anon := New(rec, Config{Strategy: TagReplace})

	anonymizedText := "[PERSON_1] works at [ORGANIZATION_1]"
	mapping := map[string]string{
		"[PERSON_1]":       "John Doe",
		"[ORGANIZATION_1]": "Acme",
	}

	result, err := anon.Deanonymize(anonymizedText, mapping)
	if err != nil {
		t.Fatalf("Deanonymize error: %v", err)
	}

	expected := "John Doe works at Acme"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestDeanonymize_WithConsistent(t *testing.T) {
	rec := &mockRecognizer{entities: nil}
	anon := New(rec, Config{Strategy: Consistent})

	anonymizedText := "[PERSON_1] and [PERSON_1] are friends"
	mapping := map[string]string{
		"[PERSON_1]": "John Doe",
	}

	result, err := anon.Deanonymize(anonymizedText, mapping)
	if err != nil {
		t.Fatalf("Deanonymize error: %v", err)
	}

	expected := "John Doe and John Doe are friends"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestAnonymize_MultipleEntities(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
		{Text: "Paris", Type: ner.TypeLOC, Start: 20, End: 25, Confidence: 1.0},
		{Text: "Acme", Type: ner.TypeORG, Start: 36, End: 39, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	sess := NewSession()
	result, err := anon.Anonymize("John Doe lives in Paris and works at Acme.", WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if len(result.Mapping) != 3 {
		t.Errorf("expected 3 mappings, got %d", len(result.Mapping))
	}

	nonce := sess.Nonce()
	if result.Mapping[expandPH(nonce, "[PERSON_1]")] != "John Doe" {
		t.Errorf("expected PERSON_1 -> John Doe")
	}
	if result.Mapping[expandPH(nonce, "[LOCATION_1]")] != "Paris" {
		t.Errorf("expected LOCATION_1 -> Paris")
	}
	if result.Mapping[expandPH(nonce, "[ORGANIZATION_1]")] != "Acme" {
		t.Errorf("expected ORGANIZATION_1 -> Acme")
	}
}

func TestAnonymize_BidirectionalMapping(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	sess := NewSession()
	result, err := anon.Anonymize("John Doe is here.", WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if len(result.Mapping) == 0 {
		t.Error("expected non-empty Mapping")
	}

	if len(result.OriginalToPlaceholder) == 0 {
		t.Error("expected non-empty OriginalToPlaceholder")
	}

	person := expandPH(sess.Nonce(), "[PERSON_1]")
	if result.OriginalToPlaceholder["John Doe"] != person {
		t.Errorf("expected OriginalToPlaceholder[John Doe] = %s, got %s",
			person, result.OriginalToPlaceholder["John Doe"])
	}
}

// --- Round-trip Anonymize → Deanonymize ---

// TestRoundTrip_SingleEntity vérifie qu'une entité unique est restaurée.
func TestRoundTrip_SingleEntity(t *testing.T) {
	original := "Jean Dupont habite à Paris."
	entities := []ner.Entity{
		spanEntity(original, "Jean Dupont", ner.TypePER),
	}
	assertRoundTrip(t, original, entities, Config{Strategy: TagReplace})
}

// TestRoundTrip_MultipleEntities vérifie la restauration de plusieurs entités
// de types différents (les remplacements sont appliqués en ordre inverse des offsets).
func TestRoundTrip_MultipleEntities(t *testing.T) {
	original := "Jean Dupont habite à Paris et travaille chez Acme."
	entities := []ner.Entity{
		spanEntity(original, "Jean Dupont", ner.TypePER),
		spanEntity(original, "Paris", ner.TypeLOC),
		spanEntity(original, "Acme", ner.TypeORG),
	}
	assertRoundTrip(t, original, entities, Config{Strategy: TagReplace})
}

// TestEnsureConsistency_CatchesMissedEntities vérifie que la passe de cohérence
// remplace les occurrences d'entité non détectées par le NER.
func TestEnsureConsistency_CatchesMissedEntities(t *testing.T) {
	original := "Marie est la responsable. Marie gère les finances."
	first := spanEntity(original, "Marie", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := expandPH(sess.Nonce(), "[PERSON_1] est la responsable. [PERSON_1] gère les finances.")
	if result.Text != expected {
		t.Errorf("consistency pass failed\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

// TestEnsureConsistency_TypeMismatch vérifie que la passe de cohérence
// uniformise les placeholders quand le même texte est détecté avec des types différents.
func TestEnsureConsistency_TypeMismatch(t *testing.T) {
	original := "Marie est ici. Marie est là."
	first := spanEntity(original, "Marie", ner.TypePER)
	second := spanEntityAfter(original, "Marie", first.End, ner.TypeLOC)
	rec := &mockRecognizer{entities: []ner.Entity{first, second}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	countPerson := strings.Count(result.Text, expandPH(sess.Nonce(), "[PERSON_1]"))
	countLocation := strings.Count(result.Text, expandPH(sess.Nonce(), "[LOCATION_1]"))
	if countPerson+countLocation != 2 {
		t.Errorf("expected 2 total placeholders, got PERSON=%d + LOCATION=%d", countPerson, countLocation)
	}
	if countPerson != 2 {
		t.Errorf("expected 2 [PERSON_1], got %d", countPerson)
	}
	if countLocation != 0 {
		t.Errorf("expected 0 [LOCATION_1], got %d", countLocation)
	}
}

// TestEnsureConsistency_DiscoveredButCorrect vérifie que la passe ne modifie pas
// un texte déjà correctement anonymisé.
func TestEnsureConsistency_AlreadyCorrect(t *testing.T) {
	original := "Jean Dupont et Jean Dupont."
	first := spanEntity(original, "Jean Dupont", ner.TypePER)
	second := spanEntityAfter(original, "Jean Dupont", first.End, ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first, second}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	if result.Text != expandPH(sess.Nonce(), "[PERSON_1] et [PERSON_1].") {
		t.Errorf("unexpected modification\n  got: %q", result.Text)
	}
}

// TestRoundTrip_ConsistentMap vérifie que les occurrences multiples de la même
// entité sont toutes remplacées puis restituées correctement.
func TestRoundTrip_ConsistentMap(t *testing.T) {
	original := "Jean Dupont a appelé Jean Dupont."
	first := spanEntity(original, "Jean Dupont", ner.TypePER)
	second := spanEntityAfter(original, "Jean Dupont", first.End, ner.TypePER)
	assertRoundTrip(t, original, []ner.Entity{first, second}, Config{Strategy: TagReplace, ConsistentMap: true})
}

// TestRoundTrip_NoEntities vérifie que le texte sans entité est retourné intact.
func TestRoundTrip_NoEntities(t *testing.T) {
	original := "Il fait beau aujourd'hui."
	assertRoundTrip(t, original, nil, Config{Strategy: TagReplace})
}

// TestRoundTrip_MultilineText vérifie le round-trip sur un texte multi-lignes,
// y compris avec des caractères multibyte (accents).
func TestRoundTrip_MultilineText(t *testing.T) {
	original := "Jean Dupont\nIngénieur\nTravaille à Paris."
	entities := []ner.Entity{
		spanEntity(original, "Jean Dupont", ner.TypePER),
		spanEntity(original, "Paris", ner.TypeLOC),
	}
	assertRoundTrip(t, original, entities, Config{Strategy: TagReplace})
}

// TestRoundTrip_AdjacentEntities vérifie que deux entités contiguës (séparées
// par un seul espace) sont restituées sans chevauchement ni perte de caractères.
func TestRoundTrip_AdjacentEntities(t *testing.T) {
	original := "Jean Paris."
	entities := []ner.Entity{
		spanEntity(original, "Jean", ner.TypePER),
		spanEntity(original, "Paris", ner.TypeLOC),
	}
	assertRoundTrip(t, original, entities, Config{Strategy: TagReplace})
}

// assertRoundTrip anonymise original, dé-anonymise le résultat et vérifie
// que le texte restauré est identique à l'original.
func assertRoundTrip(t *testing.T, original string, entities []ner.Entity, cfg Config) {
	t.Helper()

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, cfg)

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	restored, err := anon.Deanonymize(result.Text, result.Mapping)
	if err != nil {
		t.Fatalf("Deanonymize: %v", err)
	}

	if restored != original {
		t.Errorf("round-trip échoué\n  original  : %q\n  anonymisé : %q\n  restauré  : %q",
			original, result.Text, restored)
	}
}

// spanEntity construit une Entity dont les offsets byte sont calculés
// automatiquement depuis text. Panique si entityText est absent de text.
func spanEntity(text, entityText string, typ ner.EntityType) ner.Entity {
	return spanEntityAfter(text, entityText, 0, typ)
}

// spanEntityAfter construit une Entity en cherchant entityText dans text
// à partir de fromByte (pour gérer les occurrences multiples).
func spanEntityAfter(text, entityText string, fromByte int, typ ner.EntityType) ner.Entity {
	idx := strings.Index(text[fromByte:], entityText)
	if idx < 0 {
		panic("spanEntity: " + entityText + " not found in " + text)
	}
	start := fromByte + idx
	return ner.Entity{
		Text:       entityText,
		Type:       typ,
		Start:      start,
		End:        start + len(entityText),
		Confidence: 1.0,
	}
}

func itoa(i int) string {
	return string(rune('0' + i))
}

func TestPostProcess_CompletesSurnameAfterPer(t *testing.T) {
	original := "Pierre Martin est ici."
	first := spanEntity(original, "Pierre", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := expandPH(sess.Nonce(), "[PERSON_1] Martin est ici.")
	if result.Text != expected {
		t.Errorf("PostProcess failed\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

func TestPostProcess_NoSurname_NoChange(t *testing.T) {
	original := "Pierre Martin est ici."
	first := spanEntity(original, "Pierre Martin", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := expandPH(sess.Nonce(), "[PERSON_1] est ici.")
	if result.Text != expected {
		t.Errorf("unexpected modification\n  got: %q", result.Text)
	}
}

func TestPostProcess_AdjacentSurnameWithSpace(t *testing.T) {
	original := "Pierre Dupont est là."
	first := spanEntity(original, "Pierre", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := expandPH(sess.Nonce(), "[PERSON_1] Dupont est là.")
	if result.Text != expected {
		t.Errorf("PostProcess failed\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

func TestPostProcess_AccentedAdjacentSurname(t *testing.T) {
	original := "Bonjour, PierreSémard est ici."
	first := spanEntity(original, "Pierre", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	sess := NewSession()
	result, err := anon.Anonymize(original, WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	if !utf8.ValidString(result.Text) {
		t.Fatalf("texte anonymisé UTF-8 invalide : %q", result.Text)
	}
	expected := expandPH(sess.Nonce(), "Bonjour, [PERSON_1][PERSON_1] est ici.")
	if result.Text != expected {
		t.Errorf("nom accentué non absorbé\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

func TestAnonymize_SharedSurname_DoesNotConflict(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Marie Dupont", Type: ner.TypePER, Start: 0, End: 12, Confidence: 1.0},
		{Text: "Pierre Dupont", Type: ner.TypePER, Start: 14, End: 27, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	sess := NewSession()
	result, err := anon.Anonymize("Marie Dupont\n\nPierre Dupont", WithSession(sess))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	if strings.Contains(result.Text, expandPH(sess.Nonce(), "[PERSON_1] [PERSON_2]")) {
		t.Errorf("shared surname should not cause partial replacement\n  got: %q", result.Text)
	}
}
