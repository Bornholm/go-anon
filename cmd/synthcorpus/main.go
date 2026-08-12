// Command synthcorpus génère, valide et décrit un corpus synthétique annoté.
//
//	synthcorpus generate -lang fr -count 100 -out data/synth/p0
//	synthcorpus validate -corpus data/synth/p0
//	synthcorpus stats    -corpus data/synth/p0
//	synthcorpus sample   -lang fr -template facture-artisan
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bornholm/go-anon/pkg/synth/gazetteer"
	"github.com/bornholm/go-anon/pkg/synth/generate"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "generate":
		err = cmdGenerate(os.Args[2:])
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "stats":
		err = cmdStats(os.Args[2:])
	case "sample":
		err = cmdSample(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `synthcorpus <commande> [options]

  generate   produit un corpus (CoNLL BIO + JSONL + manifest)
  validate   vérifie les invariants d'un corpus produit
  stats      rapport descriptif d'un corpus
  sample     rend un document en clair, avec ses spans, pour relecture`)
}

// loadBundle charge les gazetteers : socle embarqué, éventuellement surchargé
// par un répertoire de fichiers complets.
func loadBundle(lang, dir string, alpha float64) (*gazetteer.Bundle, error) {
	opts := gazetteer.Options{Alpha: alpha}
	if dir == "" {
		return gazetteer.LoadSeed(lang, opts)
	}
	return gazetteer.LoadDir(lang, dir, opts)
}

func cmdGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	lang := fs.String("lang", "fr", "langue du corpus")
	count := fs.Int("count", 100, "nombre de documents")
	out := fs.String("out", "data/synth/p0", "répertoire de sortie")
	tmplDir := fs.String("templates", "templates", "répertoire des templates")
	gazDir := fs.String("gazetteers", "", "répertoire de gazetteers surchargeant le socle embarqué")
	seed := fs.Uint64("seed", 1, "seed globale")
	alpha := fs.Float64("alpha", 0.6, "exposant d'aplatissement des poids de gazetteer")
	optRate := fs.Float64("optional-rate", 0.6, "probabilité de retenir une section optionnelle")
	noiseRate := fs.Float64("noise-rate", 1.0, "multiplicateur du taux de bruit déclaré par template")
	noiseIntensity := fs.Float64("noise-intensity", 0.35, "probabilité d'espace parasite par caractère")
	if err := fs.Parse(args); err != nil {
		return err
	}

	templates, err := generate.LoadTemplates(*tmplDir, *lang)
	if err != nil {
		return err
	}
	bundle, err := loadBundle(*lang, *gazDir, *alpha)
	if err != nil {
		return err
	}

	opts := generate.Options{
		Seed:           *seed,
		OptionalRate:   *optRate,
		NoiseRate:      *noiseRate,
		NoiseIntensity: *noiseIntensity,
	}
	man, err := generate.Corpus(templates, bundle, *lang, *count, opts, *out)
	if err != nil {
		return err
	}

	fmt.Printf("%d documents · %d phrases · %d tokens → %s\n",
		man.Count, man.SentenceCount, man.TokenCount, *out)
	printLabelCounts(man.LabelCounts, man.TokenCount)
	return nil
}

func printLabelCounts(counts map[string]int, total int) {
	for _, tag := range []string{"B-PER", "I-PER", "B-ORG", "I-ORG", "B-LOC", "I-LOC"} {
		n := counts[tag]
		fmt.Printf("  %-6s %7d  (%.2f %%)\n", tag, n, 100*float64(n)/float64(total))
	}
}

func cmdSample(args []string) error {
	fs := flag.NewFlagSet("sample", flag.ExitOnError)
	lang := fs.String("lang", "fr", "langue")
	name := fs.String("template", "", "nom du template (défaut : le premier)")
	index := fs.Int("index", 0, "index du document")
	tmplDir := fs.String("templates", "templates", "répertoire des templates")
	gazDir := fs.String("gazetteers", "", "répertoire de gazetteers")
	seed := fs.Uint64("seed", 1, "seed globale")
	alpha := fs.Float64("alpha", 0.6, "exposant d'aplatissement")
	showSpans := fs.Bool("spans", true, "afficher les spans annotés")
	if err := fs.Parse(args); err != nil {
		return err
	}

	templates, err := generate.LoadTemplates(*tmplDir, *lang)
	if err != nil {
		return err
	}
	bundle, err := loadBundle(*lang, *gazDir, *alpha)
	if err != nil {
		return err
	}

	tmpl := templates[0]
	if *name != "" {
		found := false
		for _, t := range templates {
			if t.Name == *name {
				tmpl, found = t, true
			}
		}
		if !found {
			return fmt.Errorf("template %q introuvable", *name)
		}
	}

	opts := generate.DefaultOptions()
	opts.Seed = *seed
	text, spans := generate.Sample(tmpl, bundle, *index, opts)
	fmt.Println(text)
	if *showSpans {
		fmt.Printf("\n--- %d spans ---\n", len(spans))
		for _, s := range spans {
			fmt.Printf("%-5s [%5d:%5d] %-12s %q\n", s.Label, s.Start, s.End, s.Form, s.Text)
		}
	}
	return nil
}
