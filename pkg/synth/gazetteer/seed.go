package gazetteer

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed seed/*.tsv
var seedFS embed.FS

// Bundle regroupe les gazetteers d'une langue, indexés par nom court
// (« prenoms_f », « patronymes », « communes »…).
type Bundle struct {
	Lang string
	Sets map[string]*Set

	mu      sync.Mutex
	subsets map[string]*Set
}

// Subset retourne le sous-ensemble de Sets[name] retenu par pred, construit à
// la première demande puis mémorisé sous cacheKey.
//
// Le filtre est bien plus coûteux que le tirage ; le mémoriser sur le Bundle
// plutôt que sur le Generator évite de le refaire à chaque document. cacheKey
// doit identifier pred de façon stable — en pratique la famille demandée.
//
// Retourne nil si aucune entrée ne passe le filtre.
func (b *Bundle) Subset(name, cacheKey string, pred func(Entry) bool) *Set {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := name + "\x00" + cacheKey
	if s, ok := b.subsets[k]; ok {
		return s
	}
	s := b.MustGet(name).Subset(pred)
	if b.subsets == nil {
		b.subsets = map[string]*Set{}
	}
	b.subsets[k] = s
	return s
}

// LoadSeed charge les gazetteers embarqués pour une langue.
//
// Ces listes sont un socle de démarrage, volontairement modeste : elles rendent
// le générateur utilisable sans préparation de données. Leurs poids sont
// dérivés du rang, pas de fréquences réelles — un corpus destiné à
// l'entraînement doit charger les gazetteers complets via LoadDir.
func LoadSeed(lang string, opts Options) (*Bundle, error) {
	b := &Bundle{Lang: lang, Sets: map[string]*Set{}}
	prefix := lang + "_"
	err := fs.WalkDir(seedFS, "seed", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(p), ".tsv")
		if !strings.HasPrefix(name, prefix) {
			return nil
		}
		f, err := seedFS.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		s, err := Load(f, opts)
		if err != nil {
			return fmt.Errorf("%s : %w", p, err)
		}
		b.Sets[strings.TrimPrefix(name, prefix)] = s
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(b.Sets) == 0 {
		return nil, fmt.Errorf("aucun gazetteer embarqué pour la langue %q", lang)
	}
	return b, nil
}

// LoadDir charge les gazetteers d'un répertoire, en surchargeant ceux du socle
// embarqué portant le même nom. Permet de substituer les fichiers INSEE
// complets sans toucher au code.
func LoadDir(lang, dir string, opts Options) (*Bundle, error) {
	b, err := LoadSeed(lang, opts)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(dir, lang+"_*.tsv"))
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		f, err := os.Open(m)
		if err != nil {
			return nil, err
		}
		s, loadErr := Load(f, opts)
		f.Close()
		if loadErr != nil {
			return nil, fmt.Errorf("%s : %w", m, loadErr)
		}
		name := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(m), ".tsv"), lang+"_")
		b.Sets[name] = s
	}
	return b, nil
}

// Get retourne le Set nommé, ou une erreur explicite s'il manque.
func (b *Bundle) Get(name string) (*Set, error) {
	s, ok := b.Sets[name]
	if !ok {
		return nil, fmt.Errorf("gazetteer %q absent pour la langue %s", name, b.Lang)
	}
	return s, nil
}

// MustGet retourne le Set nommé et panique s'il manque. Réservé à
// l'initialisation, où l'absence d'un gazetteer est un défaut de programmation.
func (b *Bundle) MustGet(name string) *Set {
	s, err := b.Get(name)
	if err != nil {
		panic(err)
	}
	return s
}

// Names retourne les noms des gazetteers chargés, pour le manifest.
func (b *Bundle) Names() []string {
	names := make([]string, 0, len(b.Sets))
	for n := range b.Sets {
		names = append(names, n)
	}
	return names
}
