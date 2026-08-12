// Package render assemble un document synthétique sous forme de segments
// annotés, puis en dérive le texte et les spans.
//
// Le renderer ne produit jamais une chaîne directement : il produit une liste
// de segments dont chacun porte son label. Le texte final et les offsets sont
// calculés par concaténation. C'est ce qui rend le désalignement d'offsets
// inexprimable plutôt que seulement testé (DATASET.md § 3.2).
package render

import (
	"strings"

	"github.com/bornholm/go-anon/pkg/ner"
)

// Segment est un fragment de document. Label vide = texte non annoté.
type Segment struct {
	Text  string
	Label ner.EntityType
	// Slot et Form tracent l'origine du segment, pour l'analyse d'erreur.
	Slot string
	Form string
}

// Document est la sortie du renderer.
type Document struct {
	Segments []Segment
	Meta     Metadata
}

// Metadata accompagne chaque document dans le JSONL de sortie.
type Metadata struct {
	Index     int      `json:"index"`
	Template  string   `json:"template"`
	Type      string   `json:"type"`
	Lang      string   `json:"lang"`
	Source    string   `json:"source,omitempty"`
	Seed      uint64   `json:"seed"`
	Noised    bool     `json:"noised"`
	Sections  []string `json:"sections,omitempty"`
	NumTokens int      `json:"num_tokens"`
	Entities  []Span   `json:"entities"`
}

// Span est une entité annotée avec ses offsets byte dans le texte final.
type Span struct {
	Text  string         `json:"text"`
	Label ner.EntityType `json:"label"`
	Start int            `json:"start"`
	End   int            `json:"end"`
	Slot  string         `json:"slot,omitempty"`
	Form  string         `json:"form,omitempty"`
}

// Build concatène les segments et retourne le texte complet avec ses spans.
//
// Les offsets ne sont jamais maintenus à la main : ils sont une conséquence de
// la concaténation. Un segment annoté vide est ignoré plutôt que de produire un
// span dégénéré.
func (d *Document) Build() (string, []Span) {
	var b strings.Builder
	var spans []Span
	for _, s := range d.Segments {
		if s.Text == "" {
			continue
		}
		start := b.Len()
		b.WriteString(s.Text)
		if s.Label != "" {
			spans = append(spans, Span{
				Text:  s.Text,
				Label: s.Label,
				Start: start,
				End:   b.Len(),
				Slot:  s.Slot,
				Form:  s.Form,
			})
		}
	}
	return b.String(), spans
}

// Append ajoute un segment non annoté.
func (d *Document) Append(text string) {
	d.Segments = append(d.Segments, Segment{Text: text})
}

// AppendEntity ajoute un segment annoté.
func (d *Document) AppendEntity(text string, label ner.EntityType, slot, form string) {
	d.Segments = append(d.Segments, Segment{Text: text, Label: label, Slot: slot, Form: form})
}
