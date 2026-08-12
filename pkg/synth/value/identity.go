// Package value produit les valeurs insérées dans les documents synthétiques.
//
// Les entités annotées (PER, ORG, ADDR) sont retournées sous forme de segments
// parce qu'une même forme de surface peut porter plusieurs spans : « BERTRAND
// JULIEN Roussel Marques Carmen » est un cas réel (facture d'énergie) où deux
// personnes se suivent sans séparateur.
package value

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/render"
)

// Person est l'identité tirée pour un slot. La forme de surface varie d'une
// occurrence à l'autre ; l'identité, elle, reste stable pour un slot donné.
type Person struct {
	Civility string // M., Mme, Monsieur, Madame
	Title    string // Dr, Pr — vide la plupart du temps
	First    string
	Last     string
	Last2    string // second patronyme (usage hispanique), souvent vide
	BirthLas string // nom de naissance, pour la forme nom_naissance
	Female   bool
}

// FullLast retourne le patronyme complet, second nom inclus.
func (p Person) FullLast() string {
	if p.Last2 == "" {
		return p.Last
	}
	return p.Last + " " + p.Last2
}

var civilitiesF = []string{"Mme", "Madame", "Mme"}
var civilitiesM = []string{"M.", "Monsieur", "M."}

// NewPerson tire une identité complète.
func (g *Generator) NewPerson() Person {
	female := g.rng.Intn(2) == 0
	p := Person{Female: female}
	if female {
		p.First = title(g.pick("prenoms_f"))
		p.Civility = civilitiesF[g.rng.Intn(len(civilitiesF))]
	} else {
		p.First = title(g.pick("prenoms_m"))
		p.Civility = civilitiesM[g.rng.Intn(len(civilitiesM))]
	}
	p.Last = title(g.pick("patronymes"))
	// Double patronyme dans 15 % des cas : fréquent sur les documents français
	// pour les personnes d'origine hispanique (facture d'énergie, compte-rendu de laboratoire).
	if g.rng.Float64() < 0.15 {
		p.Last2 = title(g.pick("patronymes"))
	}
	p.BirthLas = title(g.pick("patronymes"))
	if g.rng.Float64() < 0.10 {
		p.Title = []string{"Dr", "Pr", "Me"}[g.rng.Intn(3)]
	}
	return p
}

// personForms énumère les formes observées sur documents réels. Les poids
// reflètent la fréquence relative constatée, pas une distribution mesurée :
// six documents ne font pas une statistique.
var personForms = []weighted{
	{"civ_prenom_nom", 18},
	{"civ_nom_prenom", 14},
	{"prenom_nom", 16},
	{"prenom_nom_caps", 12},
	{"nom_prenom_caps", 14},
	{"nom_seul", 6},
	{"titre_nom", 6},
	{"titre_nom_caps", 5},
	{"nom_naissance", 4},
	{"nom_prenom_etiquette", 3},
	{"couple", 2},
}

// renderPerson produit les segments d'une personne selon la forme demandée.
//
// Politique de découpage : les civilités et titres (M., Mme, Dr) restent
// **hors** du span, conformément à l'usage de WikiNER sur lequel le modèle est
// déjà entraîné. Les inclure créerait une divergence d'annotation entre le
// corpus synthétique et la base WikiNER avec laquelle il est mélangé.
func (g *Generator) renderPerson(p Person, form, slot string) []render.Segment {
	seg := func(text, form string) render.Segment {
		return render.Segment{Text: text, Label: ner.TypePER, Slot: slot, Form: form}
	}
	plain := func(text string) render.Segment { return render.Segment{Text: text} }

	switch form {
	case "civ_prenom_nom":
		return []render.Segment{plain(p.Civility + " "), seg(p.First+" "+strings.ToUpper(p.FullLast()), form)}
	case "civ_nom_prenom":
		return []render.Segment{plain(p.Civility + " "), seg(p.FullLast()+" "+p.First, form)}
	case "prenom_nom":
		return []render.Segment{seg(p.First+" "+p.FullLast(), form)}
	case "prenom_nom_caps":
		return []render.Segment{seg(strings.ToUpper(p.First+" "+p.FullLast()), form)}
	case "nom_prenom_caps":
		return []render.Segment{seg(strings.ToUpper(p.FullLast()+" "+p.First), form)}
	case "nom_seul":
		return []render.Segment{seg(strings.ToUpper(p.Last), form)}
	case "titre_nom":
		return []render.Segment{plain(g.titleOf(p) + " "), seg(strings.ToUpper(p.Last)+" "+p.First, form)}
	case "titre_nom_caps":
		return []render.Segment{plain(g.titleOf(p) + " "), seg(strings.ToUpper(p.Last+" "+p.First), form)}
	case "nom_naissance":
		// « Mme BERTRAND Carmen (Née ROUSSEL MARQUES) » — le nom de naissance est une
		// donnée personnelle à part entière, donc un second span.
		return []render.Segment{
			plain(p.Civility + " "),
			seg(strings.ToUpper(p.Last)+" "+p.First, form),
			plain(" (Née "),
			seg(strings.ToUpper(p.BirthLas), form+".naissance"),
			plain(")"),
		}
	case "nom_prenom_etiquette":
		return []render.Segment{
			seg(strings.ToUpper(p.Last), form),
			plain(" Prénom : "),
			seg(p.First, form+".prenom"),
		}
	case "couple":
		// Deux personnes juxtaposées sans séparateur (facture d'énergie). Aucun
		// indice de frontière n'existe dans le texte : la politique retenue est
		// deux spans distincts séparés par l'espace, non annoté.
		other := g.NewPerson()
		return []render.Segment{
			seg(strings.ToUpper(p.FullLast()+" "+p.First), form),
			plain(" "),
			seg(other.FullLast()+" "+other.First, form+".second"),
		}
	default:
		return []render.Segment{seg(p.First+" "+p.FullLast(), "prenom_nom")}
	}
}

