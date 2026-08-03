package pdf

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"sort"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

	"github.com/bornholm/go-anon/pkg/docprocessor"
	"github.com/bornholm/go-anon/pkg/ocr"
)

// Caviardage du contenu bitmap.
//
// **Point non négociable** : peindre un rectangle noir dans le flux de contenu
// n'efface rien. L'image d'origine reste dans le fichier et se ré-extrait en une
// commande — c'est le mode de fuite qui a valu des incidents publics à plusieurs
// administrations. Les pixels doivent réellement disparaître.
//
// La page concernée est donc **aplatie** : rendue en image, noircie aux
// emplacements voulus, puis réinjectée comme unique contenu de la page. Les
// objets d'origine — images, flux, polices — ne sont plus référencés.
//
// Ce choix a un coût qu'il faut assumer :
//
//   - la page perd sa couche texte : elle n'est plus sélectionnable ni
//     recherchable, et son poids augmente ;
//   - sa définition est plafonnée par le DPI de rendu.
//
// C'est pourquoi seules les pages effectivement concernées sont aplaties, et
// non le document entier.

// redactPaddingRatio élargit chaque boîte d'une fraction de sa hauteur. Les
// boîtes des moteurs OCR épousent les glyphes au plus près : sans marge, les
// jambages et les accents dépassent du noircissement.
const redactPaddingRatio = 0.15

// MarkRedaction enregistre une zone à caviarder, désignée par ses offsets dans
// le texte de la région d'index region (l'ordre de ReadOnlyText).
//
// Implémente docprocessor.RedactingWalker : l'appelant détecte, le walker sait
// seul traduire des offsets en pixels.
func (w *Walker) MarkRedaction(region, start, end int) {
	if region < 0 || region >= len(w.ocrPages) || start >= end {
		return
	}
	if w.redactions == nil {
		w.redactions = map[int][][2]int{}
	}
	w.redactions[region] = append(w.redactions[region], [2]int{start, end})
}

// redactionBoxes traduit les zones marquées en rectangles de pixels, par page.
func (w *Walker) redactionBoxes() map[int][]ocr.Rect {
	boxes := map[int][]ocr.Rect{}

	for region, ranges := range w.redactions {
		page := w.ocrPages[region]
		_, bounds := page.regionText()

		for _, rg := range ranges {
			for li, b := range bounds {
				start, end := max(rg[0], b[0]), min(rg[1], b[1])
				if start >= end {
					continue
				}
				found := page.lines[li].BoxesFor(start-b[0], end-b[0])
				boxes[page.pageNr] = append(boxes[page.pageNr], found...)
			}
		}
	}

	return boxes
}

// RedactedPages retourne les numéros de page qui seront aplaties, triés.
func (w *Walker) RedactedPages() []int {
	var pages []int
	for pageNr := range w.redactionBoxes() {
		pages = append(pages, pageNr)
	}
	sort.Ints(pages)
	return pages
}

// applyRedactions rend chaque page concernée depuis src, noircit les zones et
// remplace la page par l'image obtenue dans ctx.
func (w *Walker) applyRedactions(ctx *model.Context, src string) error {
	boxes := w.redactionBoxes()
	if len(boxes) == 0 {
		return nil
	}
	if w.rasterizer == nil {
		return fmt.Errorf("pdf: caviardage demandé sans rastériseur")
	}

	pages := make([]int, 0, len(boxes))
	for pageNr := range boxes {
		pages = append(pages, pageNr)
	}
	sort.Ints(pages)

	for _, pageNr := range pages {
		img, err := w.rasterizer.Render(src, pageNr, w.ocrDPI)
		if err != nil {
			return err
		}
		if err := flattenPage(ctx, pageNr, blacken(img, boxes[pageNr])); err != nil {
			return fmt.Errorf("pdf: caviardage page %d : %w", pageNr, err)
		}
	}

	return nil
}

// blacken noircit les rectangles dans une copie de l'image.
func blacken(src image.Image, boxes []ocr.Rect) *image.RGBA {
	bounds := src.Bounds()
	out := image.NewRGBA(bounds)
	draw.Draw(out, bounds, src, bounds.Min, draw.Src)

	black := image.NewUniform(image.Black.C)
	for _, b := range boxes {
		pad := max(int(float64(b.H)*redactPaddingRatio), 1)
		r := image.Rect(b.X-pad, b.Y-pad, b.Right()+pad, b.Bottom()+pad).Intersect(bounds)
		draw.Draw(out, r, black, image.Point{}, draw.Src)
	}

	return out
}

