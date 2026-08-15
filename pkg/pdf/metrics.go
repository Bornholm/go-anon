package pdf

import (
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Largeurs de glyphes.
//
// Un flux PDF ne porte pas nécessairement les espaces : beaucoup de générateurs
// positionnent chaque mot par un Td/Tm sans jamais émettre le caractère. Savoir
// s'il faut réinsérer une espace suppose de connaître la largeur du fragment
// précédent, donc les largeurs de la police.
//
// À défaut, needsSpace estimait chaque glyphe à un demi-cadratin. L'erreur est
// sans conséquence sur un mot court, mais elle s'accumule : sur « Forfait »,
// sept glyphes à 0,5 em donnent 3,5 em là où la police en occupe 2,9. L'avance
// surestimée absorbe l'écart réel, aucune espace n'est insérée, et le texte
// extrait devient « FreeForfaitMobile » — que le modèle ne peut plus segmenter.
// C'est ainsi que des factures entières ressortaient en un seul mot.
//
// Les largeurs déclarées dans le PDF suppriment cette approximation. Le
// fallback subsiste pour les polices qui n'en déclarent pas, dont les quatorze
// polices standard.

// glyphUnits est l'unité des largeurs PDF : un millième de cadratin.
const glyphUnits = 1000.0

// fontMetrics porte les largeurs d'une police, indexées par rune.
//
// Indexer par rune plutôt que par code d'octet perd la distinction entre deux
// codes rendant le même caractère, ce qui n'existe pas en pratique et
// n'intéresse pas le calcul d'un écart.
type fontMetrics struct {
	widths  map[rune]float64
	missing float64 // largeur des runes absentes de la table
}

// advance retourne la largeur du texte au corps donné, en unités du flux.
func (m *fontMetrics) advance(s string, size float64) (float64, bool) {
	if m == nil || len(m.widths) == 0 {
		return 0, false
	}
	total := 0.0
	known := 0
	for _, r := range s {
		if w, ok := m.widths[r]; ok {
			total += w
			known++
			continue
		}
		total += m.missing
	}
	// Une table qui ne couvre presque rien du fragment ne vaut pas mieux que
	// l'estimation : mieux vaut une espace de trop que des mots recollés.
	if known*2 < len([]rune(s)) {
		return 0, false
	}
	return total / glyphUnits * size, true
}

// loadPageMetrics retourne les largeurs de chaque police d'une page, indexées
// par son nom de ressource.
func loadPageMetrics(ctx *model.Context, pageNr int, cmaps map[string]*toUnicodeMap) map[string]*fontMetrics {
	out := map[string]*fontMetrics{}

	fontDict := pageFontDict(ctx, pageNr)
	if fontDict == nil {
		return out
	}

	for alias := range fontDict {
		fd := derefDict(ctx, fontDict[alias])
		if fd == nil {
			continue
		}
		if m := simpleFontMetrics(ctx, fd); m != nil {
			out[alias] = m
			continue
		}
		if m := cidFontMetrics(ctx, fd, cmaps[alias]); m != nil {
			out[alias] = m
		}
	}
	return out
}

// simpleFontMetrics lit /FirstChar et /Widths d'une police simple. Les codes y
// sont des octets, interprétés selon l'encodage de la police ; WinAnsi est le
// cas courant et le seul traité.
func simpleFontMetrics(ctx *model.Context, fd types.Dict) *fontMetrics {
	widthsObj, found := fd.Find("Widths")
	if !found {
		return nil
	}
	arr, err := ctx.DereferenceArray(widthsObj)
	if err != nil || len(arr) == 0 {
		return nil
	}
	first := 0
	if fc, ok := fd.Find("FirstChar"); ok {
		if n, err := ctx.DereferenceInteger(fc); err == nil && n != nil {
			first = n.Value()
		}
	}

	m := &fontMetrics{widths: make(map[rune]float64, len(arr)), missing: missingWidth(ctx, fd)}
	for i, w := range arr {
		width, ok := numericValue(ctx, w)
		if !ok {
			continue
		}
		code := byte(first + i)
		if first+i > 255 {
			break
		}
		m.widths[winAnsiRune(code)] = width
	}
	if len(m.widths) == 0 {
		return nil
	}
	return m
}

// cidFontMetrics lit le tableau /W du descendant d'une police composite. Le
// tableau indexe des CID ; la CMap ToUnicode les traduit en runes.
//
// /W accepte deux formes : « c [w1 w2 …] » qui énumère à partir de c, et
// « c1 c2 w » qui applique une même largeur à un intervalle.
func cidFontMetrics(ctx *model.Context, fd types.Dict, cmap *toUnicodeMap) *fontMetrics {
	descObj, found := fd.Find("DescendantFonts")
	if !found {
		return nil
	}
	descArr, err := ctx.DereferenceArray(descObj)
	if err != nil || len(descArr) == 0 {
		return nil
	}
	desc := derefDict(ctx, descArr[0])
	if desc == nil {
		return nil
	}

	defaultWidth := 1000.0
	if dw, ok := desc.Find("DW"); ok {
		if v, ok := numericValue(ctx, dw); ok {
			defaultWidth = v
		}
	}
	m := &fontMetrics{widths: map[rune]float64{}, missing: defaultWidth}

	wObj, found := desc.Find("W")
	if !found {
		return nil
	}
	wArr, err := ctx.DereferenceArray(wObj)
	if err != nil {
		return nil
	}

	set := func(cid int, width float64) {
		if cmap == nil {
			return
		}
		if r, ok := cmap.runeForCode(uint32(cid)); ok {
			m.widths[r] = width
		}
	}

	for i := 0; i < len(wArr); {
		start, ok := intValue(ctx, wArr[i])
		if !ok {
			break
		}
		if i+1 >= len(wArr) {
			break
		}
		if sub, err := ctx.DereferenceArray(wArr[i+1]); err == nil && sub != nil {
			for j, w := range sub {
				if width, ok := numericValue(ctx, w); ok {
					set(start+j, width)
				}
			}
			i += 2
			continue
		}
		end, ok := intValue(ctx, wArr[i+1])
		if !ok || i+2 >= len(wArr) {
			break
		}
		width, ok := numericValue(ctx, wArr[i+2])
		if ok && end >= start && end-start < 65536 {
			for c := start; c <= end; c++ {
				set(c, width)
			}
		}
		i += 3
	}

	if len(m.widths) == 0 {
		return nil
	}
	return m
}

func missingWidth(ctx *model.Context, fd types.Dict) float64 {
	descObj, found := fd.Find("FontDescriptor")
	if !found {
		return 500
	}
	desc := derefDict(ctx, descObj)
	if desc == nil {
		return 500
	}
	if mw, ok := desc.Find("MissingWidth"); ok {
		if v, ok := numericValue(ctx, mw); ok {
			return v
		}
	}
	return 500
}

func pageFontDict(ctx *model.Context, pageNr int) types.Dict {
	d, _, _, err := ctx.PageDict(pageNr, false)
	if err != nil || d == nil {
		return nil
	}
	res := derefDict(ctx, dictEntry(d, "Resources"))
	if res == nil {
		return nil
	}
	return derefDict(ctx, dictEntry(res, "Font"))
}

func dictEntry(d types.Dict, key string) types.Object {
	obj, found := d.Find(key)
	if !found {
		return nil
	}
	return obj
}

func derefDict(ctx *model.Context, obj types.Object) types.Dict {
	if obj == nil {
		return nil
	}
	d, err := ctx.DereferenceDict(obj)
	if err != nil {
		return nil
	}
	return d
}

func numericValue(ctx *model.Context, obj types.Object) (float64, bool) {
	o, err := ctx.Dereference(obj)
	if err != nil {
		return 0, false
	}
	switch v := o.(type) {
	case types.Integer:
		return float64(v.Value()), true
	case types.Float:
		return v.Value(), true
	}
	return 0, false
}

func intValue(ctx *model.Context, obj types.Object) (int, bool) {
	v, ok := numericValue(ctx, obj)
	return int(v), ok
}

// winAnsiRune traduit un code d'octet en rune selon Windows-1252, l'encodage
// des polices simples produites par la quasi-totalité des générateurs.
func winAnsiRune(c byte) rune {
	if c < 0x80 || c > 0x9F {
		return rune(c)
	}
	for r, b := range win1252Extra {
		if b == c {
			return r
		}
	}
	return rune(c)
}
