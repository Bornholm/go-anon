// Package template analyse les templates de documents synthétiques.
//
// La bibliothèque standard n'est pas utilisée : son rendu ne restitue pas les
// offsets des valeurs insérées, qui sont exactement ce dont le générateur a
// besoin (DATASET.md § 6.2). Le parser produit un AST que le renderer parcourt
// en émettant des segments annotés.
package template

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Template est un document modèle analysé.
type Template struct {
	Name   string
	Type   string  // facture, compte_rendu_medical…
	Lang   string  // fr, en, es
	Source string  // type de document observé dont la forme est reprise
	Weight float64 // pondération du tirage entre templates
	Noise  float64 // probabilité d'appliquer le bruit d'espacement

	// Blocks contient les corps répétables déclarés par @block … @end.
	Blocks map[string][]Node
	// blockOrder conserve l'ordre de déclaration, pour résoudre {{LINES:n-m}}
	// sans nom explicite.
	blockOrder []string

	Body []Node
}

// Node est un élément de l'AST.
type Node interface{ node() }

// Text est du texte littéral.
type Text struct{ S string }

// Placeholder est une valeur à générer. Annotated distingue les entités
// annotées (PER, ORG, ADDR en majuscules) des valeurs de contexte.
type Placeholder struct {
	Kind      string // PER, ORG, ADDR, date, amount, ref, siret, siren, iban, email, phone, decoy
	Slot      string
	Args      map[string]string
	Annotated bool
}

// Pad insère entre Min et Max espaces, pour reproduire l'entrelacement des
// colonnes du texte extrait.
type Pad struct{ Min, Max int }

// Repeat insère un bloc entre Min et Max fois.
type Repeat struct {
	Block    string
	Min, Max int
}

// Optional est une section tirée ou non. Deux sections de même nom partagent la
// décision, ce qui permet de corréler des fragments distants.
type Optional struct {
	Name string
	Body []Node
}

// Noise délimite une zone où le bruit d'espacement intra-mot peut s'appliquer.
type Noise struct {
	Kind string
	Body []Node
}

func (Text) node()        {}
func (Placeholder) node() {}
func (Pad) node()         {}
func (Repeat) node()      {}
func (Optional) node()    {}
func (Noise) node()       {}

// annotatedKinds associe les placeholders annotés à leur label CRF.
// La casse porte le sens : majuscules = annoté.
var annotatedKinds = map[string]string{
	"PER":  "PER",
	"ORG":  "ORG",
	"ADDR": "LOC",
}

// Label retourne le label CRF d'un placeholder annoté, ou "" sinon.
func (p Placeholder) Label() string { return annotatedKinds[p.Kind] }

// knownKinds recense les placeholders acceptés. Un placeholder inconnu est une
// erreur de parsing : un template qui contient une faute de frappe doit
// échouer, pas produire silencieusement du texte littéral.
var knownKinds = map[string]bool{
	"PER": true, "ORG": true, "ADDR": true,
	"date": true, "amount": true, "ref": true,
	"siret": true, "siren": true, "iban": true,
	"email": true, "phone": true, "decoy": true,
}

// KnownFamilies recense les familles d'organisation, telles que les porte la
// métadonnée « f » des gazetteers org_types et org_domaines. Un argument
// famille inconnu est une erreur de parsing : sans cela une faute de frappe
// retomberait silencieusement sur l'ensemble des familles, et le document
// mêlerait les secteurs sans que rien ne le signale.
//
// pkg/synth/value vérifie par test que cette liste couvre exactement les
// familles réellement présentes dans les gazetteers.
var KnownFamilies = map[string]bool{
	"sante": true, "social": true, "administration": true,
	"commerce": true, "technique": true, "assurance": true,
}

// Parse analyse un template complet (en-tête + corps).
func Parse(name, src string) (*Template, error) {
	t := &Template{Name: name, Weight: 1, Blocks: map[string][]Node{}}

	head, body, err := splitHeader(src)
	if err != nil {
		return nil, fmt.Errorf("%s : %w", name, err)
	}
	if err := t.parseHeader(head); err != nil {
		return nil, fmt.Errorf("%s : %w", name, err)
	}

	body, err = t.extractBlocks(body)
	if err != nil {
		return nil, fmt.Errorf("%s : %w", name, err)
	}

	nodes, rest, err := parseNodes(body, "")
	if err != nil {
		return nil, fmt.Errorf("%s : %w", name, err)
	}
	if rest != "" {
		return nil, fmt.Errorf("%s : texte résiduel après analyse", name)
	}
	t.Body = nodes

	if err := t.validate(); err != nil {
		return nil, fmt.Errorf("%s : %w", name, err)
	}
	return t, nil
}

