package value

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/template"
)

func loadBundle(t *testing.T) *gazetteer.Bundle {
	t.Helper()
	b, err := gazetteer.LoadSeed("fr", gazetteer.DefaultOptions())
	if err != nil {
		t.Fatalf("chargement du socle : %v", err)
	}
	return b
}

// Les familles sont déclarées à deux endroits : la métadonnée « f » des
// gazetteers et template.KnownFamilies, qui sert à rejeter les fautes de frappe
// au parsing. Si les deux divergent, un template valide devient irrecevable ou
// une famille inexistante passe. Ce test lie les deux.
func TestFamillesCouvrentLesGazetteers(t *testing.T) {
	b := loadBundle(t)
	for _, name := range []string{"org_types", "org_domaines"} {
		seen := map[string]bool{}
		set := b.MustGet(name)
		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 20000; i++ {
			seen[set.Pick(rng).Metadata["f"]] = true
		}
		for f := range seen {
			if f == "" {
				t.Errorf("%s : une entrée n'a pas de famille", name)
				continue
			}
			if !template.KnownFamilies[f] {
				t.Errorf("%s : famille %q absente de template.KnownFamilies", name, f)
			}
		}
		for f := range template.KnownFamilies {
			if !seen[f] {
				t.Errorf("%s : famille %q déclarée mais sans entrée", name, f)
			}
		}
	}
}

// Une famille imposée doit tenir sur toute la dénomination : c'est le
// mécanisme qui empêche un « Laboratoire de Travaux Publics » de signer un
// compte-rendu d'analyses.
func TestNewOrgInRespecteLaFamille(t *testing.T) {
	b := loadBundle(t)
	// Un mot caractéristique de chaque famille, qui ne doit apparaître que
	// dans les dénominations de cette famille.
	marqueurs := map[string]string{
		"sante":          "Médicale",
		"technique":      "Travaux",
		"social":         "Allocations",
		"administration": "Habitat",
		"commerce":       "Négoce",
	}
	for famille, marqueur := range marqueurs {
		for autre, m := range marqueurs {
			if autre == famille {
				continue
			}
			g := New(rand.New(rand.NewSource(int64(len(famille)))), b)
			for i := 0; i < 500; i++ {
				d := g.NewOrgIn(famille).Denomination
				if strings.Contains(d, m) {
					t.Errorf("famille %q : %q contient le marqueur de %q", famille, d, autre)
					break
				}
			}
		}
		_ = marqueur
	}
}

// Le défaut de la liste plate était de plafonner l'entropie ORG : 16 valeurs
// pour des milliers d'occurrences, donc de la mémorisation plutôt que de
// l'apprentissage de forme (DATASET.md § 16). Ce test fixe un plancher.
func TestOrgEntropie(t *testing.T) {
	b := loadBundle(t)
	g := New(rand.New(rand.NewSource(7)), b)
	seen := map[string]int{}
	const n = 5000
	for i := 0; i < n; i++ {
		seen[g.NewOrg().Denomination]++
	}
	if len(seen) < n*9/10 {
		t.Errorf("seulement %d dénominations distinctes sur %d tirages", len(seen), n)
	}
	// Le taux de valeurs distinctes ne dit rien de la forme de la queue : une
	// poignée de dénominations très fréquentes le laisserait presque intact
	// tout en suffisant à faire mémoriser ces valeurs au modèle. C'est cette
	// domination-là qu'il faut interdire.
	for d, c := range seen {
		if c > n/200 {
			t.Errorf("%q tirée %d fois sur %d (plafond %d)", d, c, n, n/200)
		}
	}
}

func TestAcronym(t *testing.T) {
	cases := map[string]string{
		"Caisse d'Allocations Familiales":       "CAF",
		"Centre Hospitalier Régional":           "CHR",
		"Office Public de l'Habitat":            "OPH",
		"Trésorerie Générale des Yvelines":      "TGY",
		"Agence Régionale de Santé de la Drôme": "ARSD",
		"Clinique":                                    "Clinique",
		"Établissement Français du Sang":              "EFS",
		"Chambre de Commerce et d'Industrie du Rhône": "CCIR",
	}
	for in, want := range cases {
		if got := acronym(in); got != want {
			t.Errorf("acronym(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestDePrefix(t *testing.T) {
	cases := map[string]string{
		"Valence":         "de Valence",
		"Angers":          "d'Angers",
		"Le Mans":         "du Mans",
		"Les Ulis":        "des Ulis",
		"La Rochelle":     "de La Rochelle",
		"L'Haÿ-les-Roses": "de L'Haÿ-les-Roses",
		"Évry":            "d'Évry",
	}
	for in, want := range cases {
		if got := dePrefix(in); got != want {
			t.Errorf("dePrefix(%q) = %q, attendu %q", in, got, want)
		}
	}
}
