package value

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/render"
	"github.com/bornholm/go-anon/pkg/synth/template"
)

// Generator produit les valeurs d'un document. Une instance par document :
// elle porte l'état des slots, qui ne doit jamais fuir d'un document à l'autre.
type Generator struct {
	rng    *rand.Rand
	bundle *gazetteer.Bundle
	slots  map[string]any
	// base est la date de référence du document, dont toutes les autres
	// dérivent avec des décalages plausibles.
	base time.Time
}

// New construit un générateur pour un document.
func New(rng *rand.Rand, bundle *gazetteer.Bundle) *Generator {
	return &Generator{
		rng:    rng,
		bundle: bundle,
		slots:  map[string]any{},
		base:   time.Date(2023+rng.Intn(3), time.Month(1+rng.Intn(12)), 1+rng.Intn(28), 0, 0, 0, 0, time.UTC),
	}
}

func (g *Generator) set(name string) *gazetteer.Set { return g.bundle.MustGet(name) }
func (g *Generator) pick(name string) string        { return g.set(name).PickValue(g.rng) }

// slotKey isole les slots par type : {{PER:x}} et {{ADDR:x}} sont deux slots
// distincts même s'ils portent le même nom.
func slotKey(kind, slot string) string { return kind + ":" + slot }

// Render produit les segments d'un placeholder.
//
// Un slot vide signifie « valeur autonome » : chaque occurrence tire une valeur
// neuve, sans mémorisation.
func (g *Generator) Render(p template.Placeholder) []render.Segment {
	switch p.Kind {
	case "PER":
		person := memo(g, slotKey(p.Kind, p.Slot), p.Slot, g.NewPerson)
		form := p.Args["form"]
		if form == "" {
			form = pickWeighted(g.rng, personForms)
		}
		return g.renderPerson(person, form, p.Slot)

	case "ORG":
		famille := p.Args["famille"]
		org := memo(g, slotKey(p.Kind, p.Slot), p.Slot, func() Org { return g.NewOrgIn(famille) })
		return g.renderOrg(org, p.Args["form"], p.Slot)

	case "ADDR":
		addr := memo(g, slotKey(p.Kind, p.Slot), p.Slot, g.NewAddress)
		return g.renderAddress(addr, p.Args["form"], p.Slot)
	}

	// Valeurs non annotées : un seul segment, sans label.
	return []render.Segment{{Text: g.renderPlain(p)}}
}

// memo mémorise la valeur d'un slot nommé ; un slot vide n'est pas mémorisé.
func memo[T any](g *Generator, key, slot string, mk func() T) T {
	if slot == "" {
		return mk()
	}
	if v, ok := g.slots[key]; ok {
		return v.(T)
	}
	v := mk()
	g.slots[key] = v
	return v
}

func (g *Generator) renderPlain(p template.Placeholder) string {
	switch p.Kind {
	case "date":
		return g.renderDate(p)
	case "amount":
		return g.renderAmount(p)
	case "ref":
		return memo(g, slotKey(p.Kind, p.Slot), p.Slot, func() string { return g.newRef(p.Args["pattern"]) })
	case "siren":
		return memo(g, slotKey("siren", p.Slot), p.Slot, g.newSIREN)
	case "siret":
		// Le SIRET partage son SIREN avec le slot de même nom : sur une facture
		// réelle, « Siret : 32145678200002 » et « RCS : Dijon 321456782 »
		// désignent la même entreprise.
		siren := memo(g, slotKey("siren", p.Slot), p.Slot, g.newSIREN)
		return memo(g, slotKey("siret", p.Slot), p.Slot, func() string { return g.siretFor(siren) })
	case "iban":
		return memo(g, slotKey(p.Kind, p.Slot), p.Slot, g.newIBAN)
	case "email":
		return g.renderEmail(p)
	case "phone":
		return memo(g, slotKey(p.Kind, p.Slot)+p.Args["form"], p.Slot, func() string { return g.newPhone(p.Args["form"]) })
	case "decoy":
		return g.renderDecoy(p.Slot)
	}
	return ""
}

// --- Dates ---

var moisFR = []string{"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre"}
var joursFR = []string{"Dimanche", "Lundi", "Mardi", "Mercredi", "Jeudi", "Vendredi", "Samedi"}

// dateFor mémorise une date par slot, avec un décalage tiré autour de la date
// de référence du document. L'ordre obtenu est plausible sans être un invariant
// vérifié (DATASET.md § 6.3).
func (g *Generator) dateFor(slot string) time.Time {
	if slot == "" {
		return g.base.AddDate(0, 0, g.rng.Intn(60)-30)
	}
	key := "date:" + slot
	if v, ok := g.slots[key]; ok {
		return v.(time.Time)
	}
	var d time.Time
	switch slot {
	case "issue", "record", "sample":
		d = g.base
	case "due", "debit", "delivery", "edit":
		d = g.base.AddDate(0, 0, 1+g.rng.Intn(45))
	case "quote", "visit", "acompte", "period_start", "call":
		d = g.base.AddDate(0, 0, -(1 + g.rng.Intn(120)))
	case "birth":
		d = g.base.AddDate(-(1 + g.rng.Intn(85)), 0, -g.rng.Intn(365))
	case "legal":
		d = time.Date(1980+g.rng.Intn(35), time.Month(1+g.rng.Intn(12)), 1+g.rng.Intn(28), 0, 0, 0, 0, time.UTC)
	default:
		d = g.base.AddDate(0, 0, g.rng.Intn(60)-30)
	}
	g.slots[key] = d
	return d
}

