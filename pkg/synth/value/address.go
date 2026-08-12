package value

import (
	"fmt"
	"strings"

	"github.com/bornholm/go-anon/pkg/ner"
	"github.com/bornholm/go-anon/pkg/synth/render"
)

// Address est une adresse tirée pour un slot. Toutes les formes d'un même slot
// en dérivent, ce qui garantit la cohérence voie / code postal / commune.
type Address struct {
	Number   string
	VoieType string
	VoieName string
	Postal   string
	City     string
	Cedex    string
	Freeform string
}

// NewAddress tire une adresse cohérente : le code postal provient du gazetteer
// de communes, jamais d'un tirage indépendant.
func (g *Generator) NewAddress() Address {
	commune := g.set("communes").Pick(g.rng)
	a := Address{
		City:     commune.Value,
		Postal:   commune.Metadata["cp"],
		VoieType: g.pick("voie_types"),
	}
	if a.Postal == "" {
		a.Postal = fmt.Sprintf("%05d", 1000+g.rng.Intn(94000))
	}
	a.Number = fmt.Sprintf("%d", 1+g.rng.Intn(120))
	switch n := g.rng.Float64(); {
	case n < 0.08:
		a.Number += " Bis"
	case n < 0.11:
		a.Number += " Ter"
	}
	// Une voie sur trois porte un nom de personne : c'est la source du négatif
	// « prénom + patronyme capitalisés ≠ PER » (avenue Gaston Meunier).
	if g.rng.Float64() < 0.33 {
		p := g.NewPerson()
		a.VoieName = p.First + " " + p.Last
	} else {
		a.VoieName = g.pick("voie_noms")
	}
	if g.rng.Float64() < 0.12 {
		a.Cedex = fmt.Sprintf("Cedex %02d", 1+g.rng.Intn(20))
	}
	a.Freeform = strings.ToUpper(stripAccents(
		[]string{
			"EN FACE DE LA CABINE TELEPHONIQUE",
			"LIEU DIT LES GRANDES TERRES",
			"DERRIERE L'ANCIENNE POSTE",
			"ROUTE DEPARTEMENTALE SANS NUMERO",
		}[g.rng.Intn(4)]))
	return a
}

// Street retourne la ligne de voie (« 2 Bis petite rue du Lavoir »).
func (a Address) Street() string {
	return a.Number + " " + a.VoieType + " " + a.VoieName
}

// CityCode retourne « 21400 Montigny le roi ».
func (a Address) CityCode() string { return a.Postal + " " + a.City }

// CityCedex retourne « 92120 Montrouge Cedex 08 » si un cedex a été tiré.
func (a Address) CityCedex() string {
	if a.Cedex == "" {
		return a.CityCode()
	}
	return a.CityCode() + " " + a.Cedex
}

func (g *Generator) renderAddress(a Address, form, slot string) []render.Segment {
	seg := func(text, form string) render.Segment {
		return render.Segment{Text: text, Label: ner.TypeLOC, Slot: slot, Form: form}
	}
	switch form {
	case "street":
		return []render.Segment{seg(a.Street(), form)}
	case "street_caps":
		return []render.Segment{seg(strings.ToUpper(stripAccents(a.Street())), form)}
	case "citycode":
		return []render.Segment{seg(a.CityCode(), form)}
	case "citycode_caps":
		return []render.Segment{seg(strings.ToUpper(stripAccents(a.CityCode())), form)}
	case "citycode_dash":
		// « 21400 − CHATILLON », signe moins U+2212 (compte-rendu de laboratoire).
		return []render.Segment{seg(a.Postal+" − "+strings.ToUpper(stripAccents(a.City)), form)}
	case "citycedex":
		return []render.Segment{seg(a.CityCedex(), form)}
	case "citycedex_caps":
		return []render.Segment{seg(strings.ToUpper(stripAccents(a.CityCedex())), form)}
	case "city":
		return []render.Segment{seg(a.City, form)}
	case "city_caps":
		return []render.Segment{seg(strings.ToUpper(stripAccents(a.City)), form)}
	case "inline":
		return []render.Segment{seg(a.Street()+" "+a.CityCode(), form)}
	case "inline_dash_caps":
		return []render.Segment{seg(strings.ToUpper(stripAccents(a.Street()+" − "+a.CityCode())), form)}
	case "freeform":
		// Adresse non normalisée d'un champ libre : annotée quand même, elle
		// localise la personne (facture d'énergie, « lieu de consommation »).
		return []render.Segment{seg(a.Freeform, form)}
	default:
		return []render.Segment{seg(a.Street()+" "+a.CityCode(), "inline")}
	}
}
