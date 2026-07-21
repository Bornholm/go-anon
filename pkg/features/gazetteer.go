package features

import (
	"bufio"
	"io"
	"strings"
)

// Gazetteer est un dictionnaire de lookup rapide (O(1)) pour les entités nommées.
// Exemples d'usage : prénoms, noms de communes, noms d'organisations.
// Toutes les entrées et lookups sont normalisés en minuscules (case-insensitive).
// La map entries stocke le nombre d'occurrences (0 = absent via valeur zéro Go).
type Gazetteer struct {
	name    string
	entries map[string]uint32 // fréquence d'occurrence ; absent si clé manquante
	total   int
}

// LoadGazetteer construit un Gazetteer en lisant une source texte,
// une entrée par ligne. Les lignes vides et celles commençant par '#' sont ignorées.
func LoadGazetteer(name string, r io.Reader) (*Gazetteer, error) {
	g := &Gazetteer{
		name:    name,
		entries: make(map[string]uint32),
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		g.entries[lower]++
		g.total++
	}

	return g, scanner.Err()
}

// Contains retourne true si word (insensible à la casse) est dans le gazetteer.
func (g *Gazetteer) Contains(word string) bool {
	return g.entries[strings.ToLower(word)] > 0
}

// ContainsLower est identique à Contains mais attend un mot déjà en
// minuscules, évitant l'allocation ToLower dans le chemin chaud.
func (g *Gazetteer) ContainsLower(lowerWord string) bool {
	return g.entries[lowerWord] > 0
}

// FrequencyLower est identique à Frequency mais attend un mot déjà en minuscules.
func (g *Gazetteer) FrequencyLower(lowerWord string) int {
	return int(g.entries[lowerWord])
}

// Frequency retourne le nombre d'occurrences du mot dans le gazetteer.
// Retourne 0 si le mot n'est pas dans le gazetteer.
func (g *Gazetteer) Frequency(word string) int {
	return int(g.entries[strings.ToLower(word)])
}

// IsUnique retourne true si le mot apparaît exactement une fois dans le gazetteer.
// Utile pour identifier les entrées très spécifiques (noms de villes rares, etc.)
func (g *Gazetteer) IsUnique(word string) bool {
	return g.entries[strings.ToLower(word)] == 1
}

// IsCommon retourne true si le mot apparaît plus de 'threshold' fois.
// Utile pour identifier les noms très populaires (prénoms communs).
func (g *Gazetteer) IsCommon(word string, threshold int) bool {
	return int(g.entries[strings.ToLower(word)]) > threshold
}

// ContainsSequence retourne true si la jointure espace des mots words[start:end]
// (insensible à la casse) est dans le gazetteer.
// Utile pour les entités multi-mots comme "New York" ou "Air France".
func (g *Gazetteer) ContainsSequence(words []string, start, end int) bool {
	if start < 0 || end > len(words) || start >= end {
		return false
	}
	phrase := strings.ToLower(strings.Join(words[start:end], " "))
	return g.entries[phrase] > 0
}

// ContainsSequenceLower est identique à ContainsSequence mais attend des mots
// déjà en minuscules, évitant ainsi l'allocation ToLower.
func (g *Gazetteer) ContainsSequenceLower(lowerWords []string, start, end int) bool {
	if start < 0 || end > len(lowerWords) || start >= end {
		return false
	}
	phrase := strings.Join(lowerWords[start:end], " ")
	return g.entries[phrase] > 0
}

// Name retourne le nom du gazetteer tel que passé à LoadGazetteer.
func (g *Gazetteer) Name() string {
	return g.name
}
