package ner

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/features"
)

// testFirstNames construit un gazetteer de prénoms pour les tests.
func testFirstNames(names ...string) *features.Gazetteer {
	g, _ := features.LoadGazetteer("firstnames", strings.NewReader(strings.Join(names, "\n")))
	return g
}

// helpers

func entity(text string, typ EntityType, confidence float64) Entity {
	return Entity{Text: text, Type: typ, Start: 0, End: len(text), Confidence: confidence}
}

func entityWithSpan(text string, typ EntityType, start, end int, confidence float64) Entity {
	return Entity{Text: text, Type: typ, Start: start, End: end, Confidence: confidence}
}

func buildEntities(text string, specs []struct {
	substr string
	typ    EntityType
	conf   float64
}) []Entity {
	entities := make([]Entity, 0, len(specs))
	pos := 0
	for _, spec := range specs {
		idx := findSubstr(text, spec.substr, pos)
		if idx < 0 {
			panic("buildEntities: " + spec.substr + " not found in " + text)
		}
		entities = append(entities, Entity{
			Text:       spec.substr,
			Type:       spec.typ,
			Start:      idx,
			End:        idx + len(spec.substr),
			Confidence: spec.conf,
		})
		pos = idx + len(spec.substr)
	}
	return entities
}

func findSubstr(s, substr string, from int) int {
	for i := from; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// --- MinConfidenceFilter ---

func TestMinConfidenceFilter_RemovesBelow(t *testing.T) {
	entities := []Entity{
		entity("Jean Dupont", TypePER, 0.9),
		entity("Ingénieur", TypePER, 0.3),
		entity("Paris", TypeLOC, 0.8),
	}
	got := MinConfidenceFilter(0.5)("", entities)
	if len(got) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(got))
	}
	if got[0].Text != "Jean Dupont" || got[1].Text != "Paris" {
		t.Errorf("entités inattendues : %v", got)
	}
}

func TestMinConfidenceFilter_KeepsExact(t *testing.T) {
	entities := []Entity{entity("Jean", TypePER, 0.5)}
	got := MinConfidenceFilter(0.5)("", entities)
	if len(got) != 1 {
		t.Errorf("le seuil est inclusif : attendu 1 entité, got %d", len(got))
	}
}

func TestMinConfidenceFilter_EmptyInput(t *testing.T) {
	got := MinConfidenceFilter(0.5)("", nil)
	if len(got) != 0 {
		t.Errorf("attendu 0 entité sur entrée nil, got %d", len(got))
	}
}

// --- MaxTokensFilter ---

func TestMaxTokensFilter_RemovesLong(t *testing.T) {
	entities := []Entity{
		entity("Jean Dupont", TypePER, 1.0),                      // 2 tokens — OK
		entity("EOLE Hâpy Hâpy-Master Hâpy-Node", TypeMISC, 1.0), // 4 tokens — trop long
	}
	got := MaxTokensFilter(3)("", entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Jean Dupont" {
		t.Errorf("mauvaise entité conservée : %q", got[0].Text)
	}
}

func TestMaxTokensFilter_KeepsExact(t *testing.T) {
	entities := []Entity{entity("A B C", TypePER, 1.0)} // exactement 3 tokens
	got := MaxTokensFilter(3)("", entities)
	if len(got) != 1 {
		t.Errorf("la limite est inclusive : attendu 1 entité, got %d", len(got))
	}
}

func TestMaxTokensFilter_SingleToken(t *testing.T) {
	entities := []Entity{entity("Paris", TypeLOC, 1.0)}
	got := MaxTokensFilter(1)("", entities)
	if len(got) != 1 {
		t.Errorf("attendu 1 entité pour token unique, got %d", len(got))
	}
}

// --- MinRunesFilter ---

func TestMinRunesFilter_RemovesFragments(t *testing.T) {
	// Débris d'extraction relevés sur une facture dont le crénage disloque les
	// mots : le CRF les étiquette, ils ne désignent personne.
	entities := []Entity{
		entity("1", TypeLOC, 0.9),
		entity("A", TypeLOC, 0.9),
		entity("-", TypeLOC, 0.9),
		entity("21510 BEAULIEU", TypeLOC, 0.9),
	}
	got := MinRunesFilter(2)("", entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d : %v", len(got), got)
	}
	if got[0].Text != "21510 BEAULIEU" {
		t.Errorf("mauvaise entité conservée : %q", got[0].Text)
	}
}