// flattenPage remplace le contenu de la page par img, qui en devient l'unique
// ressource. Les objets d'origine cessent d'être référencés : c'est ce qui rend
// l'effacement réel plutôt que cosmétique.
func flattenPage(ctx *model.Context, pageNr int, img *image.RGBA) error {
	pageDict, _, attrs, err := ctx.PageDict(pageNr, false)
	if err != nil {
		return err
	}
	if attrs == nil || attrs.MediaBox == nil {
		return fmt.Errorf("MediaBox introuvable")
	}
	box := attrs.MediaBox

	imgRef, err := imageXObject(ctx, img)
	if err != nil {
		return err
	}

	// Le carré unité de l'image est étiré aux dimensions de la page, à son
	// origine — une MediaBox ne commence pas nécessairement en (0, 0).
	content := fmt.Sprintf("q %f 0 0 %f %f %f cm /ImRedact Do Q",
		box.Width(), box.Height(), box.LL.X, box.LL.Y)

	sd, err := ctx.NewStreamDictForBuf([]byte(content))
	if err != nil {
		return err
	}
	if err := sd.Encode(); err != nil {
		return err
	}
	contentRef, err := ctx.IndRefForNewObject(*sd)
	if err != nil {
		return err
	}

	pageDict.Update("Contents", *contentRef)
	pageDict.Update("Resources", types.Dict{
		"XObject": types.Dict{"ImRedact": *imgRef},
	})
	// Les annotations survivraient à l'aplatissement en portant leur propre
	// texte ; la sanitisation les signale déjà, autant ne pas les traîner sur
	// une page dont tout le reste a été remplacé.
	delete(pageDict, "Annots")

	return nil
}

// imageXObject enregistre img comme XObject image, en RGB brut compressé.
//
// Flate plutôt que JPEG : le caviardage doit être exact. Un ré-encodage avec
// perte laisserait des artefacts autour des zones noircies, et rien ne garantit
// qu'un contraste résiduel n'y redevienne lisible après traitement.
func imageXObject(ctx *model.Context, img *image.RGBA) (*types.IndirectRef, error) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	rgb := make([]byte, 0, width*height*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			i := img.PixOffset(x, y)
			rgb = append(rgb, img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	}

	sd, err := ctx.NewStreamDictForBuf(rgb)
	if err != nil {
		return nil, err
	}
	sd.InsertName("Type", "XObject")
	sd.InsertName("Subtype", "Image")
	sd.InsertInt("Width", width)
	sd.InsertInt("Height", height)
	sd.InsertName("ColorSpace", "DeviceRGB")
	sd.InsertInt("BitsPerComponent", 8)

	if err := sd.Encode(); err != nil {
		return nil, err
	}
	return ctx.IndRefForNewObject(*sd)
}

// saveWithRedactions écrit le document en deux temps.
//
// Le rendu doit porter sur le document **déjà anonymisé** : aplatir une page
// rendue depuis l'original y réinjecterait le texte en clair que le pipeline
// venait de remplacer. D'où le fichier intermédiaire, relu dans un contexte
// neuf — réécrire deux fois le même contexte pdfcpu n'est pas un usage prévu.
func (w *Walker) saveWithRedactions(outputPath string) error {
	tmp, err := os.CreateTemp("", "go-anon-*.pdf")
	if err != nil {
		return fmt.Errorf("pdf: fichier intermédiaire : %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	// Le fichier intermédiaire porte le document anonymisé mais non caviardé :
	// il ne doit pas survivre à l'appel.
	defer os.Remove(tmpPath)

	if err := pdfapi.WriteContextFile(w.ctx, tmpPath); err != nil {
		return err
	}

	ctx, err := pdfapi.ReadContextFile(tmpPath)
	if err != nil {
		return fmt.Errorf("pdf: relecture intermédiaire : %w", err)
	}
	if err := w.applyRedactions(ctx, tmpPath); err != nil {
		return err
	}

	return pdfapi.WriteContextFile(ctx, outputPath)
}

// VisualText implémente docprocessor.VisualVerifier : il relit un document PDF
// écrit et rend son texte tel qu'un lecteur le voit.
//
// Contrairement à ReadOnlyText, qui expose ce que l'OCR avait lu de l'entrée, ce
// chemin repart du **fichier produit** : c'est ce qui lui permet de constater
// une boîte de caviardage mal placée, ou un remplacement qui n'a pas pris.
func (w *Walker) VisualText(path string) ([]docprocessor.ReadOnlyRegion, error) {
	if w.verifyEngine == nil || w.rasterizer == nil {
		return nil, nil
	}

	var regions []docprocessor.ReadOnlyRegion
	for pageNr := 1; pageNr <= w.ctx.PageCount; pageNr++ {
		img, err := w.rasterizer.Render(path, pageNr, w.ocrDPI)
		if err != nil {
			return nil, err
		}
		words, err := w.verifyEngine.Recognize(img, w.ocrLang)
		if err != nil {
			return nil, fmt.Errorf("pdf: relecture page %d : %w", pageNr, err)
		}

		page := ocrPage{pageNr: pageNr, lines: ocr.Lines(words)}
		if len(page.lines) == 0 {
			continue
		}
		text, _ := page.regionText()
		regions = append(regions, docprocessor.ReadOnlyRegion{
			Label: fmt.Sprintf("page %d (relecture)", pageNr),
			Text:  text,
		})
	}

	return regions, nil
}
