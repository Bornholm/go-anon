package pdf

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Détection des pages « raster » (contenu scanné, sans couche texte).
//
// Une page est réputée scannée lorsqu'elle est majoritairement couverte par des
// images alors qu'elle ne porte quasiment aucun texte extractible. Le pipeline
// d'anonymisation n'a alors aucune prise sur elle : sans signalement, le
// document ressortirait intact et déclaré conforme.
//
// Les deux conditions sont cumulatives et volontairement conservatrices : le
// faux positif (refuser une page de texte illustrée) est le risque dominant de
// cette heuristique.
const (
	// rasterCoverageThreshold est la fraction de la surface de la page qui doit
	// être couverte par des images pour que la page soit candidate.
	rasterCoverageThreshold = 0.5
	// rasterMaxTextRunes est le nombre de caractères non blancs extraits en deçà
	// duquel la page est réputée dépourvue de couche texte.
	rasterMaxTextRunes = 100
	// rasterMaxFormDepth borne la récursion dans les XObject Form imbriqués
	// (et garantit la terminaison en présence d'un cycle de références).
	rasterMaxFormDepth = 3
)

// ctm est une matrice de transformation PDF [a b c d e f], représentant
//
//	| a b 0 |
//	| c d 0 |
//	| e f 1 |
type ctm [6]float64

var identityCTM = ctm{1, 0, 0, 1, 0, 0}

// mul retourne m × n, c'est-à-dire la transformation qui applique m puis n.
func (m ctm) mul(n ctm) ctm {
	return ctm{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// unitArea est l'aire, en points carrés, du carré unité transformé par m.
// Une image XObject est toujours dessinée dans ce carré unité : c'est donc la
// surface qu'elle occupe sur la page.
func (m ctm) unitArea() float64 {
	return math.Abs(m[0]*m[3] - m[1]*m[2])
}

// pageKind classe une page selon la prise que le pipeline texte a réellement
// sur ce qu'un lecteur y voit.
type pageKind int

const (
	// pageNormal : du texte visible en quantité, donc anonymisable.
	pageNormal pageKind = iota
	// pageRaster : image dominante, aucune couche texte. Le pipeline n'a aucune
	// prise ; sans signalement le document ressortirait intact.
	pageRaster
	// pageHybrid : image dominante surmontée d'une couche texte invisible (PDF
	// « searchable », sortie de scanner ou d'un OCR préalable). Le pipeline
	// anonymise la couche texte — et seulement elle. Les pixels d'origine
	// restent lisibles à l'œil comme par n'importe quel OCR, alors que toutes
	// les vérifications textuelles passent au vert. Plus dangereux que
	// pageRaster, précisément parce que ça ne se voit pas.
	pageHybrid
)

// classifyPage détermine la prise du pipeline sur une page.
// En cas de doute (MediaBox absente, ressources illisibles), retourne
// pageNormal : la détection ne doit pas transformer un PDF exotique en refus
// systématique.
func classifyPage(ctx *model.Context, pageNr int, content []byte, tokens []textToken) pageKind {
	visible, invisible := countTextRunes(tokens)

	// Du texte visible en quantité : la page est lisible et traitable, quelle
	// que soit la surface occupée par les images (fond, filigrane, illustration).
	if visible >= rasterMaxTextRunes {
		return pageNormal
	}

	d, _, attrs, err := ctx.PageDict(pageNr, false)
	if err != nil || attrs == nil || attrs.MediaBox == nil {
		return pageNormal
	}
	pageArea := attrs.MediaBox.Width() * attrs.MediaBox.Height()
	if pageArea <= 0 {
		return pageNormal
	}

	res := pageResources(ctx, d, attrs)
	if imageArea(ctx, content, res, identityCTM, 0)/pageArea < rasterCoverageThreshold {
		return pageNormal
	}

	// Image dominante et rien de visible : reste à savoir si un OCR a déjà
	// déposé une couche texte par-dessus. Un seul mot suffit à trancher — sa
	// présence prouve que le texte extractible ne décrit pas ce qui est rendu.
	if invisible > 0 {
		return pageHybrid
	}
	return pageRaster
}

// RasterPages retourne les numéros de page (base 1) détectées comme scannées
// sans couche texte. Leur contenu n'est pas anonymisable par le pipeline texte.
func (w *Walker) RasterPages() []int {
	return append([]int(nil), w.rasterPages...)
}

// HybridPages retourne les pages scannées surmontées d'une couche texte
// invisible : le texte extractible y est anonymisé, mais les pixels rendus au
// lecteur restent inchangés.
func (w *Walker) HybridPages() []int {
	return append([]int(nil), w.hybridPages...)
}

// modifiedInvisiblePages retourne les pages sur lesquelles le pipeline a
// remplacé du texte entièrement invisible, hors pages déjà classées hybrides.
//
// C'est le complément indispensable au critère de couverture : un bloc scanné
// minoritaire dans la page (signature, tampon, figure) surmonté de sa couche
// OCR reste sous le seuil de pageHybrid, alors que le remplacement y produit
// exactement la même illusion — texte assaini, pixels intacts.
func (w *Walker) modifiedInvisiblePages() []int {
	hybrid := make(map[int]bool, len(w.hybridPages))
	for _, nr := range w.hybridPages {
		hybrid[nr] = true
	}

	seen := map[int]bool{}
	var pages []int
	for _, seg := range w.segments {
		if !seg.modified || !seg.invisible {
			continue
		}
		nr := w.pages[seg.pageIdx].pageNr
		if hybrid[nr] || seen[nr] {
			continue
		}
		seen[nr] = true
		pages = append(pages, nr)
	}
	sort.Ints(pages)
	return pages
}

// formatPageList rend une liste de numéros de page en collapsant les plages
// contiguës : [1 2 3 7 9 10] → « 1-3, 7, 9-10 ».
func formatPageList(pages []int) string {
	if len(pages) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(pages); {
		j := i
		for j+1 < len(pages) && pages[j+1] == pages[j]+1 {
			j++
		}
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(pages[i]))
		if j > i {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(pages[j]))
		}
		i = j + 1
	}
	return b.String()
}

