package pdf

import (
	"fmt"

	"github.com/bornholm/go-anon/pkg/docprocessor"
	"github.com/bornholm/go-anon/pkg/ocr"
)

// Reconnaissance optique des pages.
//
// Le pipeline texte ne voit que ce qu'une couche texte décrit. Tout le reste —
// page scannée, encart bitmap, tampon, signature — lui est invisible, et sans
// ce chemin le document ressortirait en paraissant traité.
//
// **Systématique, sans classification.** Toutes les pages sont océrisées, y
// compris celles qui portent déjà du texte. Trier en amont supposerait de
// savoir reconnaître ce qui mérite un OCR, c'est-à-dire de refaire l'heuristique
// de la phase 0 avec ses faux négatifs — page composite, formulaire
// partiellement scanné, encart au milieu d'un rapport natif. L'union des vues
// rend ce tri inutile : au pire l'OCR redit ce que la couche texte disait déjà.

// OCROptions paramètre l'océrisation d'un document.
type OCROptions struct {
	Engine     ocr.Engine
	Rasterizer Rasterizer
	// Lang est un code ISO 639-1 ; vide, le moteur choisit son défaut.
	Lang string
	// DPI de rastérisation ; 0 pour DefaultDPI.
	DPI int
	// MinConfidence écarte les mots en deçà du seuil (0 = tout conserver).
	MinConfidence float64
	// VerifyEngine relit le document produit (VisualText). Il **doit** être en
	// mode épars : l'analyse de mise en page classe une page portant un aplat
	// de caviardage comme non textuelle et ne rend alors plus rien, ce qui
	// donnerait une vérification passant toujours. Cf. ocr.PSMSparse.
	//
	// Nil désactive la vérification visuelle.
	VerifyEngine ocr.Engine
}

// ocrPage conserve le résultat d'un OCR pour une page.
type ocrPage struct {
	pageNr int
	lines  []ocr.Line
}

// regionText rend le texte exposé pour cette page et, pour chaque ligne, ses
// bornes dans ce texte.
//
// Source unique de la correspondance offset ↔ ligne : c'est elle qui permet de
// repartir d'un offset d'entité vers les boîtes de pixels. La dupliquer entre
// l'exposition et le caviardage les ferait diverger au premier changement de
// séparateur.
func (p ocrPage) regionText() (string, [][2]int) {
	bounds := make([][2]int, len(p.lines))
	total := 0
	for i, l := range p.lines {
		bounds[i] = [2]int{total, total + len(l.Text)}
		total += len(l.Text) + 1 // séparateur '\n'
	}

	buf := make([]byte, 0, total)
	for i, l := range p.lines {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, l.Text...)
	}
	return string(buf), bounds
}

// RunOCR rastérise puis océrise chaque page du document, et conserve les lignes
// reconnues. À appeler avant Process : le texte obtenu alimente le rapport des
// surfaces que le pipeline peut lire sans pouvoir les réécrire.
//
// Les erreurs de rendu ou de reconnaissance d'une page sont **remontées**, pas
// avalées : une page qu'on n'a pas su lire est une page dont on ignore le
// contenu, ce qui est exactement la situation que ce chantier corrige.
func (w *Walker) RunOCR(opts OCROptions) error {
	if opts.Engine == nil {
		return fmt.Errorf("pdf: OCR demandé sans moteur")
	}
	if opts.Rasterizer == nil {
		return fmt.Errorf("pdf: OCR demandé sans rastériseur")
	}
	if err := opts.Engine.Available(); err != nil {
		return err
	}
	if err := opts.Rasterizer.Available(); err != nil {
		return err
	}

	// Mémorisés pour le caviardage : il doit rendre les pages exactement comme
	// l'OCR les a vues, sans quoi les boîtes ne désignent plus les mêmes pixels.
	w.rasterizer = opts.Rasterizer
	w.ocrDPI = opts.DPI
	w.verifyEngine = opts.VerifyEngine
	w.ocrLang = opts.Lang

	w.ocrPages = nil
	for pageNr := 1; pageNr <= w.ctx.PageCount; pageNr++ {
		img, err := opts.Rasterizer.Render(w.inputPath, pageNr, opts.DPI)
		if err != nil {
			return err
		}

		words, err := opts.Engine.Recognize(img, opts.Lang)
		if err != nil {
			return fmt.Errorf("pdf: OCR page %d : %w", pageNr, err)
		}

		lines := ocr.Lines(ocr.FilterConfidence(words, opts.MinConfidence))
		if len(lines) == 0 {
			continue
		}
		w.ocrPages = append(w.ocrPages, ocrPage{pageNr: pageNr, lines: lines})
	}

	return nil
}

// OCRLines retourne les lignes reconnues sur une page (base 1), ou nil si la
// page n'a pas été océrisée. Les boîtes sont en pixels du rendu, à la
// résolution demandée — le caviardage pixel en aura besoin.
func (w *Walker) OCRLines(pageNr int) []ocr.Line {
	for _, p := range w.ocrPages {
		if p.pageNr == pageNr {
			return p.lines
		}
	}
	return nil
}

// ReadOnlyText implémente docprocessor.ReadOnlyTextWalker : il expose le texte
// océrisé, que le pipeline sait analyser mais pas encore réécrire.
//
// Une région par page, les lignes jointes par saut de ligne — la mise en page
// d'origine est perdue de toute façon, et le découpage en lignes du recognizer
// reste le comportement attendu sur ce texte.
func (w *Walker) ReadOnlyText() []docprocessor.ReadOnlyRegion {
	regions := make([]docprocessor.ReadOnlyRegion, 0, len(w.ocrPages))
	for _, p := range w.ocrPages {
		text, _ := p.regionText()
		regions = append(regions, docprocessor.ReadOnlyRegion{
			Label: fmt.Sprintf("page %d (OCR)", p.pageNr),
			Text:  text,
		})
	}
	return regions
}
