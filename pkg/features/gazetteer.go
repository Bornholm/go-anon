package features

import (
	"bufio"
	"io"
	"strings"
)

// Gazetteer est un dictionnaire de lookup rapide (O(1)) pour les entités nommées.
// Exemples d'usage : prénoms, noms de communes, noms d'organisations.
// Toutes les entrées et lookups sont normalisés en minuscules (case-insensitive).
type Gazetteer struct {
	name       string
	entries    map[string]struct{}
	frequency  map[string]int  // nombre d'occurrences de chaque entrée
	uniqueMask map[string]bool // true si l'entrée apparaît exactement une fois
	total      int
}

// LoadGazetteer construit un Gazetteer en lisant une source texte,
// une entrée par ligne. Les lignes vides et celles commençant par '#' sont ignorées.
func LoadGazetteer(name string, r io.Reader) (*Gazetteer, error) {
	g := &Gazetteer{
		name:       name,
		entries:    make(map[string]struct{}),
		frequency:  make(map[string]int),
		uniqueMask: make(map[string]bool),
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		g.entries[lower] = struct{}{}
		g.frequency[lower]++
		g.total++
	}

	// Calculer le masque d'unicité
	for entry, freq := range g.frequency {
		g.uniqueMask[entry] = (freq == 1)
	}

	return g, scanner.Err()
}

// Contains retourne true si word (insensible à la casse) est dans le gazetteer.
func (g *Gazetteer) Contains(word string) bool {
	_, ok := g.entries[strings.ToLower(word)]
	return ok
}

// Frequency retourne le nombre d'occurrences du mot dans le gazetteer.
// Retourne 0 si le mot n'est pas dans le gazetteer.
func (g *Gazetteer) Frequency(word string) int {
	return g.frequency[strings.ToLower(word)]
}

// IsUnique retourne true si le mot apparaît exactement une fois dans le gazetteer.
// Utile pour identifier les entrées très spécifiques (noms de villes rares, etc.)
func (g *Gazetteer) IsUnique(word string) bool {
	return g.uniqueMask[strings.ToLower(word)]
}

// IsCommon retourne true si le mot apparaît plus de 'threshold' fois.
// Utile pour identifier les noms très populaires (prénoms communs).
func (g *Gazetteer) IsCommon(word string, threshold int) bool {
	return g.frequency[strings.ToLower(word)] > threshold
}

// ContainsSequence retourne true si la jointure espace des mots words[start:end]
// (insensible à la casse) est dans le gazetteer.
// Utile pour les entités multi-mots comme "New York" ou "Air France".
func (g *Gazetteer) ContainsSequence(words []string, start, end int) bool {
	if start < 0 || end > len(words) || start >= end {
		return false
	}
	phrase := strings.ToLower(strings.Join(words[start:end], " "))
	_, ok := g.entries[phrase]
	return ok
}

// Name retourne le nom du gazetteer tel que passé à LoadGazetteer.
func (g *Gazetteer) Name() string {
	return g.name
}