func TestMinRunesFilter_CountsRunesNotBytes(t *testing.T) {
	// « Éa » fait 3 octets et 2 runes : compter les octets laisserait passer
	// des fragments d'un caractère dès qu'ils sont accentués.
	entities := []Entity{
		entity("Éa", TypePER, 0.9),
		entity("É", TypePER, 0.9),
	}
	got := MinRunesFilter(2)("", entities)
	if len(got) != 1 || got[0].Text != "Éa" {
		t.Errorf("attendu la seule entité de 2 runes, got %v", got)
	}
}

func TestMinRunesFilter_IgnoresSurroundingSpaces(t *testing.T) {
	entities := []Entity{entity("  A  ", TypeLOC, 0.9)}
	if got := MinRunesFilter(2)("", entities); len(got) != 0 {
		t.Errorf("les espaces de bordure ne comptent pas : got %v", got)
	}
}

// --- BlocklistFilter ---

func TestBlocklistFilter_RemovesAllBlocked(t *testing.T) {
	entities := []Entity{
		entity("Ingénieur Logiciels Libres", TypePER, 0.9),
		entity("Jean Dupont", TypePER, 0.9),
	}
	got := BlocklistFilter(TypePER, "ingénieur", "logiciels", "libres")("", entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Jean Dupont" {
		t.Errorf("mauvaise entité conservée : %q", got[0].Text)
	}
}

func TestBlocklistFilter_KeepsPartialMatch(t *testing.T) {
	// "Jean Ingénieur" : "Jean" n'est pas dans la blocklist → entité conservée.
	entities := []Entity{entity("Jean Ingénieur", TypePER, 0.9)}
	got := BlocklistFilter(TypePER, "ingénieur")("", entities)
	if len(got) != 1 {
		t.Errorf("une entité partiellement bloquée ne doit pas être supprimée")
	}
}

func TestBlocklistFilter_CaseInsensitive(t *testing.T) {
	entities := []Entity{entity("INGÉNIEUR LIBRES", TypePER, 0.9)}
	got := BlocklistFilter(TypePER, "ingénieur", "libres")("", entities)
	if len(got) != 0 {
		t.Errorf("la comparaison doit être insensible à la casse")
	}
}

func TestBlocklistFilter_OtherTypeIgnored(t *testing.T) {
	// L'entité est de type LOC, pas PER → le filtre ne s'applique pas.
	entities := []Entity{entity("Ingénieur Libres", TypeLOC, 0.9)}
	got := BlocklistFilter(TypePER, "ingénieur", "libres")("", entities)
	if len(got) != 1 {
		t.Errorf("le filtre ne doit pas affecter les autres types d'entités")
	}
}

// --- WithPostFilters (intégration) ---

func TestWithPostFilters_AppliesInOrder(t *testing.T) {
	var trace []string
	f1 := EntityFilter(func(_ string, e []Entity) []Entity { trace = append(trace, "f1"); return e })
	f2 := EntityFilter(func(_ string, e []Entity) []Entity { trace = append(trace, "f2"); return e })

	rec := &Recognizer{
		sentenceBoundaries: map[string]bool{".": true},
		postFilters:        nil,
	}
	_ = WithPostFilters(f1, f2)(rec)

	// Simuler l'application manuelle des filtres (comme dans Recognize).
	entities := []Entity{}
	for _, f := range rec.postFilters {
		entities = f("", entities)
	}

	if len(trace) != 2 || trace[0] != "f1" || trace[1] != "f2" {
		t.Errorf("ordre d'application incorrect : %v", trace)
	}
}

// --- countTokens (unitaire) ---

