package docprocessor

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/ner"
)

// viewSep est le séparateur de recomposition. Une espace, et non « \n » :
// `pkg/ner` découpe d'abord par ligne (recognizer.go:289), donc joindre par un
// saut de ligne reproduirait exactement la segmentation du walker et ne
// montrerait au modèle rien qu'il n'ait déjà vu.
const viewSep = " "

// BlockWalker est implémentée par les Walkers capables de regrouper leurs
// segments en blocs logiques — typiquement le PDF, qui segmente à la ligne et
// peut reconstituer les paragraphes à partir de la géométrie de la page.
//
// Blocks retourne, pour chaque bloc, les rangs des segments qui le composent
// dans l'ordre du parcours. Un walker qui ne l'implémente pas perd simplement
// la vue « bloc » ; les autres vues continuent de s'appliquer.
type BlockWalker interface {
	Walker
	Blocks() [][]int
}

// collectTexts parcourt le walker en lecture seule et retourne le texte source
// de chaque segment. Doit précéder toute réécriture : les vues se construisent
// sur le texte d'origine, pas sur une sortie partiellement anonymisée.
func collectTexts(walker Walker) ([]string, error) {
	var texts []string
	err := walker.Walk(func(seg Segment) error {
		texts = append(texts, seg.Text)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return texts, nil
}

// detectViews recompose le document sous plusieurs angles, y relance la
// détection, et redistribue les entités trouvées sur les segments concernés.
//
// Trois vues sont construites :
//
//   - « paires » — chaque couple de segments consécutifs. Franchit les
//     frontières de la segmentation, là où tombent les entités coupées.
//   - « bloc » — les segments regroupés en paragraphes, quand le walker sait
//     les reconstituer (BlockWalker). C'est la vue qui rend au modèle le
//     contexte de phrase dont la segmentation le prive, et elle bénéficie à
//     toutes les entités du bloc, pas seulement à celles qui sont coupées.
//   - « document » — tout le texte joint. Rattrape les coupures à distance
//     (saut de page, note de bas de page) que les paires ne voient pas.
//
// L'union est monotone en rappel : aucune vue ne peut retirer ce qu'une autre a
// trouvé. C'est ce qui autorise un regroupement en blocs agressif — au pire il
// n'apporte rien, il ne peut pas dégrader. La réconciliation des chevauchements
// se fait plus tard, dans anonymizer.reconcileEntities, qui a besoin du texte du
// segment pour trancher.
//
// Coût : environ quatre passes de détection sur l'ensemble du document — une par
// paire (chaque segment apparaît dans deux paires), une par bloc, plus une sur
// le tout.
func (p *Processor) detectViews(walker Walker, texts []string) (map[int][]ner.Entity, error) {
	extras := make(map[int][]ner.Entity)

	addView := func(indices []int) error {
		// Une vue d'un seul segment n'apporte rien : le recognizer le verra de
		// toute façon au moment de l'anonymiser.
		if len(indices) < 2 {
			return nil
		}
		joined, bounds := joinSegments(texts, indices)
		entities, err := p.anon.Detect(joined)
		if err != nil {
			return fmt.Errorf("détection multi-vues : %w", err)
		}
		for _, ent := range entities {
			projectEntity(joined, bounds, indices, ent, extras)
		}
		return nil
	}

	for i := 0; i+1 < len(texts); i++ {
		if err := addView([]int{i, i + 1}); err != nil {
			return nil, err
		}
	}

	if bw, ok := walker.(BlockWalker); ok {
		for _, block := range bw.Blocks() {
			if err := addView(block); err != nil {
				return nil, err
			}
		}
	}

	all := make([]int, len(texts))
	for i := range all {
		all[i] = i
	}
	if err := addView(all); err != nil {
		return nil, err
	}

	return extras, nil
}

// joinSegments concatène les segments désignés par indices et retourne, pour
// chacun, ses bornes dans la concaténation.
//
// La césure est recollée : « Du- » suivi de « pont » devient « Dupont ». Le
// trait d'union est retiré de la contribution du segment de gauche, ce qui
// préserve l'invariant dont dépend projectEntity — la portion contribuée reste
// un **préfixe** du texte du segment, donc les offsets projetés restent des
// indices valides dans ce texte.
func joinSegments(texts []string, indices []int) (string, [][2]int) {
	bounds := make([][2]int, len(indices))
	var b strings.Builder

	for k, idx := range indices {
		text := texts[idx]
		sep := viewSep
		if k+1 < len(indices) && isHyphenated(text, texts[indices[k+1]]) {
			text = text[:len(text)-len("-")]
			sep = ""
		}

		start := b.Len()
		b.WriteString(text)
		bounds[k] = [2]int{start, b.Len()}

		if k+1 < len(indices) {
			b.WriteString(sep)
		}
	}

	return b.String(), bounds
}

// isHyphenated rapporte si left se termine par une césure que right poursuit.
//
// La règle est locale et donc imparfaite : distinguer une vraie césure d'un
// trait d'union lexical tombé en fin de ligne demanderait un lexique.
// L'arbitrage suit la posture — « porte- » + « monnaie » sera recollé à tort en
// « portemonnaie », ce qui ne coûte rien puisque ce n'est pas une entité, alors
// que refuser de recoller « Du- » + « pont » manquerait un nom.
//
// La condition sur la minuscule protège le cas qui compte vraiment : les noms
// propres composés (« Saint- » + « Étienne ») gardent leur trait d'union.
func isHyphenated(left, right string) bool {
	if !strings.HasSuffix(left, "-") || len(left) < 2 {
		return false
	}
	before, _ := utf8.DecodeLastRuneInString(left[:len(left)-1])
	if !unicode.IsLetter(before) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(right)
	return unicode.IsLower(first)
}

// projectEntity redistribue une entité détectée sur une vue vers les segments
// qu'elle recouvre, en la scindant à leurs frontières.
//
// Une entité à cheval devient donc deux entités, une par segment, chacune
// anonymisée séparément. La fuite est fermée — c'est l'objectif — mais les deux
// moitiés reçoivent des placeholders distincts, là où un lecteur n'en verrait
// qu'une seule personne. Unifier le pseudonyme demanderait que l'anonymiseur
// accepte une forme de surface canonique distincte du span remplacé ; c'est un
// raffinement de qualité de pseudonymisation, pas de couverture.
func projectEntity(joined string, bounds [][2]int, indices []int, ent ner.Entity, out map[int][]ner.Entity) {
	for k, b := range bounds {
		start := max(ent.Start, b[0])
		end := min(ent.End, b[1])
		if start >= end {
			continue
		}
		seg := indices[k]
		out[seg] = append(out[seg], ner.Entity{
			Text:       joined[start:end],
			Type:       ent.Type,
			Start:      start - b[0],
			End:        end - b[0],
			Confidence: ent.Confidence,
		})
	}
}
