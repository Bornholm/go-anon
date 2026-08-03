package pdf

import "sort"

// Regroupement des lignes en blocs logiques (paragraphes).
//
// Le walker segmente à la ligne, alors que le modèle est entraîné sur des
// phrases : il ne voit donc jamais qu'un fragment. Le bloc reconstitue l'unité
// linguistique, ce qui bénéficie à *toutes* les entités de la page, et pas
// seulement à celles qui tombent sur une coupure.
//
// Le regroupement est **conservateur par construction** : en cas de doute, on ne
// joint pas. Le mode d'échec à éviter est la fusion de deux colonnes, qui
// produit du charabia et serait donc pire que l'état actuel. Ce que le
// regroupement refuse de joindre, la vue « paires » le rattrape de toute façon.
const (
	// blockGapRatio : au-delà de ce multiple de l'interligne courant, la rupture
	// verticale est lue comme une fin de paragraphe.
	blockGapRatio = 1.5
	// blockMinGap : en deçà, deux lignes sont considérées à la même hauteur
	// (exposants, notes marginales) et ne forment pas une progression de texte.
	blockMinGap = 0.5
)

// Blocks implémente docprocessor.BlockWalker : il retourne, pour chaque bloc de
// plus d'une ligne, les rangs des segments qui le composent dans l'ordre du
// parcours.
//
// Les blocs d'une seule ligne sont omis : ils n'apportent rien, le segment
// étant de toute façon analysé tel quel.
func (w *Walker) Blocks() [][]int {
	var blocks [][]int

	for _, page := range w.segmentsByPage() {
		for _, block := range groupPageIntoBlocks(w, page) {
			if len(block) > 1 {
				blocks = append(blocks, block)
			}
		}
	}

	return blocks
}

// segmentsByPage répartit les rangs de segments par page, dans l'ordre du
// parcours. Un bloc ne traverse jamais une frontière de page.
func (w *Walker) segmentsByPage() [][]int {
	var pages [][]int
	current := -1
	for i, seg := range w.segments {
		if seg.pageIdx != current {
			pages = append(pages, nil)
			current = seg.pageIdx
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], i)
	}
	return pages
}

// segmentY retourne l'ordonnée de la ligne portée par un segment.
func (w *Walker) segmentY(segIdx int) float64 {
	seg := w.segments[segIdx]
	return w.pages[seg.pageIdx].tokens[seg.tokenStart].yPos
}

// groupPageIntoBlocks découpe les segments d'une page en blocs.
//
// La mesure est purement verticale. Deux ruptures ouvrent un bloc :
//
//   - une **remontée** (gap ≤ 0) : le texte repart vers le haut de la page,
//     signature d'un changement de colonne. C'est ce qui protège du pire cas
//     sans nécessiter les abscisses ;
//   - un **interligne rompu** (gap > blockGapRatio × interligne courant) : saut
//     de paragraphe, titre, changement de corps.
//
// L'interligne de référence est la médiane des écarts de la page, robuste aux
// quelques grands sauts qu'une moyenne laisserait dériver.
func groupPageIntoBlocks(w *Walker, segments []int) [][]int {
	if len(segments) < 2 {
		return [][]int{segments}
	}

	gaps := make([]float64, 0, len(segments)-1)
	for i := 0; i+1 < len(segments); i++ {
		gaps = append(gaps, w.segmentY(segments[i])-w.segmentY(segments[i+1]))
	}

	ref := medianPositive(gaps)
	if ref <= 0 {
		// Aucune progression verticale exploitable : ne rien regrouper.
		return singletons(segments)
	}

	var blocks [][]int
	current := []int{segments[0]}
	for i, gap := range gaps {
		if gap < blockMinGap || gap > blockGapRatio*ref {
			blocks = append(blocks, current)
			current = nil
		}
		current = append(current, segments[i+1])
	}
	return append(blocks, current)
}

// medianPositive retourne la médiane des écarts strictement positifs, ou 0 s'il
// n'y en a aucun.
func medianPositive(gaps []float64) float64 {
	positive := make([]float64, 0, len(gaps))
	for _, g := range gaps {
		if g >= blockMinGap {
			positive = append(positive, g)
		}
	}
	if len(positive) == 0 {
		return 0
	}
	sort.Float64s(positive)
	return positive[len(positive)/2]
}

func singletons(segments []int) [][]int {
	blocks := make([][]int, len(segments))
	for i, s := range segments {
		blocks[i] = []int{s}
	}
	return blocks
}