// splitHeader sépare l'en-tête « clé: valeur » du corps, au premier « --- »
// isolé sur sa ligne.
func splitHeader(src string) (head, body string, err error) {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "---" {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return "", "", fmt.Errorf("en-tête non terminé (ligne « --- » attendue)")
}

func (t *Template) parseHeader(head string) error {
	for _, l := range strings.Split(head, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			return fmt.Errorf("en-tête : ligne %q sans « : »", l)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "type":
			t.Type = v
		case "lang":
			t.Lang = v
		case "source":
			t.Source = v
		case "weight":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("en-tête : weight invalide %q", v)
			}
			t.Weight = f
		case "noise":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("en-tête : noise invalide %q", v)
			}
			t.Noise = f
		default:
			return fmt.Errorf("en-tête : clé inconnue %q", k)
		}
	}
	if t.Type == "" || t.Lang == "" {
		return fmt.Errorf("en-tête : « type » et « lang » sont obligatoires")
	}
	return nil
}

// extractBlocks retire les régions @block nom … @end du corps et les analyse
// séparément. Le découpage est fait ligne à ligne : un bloc est une unité de
// mise en page, il commence et finit sur ses propres lignes.
func (t *Template) extractBlocks(body string) (string, error) {
	var out []string
	var cur []string
	var curName string
	inBlock := false

	for _, l := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(trimmed, "@block "):
			if inBlock {
				return "", fmt.Errorf("@block %q imbriqué dans %q", trimmed, curName)
			}
			inBlock = true
			curName = strings.TrimSpace(strings.TrimPrefix(trimmed, "@block "))
			if curName == "" {
				return "", fmt.Errorf("@block sans nom")
			}
			if _, dup := t.Blocks[curName]; dup {
				return "", fmt.Errorf("@block %q déclaré deux fois", curName)
			}
			cur = nil
		case trimmed == "@end":
			if !inBlock {
				return "", fmt.Errorf("@end sans @block")
			}
			nodes, rest, err := parseNodes(strings.Join(cur, "\n"), "")
			if err != nil {
				return "", fmt.Errorf("bloc %q : %w", curName, err)
			}
			if rest != "" {
				return "", fmt.Errorf("bloc %q : texte résiduel", curName)
			}
			t.Blocks[curName] = nodes
			t.blockOrder = append(t.blockOrder, curName)
			inBlock = false
		case inBlock:
			cur = append(cur, l)
		default:
			out = append(out, l)
		}
	}
	if inBlock {
		return "", fmt.Errorf("@block %q non fermé", curName)
	}
	return strings.Join(out, "\n"), nil
}

// parseNodes analyse récursivement jusqu'au terminateur attendu (vide au
// niveau racine). Retourne les nœuds et le reste non consommé.
func parseNodes(src, terminator string) ([]Node, string, error) {
	var nodes []Node
	var lit strings.Builder

	flush := func() {
		if lit.Len() > 0 {
			nodes = append(nodes, Text{S: lit.String()})
			lit.Reset()
		}
	}

	for len(src) > 0 {
		if terminator != "" && strings.HasPrefix(src, terminator) {
			flush()
			return nodes, src[len(terminator):], nil
		}

		switch {
		case strings.HasPrefix(src, "{{"):
			end := strings.Index(src, "}}")
			if end < 0 {
				return nil, "", fmt.Errorf("« {{ » non fermé")
			}
			inner := src[2:end]
			src = src[end+2:]

			// {{/noise}} est un terminateur, jamais un placeholder.
			if strings.HasPrefix(inner, "/") {
				return nil, "", fmt.Errorf("fermeture inattendue {{%s}}", inner)
			}
			if strings.HasPrefix(inner, "noise:") {
				flush()
				body, rest, err := parseNodes(src, "{{/noise}}")
				if err != nil {
					return nil, "", err
				}
				nodes = append(nodes, Noise{Kind: strings.TrimPrefix(inner, "noise:"), Body: body})
				src = rest
				continue
			}
			n, err := parseDirective(inner)
			if err != nil {
				return nil, "", err
			}
			flush()
			nodes = append(nodes, n)

		case strings.HasPrefix(src, "[?"):
			end := strings.Index(src, "]")
			if end < 0 {
				return nil, "", fmt.Errorf("« [? » non fermé")
			}
			name := src[2:end]
			src = src[end+1:]
			body, rest, err := parseNodes(src, "[/]")
			if err != nil {
				return nil, "", err
			}
			flush()
			nodes = append(nodes, Optional{Name: name, Body: body})
			src = rest

		default:
			r, size := utf8.DecodeRuneInString(src)
			lit.WriteRune(r)
			src = src[size:]
		}
	}

	if terminator != "" {
		return nil, "", fmt.Errorf("section non fermée (%s attendu)", terminator)
	}
	flush()
	return nodes, "", nil
}

