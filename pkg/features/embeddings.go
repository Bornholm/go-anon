package features

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Embeddings struct {
	vectors map[string][]float64
	dim     int
}

func LoadEmbeddings(path string) (*Embeddings, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open embeddings file: %w", err)
	}
	defer file.Close()

	vectors := make(map[string][]float64)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		word := strings.ToLower(parts[0])
		vec := make([]float64, len(parts)-1)

		for i, s := range parts[1:] {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				continue
			}
			vec[i] = v
		}

		if len(vec) > 0 {
			vectors[word] = vec
		}

		if lineNum%50000 == 0 {
			fmt.Printf("Loaded %d embeddings...\n", lineNum)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading embeddings file: %w", err)
	}

	var dim int
	for _, v := range vectors {
		dim = len(v)
		break
	}

	fmt.Printf("Loaded %d embeddings, dimension=%d\n", len(vectors), dim)

	return &Embeddings{vectors: vectors, dim: dim}, nil
}

func (e *Embeddings) Vector(word string) []float64 {
	return e.vectors[strings.ToLower(word)]
}

func (e *Embeddings) Dim() int {
	return e.dim
}

func (e *Embeddings) Contains(word string) bool {
	_, ok := e.vectors[strings.ToLower(word)]
	return ok
}
