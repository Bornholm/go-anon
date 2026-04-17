package ner

import (
	"testing"
)

// helpers

func entity(text string, typ EntityType, confidence float64) Entity {
	return Entity{Text: text, Type: typ, Start: 0, End: len(text), Confidence: confidence}
}

// --- MinConfidenceFilter ---

func TestMinConfidenceFilter_RemovesBelow(t *testing.T) {
	entities := []Entity{
		entity("Jean Dupont", TypePER, 0.9),
		entity("Ingénieur", TypePER, 0.3),
		entity("Paris", TypeLOC, 0.8),
	}
	got := MinConfidenceFilter(0.5)(entities)
	if len(got) != 2 {
		t.Fatalf("attendu 2 entités, got %d", len(got))
	}
	if got[0].Text != "Jean Dupont" || got[1].Text != "Paris" {
		t.Errorf("entités inattendues : %v", got)
	}
}

func TestMinConfidenceFilter_KeepsExact(t *testing.T) {
	entities := []Entity{entity("Jean", TypePER, 0.5)}
	got := MinConfidenceFilter(0.5)(entities)
	if len(got) != 1 {
		t.Errorf("le seuil est inclusif : attendu 1 entité, got %d", len(got))
	}
}

func TestMinConfidenceFilter_EmptyInput(t *testing.T) {
	got := MinConfidenceFilter(0.5)(nil)
	if len(got) != 0 {
		t.Errorf("attendu 0 entité sur entrée nil, got %d", len(got))
	}
}

// --- MaxTokensFilter ---

func TestMaxTokensFilter_RemovesLong(t *testing.T) {
	entities := []Entity{
		entity("Jean Dupont", TypePER, 1.0),                   // 2 tokens — OK
		entity("EOLE Hâpy Hâpy-Master Hâpy-Node", TypeMISC, 1.0), // 4 tokens — trop long
	}
	got := MaxTokensFilter(3)(entities)
	if len(got) != 1 {
		t.Fatalf("attendu 1 entité, got %d", len(got))
	}
	if got[0].Text != "Jean Dupont" {
		t.Errorf("mauvaise entité conservée : %q", got[0].Text)
	}
}

func TestMaxTokensFilter_KeepsExact(t *testing.T) {
	entities := []Entity{entity("A B C", TypePER, 1.0)} // exactement 3 tokens
	got := MaxTokensFilter(3)(entities)
	if len(got) != 1 {
		t.Errorf("la limite est inclusive : attendu 1 entité, got %d", len(got))
	}
}

func TestMaxTokensFilter_SingleToken(t *testing.T) {
	entities := []Entity{entity("Paris", TypeLOC, 1.0)}
	got := MaxTokensFilter(1)(entities)
	if len(got) != 1 {
		t.Errorf("attendu 1 entité pour token unique, got %d", len(got))
	}
}

// --- BlocklistFilter ---

func TestBlocklistFilter_RemovesAllBlocked(t *testing.T) {
	entities := []Entity{
		entity("Ingénieur Logiciels Libres", TypePER, 0.9),
		entity("Jean Dupont", TypePER, 0.9),
	}
	got := BlocklistFilter(TypePER, "ingénieur", "logiciels", "libres")(entities)
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
	got := BlocklistFilter(TypePER, "ingénieur")(entities)
	if len(got) != 1 {
		t.Errorf("une entité partiellement bloquée ne doit pas être supprimée")
	}
}

func TestBlocklistFilter_CaseInsensitive(t *testing.T) {
	entities := []Entity{entity("INGÉNIEUR LIBRES", TypePER, 0.9)}
	got := BlocklistFilter(TypePER, "ingénieur", "libres")(entities)
	if len(got) != 0 {
		t.Errorf("la comparaison doit être insensible à la casse")
	}
}

func TestBlocklistFilter_OtherTypeIgnored(t *testing.T) {
	// L'entité est de type LOC, pas PER → le filtre ne s'applique pas.
	entities := []Entity{entity("Ingénieur Libres", TypeLOC, 0.9)}
	got := BlocklistFilter(TypePER, "ingénieur", "libres")(entities)
	if len(got) != 1 {
		t.Errorf("le filtre ne doit pas affecter les autres types d'entités")
	}
}

// --- WithPostFilters (intégration) ---

func TestWithPostFilters_AppliesInOrder(t *testing.T) {
	var trace []string
	f1 := EntityFilter(func(e []Entity) []Entity { trace = append(trace, "f1"); return e })
	f2 := EntityFilter(func(e []Entity) []Entity { trace = append(trace, "f2"); return e })

	rec := &Recognizer{
		sentenceBoundaries: map[string]bool{".": true},
		postFilters:        nil,
	}
	_ = WithPostFilters(f1, f2)(rec)

	// Simuler l'application manuelle des filtres (comme dans Recognize).
	entities := []Entity{}
	for _, f := range rec.postFilters {
		entities = f(entities)
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