func (g *Generator) renderDate(p template.Placeholder) string {
	d := g.dateFor(p.Slot)
	h, m, s := g.rng.Intn(24), g.rng.Intn(60), g.rng.Intn(60)
	switch p.Args["format"] {
	case "dash":
		return d.Format("02-01-2006")
	case "dm_dash":
		return d.Format("02-01")
	case "long":
		return fmt.Sprintf("%d %s %d", d.Day(), moisFR[d.Month()-1], d.Year())
	case "slash_time":
		return fmt.Sprintf("%s %02d:%02d", d.Format("02/01/2006"), h, m)
	case "dash_time":
		return fmt.Sprintf("%s %02d:%02d:%02d", d.Format("02-01-2006"), h, m, s)
	case "long_time":
		mois := title(moisFR[d.Month()-1])
		return fmt.Sprintf("%s %02d %s %d à %02dh%02d", joursFR[int(d.Weekday())], d.Day(), mois, d.Year(), h, m)
	default:
		return d.Format("02/01/2006")
	}
}

// --- Montants ---

func (g *Generator) renderAmount(p template.Placeholder) string {
	if p.Args["form"] == "medical" {
		return fmt.Sprintf("%d,%02d %s", g.rng.Intn(200), g.rng.Intn(100),
			[]string{"g/dL", "T/L", "G/L", "%", "fL", "pg", "mmol/L"}[g.rng.Intn(7)])
	}
	key := "amount:" + p.Slot
	if p.Slot != "" {
		if v, ok := g.slots[key]; ok {
			return v.(string)
		}
	}
	cents := g.rng.Intn(100)
	units := 1 + g.rng.Intn(8000)
	sep := []string{",", "."}[g.rng.Intn(2)]
	var s string
	if units >= 1000 {
		s = fmt.Sprintf("%d %03d%s%02d", units/1000, units%1000, sep, cents)
	} else {
		s = fmt.Sprintf("%d%s%02d", units, sep, cents)
	}
	// Le symbole monétaire est présent une fois sur deux : sur les documents
	// réels, les colonnes de tableau l'omettent et les totaux le portent.
	switch n := g.rng.Float64(); {
	case n < 0.35:
		s += " €"
	case n < 0.45:
		s += " e" // restitution LaTeX de « € » observée sur la facture télécom
	}
	if p.Slot != "" {
		g.slots[key] = s
	}
	return s
}

// --- Références ---

func (g *Generator) newRef(pattern string) string {
	switch pattern {
	case "FA", "DE", "FD":
		return fmt.Sprintf("%s%08d", pattern, g.rng.Intn(100000000))
	case "FPX":
		return fmt.Sprintf("FPX%d%s%06d", 2020+g.rng.Intn(6),
			string(rune('A'+g.rng.Intn(26))), g.rng.Intn(1000000))
	case "CS":
		return fmt.Sprintf("CS%010d", g.rng.Intn(10000000000))
	case "numeric10":
		return fmt.Sprintf("%010d", g.rng.Int63n(10000000000))
	default:
		return fmt.Sprintf("REF-%d-%05d", 2020+g.rng.Intn(6), g.rng.Intn(100000))
	}
}

// --- Téléphones ---

func (g *Generator) newPhone(form string) string {
	prefix := []string{"01", "02", "03", "04", "05", "06", "07", "09"}[g.rng.Intn(8)]
	d := make([]string, 4)
	for i := range d {
		d[i] = fmt.Sprintf("%02d", g.rng.Intn(100))
	}
	switch form {
	case "dotted":
		return prefix + "." + strings.Join(d, ".")
	case "spaced":
		return prefix + " " + strings.Join(d, " ")
	default:
		return prefix + strings.Join(d, "")
	}
}

// --- Emails ---

func (g *Generator) renderEmail(p template.Placeholder) string {
	local := "contact"
	domain := g.pick("activites")
	if from := p.Args["from"]; from != "" {
		if v, ok := g.slots[slotKey("PER", from)]; ok {
			person := v.(Person)
			local = strings.ToLower(stripAccents(person.First + "." + person.Last))
		} else if v, ok := g.slots[slotKey("ORG", from)]; ok {
			org := v.(Org)
			local = strings.ToLower(stripAccents(strings.Fields(org.Denomination)[0]))
			domain = org.Short
		}
	}
	domain = strings.ToLower(stripAccents(strings.ReplaceAll(domain, " ", "-")))
	addr := local + "@" + domain + "." + []string{"fr", "com", "org"}[g.rng.Intn(3)]
	if g.rng.Float64() < 0.15 {
		addr = strings.ToUpper(addr)
	}
	// cut=eol reproduit la césure de fin de ligne observée sur la facture d'établissement public, qui met
	// reEmail en défaut en tronquant le domaine.
	if p.Args["cut"] == "eol" && g.rng.Float64() < 0.5 {
		at := strings.Index(addr, "@")
		cut := at + 1 + g.rng.Intn(max(1, len(addr)-at-4))
		addr = addr[:cut] + "-\n" + addr[cut:]
	}
	return addr
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
