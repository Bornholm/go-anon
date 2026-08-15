// Package generate assemble les documents synthétiques : parcours de l'AST,
// tirage des valeurs, application du bruit, projection BIO.
package generate

import (
	"hash/fnv"
	"math/rand"
	"strings"

	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/render"
	"github.com/bornholm/go-anon/pkg/synth/template"
	"github.com/bornholm/go-anon/pkg/synth/value"
)

// Options pilote la génération d'un corpus.
type Options struct {
	// Seed globale ; chaque document en dérive la sienne.
	Seed uint64
	// OptionalRate est la probabilité de retenir une section optionnelle.
	OptionalRate float64
	// NoiseRate multiplie le taux déclaré par chaque template.
	NoiseRate float64
	// NoiseIntensity est la probabilité qu'un caractère d'une zone bruitée soit
	// suivi d'un espace parasite.
	NoiseIntensity float64
}

// DefaultOptions retourne des réglages de départ.
func DefaultOptions() Options {
	return Options{OptionalRate: 0.6, NoiseRate: 1.0, NoiseIntensity: 0.35}
}

// DocumentSeed dérive la seed d'un document.
//
// La dérivation est hiérarchique : régénérer le document n ne demande pas de
// rejouer les n−1 précédents, et ajouter des documents en fin de corpus ne
// modifie pas les existants.
func DocumentSeed(global uint64, typ, lang string, index int) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(global >> (8 * i))
	}
	h.Write(buf[:])
	h.Write([]byte(typ))
	h.Write([]byte(lang))
	for i := 0; i < 8; i++ {
		buf[i] = byte(uint64(index) >> (8 * i))
	}
	h.Write(buf[:])
	return h.Sum64()
}

// Document génère un document à partir d'un template.
func Document(t *template.Template, bundle *gazetteer.Bundle, index int, opts Options) *render.Document {
	seed := DocumentSeed(opts.Seed, t.Type, t.Lang, index)
	rng := rand.New(rand.NewSource(int64(seed)))
	gen := value.New(rng, bundle)

	r := &walker{
		tmpl:      t,
		rng:       rng,
		gen:       gen,
		opts:      opts,
		decisions: map[string]bool{},
	}
	doc := &render.Document{}
	r.walk(t.Body, doc, false)

	doc.Meta = render.Metadata{
		Index:    index,
		Template: t.Name,
		Type:     t.Type,
		Lang:     t.Lang,
		Source:   t.Source,
		Seed:     seed,
		Noised:   r.noised,
		Sections: r.sections,
	}
	return doc
}

type walker struct {
	tmpl      *template.Template
	rng       *rand.Rand
	gen       *value.Generator
	opts      Options
	decisions map[string]bool
	sections  []string
	noised    bool
}

func (w *walker) walk(nodes []template.Node, doc *render.Document, noisy bool) {
	for _, n := range nodes {
		switch v := n.(type) {
		case template.Text:
			doc.Segments = append(doc.Segments, w.noise(render.Segment{Text: v.S}, noisy))

		case template.Pad:
			n := v.Min
			if v.Max > v.Min {
				n += w.rng.Intn(v.Max - v.Min + 1)
			}
			doc.Append(strings.Repeat(" ", n))

		case template.Placeholder:
			for _, seg := range w.gen.Render(v) {
				doc.Segments = append(doc.Segments, w.noise(seg, noisy))
			}

		case template.Optional:
			if w.keep(v.Name) {
				w.sections = append(w.sections, v.Name)
				w.walk(v.Body, doc, noisy)
			}

		case template.Noise:
			apply := w.rng.Float64() < w.tmpl.Noise*w.opts.NoiseRate
			if apply {
				w.noised = true
			}
			w.walk(v.Body, doc, noisy || apply)

		case template.Repeat:
			count := v.Min
			if v.Max > v.Min {
				count += w.rng.Intn(v.Max - v.Min + 1)
			}
			body := w.tmpl.Blocks[v.Block]
			for i := 0; i < count; i++ {
				w.walk(body, doc, noisy)
				doc.Append("\n")
			}
		}
	}
}

// keep mémorise la décision par nom de section : deux sections homonymes,
// même distantes, sont retenues ou écartées ensemble.
func (w *walker) keep(name string) bool {
	if d, ok := w.decisions[name]; ok {
		return d
	}
	d := w.rng.Float64() < w.opts.OptionalRate
	w.decisions[name] = d
	return d
}

// noise applique l'espacement intra-mot à un segment.
//
// L'altération est **locale au segment** : le texte change, mais l'offset du
// segment est recalculé par la concaténation finale. C'est ce qui rend le
// désalignement impossible plutôt que seulement testé.
func (w *walker) noise(s render.Segment, noisy bool) render.Segment {
	if !noisy || s.Text == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s.Text) * 2)
	for _, r := range s.Text {
		b.WriteRune(r)
		if r != ' ' && r != '\n' && w.rng.Float64() < w.opts.NoiseIntensity {
			b.WriteByte(' ')
		}
	}
	s.Text = b.String()
	if s.Label != "" {
		// Un span ne doit pas se terminer par l'espace parasite : la frontière
		// annotée resterait correcte, mais le span porterait un caractère qui
		// n'appartient à aucun token.
		s.Text = strings.TrimRight(s.Text, " ")
	}
	return s
}