// countTextRunes sépare les caractères non blancs effectivement rendus de ceux
// portés par une couche invisible (mode Tr 3 ou 7).
func countTextRunes(tokens []textToken) (visible, invisible int) {
	for _, t := range tokens {
		n := 0
		for _, r := range t.text {
			if !unicode.IsSpace(r) {
				n++
			}
		}
		if t.isInvisible() {
			invisible += n
		} else {
			visible += n
		}
	}
	return visible, invisible
}

// pageResources retourne le dictionnaire de ressources effectif de la page :
// celui porté par la page si présent, sinon celui hérité de l'arbre de pages.
func pageResources(ctx *model.Context, d types.Dict, attrs *model.InheritedPageAttrs) types.Dict {
	if obj, found := d.Find("Resources"); found && obj != nil {
		if res, err := ctx.DereferenceDict(obj); err == nil && res != nil {
			return res
		}
	}
	return attrs.Resources
}

// imageArea somme la surface couverte par les images dessinées dans content,
// exprimée dans l'espace de la page. base est la matrice courante à l'entrée du
// flux ; depth borne la récursion dans les XObject Form.
//
// Les recouvrements entre images sont comptés plusieurs fois et une image
// débordant de la page compte pour sa surface entière : la mesure majore donc
// la couverture réelle. C'est le sens acceptable pour une détection dont le
// coût de l'oubli est une fuite silencieuse.
func imageArea(ctx *model.Context, content []byte, res types.Dict, base ctm, depth int) float64 {
	images, forms := xobjectsByKind(ctx, res)

	total := 0.0
	cur := base
	var gsStack []ctm
	var operands [][]byte

	i, n := 0, len(content)
	for i < n {
		if isWS(content[i]) {
			i++
			continue
		}
		if content[i] == '%' {
			for i < n && content[i] != '\n' && content[i] != '\r' {
				i++
			}
			continue
		}
		if content[i] == '(' {
			_, end := scanLiteralString(content, i)
			i = end
			continue
		}
		if content[i] == '<' {
			if i+1 < n && content[i+1] == '<' {
				i = skipDict(content, i)
				continue
			}
			_, end := scanHexString(content, i)
			i = end
			continue
		}
		if content[i] == '[' {
			_, end := scanArray(content, i)
			i = end
			continue
		}
		if isNumericStart(content[i]) {
			raw, end := scanNumber(content, i)
			operands = append(operands, raw)
			i = end
			continue
		}
		if content[i] == '/' {
			raw, end := scanName(content, i)
			operands = append(operands, raw)
			i = end
			continue
		}

		kwStart := i
		for i < n && !isWS(content[i]) && !isDelimiter(content[i]) {
			i++
		}
		if i == kwStart {
			i++
			continue
		}
		op := string(content[kwStart:i])

		switch op {
		case "q":
			gsStack = append(gsStack, cur)

		case "Q":
			if len(gsStack) > 0 {
				cur = gsStack[len(gsStack)-1]
				gsStack = gsStack[:len(gsStack)-1]
			}

		case "cm":
			if len(operands) >= 6 {
				var m ctm
				for k, raw := range operands[len(operands)-6:] {
					m[k] = parseFloatBytes(raw)
				}
				cur = m.mul(cur)
			}

		case "Do":
			if len(operands) >= 1 {
				name := xobjectName(operands[len(operands)-1])
				switch {
				case images[name]:
					total += cur.unitArea()
				case forms[name] != nil && depth < rasterMaxFormDepth:
					total += formImageArea(ctx, forms[name], res, cur, depth+1)
				}
			}

		case "BI":
			// Image en ligne : dessinée elle aussi dans le carré unité. Le saut
			// jusqu'à EI est indispensable — les données brutes qui suivent ID
			// contiendraient sinon des octets interprétés comme des opérateurs.
			total += cur.unitArea()
			i = skipInlineImage(content, i)
		}

		operands = operands[:0]
	}

	return total
}

