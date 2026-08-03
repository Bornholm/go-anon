package anonymizer

import (
	"fmt"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

func ent(start, end int, text string) ner.Entity {
	return ner.Entity{Text: text, Type: ner.TypePER, Start: start, End: end}
}

func spans(entities []ner.Entity) string {
	out := make([][3]any, len(entities))
	for i, e := range entities {
		out[i] = [3]any{e.Start, e.End, e.Text}
	}
	return fmt.Sprint(out)
}

// TestReconcile_NoExtraIsNoOp : sans entité fournie, le chemin nominal doit être
// rigoureusement inchangé — même contenu et même slice.
func TestReconcile_NoExtraIsNoOp(t *testing.T) {
	base := []ner.Entity{ent(0, 4, "Jean"), ent(10, 16, "Dupont")}

	got := reconcileEntities(base, nil, "Jean etc Dupont")
	if &got[0] != &base[0] {
		t.Error("la slice du recognizer devrait être rendue telle quelle")
	}
	if spans(got) != spans(base) {
		t.Errorf("contenu modifié : %s", spans(got))
	}
}

func TestReconcile(t *testing.T) {
	const text = "Jean Dupont habite Paris"

	tests := []struct {
		name  string
		base  []ner.Entity
		extra []ner.Entity
		want  string
	}{
		{
			name:  "spans disjoints : union triée par offset",
			base:  []ner.Entity{ent(19, 24, "Paris")},
			extra: []ner.Entity{ent(0, 11, "")},
			want:  "[[0 11 Jean Dupont] [19 24 Paris]]",
		},
		{
			// Le cœur de la posture : entre deux lectures concurrentes, la plus
			// large gagne. « Jean » seul serait sous-anonymisé.
			name:  "chevauchement : le span le plus large gagne",
			base:  []ner.Entity{ent(0, 4, "Jean")},
			extra: []ner.Entity{ent(0, 11, "")},
			want:  "[[0 11 Jean Dupont]]",
		},
		{
			name:  "chevauchement partiel décalé",
			base:  []ner.Entity{ent(5, 11, "Dupont")},
			extra: []ner.Entity{ent(0, 11, "")},
			want:  "[[0 11 Jean Dupont]]",
		},
		{
			// À largeur égale, l'entité du modèle est retenue : elle porte le
			// score de confiance.
			name:  "largeur égale : le modèle l'emporte",
			base:  []ner.Entity{ent(0, 4, "Jean")},
			extra: []ner.Entity{ent(0, 4, "")},
			want:  "[[0 4 Jean]]",
		},
		{
			name:  "entité fournie hors bornes : écartée",
			base:  []ner.Entity{ent(0, 4, "Jean")},
			extra: []ner.Entity{ent(100, 200, "")},
			want:  "[[0 4 Jean]]",
		},
		{
			name:  "intervalle vide ou inversé : écarté",
			base:  []ner.Entity{ent(0, 4, "Jean")},
			extra: []ner.Entity{ent(7, 7, ""), ent(9, 5, "")},
			want:  "[[0 4 Jean]]",
		},
		{
			// La forme de surface fournie décrit une autre recomposition : seuls
			// les offsets font foi, et Text est repris du texte courant. Sans ça
			// la substitution serait rejetée par le contrôle d'égalité.
			name:  "forme de surface reprise du texte courant",
			base:  nil,
			extra: []ner.Entity{{Text: "obsolète", Type: ner.TypePER, Start: 19, End: 24}},
			want:  "[[19 24 Paris]]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spans(reconcileEntities(tt.base, tt.extra, text)); got != tt.want {
				t.Errorf("= %s, attendu %s", got, tt.want)
			}
		})
	}
}

// TestReconcile_OutputIsDisjoint : la passe de remplacement d'Anonymize exige
// des spans disjoints — elle réécrit de droite à gauche et abandonne
// silencieusement toute entité dont le texte ne correspond plus. Un
// chevauchement résiduel laisserait donc une portion de la zone en clair.
func TestReconcile_OutputIsDisjoint(t *testing.T) {
	const text = "Jean Dupont habite Paris"

	got := reconcileEntities(
		[]ner.Entity{ent(0, 4, "Jean"), ent(5, 11, "Dupont"), ent(19, 24, "Paris")},
		[]ner.Entity{ent(0, 11, ""), ent(2, 8, ""), ent(19, 24, "")},
		text,
	)

	for i := 1; i < len(got); i++ {
		if got[i].Start < got[i-1].End {
			t.Fatalf("spans chevauchants en sortie : %s", spans(got))
		}
	}
}

// TestWithAdditionalEntities_EndToEnd : une entité fournie est bien remplacée,
// alors que le recognizer ne détecte rien.
func TestWithAdditionalEntities_EndToEnd(t *testing.T) {
	anon := New(noopRecognizer{}, Config{Strategy: TagReplace})

	res, err := anon.Anonymize("contacter Dupont demain",
		WithAdditionalEntities([]ner.Entity{{Type: ner.TypePER, Start: 10, End: 16}}))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if got := res.Text; got == "contacter Dupont demain" {
		t.Fatalf("l'entité fournie n'a pas été remplacée : %q", got)
	}
	if res.OriginalToPlaceholder["Dupont"] == "" {
		t.Errorf("mapping absent pour la forme fournie : %v", res.OriginalToPlaceholder)
	}
}

// noopRecognizer ne détecte rien : isole l'effet de WithAdditionalEntities.
type noopRecognizer struct{}

func (noopRecognizer) Recognize(string) ([]ner.Entity, error) { return nil, nil }
