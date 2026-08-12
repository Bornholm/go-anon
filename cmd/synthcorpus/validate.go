package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bornholm/go-anon/pkg/checksum"
	"github.com/bornholm/go-anon/pkg/ner"
	"github.com/bornholm/go-anon/pkg/synth/render"
)

// validLabels borne le jeu de labels attendu. Tout autre label est une erreur :
// le corpus est mélangé à WikiNER, un label inconnu contaminerait le modèle.
var validLabels = map[string]bool{"PER": true, "LOC": true, "ORG": true}

type report struct {
	errors   []string
	warnings []string
}

func (r *report) errf(format string, a ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, a...))
}

func (r *report) warnf(format string, a ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, a...))
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	dir := fs.String("corpus", "data/synth/p0", "répertoire du corpus")
	maxErrors := fs.Int("max-errors", 20, "nombre d'erreurs affichées")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rep := &report{}
	if err := validateCoNLL(filepath.Join(*dir, "corpus.conll"), rep); err != nil {
		return err
	}
	if err := validateDocuments(filepath.Join(*dir, "documents.jsonl"), rep); err != nil {
		return err
	}

	for i, w := range rep.warnings {
		if i >= *maxErrors {
			fmt.Printf("… et %d autres avertissements\n", len(rep.warnings)-i)
			break
		}
		fmt.Println("avertissement :", w)
	}
	for i, e := range rep.errors {
		if i >= *maxErrors {
			fmt.Fprintf(os.Stderr, "… et %d autres erreurs\n", len(rep.errors)-i)
			break
		}
		fmt.Fprintln(os.Stderr, "erreur :", e)
	}
	if len(rep.errors) > 0 {
		return fmt.Errorf("%d erreurs — corpus non publiable", len(rep.errors))
	}
	fmt.Printf("corpus valide (%d avertissements)\n", len(rep.warnings))
	return nil
}

// validateCoNLL contrôle la bonne formation des séquences BIO.
func validateCoNLL(path string, rep *report) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line, prev := 0, "O"
	for sc.Scan() {
		line++
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" {
			prev = "O"
			continue
		}
		cols := strings.Split(raw, "\t")
		if len(cols) != 2 {
			rep.errf("ligne %d : %d colonnes au lieu de 2", line, len(cols))
			continue
		}
		word, tag := cols[0], cols[1]
		if word == "" {
			rep.errf("ligne %d : token vide", line)
		}
		if tag != "O" {
			if len(tag) < 3 || tag[1] != '-' {
				rep.errf("ligne %d : tag mal formé %q", line, tag)
				prev = "O"
				continue
			}
			if !validLabels[tag[2:]] {
				rep.errf("ligne %d : label hors périmètre %q", line, tag[2:])
			}
			// Un I- doit suivre un B- ou un I- du même type. Un I- orphelin est
			// la corruption la plus courante d'un corpus BIO.
			if tag[0] == 'I' && (prev == "O" || prev[2:] != tag[2:]) {
				rep.errf("ligne %d : %q orphelin (précédent : %q)", line, tag, prev)
			}
		}
		prev = tag
	}
	return sc.Err()
}