func TestCountTokens(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"Paris", 1},
		{"Jean Dupont", 2},
		{"A B C D", 4},
		{"  spaces  ", 1},
	}
	for _, c := range cases {
		got := countTokens(c.s)
		if got != c.want {
			t.Errorf("countTokens(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// --- MergePass ---

func TestMergePass_PerLoc_AdjacentFusesIntoPer(t *testing.T) {
	text := "BenjaminGaude"
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Benjamin", TypePER, 0.9},
		{"Gaude", TypeLOC, 0.7},
	})
	got := MergePass()(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité fusionnée, got %d", len(got))
	}
	if got[0].Text != "BenjaminGaude" {
		t.Errorf("texte fusionné incorrect : %q", got[0].Text)
	}
	if got[0].Type != TypePER {
		t.Errorf("type должен быть PER, got %s", got[0].Type)
	}
	if got[0].Confidence != 0.9 {
		t.Errorf("confiance devrait être celle de l'entité dominante (0.9), got %.2f", got[0].Confidence)
	}
}

func TestMergePass_PerPer_AdjacentFusesIntoPer(t *testing.T) {
	text := "AliceMartin"
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Alice", TypePER, 0.9},
		{"Martin", TypePER, 0.8},
	})
	got := MergePass()(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité fusionnée, got %d", len(got))
	}
	if got[0].Text != "AliceMartin" {
		t.Errorf("texte fusionné incorrect : %q", got[0].Text)
	}
}

func TestMergePass_NonAdjacent_NoFuse(t *testing.T) {
	entities := []Entity{
		{Text: "Alice", Type: TypePER, Start: 0, End: 5, Confidence: 0.9},
		{Text: "Lyon", Type: TypeLOC, Start: 20, End: 24, Confidence: 0.7},
	}
	got := MergePass()("Alice est là. Lyon aussi.", entities)
	if len(got) != 2 {
		t.Errorf("entités non adjacentes ne doivent pas fusionner : got %d", len(got))
	}
}

func TestMergePass_LocLoc_AdjacentFuses(t *testing.T) {
	text := "NewYorkCity"
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"New", TypeLOC, 0.9},
		{"York", TypeLOC, 0.8},
	})
	got := MergePass()(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité fusionnée, got %d", len(got))
	}
	if got[0].Text != "NewYork" {
		t.Errorf("texte fusionné incorrect : %q", got[0].Text)
	}
}

func TestMergePass_MultipleConverges(t *testing.T) {
	text := "JeanDupont habite a Paris"
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Jean", TypePER, 0.9},
		{"Dupont", TypeLOC, 0.7},
		{"Paris", TypeLOC, 0.8},
	})
	got := MergePass()(text, entities)
	if len(got) != 2 {
		t.Errorf("attendu 2 entités (JeanDupont + Paris), got %d: %v", len(got), got)
	}
}

func TestMergePass_EmptyInput(t *testing.T) {
	got := MergePass()("", nil)
	if len(got) != 0 {
		t.Errorf("nil en entrée doit retourner slice vide")
	}
}

// --- NameCompletionPass ---

func TestNameCompletionPass_SingleFirstName_CompletesWithSurname(t *testing.T) {
	text := "BenjaminGaude est developpeur."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Benjamin", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Benjamin"))(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "BenjaminGaude" {
		t.Errorf("nom incomplet : %q", got[0].Text)
	}
}

func TestNameCompletionPass_AlreadyComplete_NoChange(t *testing.T) {
	text := "JeanDupont est la."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"JeanDupont", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Jean"))(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "JeanDupont" {
		t.Errorf("nom déjà complet ne doit pas changer : %q", got[0].Text)
	}
}

func TestNameCompletionPass_NoSpaceAfter_Skips(t *testing.T) {
	text := "Alice."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Alice", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Alice"))(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Alice" {
		t.Errorf("ne doit pas compléter sans espace : %q", got[0].Text)
	}
}

func TestNameCompletionPass_StopWordAfter_Skips(t *testing.T) {
	text := "Jean le developpeur"
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Jean", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Jean"))(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Jean" {
		t.Errorf("ne doit pas compléter sur stop-word : %q", got[0].Text)
	}
}

func TestNameCompletionPass_AlreadyCovered_Skips(t *testing.T) {
	text := "Benjamin et Jean."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Benjamin", TypePER, 0.9},
		{"Jean", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Benjamin", "Jean"))(text, entities)
	if len(got) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(got))
	}
	if got[0].Text != "Benjamin" || got[1].Text != "Jean" {
		t.Errorf("ne doit pas modifier : %v", got)
	}
}

func TestNameCompletionPass_KnownLocSurname_Skips(t *testing.T) {
	text := "Bordeaux habite a Paris."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Bordeaux", TypeLOC, 0.9},
		{"Paris", TypeLOC, 0.9},
	})
	got := NameCompletionPass(testFirstNames())(text, entities)
	if len(got) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(got))
	}
}

