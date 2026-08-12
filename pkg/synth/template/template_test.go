package template

import (
	"strings"
	"testing"
)

const header = "type: facture\nlang: fr\n---\n"

func parse(t *testing.T, body string) *Template {
	t.Helper()
	tmpl, err := Parse("test", header+body)
	if err != nil {
		t.Fatalf("Parse : %v", err)
	}
	return tmpl
}

func TestParseHeader(t *testing.T) {
	tmpl, err := Parse("x", "type: facture\nlang: fr\nsource: facture d'établissement public\nweight: 2.5\nnoise: 0.4\n---\ncorps")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Type != "facture" || tmpl.Lang != "fr" || tmpl.Source != "facture d'établissement public" {
		t.Errorf("en-tête mal analysé : %+v", tmpl)
	}
	if tmpl.Weight != 2.5 || tmpl.Noise != 0.4 {
		t.Errorf("weight/noise mal analysés : %v %v", tmpl.Weight, tmpl.Noise)
	}
}

func TestParseHeaderErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"sans séparateur", "type: facture\nlang: fr\ncorps"},
		{"type manquant", "lang: fr\n---\ncorps"},
		{"lang manquante", "type: facture\n---\ncorps"},
		{"clé inconnue", "type: facture\nlang: fr\ncouleur: bleu\n---\n"},
		{"weight invalide", "type: facture\nlang: fr\nweight: beaucoup\n---\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse("x", tt.src); err == nil {
				t.Error("erreur attendue")
			}
		})
	}
}

func TestParsePlaceholder(t *testing.T) {
	tmpl := parse(t, "Client : {{PER:buyer|form=civ_prenom_nom}} fin")
	if len(tmpl.Body) != 3 {
		t.Fatalf("attendu 3 nœuds, got %d : %+v", len(tmpl.Body), tmpl.Body)
	}
	p, ok := tmpl.Body[1].(Placeholder)
	if !ok {
		t.Fatalf("nœud 1 n'est pas un Placeholder : %T", tmpl.Body[1])
	}
	if p.Kind != "PER" || p.Slot != "buyer" || p.Args["form"] != "civ_prenom_nom" {
		t.Errorf("placeholder mal analysé : %+v", p)
	}
	if !p.Annotated || p.Label() != "PER" {
		t.Errorf("PER doit être annoté, got Annotated=%v Label=%q", p.Annotated, p.Label())
	}
}

// TestCaseCarriesMeaning couvre la convention centrale : majuscules = annoté.
func TestCaseCarriesMeaning(t *testing.T) {
	tests := []struct {
		kind      string
		label     string
		annotated bool
	}{
		{"PER", "PER", true},
		{"ORG", "ORG", true},
		{"ADDR", "LOC", true},
		{"date", "", false},
		{"siret", "", false},
		{"decoy", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			tmpl := parse(t, "{{"+tt.kind+":x}}")
			p := tmpl.Body[0].(Placeholder)
			if p.Annotated != tt.annotated || p.Label() != tt.label {
				t.Errorf("{{%s}} : Annotated=%v Label=%q, attendu %v/%q",
					tt.kind, p.Annotated, p.Label(), tt.annotated, tt.label)
			}
		})
	}
}

func TestParseUnknownPlaceholderFails(t *testing.T) {
	// Une faute de frappe doit échouer, jamais produire du texte littéral :
	// un template silencieusement dégradé produirait un corpus dégradé.
	if _, err := Parse("x", header+"{{PERSONNE:buyer}}"); err == nil {
		t.Error("placeholder inconnu doit être une erreur")
	}
}

func TestParsePad(t *testing.T) {
	tmpl := parse(t, "a{{pad:4-8}}b")
	p, ok := tmpl.Body[1].(Pad)
	if !ok {
		t.Fatalf("attendu Pad, got %T", tmpl.Body[1])
	}
	if p.Min != 4 || p.Max != 8 {
		t.Errorf("bornes %d-%d, attendu 4-8", p.Min, p.Max)
	}
	if _, err := Parse("x", header+"{{pad:8-4}}"); err == nil {
		t.Error("bornes inversées doivent échouer")
	}
}

