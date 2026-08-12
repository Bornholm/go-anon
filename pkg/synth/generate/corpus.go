package generate

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/render"
	"github.com/bornholm/go-anon/pkg/synth/template"
)

// LoadTemplates lit tous les templates d'une langue depuis un répertoire.
func LoadTemplates(dir, lang string) ([]*template.Template, error) {
	matches, err := filepath.Glob(filepath.Join(dir, lang, "*.tmpl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("aucun template dans %s pour la langue %s", dir, lang)
	}
	var out []*template.Template
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		t, err := template.Parse(strings.TrimSuffix(filepath.Base(m), ".tmpl"), string(b))
		if err != nil {
			return nil, err
		}
		if t.Lang != lang {
			return nil, fmt.Errorf("%s : lang=%q dans un répertoire %q", m, t.Lang, lang)
		}
		out = append(out, t)
	}
	return out, nil
}

// Manifest accompagne un corpus généré. Sans lui, un écart de performance
// entre deux entraînements est inexploitable : impossible de distinguer un
// changement de modèle d'un changement de corpus (DATASET.md § 11.2).
type Manifest struct {
	Generator     string            `json:"generator"`
	GeneratedAt   string            `json:"generated_at"`
	Seed          uint64            `json:"seed"`
	Lang          string            `json:"lang"`
	Count         int               `json:"count"`
	Options       Options           `json:"options"`
	Tokenizer     string            `json:"tokenizer"`
	Templates     map[string]string `json:"templates"` // nom → SHA-256
	Gazetteers    []string          `json:"gazetteers"`
	LabelCounts   map[string]int    `json:"label_counts"`
	TokenCount    int               `json:"token_count"`
	SentenceCount int               `json:"sentence_count"`
}

// GeneratorVersion identifie la version du générateur dans le manifest.
const GeneratorVersion = "synthcorpus/0.1"

// Corpus produit un corpus complet et écrit les trois sorties : CoNLL BIO,
// JSONL de métadonnées, manifest.
func Corpus(templates []*template.Template, bundle *gazetteer.Bundle, lang string, count int, opts Options, outDir string) (*Manifest, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	conllFile, err := os.Create(filepath.Join(outDir, "corpus.conll"))
	if err != nil {
		return nil, err
	}
	defer conllFile.Close()
	metaFile, err := os.Create(filepath.Join(outDir, "documents.jsonl"))
	if err != nil {
		return nil, err
	}
	defer metaFile.Close()

	conll := bufio.NewWriter(conllFile)
	meta := bufio.NewWriter(metaFile)
	defer conll.Flush()
	defer meta.Flush()

	man := &Manifest{
		Generator:   GeneratorVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Seed:        opts.Seed,
		Lang:        lang,
		Count:       count,
		Options:     opts,
		Tokenizer:   fmt.Sprintf("%T", Tokenizer(lang)),
		Templates:   map[string]string{},
		Gazetteers:  bundle.Names(),
		LabelCounts: map[string]int{},
	}
	sort.Strings(man.Gazetteers)
	for _, t := range templates {
		man.Templates[t.Name] = ""
	}

	// Le choix du template est tiré sur une source dédiée : il ne doit pas
	// dépendre du contenu des documents précédents.
	pickRng := rand.New(rand.NewSource(int64(opts.Seed) ^ 0x5eed))
	totalWeight := 0.0
	for _, t := range templates {
		totalWeight += t.Weight
	}

	for i := 0; i < count; i++ {
		t := pickTemplate(templates, totalWeight, pickRng)
		doc := Document(t, bundle, i, opts)
		text, spans := doc.Build()

		sentences, err := ProjectBIO(text, spans, lang)
		if err != nil {
			return nil, fmt.Errorf("document %d (%s) : %w", i, t.Name, err)
		}

		for _, s := range sentences {
			for _, tok := range s {
				if _, err := fmt.Fprintf(conll, "%s\t%s\n", tok.Word, tok.Tag); err != nil {
					return nil, err
				}
				man.TokenCount++
				if tok.Tag != "O" {
					man.LabelCounts[tok.Tag]++
				}
			}
			fmt.Fprintln(conll)
			man.SentenceCount++
		}

		doc.Meta.Entities = spans
		doc.Meta.NumTokens = countTokens(sentences)
		line, err := json.Marshal(doc.Meta)
		if err != nil {
			return nil, err
		}
		meta.Write(line)
		meta.WriteByte('\n')
	}

	if err := hashTemplates(man, templates); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(outDir, "manifest.json"), man); err != nil {
		return nil, err
	}
	return man, nil
}

func pickTemplate(templates []*template.Template, total float64, rng *rand.Rand) *template.Template {
	target := rng.Float64() * total
	for _, t := range templates {
		target -= t.Weight
		if target <= 0 {
			return t
		}
	}
	return templates[len(templates)-1]
}

func countTokens(sentences []corpus.Sentence) int {
	n := 0
	for _, s := range sentences {
		n += len(s)
	}
	return n
}

// hashTemplates empreinte les templates : deux corpus produits avec la même
// seed mais des templates modifiés ne sont pas comparables.
func hashTemplates(man *Manifest, templates []*template.Template) error {
	for _, t := range templates {
		h := sha256.New()
		fmt.Fprintf(h, "%s\n%s\n%s\n%v\n%v\n", t.Name, t.Type, t.Source, t.Weight, t.Noise)
		if err := hashNodes(h, t.Body); err != nil {
			return err
		}
		names := make([]string, 0, len(t.Blocks))
		for n := range t.Blocks {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(h, "block:%s\n", n)
			if err := hashNodes(h, t.Blocks[n]); err != nil {
				return err
			}
		}
		man.Templates[t.Name] = hex.EncodeToString(h.Sum(nil))
	}
	return nil
}

func hashNodes(w io.Writer, nodes []template.Node) error {
	for _, n := range nodes {
		if _, err := fmt.Fprintf(w, "%#v\n", n); err != nil {
			return err
		}
		switch v := n.(type) {
		case template.Optional:
			if err := hashNodes(w, v.Body); err != nil {
				return err
			}
		case template.Noise:
			if err := hashNodes(w, v.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Sample rend un document complet en texte, pour l'inspection manuelle du
// palier P0 : un corpus qu'aucun humain n'a relu n'est pas validé.
func Sample(t *template.Template, bundle *gazetteer.Bundle, index int, opts Options) (string, []render.Span) {
	doc := Document(t, bundle, index, opts)
	return doc.Build()
}
