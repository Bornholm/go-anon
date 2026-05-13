package csv

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// Walker implémente docprocessor.Walker pour les fichiers CSV et TSV.
// Chaque cellule non vide est exposée comme un Segment indépendant.
// Le séparateur est détecté automatiquement à l'ouverture du fichier.
type Walker struct {
	sep  rune
	rows [][]cell
}

type cell struct {
	value string
}

// NewWalkerFromFile ouvre un fichier CSV/TSV et retourne un Walker prêt à l'emploi.
// Le séparateur est détecté automatiquement parmi ',', '\t' et ';'.
func NewWalkerFromFile(path string) (docprocessor.Walker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture %q : %w", path, err)
	}

	sep := detectSeparator(data)

	r := csv.NewReader(strings.NewReader(string(data)))
	r.Comma = sep
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	rawRows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV %q : %w", path, err)
	}

	rows := make([][]cell, len(rawRows))
	for i, raw := range rawRows {
		rows[i] = make([]cell, len(raw))
		for j, v := range raw {
			rows[i][j] = cell{value: v}
		}
	}

	return &Walker{sep: sep, rows: rows}, nil
}

// Walk appelle fn pour chaque cellule non vide du fichier.
func (w *Walker) Walk(fn func(docprocessor.Segment) error) error {
	for i := range w.rows {
		for j := range w.rows[i] {
			if w.rows[i][j].value == "" {
				continue
			}
			localI, localJ := i, j
			seg := docprocessor.Segment{
				Text: w.rows[i][j].value,
				Replace: func(anonymized string) {
					w.rows[localI][localJ].value = anonymized
				},
			}
			if err := fn(seg); err != nil {
				return err
			}
		}
	}
	return nil
}

// SaveTo écrit le CSV anonymisé vers outputPath en conservant le séparateur d'origine.
func (w *Walker) SaveTo(outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("création %q : %w", outputPath, err)
	}
	defer f.Close()

	wr := csv.NewWriter(f)
	wr.Comma = w.sep

	for _, row := range w.rows {
		record := make([]string, len(row))
		for j, c := range row {
			record[j] = c.value
		}
		if err := wr.Write(record); err != nil {
			return fmt.Errorf("écriture ligne CSV : %w", err)
		}
	}

	wr.Flush()
	return wr.Error()
}

// detectSeparator échantillonne les 8 premières lignes non vides et choisit
// le séparateur candidat (tab, virgule, point-virgule) le plus fréquent.
func detectSeparator(data []byte) rune {
	candidates := []rune{'\t', ',', ';'}
	counts := make(map[rune]int, len(candidates))

	lines := strings.SplitN(string(data), "\n", 9)
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		for _, c := range candidates {
			counts[c] += strings.Count(line, string(c))
		}
	}

	best := ','
	max := -1
	for _, c := range candidates {
		if counts[c] > max {
			max = counts[c]
			best = c
		}
	}

	// Si aucun séparateur candidat n'est trouvé, tenter la détection via csv.Reader
	if max == 0 {
		return detectSeparatorViaReader(data, candidates)
	}
	return best
}

// detectSeparatorViaReader tente chaque séparateur et retient celui qui produit
// le plus de colonnes sur la première ligne (heuristique de fallback).
func detectSeparatorViaReader(data []byte, candidates []rune) rune {
	best := ','
	maxCols := 0

	for _, sep := range candidates {
		r := csv.NewReader(strings.NewReader(string(data)))
		r.Comma = sep
		r.LazyQuotes = true
		r.FieldsPerRecord = -1

		record, err := r.Read()
		if err != nil && err != io.EOF {
			continue
		}
		if len(record) > maxCols {
			maxCols = len(record)
			best = sep
		}
	}
	return best
}