func TestParseOptional(t *testing.T) {
	tmpl := parse(t, "avant[?contact]dedans {{PER:c}}[/]après")
	var opt Optional
	found := false
	for _, n := range tmpl.Body {
		if o, ok := n.(Optional); ok {
			opt, found = o, true
		}
	}
	if !found {
		t.Fatalf("aucune section optionnelle : %+v", tmpl.Body)
	}
	if opt.Name != "contact" {
		t.Errorf("nom %q, attendu contact", opt.Name)
	}
	if len(opt.Body) != 2 {
		t.Errorf("corps de section : %d nœuds, attendu 2 : %+v", len(opt.Body), opt.Body)
	}
	if _, err := Parse("x", header+"[?x]jamais fermé"); err == nil {
		t.Error("section non fermée doit échouer")
	}
}

func TestParseBlockAndRepeat(t *testing.T) {
	src := header + "@block lignes\n{{amount:pu}}\n@end\n\ndébut\n{{LINES:3-8}}\nfin"
	tmpl, err := Parse("x", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Blocks) != 1 || len(tmpl.Blocks["lignes"]) == 0 {
		t.Fatalf("bloc non extrait : %+v", tmpl.Blocks)
	}
	var rep Repeat
	found := false
	for _, n := range tmpl.Body {
		if r, ok := n.(Repeat); ok {
			rep, found = r, true
		}
	}
	if !found {
		t.Fatal("aucun Repeat dans le corps")
	}
	// {{LINES:n-m}} sans nom doit se résoudre vers l'unique bloc déclaré.
	if rep.Block != "lignes" || rep.Min != 3 || rep.Max != 8 {
		t.Errorf("repeat mal résolu : %+v", rep)
	}
}

func TestBlockErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"bloc non fermé", "@block a\nx\n"},
		{"@end orphelin", "@end\n"},
		{"bloc dupliqué", "@block a\nx\n@end\n@block a\ny\n@end\n"},
		{"bloc inconnu", "@block a\nx\n@end\n{{LINES:absent:1-2}}"},
		{"LINES ambigu", "@block a\nx\n@end\n@block b\ny\n@end\n{{LINES:1-2}}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse("x", header+tt.body); err == nil {
				t.Error("erreur attendue")
			}
		})
	}
}

func TestParseNoise(t *testing.T) {
	tmpl := parse(t, "a{{noise:intraword}}bruité {{PER:x}}{{/noise}}b")
	var n Noise
	found := false
	for _, node := range tmpl.Body {
		if v, ok := node.(Noise); ok {
			n, found = v, true
		}
	}
	if !found {
		t.Fatalf("zone de bruit absente : %+v", tmpl.Body)
	}
	if n.Kind != "intraword" || len(n.Body) != 2 {
		t.Errorf("zone de bruit mal analysée : %+v", n)
	}
	if _, err := Parse("x", header+"{{noise:intraword}}jamais fermé"); err == nil {
		t.Error("zone de bruit non fermée doit échouer")
	}
}

func TestParseUnclosedBraces(t *testing.T) {
	if _, err := Parse("x", header+"{{PER:x"); err == nil {
		t.Error("« {{ » non fermé doit échouer")
	}
}

// TestParseRealTemplates vérifie que les templates livrés restent analysables :
// ils sont la référence de la syntaxe.
func TestParseRealTemplates(t *testing.T) {
	tmpl := parse(t, strings.Join([]string{
		"{{ORG:seller|form=juridique_patronyme}}",
		"{{ADDR:seller_addr|form=street}}",
		"{{pad:100-110}}{{PER:buyer|form=civ_nom_prenom}}",
		"SIRET : {{siret:seller}} - RCS : {{ADDR:c|form=city}} {{siren:seller}}",
		"IBAN: {{iban:seller}}",
		"{{email:seller|from=seller|cut=eol}}",
	}, "\n"))
	placeholders := 0
	for _, n := range tmpl.Body {
		if _, ok := n.(Placeholder); ok {
			placeholders++
		}
	}
	// 8 placeholders : ORG, ADDR, PER, siret, ADDR, siren, iban, email.
	// {{pad}} n'en est pas un — c'est un nœud distinct.
	if placeholders != 8 {
		t.Errorf("%d placeholders analysés, attendu 8", placeholders)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(header + "{{PER:x}}")
	f.Add(header + "[?a]{{ORG:y}}[/]")
	f.Add(header + "@block b\n{{pad:1-2}}\n@end\n{{LINES:1-3}}")
	f.Fuzz(func(t *testing.T, src string) {
		// Le parser doit soit réussir, soit retourner une erreur — jamais
		// paniquer sur une entrée malformée.
		_, _ = Parse("fuzz", src)
	})
}