// parseDirective analyse le contenu d'un {{…}} hors sections.
func parseDirective(inner string) (Node, error) {
	if inner == "" {
		return nil, fmt.Errorf("directive vide {{}}")
	}
	head, args, _ := strings.Cut(inner, "|")
	kind, spec, hasSpec := strings.Cut(head, ":")

	switch kind {
	case "pad":
		if !hasSpec {
			return nil, fmt.Errorf("{{pad}} sans bornes")
		}
		min, max, err := parseRange(spec)
		if err != nil {
			return nil, fmt.Errorf("{{pad:%s}} : %w", spec, err)
		}
		return Pad{Min: min, Max: max}, nil

	case "LINES":
		if !hasSpec {
			return nil, fmt.Errorf("{{LINES}} sans bornes")
		}
		block := ""
		rangeSpec := spec
		// Forme {{LINES:nom:n-m}} : le nom du bloc précède les bornes.
		if a, b, ok := strings.Cut(spec, ":"); ok {
			block, rangeSpec = a, b
		}
		min, max, err := parseRange(rangeSpec)
		if err != nil {
			return nil, fmt.Errorf("{{LINES:%s}} : %w", spec, err)
		}
		return Repeat{Block: block, Min: min, Max: max}, nil
	}

	if !knownKinds[kind] {
		return nil, fmt.Errorf("placeholder inconnu {{%s}}", kind)
	}
	// Un placeholder sans slot est autonome : chaque occurrence tire une valeur
	// neuve, sans corrélation avec les autres.
	p := Placeholder{Kind: kind, Slot: spec, Args: map[string]string{}}
	p.Annotated = p.Label() != ""
	for _, a := range strings.Split(args, "|") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf("{{%s}} : argument %q sans « = »", inner, a)
		}
		p.Args[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if f, ok := p.Args["famille"]; ok {
		if kind != "ORG" {
			return nil, fmt.Errorf("{{%s}} : l'argument famille n'a de sens que sur ORG", inner)
		}
		if !KnownFamilies[f] {
			return nil, fmt.Errorf("{{%s}} : famille inconnue %q", inner, f)
		}
	}
	return p, nil
}

func parseRange(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return 0, 0, fmt.Errorf("borne invalide %q", s)
		}
		return n, n, nil
	}
	min, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil {
		return 0, 0, fmt.Errorf("borne min invalide %q", a)
	}
	max, err := strconv.Atoi(strings.TrimSpace(b))
	if err != nil {
		return 0, 0, fmt.Errorf("borne max invalide %q", b)
	}
	if min > max {
		return 0, 0, fmt.Errorf("bornes inversées (%d > %d)", min, max)
	}
	return min, max, nil
}

// validate résout les références de bloc et vérifie la cohérence globale.
func (t *Template) validate() error {
	var walk func([]Node) error
	walk = func(nodes []Node) error {
		for i, n := range nodes {
			switch v := n.(type) {
			case Repeat:
				if v.Block == "" {
					if len(t.blockOrder) != 1 {
						return fmt.Errorf("{{LINES}} sans nom de bloc mais %d blocs déclarés", len(t.blockOrder))
					}
					v.Block = t.blockOrder[0]
					nodes[i] = v
				}
				if _, ok := t.Blocks[v.Block]; !ok {
					return fmt.Errorf("bloc %q référencé mais non déclaré", v.Block)
				}
			case Optional:
				if err := walk(v.Body); err != nil {
					return err
				}
			case Noise:
				if err := walk(v.Body); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(t.Body)
}