func TestNameCompletionPass_EmptyInput(t *testing.T) {
	got := NameCompletionPass(nil)("", nil)
	if len(got) != 0 {
		t.Errorf("nil en entrée doit retourner slice vide")
	}
}

func TestFirstNameDetectionFilter_DetectsUncoveredFirstNames(t *testing.T) {
	text := "Vincent est ici."
	filter := FirstNameDetectionFilter(testFirstNames("Vincent"), nil)
	entities := filter(text, nil)

	if len(entities) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(entities))
	}
	if entities[0].Text != "Vincent" {
		t.Errorf("attendu 'Vincent', got %q", entities[0].Text)
	}
	if entities[0].Type != TypePER {
		t.Errorf("attendu TypePER, got %v", entities[0].Type)
	}
}

func TestFirstNameDetectionFilter_AlreadyCovered_Skips(t *testing.T) {
	text := "Vincent est ici."
	existing := []Entity{{Text: "Vincent", Type: TypePER, Start: 0, End: 7, Confidence: 1.0}}
	filter := FirstNameDetectionFilter(testFirstNames("Vincent"), nil)
	entities := filter(text, existing)

	if len(entities) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(entities))
	}
}

func TestFirstNameDetectionFilter_NonFirstName_Skips(t *testing.T) {
	text := "Bonjour tout le monde."
	filter := FirstNameDetectionFilter(testFirstNames("Vincent"), nil)
	entities := filter(text, nil)

	if len(entities) != 0 {
		t.Errorf("attendu 0 entités pour non-prénom, got %d", len(entities))
	}
}

func TestFirstNameDetectionFilter_MultipleFirstNames(t *testing.T) {
	text := "Vincent et Benjamin sont là."
	filter := FirstNameDetectionFilter(testFirstNames("Vincent", "Benjamin"), nil)
	entities := filter(text, nil)

	if len(entities) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(entities))
	}
	if entities[0].Text != "Vincent" {
		t.Errorf("attendu 'Vincent', got %q", entities[0].Text)
	}
	if entities[1].Text != "Benjamin" {
		t.Errorf("attendu 'Benjamin', got %q", entities[1].Text)
	}
}

func TestFirstNameDetectionFilter_StopWordsExcluded(t *testing.T) {
	text := "Le développeur"
	stopWords := map[string]bool{"le": true, "la": true, "de": true}
	filter := FirstNameDetectionFilter(testFirstNames("Le", "De"), stopWords)
	entities := filter(text, nil)

	if len(entities) != 0 {
		t.Errorf("attendu 0 entités pour stop words, got %d", len(entities))
	}
}

// --- Correctifs Unicode (byte vs rune) ---

func TestFirstNameDetectionFilter_AccentedFirstNames(t *testing.T) {
	text := "Éric travaille avec Frédéric."
	filter := FirstNameDetectionFilter(testFirstNames("Éric", "Frédéric"), nil)
	entities := filter(text, nil)

	if len(entities) != 2 {
		t.Fatalf("attendu 2 entités PER accentuées, got %d: %v", len(entities), entities)
	}
	if entities[0].Text != "Éric" || entities[0].Start != 0 || entities[0].End != len("Éric") {
		t.Errorf("première entité incorrecte : %+v", entities[0])
	}
	if entities[1].Text != "Frédéric" {
		t.Errorf("seconde entité incorrecte : %+v", entities[1])
	}
}

func TestNameCompletionPass_AccentedSurname(t *testing.T) {
	text := "Jean Sémard est développeur."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Jean", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Jean"))(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Jean Sémard" {
		t.Errorf("nom accentué mal complété : %q", got[0].Text)
	}
	if got[0].End != len("Jean Sémard") {
		t.Errorf("offset End incorrect : %d (attendu %d)", got[0].End, len("Jean Sémard"))
	}
}

func TestNameCompletionPass_AccentedInitialSurname(t *testing.T) {
	text := "Marc Étienne est là."
	entities := buildEntities(text, []struct {
		substr string
		typ    EntityType
		conf   float64
	}{
		{"Marc", TypePER, 0.9},
	})
	got := NameCompletionPass(testFirstNames("Marc"))(text, entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Marc Étienne" {
		t.Errorf("nom à initiale accentuée mal complété : %q", got[0].Text)
	}
}