// formImageArea descend dans un XObject Form et somme la surface des images
// qu'il dessine, exprimée dans l'espace du flux appelant.
func formImageArea(ctx *model.Context, obj types.Object, parentRes types.Dict, cur ctm, depth int) float64 {
	sd, _, err := ctx.DereferenceStreamDict(obj)
	if err != nil || sd == nil {
		return 0
	}
	if err := sd.Decode(); err != nil || len(sd.Content) == 0 {
		return 0
	}

	// La /Matrix du formulaire s'applique avant la matrice courante.
	m := identityCTM
	if arr := sd.ArrayEntry("Matrix"); len(arr) == 6 {
		for k, o := range arr {
			if f, err := ctx.DereferenceNumber(o); err == nil {
				m[k] = f
			}
		}
	}

	res := parentRes
	if obj, found := sd.Find("Resources"); found && obj != nil {
		if own, err := ctx.DereferenceDict(obj); err == nil && own != nil {
			res = own
		}
	}

	return imageArea(ctx, sd.Content, res, m.mul(cur), depth)
}

// xobjectsByKind répartit les XObject des ressources entre images et
// formulaires, indexés par leur nom tel qu'il apparaît devant l'opérateur Do.
func xobjectsByKind(ctx *model.Context, res types.Dict) (map[string]bool, map[string]types.Object) {
	images := map[string]bool{}
	forms := map[string]types.Object{}
	if res == nil {
		return images, forms
	}

	obj, found := res.Find("XObject")
	if !found || obj == nil {
		return images, forms
	}
	xod, err := ctx.DereferenceDict(obj)
	if err != nil || xod == nil {
		return images, forms
	}

	for name, o := range xod {
		sd, _, err := ctx.DereferenceStreamDict(o)
		if err != nil || sd == nil {
			continue
		}
		subtype := sd.NameEntry("Subtype")
		if subtype == nil {
			continue
		}
		switch *subtype {
		case "Image":
			images[name] = true
		case "Form":
			forms[name] = o
		}
	}

	return images, forms
}

// xobjectName retire le '/' initial d'un nom PDF brut.
func xobjectName(raw []byte) string {
	if len(raw) > 0 && raw[0] == '/' {
		return string(raw[1:])
	}
	return string(raw)
}

// skipInlineImage saute une image en ligne (BI … ID <données> EI) et retourne
// l'offset suivant l'opérateur EI. pos est l'offset juste après le mot-clé BI.
//
// Le marqueur de fin est cherché comme un EI isolé par des blancs : les données
// binaires peuvent contenir la séquence « EI » à l'intérieur d'un mot.
func skipInlineImage(content []byte, pos int) int {
	n := len(content)

	// Trouver l'opérateur ID, qui précède immédiatement les données.
	data := -1
	for i := pos; i+1 < n; i++ {
		if content[i] == 'I' && content[i+1] == 'D' &&
			(i == 0 || isWS(content[i-1]) || isDelimiter(content[i-1])) &&
			(i+2 >= n || isWS(content[i+2])) {
			data = i + 3 // ID + l'unique blanc de séparation
			break
		}
	}
	if data < 0 || data >= n {
		return n
	}

	for i := data; i+1 < n; i++ {
		if content[i] == 'E' && content[i+1] == 'I' &&
			isWS(content[i-1]) &&
			(i+2 >= n || isWS(content[i+2]) || isDelimiter(content[i+2])) {
			return i + 2
		}
	}
	return n
}