// validateDocuments contrôle les invariants portés par les métadonnées.
func validateDocuments(path string, rep *report) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	labelCount := map[ner.EntityType]int{}
	valueCount := map[string]int{}
	templateCount := map[string]int{}
	total := 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var meta render.Metadata
		if err := json.Unmarshal(sc.Bytes(), &meta); err != nil {
			rep.errf("document %d : JSONL illisible : %v", total, err)
			continue
		}
		total++
		templateCount[meta.Template]++

		var prevEnd int
		for _, s := range meta.Entities {
			labelCount[s.Label]++
			valueCount[string(s.Label)+"|"+s.Text]++
			if s.Start >= s.End {
				rep.errf("doc %d : span dégénéré %+v", meta.Index, s)
			}
			if s.Start < prevEnd {
				rep.errf("doc %d : spans chevauchants ou non triés à l'offset %d", meta.Index, s.Start)
			}
			prevEnd = s.End
			if !validLabels[string(s.Label)] {
				rep.errf("doc %d : label hors périmètre %q", meta.Index, s.Label)
			}
			if strings.TrimSpace(s.Text) != s.Text {
				rep.errf("doc %d : span entouré d'espaces %q", meta.Index, s.Text)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if total == 0 {
		rep.errf("corpus vide")
		return nil
	}

	// Distribution des labels : une classe sous-représentée fausse
	// l'entraînement sans le signaler.
	sum := 0
	for _, n := range labelCount {
		sum += n
	}
	for _, l := range []ner.EntityType{ner.TypePER, ner.TypeORG, ner.TypeLOC} {
		share := float64(labelCount[l]) / float64(sum)
		if share < 0.10 {
			rep.warnf("label %s sous-représenté : %.1f %% des entités", l, 100*share)
		}
	}

	// Duplication de valeurs : signale un gazetteer trop étroit ou un alpha
	// mal réglé, avant d'avoir gaspillé un cycle d'entraînement.
	dup := 0
	for _, n := range valueCount {
		if n > 1 {
			dup += n - 1
		}
	}
	if rate := float64(dup) / float64(sum); rate > 0.5 {
		rep.warnf("taux de duplication des valeurs d'entités : %.1f %%", 100*rate)
	}

	// Couverture des templates : un template jamais tiré ne contribue à rien.
	expected := float64(total) / float64(len(templateCount))
	for name, n := range templateCount {
		if math.Abs(float64(n)-expected)/expected > 0.5 {
			rep.warnf("template %s tiré %d fois (attendu ~%.0f)", name, n, expected)
		}
	}
	return nil
}

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	dir := fs.String("corpus", "data/synth/p0", "répertoire du corpus")
	if err := fs.Parse(args); err != nil {
		return err
	}

	f, err := os.Open(filepath.Join(*dir, "documents.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()

	type labelStat struct {
		count    int
		distinct map[string]int
		length   int
	}
	labels := map[ner.EntityType]*labelStat{}
	templates := map[string]int{}
	forms := map[string]int{}
	noised, total := 0, 0
	idOK, idTotal := 0, 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var meta render.Metadata
		if err := json.Unmarshal(sc.Bytes(), &meta); err != nil {
			return err
		}
		total++
		templates[meta.Template]++
		if meta.Noised {
			noised++
		}
		for _, s := range meta.Entities {
			st := labels[s.Label]
			if st == nil {
				st = &labelStat{distinct: map[string]int{}}
				labels[s.Label] = st
			}
			st.count++
			st.distinct[s.Text]++
			st.length += len(strings.Fields(s.Text))
			forms[s.Form]++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	// Contrôle croisé : les identifiants du corpus doivent passer les mêmes
	// clés que celles appliquées en production.
	conll, err := os.ReadFile(filepath.Join(*dir, "corpus.conll"))
	if err == nil {
		text := strings.ReplaceAll(string(conll), "\tO", "")
		for _, e := range ner.RegexEntityFilter(ner.BuiltinRegexPatterns)(text, nil) {
			switch e.Type {
			case ner.TypeSIRET:
				idTotal++
				if checksum.SIRET(e.Text) {
					idOK++
				}
			case ner.TypeIBAN:
				idTotal++
				if checksum.IBAN(e.Text) {
					idOK++
				}
			}
		}
	}

	fmt.Printf("documents          %d\n", total)
	fmt.Printf("avec bruit         %d (%.0f %%)\n", noised, 100*float64(noised)/float64(total))
	fmt.Println("\nlabel   occurrences  valeurs distinctes  longueur moy.  entropie")
	names := make([]string, 0, len(labels))
	for l := range labels {
		names = append(names, string(l))
	}
	sort.Strings(names)
	for _, n := range names {
		st := labels[ner.EntityType(n)]
		fmt.Printf("%-6s %11d %19d %14.2f %9.2f\n",
			n, st.count, len(st.distinct),
			float64(st.length)/float64(st.count), entropy(st.distinct, st.count))
	}

	fmt.Println("\nformes de surface")
	formNames := make([]string, 0, len(forms))
	for f := range forms {
		formNames = append(formNames, f)
	}
	sort.Slice(formNames, func(i, j int) bool { return forms[formNames[i]] > forms[formNames[j]] })
	for _, f := range formNames {
		fmt.Printf("  %-24s %6d\n", f, forms[f])
	}

	fmt.Println("\ntemplates")
	tNames := make([]string, 0, len(templates))
	for t := range templates {
		tNames = append(tNames, t)
	}
	sort.Strings(tNames)
	for _, t := range tNames {
		fmt.Printf("  %-32s %6d\n", t, templates[t])
	}

	if idTotal > 0 {
		fmt.Printf("\nidentifiants détectés par les patterns de production : %d, clé valide : %d (%.0f %%)\n",
			idTotal, idOK, 100*float64(idOK)/float64(idTotal))
	}
	return nil
}

// entropy mesure la diversité lexicale d'un label : une entropie basse signale
// un gazetteer trop étroit.
func entropy(counts map[string]int, total int) float64 {
	h := 0.0
	for _, n := range counts {
		p := float64(n) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}