func (g *Generator) titleOf(p Person) string {
	if p.Title != "" {
		return p.Title
	}
	return "Dr"
}

// Org est une organisation tirée pour un slot.
type Org struct {
	Denomination string // dénomination sans forme juridique
	LegalForm    string // SARL, SAS…
	Short        string // nom court, pour les usages en texte courant
	Capital      string
	FromPerson   bool // dénomination bâtie sur un patronyme
}

// NewOrg tire une organisation.
//
// Une raison sociale sur deux est construite sur un patronyme (« SARLU
// Cheminées Fabien Coudray ») : c'est la forme dominante chez les artisans
// et le principal piège à faux positif PER (docs/observations-corpus-reel.md § 3).
func (g *Generator) NewOrg() Org { return g.NewOrgIn("") }

// NewOrgIn tire une organisation d'une famille imposée (« sante », « social »,
// « administration », « commerce », « technique »).
//
// Une famille non vide exclut la raison sociale bâtie sur un patronyme et la
// liste plate, dont on ne sait pas dans quel secteur les ranger : un
// compte-rendu d'analyses ne doit pas être signé d'un « Syndicat de
// Construction du Var ». Une famille vide laisse les trois branches ouvertes.
func (g *Generator) NewOrgIn(famille string) Org {
	o := Org{LegalForm: g.pick("formes_juridiques")}
	o.Capital = fmt.Sprintf("%d 000.00 €", 1+g.rng.Intn(500))
	if famille != "" {
		o.Denomination = g.composeOrgName(famille)
		o.Short = acronym(o.Denomination)
		return o
	}
	switch r := g.rng.Float64(); {
	case r < 0.5:
		p := g.NewPerson()
		o.Denomination = g.pick("activites") + " " + p.First + " " + p.Last
		o.Short = p.Last
		o.FromPerson = true
	case r < 0.92:
		o.Denomination = g.composeOrgName("")
		o.Short = acronym(o.Denomination)
	default:
		// Liste plate : elle porte les formes que la composition ne produit
		// pas (« Bourgogne Formation internationale »).
		o.Denomination = g.pick("org_denominations")
		o.Short = acronym(o.Denomination)
	}
	return o
}

// composeOrgName construit une dénomination par assemblage
// tête + qualificatif + domaine + ancrage territorial.
//
// Une liste plate de dénominations plafonne l'entropie du corpus et fait
// mémoriser les organisations au modèle au lieu de lui faire apprendre leur
// forme (DATASET.md § 16, lot 1). L'assemblage porte les quatre composants à
// plusieurs centaines de milliers de combinaisons.
//
// Les trois contraintes qui rendent le résultat lisible : le qualificatif
// s'accorde en genre avec la tête, le domaine appartient à la même famille
// qu'elle, et au moins un des deux est présent.
// Une famille vide laisse la tête libre, et c'est elle qui fixe alors la
// famille des composants suivants.
func (g *Generator) composeOrgName(famille string) string {
	types := g.set("org_types")
	if famille != "" {
		if sub := g.bundle.Subset("org_types", famille, func(e gazetteer.Entry) bool {
			return e.Metadata["f"] == famille
		}); sub != nil {
			types = sub
		}
	}
	head := types.Pick(g.rng)
	famille = head.Metadata["f"]

	parts := []string{head.Value}

	withQualif := g.rng.Float64() < 0.55
	withDomaine := !withQualif || g.rng.Float64() < 0.6

	if withQualif {
		set := g.bundle.Subset("org_qualificatifs", famille, func(e gazetteer.Entry) bool {
			allowed, ok := e.Metadata["f"]
			return !ok || slices.Contains(strings.Fields(allowed), famille)
		})
		if set != nil {
			q := set.Pick(g.rng)
			if head.Metadata["g"] == "f" && q.Metadata["fem"] != "" {
				parts = append(parts, q.Metadata["fem"])
			} else {
				parts = append(parts, q.Value)
			}
		}
	}
	if withDomaine {
		set := g.bundle.Subset("org_domaines", famille, func(e gazetteer.Entry) bool {
			return e.Metadata["f"] == famille
		})
		if set != nil {
			parts = append(parts, set.PickValue(g.rng))
		}
	}

	// L'ancrage territorial introduit un nom de lieu à l'intérieur d'un span
	// ORG — piège utile pour le modèle, qui doit apprendre à ne pas le
	// découper en LOC.
	//
	// Il est obligatoire dès que la dénomination n'a pas ses deux compléments :
	// « Agence de l'Habitat » ou « Caisse Régionale » ne comptent qu'une
	// centaine de combinaisons par famille, assez peu pour que le corpus se
	// mette à les répéter. Ailleurs il reste minoritaire, parce qu'il allonge
	// la dénomination.
	r := g.rng.Float64()
	switch {
	case !withQualif || !withDomaine || r < 0.20:
		parts = append(parts, g.pick("org_territoires"))
	case r < 0.32:
		parts = append(parts, dePrefix(g.pick("communes")))
	}
	return strings.Join(parts, " ")
}

