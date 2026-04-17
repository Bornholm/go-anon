// Package cmdutil fournit des utilitaires partagés entre les commandes CLI.
package cmdutil

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/bornholm/go-anon/pkg/features"
)

// ParseGazetteers parse la valeur du flag -gazetteers au format
// "nom1:chemin1.txt,nom2:chemin2.txt" et charge chaque fichier.
//
// Retourne une map vide (non nil) si flagValue est vide.
// Retourne une erreur si un fichier est introuvable ou illisible.
func ParseGazetteers(flagValue string) (map[string]*features.Gazetteer, error) {
	result := make(map[string]*features.Gazetteer)

	if strings.TrimSpace(flagValue) == "" {
		return result, nil
	}

	entries := strings.Split(flagValue, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		name, path, err := splitEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("gazetteers: entrée %q invalide : %w", entry, err)
		}

		gaz, err := loadGazetteerFile(name, path)
		if err != nil {
			return nil, err
		}

		result[name] = gaz
	}

	return result, nil
}

// GazetteerNames retourne les noms triés des gazetteers d'une map.
// Utile pour remplir FeatureConfig.GazetteerNames lors de la sérialisation.
func GazetteerNames(gazetteers map[string]*features.Gazetteer) []string {
	if len(gazetteers) == 0 {
		return nil
	}
	names := make([]string, 0, len(gazetteers))
	for name := range gazetteers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// splitEntry découpe "nom:chemin" en (nom, chemin).
func splitEntry(entry string) (name, path string, err error) {
	idx := strings.Index(entry, ":")
	if idx <= 0 {
		return "", "", fmt.Errorf("format attendu \"nom:chemin\", got %q", entry)
	}
	return strings.TrimSpace(entry[:idx]), strings.TrimSpace(entry[idx+1:]), nil
}

// loadGazetteerFile ouvre le fichier et charge le gazetteer.
func loadGazetteerFile(name, path string) (*features.Gazetteer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("gazetteers: ouverture %q (gazetteer %q) : %w", path, name, err)
	}
	defer f.Close()

	gaz, err := features.LoadGazetteer(name, f)
	if err != nil {
		return nil, fmt.Errorf("gazetteers: chargement %q depuis %q : %w", name, path, err)
	}

	return gaz, nil
}
