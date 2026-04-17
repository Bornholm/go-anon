package anonymizer

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

type mockRecognizer struct {
	entities []ner.Entity
}

func (m *mockRecognizer) Recognize(text string) ([]ner.Entity, error) {
	return m.entities, nil
}

func TestAnonymize_TagReplace(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	result, err := anon.Anonymize("John Doe works at Acme.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if !strings.Contains(result.Text, "[PERSON_1]") {
		t.Errorf("expected [PERSON_1] in result, got %q", result.Text)
	}

	if result.Mapping["[PERSON_1]"] != "John Doe" {
		t.Errorf("mapping incorrect: got %s", result.Mapping["[PERSON_1]"])
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

	result, err := anon.Anonymize("Alice is here.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if !strings.Contains(result.Text, "[PER_") {
		t.Errorf("expected [PER_...] in result, got %q", result.Text)
	}

	for placeholder := range result.Mapping {
		expectedLen := 12
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

	result, err := anon.Anonymize("John Doe works with JOHN DOE.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	count := strings.Count(result.Text, "[PERSON_1]")
	if count != 2 {
		t.Errorf("expected 2 occurrences of [PERSON_1], got %d", count)
	}

	firstOriginal, ok := result.OriginalToPlaceholder["John Doe"]
	if !ok {
		t.Error("expected OriginalToPlaceholder entry for 'John Doe'")
	}
	if firstOriginal != "[PERSON_1]" {
		t.Errorf("expected [PERSON_1], got %s", firstOriginal)
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

	result, err := anon.Anonymize("John Doe lives in Paris.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if !strings.Contains(result.Text, "[PERSON_1]") {
		t.Errorf("expected [PERSON_1], got %q", result.Text)
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

	result, err := anon.Anonymize("John Doe lives in Paris and works at Acme.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if len(result.Mapping) != 3 {
		t.Errorf("expected 3 mappings, got %d", len(result.Mapping))
	}

	if result.Mapping["[PERSON_1]"] != "John Doe" {
		t.Errorf("expected PERSON_1 -> John Doe")
	}
	if result.Mapping["[LOCATION_1]"] != "Paris" {
		t.Errorf("expected LOCATION_1 -> Paris")
	}
	if result.Mapping["[ORGANIZATION_1]"] != "Acme" {
		t.Errorf("expected ORGANIZATION_1 -> Acme")
	}
}

func TestAnonymize_BidirectionalMapping(t *testing.T) {
	entities := []ner.Entity{
		{Text: "John Doe", Type: ner.TypePER, Start: 0, End: 8, Confidence: 1.0},
	}

	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace})

	result, err := anon.Anonymize("John Doe is here.")
	if err != nil {
		t.Fatalf("Anonymize error: %v", err)
	}

	if len(result.Mapping) == 0 {
		t.Error("expected non-empty Mapping")
	}

	if len(result.OriginalToPlaceholder) == 0 {
		t.Error("expected non-empty OriginalToPlaceholder")
	}

	if result.OriginalToPlaceholder["John Doe"] != "[PERSON_1]" {
		t.Errorf("expected OriginalToPlaceholder[John Doe] = [PERSON_1], got %s",
			result.OriginalToPlaceholder["John Doe"])
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
	original := "Laetitia est la responsable. Laetitia gère les finances."
	first := spanEntity(original, "Laetitia", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := "[PERSON_1] est la responsable. [PERSON_1] gère les finances."
	if result.Text != expected {
		t.Errorf("consistency pass failed\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

// TestEnsureConsistency_TypeMismatch vérifie que la passe de cohérence
// uniformise les placeholders quand le même texte est détecté avec des types différents.
func TestEnsureConsistency_TypeMismatch(t *testing.T) {
	original := "Laetitia est ici. Laetitia est là."
	first := spanEntity(original, "Laetitia", ner.TypePER)
	second := spanEntityAfter(original, "Laetitia", first.End, ner.TypeLOC)
	rec := &mockRecognizer{entities: []ner.Entity{first, second}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	countPerson := strings.Count(result.Text, "[PERSON_1]")
	countLocation := strings.Count(result.Text, "[LOCATION_1]")
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

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	if result.Text != "[PERSON_1] et [PERSON_1]." {
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
	original := "Benjamin Bohard est ici."
	first := spanEntity(original, "Benjamin", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := "[PERSON_1] Bohard est ici."
	if result.Text != expected {
		t.Errorf("PostProcess failed\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

func TestPostProcess_NoSurname_NoChange(t *testing.T) {
	original := "Benjamin Bohard est ici."
	first := spanEntity(original, "Benjamin Bohard", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := "[PERSON_1] est ici."
	if result.Text != expected {
		t.Errorf("unexpected modification\n  got: %q", result.Text)
	}
}

func TestPostProcess_AdjacentSurnameWithSpace(t *testing.T) {
	original := "Arnaud Fornerot est la."
	first := spanEntity(original, "Arnaud", ner.TypePER)
	rec := &mockRecognizer{entities: []ner.Entity{first}}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass(), SurnameCompletionPass()}})

	result, err := anon.Anonymize(original)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	expected := "[PERSON_1] Fornerot est la."
	if result.Text != expected {
		t.Errorf("PostProcess failed\n  expected: %q\n  got:      %q", expected, result.Text)
	}
}

func TestAnonymize_SharedSurname_DoesNotConflict(t *testing.T) {
	entities := []ner.Entity{
		{Text: "Laetitia Fornerot", Type: ner.TypePER, Start: 0, End: 16, Confidence: 1.0},
		{Text: "Arnaud Fornerot", Type: ner.TypePER, Start: 18, End: 34, Confidence: 1.0},
	}
	rec := &mockRecognizer{entities: entities}
	anon := New(rec, Config{Strategy: TagReplace, ConsistentMap: true, Passes: []AnonymizePass{ConsistencyPass()}})

	result, err := anon.Anonymize("Laetitia Fornerot\n\nArnaud Fornerot")
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}

	if strings.Contains(result.Text, "[PERSON_1] [PERSON_2]") {
		t.Errorf("shared surname should not cause partial replacement\n  got: %q", result.Text)
	}
}