// dePrefix préfixe un nom de lieu de la préposition « de » contractée selon
// l'article que le nom porte déjà : Le Mans → du Mans, Les Sables → des
// Sables, Angers → d'Angers.
func dePrefix(place string) string {
	switch {
	case strings.HasPrefix(place, "Le "):
		return "du " + strings.TrimPrefix(place, "Le ")
	case strings.HasPrefix(place, "Les "):
		return "des " + strings.TrimPrefix(place, "Les ")
	case strings.HasPrefix(place, "La "):
		return "de " + place
	case strings.HasPrefix(place, "L'"):
		return "de " + place
	}
	r, _ := utf8.DecodeRuneInString(stripAccents(place))
	if strings.ContainsRune("AEIOUY", r) {
		return "d'" + place
	}
	return "de " + place
}

// motsOutils sont les mots que l'acronymisation saute.
var motsOutils = map[string]bool{
	"de": true, "du": true, "des": true, "la": true, "le": true, "les": true,
	"et": true, "à": true, "au": true, "aux": true, "en": true, "l": true, "d": true,
}

// acronym réduit une dénomination à son sigle, comme le fait l'usage français
// pour les organismes (CHU, CAF, CPAM, ANAH). Les sigles sont fréquents dans
// les documents réels et constituent une forme ORG à part entière.
//
// Une dénomination trop courte pour donner un sigle lisible est rendue par son
// premier mot.
func acronym(denomination string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(denomination, func(r rune) bool {
		return r == ' ' || r == '\'' || r == '-' || r == '’'
	}) {
		if motsOutils[strings.ToLower(w)] {
			continue
		}
		r, _ := utf8.DecodeRuneInString(stripAccents(w))
		b.WriteRune(unicode.ToUpper(r))
		if b.Len() >= 5 {
			break
		}
	}
	if b.Len() < 2 {
		return strings.Fields(denomination)[0]
	}
	return b.String()
}

func (g *Generator) renderOrg(o Org, form, slot string) []render.Segment {
	seg := func(text, form string) render.Segment {
		return render.Segment{Text: text, Label: ner.TypeORG, Slot: slot, Form: form}
	}
	switch form {
	case "court":
		return []render.Segment{seg(o.Short, form)}
	case "caps":
		return []render.Segment{seg(strings.ToUpper(stripAccents(o.Denomination)), form)}
	case "caps_tirets":
		// Le signe moins U+2212 est ce que restitue l'extraction du compte-rendu de laboratoire, et
		// non le trait d'union U+002D.
		s := strings.ReplaceAll(strings.ToUpper(stripAccents(o.Denomination)), " ", "−")
		return []render.Segment{seg(s, form)}
	case "juridique_patronyme":
		return []render.Segment{seg(o.LegalForm+" "+o.Denomination, form)}
	case "juridique_capital":
		return []render.Segment{
			seg(o.Denomination, form),
			render.Segment{Text: " – " + o.LegalForm + " au capital de " + o.Capital},
		}
	default:
		return []render.Segment{seg(o.Denomination, form)}
	}
}

type weighted struct {
	value  string
	weight int
}

func pickWeighted(rng *rand.Rand, items []weighted) string {
	total := 0
	for _, it := range items {
		total += it.weight
	}
	n := rng.Intn(total)
	for _, it := range items {
		n -= it.weight
		if n < 0 {
			return it.value
		}
	}
	return items[len(items)-1].value
}

// title met la première lettre en majuscule, le reste en minuscules. Les
// gazetteers de prénoms sont en minuscules dans les sources ouvertes.
func title(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Fields(strings.ToLower(s))
	for i, p := range parts {
		r := []rune(p)
		parts[i] = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return strings.Join(parts, " ")
}

var accentFolder = strings.NewReplacer(
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"à", "a", "â", "a", "ä", "a",
	"î", "i", "ï", "i", "ô", "o", "ö", "o",
	"ù", "u", "û", "u", "ü", "u", "ç", "c",
	"É", "E", "È", "E", "Ê", "E", "À", "A", "Ô", "O", "Ç", "C",
)

// stripAccents retire les diacritiques, comme le font les systèmes de gestion
// qui saisissent en capitales (compte-rendu de laboratoire : « CLINIQUE PRIVEE ANESTHESIE »).
func stripAccents(s string) string { return accentFolder.Replace(s) }

var _ = gazetteer.DefaultOptions
