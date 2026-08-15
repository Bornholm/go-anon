package pdf

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bornholm/go-anon/pkg/docprocessor"
	"github.com/bornholm/go-anon/pkg/ocr"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcore "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const yProximity = 3.0 // points: Y tolerance for grouping text on same line

// fontInfo tracks the active font state within a text block.
type fontInfo struct {
	name    string
	size    float64
	cmap    *toUnicodeMap // nil if not a CIDFont or no ToUnicode
	metrics *fontMetrics  // largeurs déclarées ; nil si la police n'en publie pas
}

// textToken is one text-rendering operation in a content stream.
type textToken struct {
	start      int      // byte offset of first operand in content stream (inclusive)
	end        int      // byte offset after operator keyword (exclusive)
	text       string   // decoded Unicode content
	xPos       float64  // X translation at render time
	yPos       float64  // Y translation at render time
	font       fontInfo // active font at render time
	isUTF16    bool     // true if original was UTF-16BE hex string
	isCIDFont  bool     // true if font uses glyph indices decoded via CMap
	renderMode int      // text rendering mode (Tr) at render time
}

// isInvisible rapporte si le token n'est pas rendu à l'écran : mode 3
// (invisible) ou 7 (détourage seul). C'est la signature d'une couche OCR
// superposée à une image — le texte est extractible mais le lecteur voit les
// pixels, pas lui.
func (t textToken) isInvisible() bool {
	return t.renderMode == 3 || t.renderMode == 7
}

// pageData holds extracted content for one PDF page.
type pageData struct {
	pageNr  int
	content []byte // decompressed consolidated content stream
	tokens  []textToken
}

// segmentData is a docprocessor.Segment backed by one or more consecutive textTokens.
type segmentData struct {
	pageIdx     int
	tokenStart  int
	tokenEnd    int
	text        string
	replacement string
	modified    bool
	// invisible : tous les tokens du segment sont en mode de rendu non visible.
	// Anonymiser un tel segment ne change rien à ce que le lecteur voit.
	invisible bool
}

// Walker implements docprocessor.Walker for PDF files.
type Walker struct {
	inputPath string
	ctx       *model.Context
	pages     []pageData
	segments  []segmentData
	// rasterPages liste les numéros de page (base 1) détectées comme scannées :
	// leur contenu échappe entièrement au pipeline d'anonymisation. Signalées
	// par Sanitize comme surface non traitée.
	rasterPages []int
	// hybridPages liste les pages scannées surmontées d'une couche texte
	// invisible : le pipeline y anonymise le texte extractible, mais les pixels
	// rendus au lecteur restent intacts.
	hybridPages []int
	// ocrPages porte le résultat de RunOCR, quand il a été demandé.
	ocrPages []ocrPage
	// rasterizer et ocrDPI sont mémorisés par RunOCR : le caviardage doit rendre
	// les pages exactement comme l'OCR les a vues, sinon les boîtes ne
	// correspondent plus aux pixels.
	rasterizer Rasterizer
	ocrDPI     int
	// verifyEngine relit le document produit ; nil désactive la vérification
	// visuelle.
	verifyEngine ocr.Engine
	ocrLang      string
	// redactions : zones à caviarder, par index de région OCR, en offsets dans
	// le texte de la région.
	redactions map[int][][2]int
}

// NewWalkerFromFile opens a PDF and extracts its text segments.
func NewWalkerFromFile(path string) (docprocessor.Walker, error) {
	ctx, err := pdfapi.ReadContextFile(path)
	if err != nil {
		return nil, fmt.Errorf("pdf: parse: %w", err)
	}

	w := &Walker{inputPath: path, ctx: ctx}

	for pageNr := 1; pageNr <= ctx.PageCount; pageNr++ {
		// Load ToUnicode CMaps for fonts on this page
		cmaps, err := loadPageCMaps(ctx, pageNr)
		if err != nil {
			cmaps = map[string]*toUnicodeMap{}
		}
		metrics := loadPageMetrics(ctx, pageNr, cmaps)

		r, err := pdfcore.ExtractPageContent(ctx, pageNr)
		if err != nil || r == nil {
			continue
		}
		contentBytes, err := io.ReadAll(r)
		if err != nil {
			continue
		}

		tokens := extractTextTokens(contentBytes, cmaps, metrics)

		// À évaluer avant le filtrage des pages sans token : c'est précisément
		// une page sans texte extractible qui est susceptible d'être un scan.
		switch classifyPage(ctx, pageNr, contentBytes, tokens) {
		case pageRaster:
			w.rasterPages = append(w.rasterPages, pageNr)
		case pageHybrid:
			w.hybridPages = append(w.hybridPages, pageNr)
		}

		if len(tokens) == 0 {
			continue
		}

		pageIdx := len(w.pages)
		w.pages = append(w.pages, pageData{
			pageNr:  pageNr,
			content: contentBytes,
			tokens:  tokens,
		})
		w.segments = append(w.segments, groupIntoSegments(pageIdx, tokens)...)
	}

	return w, nil
}

func (w *Walker) Walk(fn func(docprocessor.Segment) error) error {
	for i := range w.segments {
		i := i
		seg := &w.segments[i]
		if err := fn(docprocessor.Segment{
			Text: seg.text,
			Replace: func(anonymized string) {
				seg.replacement = anonymized
				seg.modified = true
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// SaveTo rewrites modified content streams and writes the PDF to outputPath.
func (w *Walker) SaveTo(outputPath string) error {
	modByPage := make(map[int][]int)
	for i, seg := range w.segments {
		if seg.modified {
			modByPage[seg.pageIdx] = append(modByPage[seg.pageIdx], i)
		}
	}

	for pageIdx, segIndices := range modByPage {
		pd := &w.pages[pageIdx]

		type repl struct {
			text    string
			tok     textToken
			isFirst bool
		}
		repls := make(map[int]repl)
		for _, si := range segIndices {
			seg := w.segments[si]
			for ti := seg.tokenStart; ti <= seg.tokenEnd; ti++ {
				tok := pd.tokens[ti]
				if ti == seg.tokenStart {
					repls[ti] = repl{text: seg.replacement, tok: tok, isFirst: true}
				} else {
					repls[ti] = repl{text: "", tok: tok, isFirst: false}
				}
			}
		}

		var out bytes.Buffer
		prevEnd := 0
		for ti, tok := range pd.tokens {
			out.Write(pd.content[prevEnd:tok.start])
			if r, ok := repls[ti]; ok {
				if r.isFirst {
					emitReplacement(&out, r.text, r.tok)
				} else {
					// Zero out this token – emit no text
					out.WriteString("() Tj")
				}
			} else {
				out.Write(pd.content[tok.start:tok.end])
			}
			prevEnd = tok.end
		}
		out.Write(pd.content[prevEnd:])

		// Ensure Helvetica is available in page font resources (for replacement text).
		if err := ensureHelvetica(w.ctx, pd.pageNr); err != nil {
			return fmt.Errorf("pdf: add helvetica page %d: %w", pd.pageNr, err)
		}

		sd, err := w.ctx.NewStreamDictForBuf(out.Bytes())
		if err != nil {
			return fmt.Errorf("pdf: new stream dict page %d: %w", pd.pageNr, err)
		}
		if err := sd.Encode(); err != nil {
			return fmt.Errorf("pdf: encode stream page %d: %w", pd.pageNr, err)
		}
		ir, err := w.ctx.IndRefForNewObject(*sd)
		if err != nil {
			return fmt.Errorf("pdf: register stream page %d: %w", pd.pageNr, err)
		}

		d, _, _, err := w.ctx.PageDict(pd.pageNr, false)
		if err != nil {
			return fmt.Errorf("pdf: page dict %d: %w", pd.pageNr, err)
		}
		d.Update("Contents", *ir)
	}

	if len(w.redactions) > 0 {
		return w.saveWithRedactions(outputPath)
	}
	return pdfapi.WriteContextFile(w.ctx, outputPath)
}

// emitReplacement writes the PDF content stream operators for a single replacement.
// It switches to Helvetica, emits the replacement text, then switches back.
func emitReplacement(out *bytes.Buffer, text string, tok textToken) {
	size := tok.font.size
	if size <= 0 {
		size = 10
	}
	origFont := tok.font.name
	if origFont == "" {
		origFont = "F1"
	}

	// Try to re-encode using the original font's CMap (preserves font metrics).
	if tok.isCIDFont && tok.font.cmap != nil {
		if encoded := tok.font.cmap.encodeString(text); encoded != nil {
			writeHexString(out, encoded)
			fmt.Fprintf(out, " Tj")
			return
		}
	}

	// Fallback: switch to Helvetica for the replacement, then restore original font.
	fmt.Fprintf(out, "/Helvetica %.4g Tf", size)
	out.WriteByte('\n')
	out.Write(encodePDFString(text, false))
	out.WriteString(" Tj\n")
	// Restore original font
	fmt.Fprintf(out, "/%s %.4g Tf", origFont, size)
}

// ensureHelvetica adds an /Helvetica core font entry to the page's Resources/Font if absent.
func ensureHelvetica(ctx *model.Context, pageNr int) error {
	d, _, _, err := ctx.PageDict(pageNr, false)
	if err != nil {
		return err
	}

	// Get or create Resources dict
	resObj, resFound := d.Find("Resources")
	var resDict types.Dict
	if resFound && resObj != nil {
		o, err := ctx.Dereference(resObj)
		if err == nil {
			if rd, ok := o.(types.Dict); ok {
				resDict = rd
			}
		}
	}
	if resDict == nil {
		resDict = types.NewDict()
		d.Insert("Resources", resDict)
	}

	// Get or create Font sub-dict
	fontObj, fontFound := resDict.Find("Font")
	var fontDict types.Dict
	if fontFound && fontObj != nil {
		o, err := ctx.Dereference(fontObj)
		if err == nil {
			if fd, ok := o.(types.Dict); ok {
				fontDict = fd
			}
		}
	}
	if fontDict == nil {
		fontDict = types.NewDict()
		resDict.Insert("Font", fontDict)
	}

	// Add Helvetica if not present
	if _, found := fontDict.Find("Helvetica"); !found {
		helveticaDict := types.NewDict()
		helveticaDict.InsertName("Type", "Font")
		helveticaDict.InsertName("Subtype", "Type1")
		helveticaDict.InsertName("BaseFont", "Helvetica")
		helveticaDict.InsertName("Encoding", "WinAnsiEncoding")
		ir, err := ctx.IndRefForNewObject(helveticaDict)
		if err != nil {
			return err
		}
		fontDict.Insert("Helvetica", *ir)
	}

	return nil
}

// loadPageCMaps returns a map of font name → ToUnicodeMap for all fonts on a page.
func loadPageCMaps(ctx *model.Context, pageNr int) (map[string]*toUnicodeMap, error) {
	cmaps := make(map[string]*toUnicodeMap)

	d, _, _, err := ctx.PageDict(pageNr, false)
	if err != nil {
		return cmaps, err
	}

	resObj, found := d.Find("Resources")
	if !found || resObj == nil {
		return cmaps, nil
	}
	resObj, err = ctx.Dereference(resObj)
	if err != nil {
		return cmaps, nil
	}
	resDict, ok := resObj.(types.Dict)
	if !ok {
		return cmaps, nil
	}

	fontObj, found := resDict.Find("Font")
	if !found || fontObj == nil {
		return cmaps, nil
	}
	fontObj, err = ctx.Dereference(fontObj)
	if err != nil {
		return cmaps, nil
	}
	fontDict, ok := fontObj.(types.Dict)
	if !ok {
		return cmaps, nil
	}

	for fontAlias := range fontDict {
		fontEntry, _ := fontDict.Find(fontAlias)
		if fontEntry == nil {
			continue
		}
		fontEntry, err = ctx.Dereference(fontEntry)
		if err != nil {
			continue
		}
		fd, ok := fontEntry.(types.Dict)
		if !ok {
			continue
		}

		tuObj, found := fd.Find("ToUnicode")
		if !found || tuObj == nil {
			continue
		}
		tuObj, err = ctx.Dereference(tuObj)
		if err != nil {
			continue
		}
		tuSD, ok := tuObj.(types.StreamDict)
		if !ok {
			continue
		}
		if err := tuSD.Decode(); err != nil {
			continue
		}
		cmaps[fontAlias] = parseToUnicodeCMap(tuSD.Content)
	}

	return cmaps, nil
}

// ── Content stream parser ──────────────────────────────────────────────────

// extractTextTokens parses a PDF content stream and returns all text-rendering
// operations with their byte ranges and decoded text.
func extractTextTokens(content []byte, cmaps map[string]*toUnicodeMap, metrics map[string]*fontMetrics) []textToken {
	var tokens []textToken

	type matrix [6]float64
	identity := matrix{1, 0, 0, 1, 0, 0}
	var textM, lineM matrix
	textLeading := 0.0
	inText := false
	currentFont := fontInfo{name: "", size: 10}

	// Le mode de rendu (Tr) appartient à l'état graphique : il est sauvegardé
	// par q / restauré par Q, et n'est PAS réinitialisé par BT. Seul ce champ
	// est empilé — le reste de l'état (fonte, matrices) suit le comportement
	// historique, que l'on ne modifie pas ici pour ne pas déplacer les offsets.
	renderMode := 0
	var renderModeStack []int

	type stackItem struct {
		raw   []byte
		start int
		end   int
	}
	var stack []stackItem

	i := 0
	n := len(content)

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
			start := i
			raw, end := scanLiteralString(content, i)
			stack = append(stack, stackItem{raw, start, end})
			i = end
			continue
		}
		if content[i] == '<' {
			if i+1 < n && content[i+1] == '<' {
				end := skipDict(content, i)
				stack = append(stack, stackItem{nil, i, end})
				i = end
				continue
			}
			start := i
			raw, end := scanHexString(content, i)
			stack = append(stack, stackItem{raw, start, end})
			i = end
			continue
		}
		if content[i] == '[' {
			start := i
			raw, end := scanArray(content, i)
			stack = append(stack, stackItem{raw, start, end})
			i = end
			continue
		}
		if isNumericStart(content[i]) {
			start := i
			raw, end := scanNumber(content, i)
			stack = append(stack, stackItem{raw, start, end})
			i = end
			continue
		}
		if content[i] == '/' {
			start := i
			raw, end := scanName(content, i)
			stack = append(stack, stackItem{raw, start, end})
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
		opEnd := i

		switch op {
		case "q":
			renderModeStack = append(renderModeStack, renderMode)
			stack = stack[:0]

		case "Q":
			if n := len(renderModeStack); n > 0 {
				renderMode = renderModeStack[n-1]
				renderModeStack = renderModeStack[:n-1]
			}
			stack = stack[:0]

		case "Tr":
			if len(stack) >= 1 {
				renderMode = int(parseFloatBytes(stack[len(stack)-1].raw))
			}
			stack = stack[:0]

		case "BI":
			// Image en ligne : sauter jusqu'à EI. Sans cela les données binaires
			// qui suivent ID sont interprétées comme des opérateurs et peuvent
			// engloutir du texte réel (une parenthèse ouvrante dans les pixels
			// déclencherait scanLiteralString).
			i = skipInlineImage(content, i)
			stack = stack[:0]

		case "BT":
			inText = true
			textM = identity
			lineM = identity
			stack = stack[:0]

		case "ET":
			inText = false
			stack = stack[:0]

		case "Tf":
			// /fontName size Tf
			if len(stack) >= 2 {
				nameRaw := stack[len(stack)-2].raw
				sizeRaw := stack[len(stack)-1].raw
				if nameRaw != nil {
					fontName := strings.TrimPrefix(string(nameRaw), "/")
					currentFont.name = fontName
					currentFont.cmap = cmaps[fontName]
					currentFont.metrics = metrics[fontName]
					currentFont.size = parseFloatBytes(sizeRaw)
				}
			}
			stack = stack[:0]

		case "Tm":
			if len(stack) >= 6 {
				for k, s := range stack[len(stack)-6:] {
					textM[k] = parseFloatBytes(s.raw)
				}
				lineM = textM
			}
			stack = stack[:0]

		case "Td", "TD":
			if len(stack) >= 2 {
				tx := parseFloatBytes(stack[len(stack)-2].raw)
				ty := parseFloatBytes(stack[len(stack)-1].raw)
				lineM[4] += tx
				lineM[5] += ty
				textM = lineM
				if op == "TD" {
					textLeading = -ty
				}
			}
			stack = stack[:0]

		case "T*":
			lineM[5] -= textLeading
			textM = lineM
			stack = stack[:0]

		case "TL":
			if len(stack) >= 1 {
				textLeading = parseFloatBytes(stack[len(stack)-1].raw)
			}
			stack = stack[:0]

		case "Tj":
			if inText && len(stack) >= 1 {
				s := stack[len(stack)-1]
				if s.raw != nil {
					text, isUTF16, isCID := decodeTextBytes(s.raw, currentFont.cmap)
					if strings.TrimSpace(text) != "" {
						tokens = append(tokens, textToken{
							start:      s.start,
							end:        opEnd,
							text:       text,
							xPos:       textM[4],
							yPos:       textM[5],
							font:       currentFont,
							isUTF16:    isUTF16,
							isCIDFont:  isCID,
							renderMode: renderMode,
						})
					}
				}
			}
			stack = stack[:0]

		case "TJ":
			if inText && len(stack) >= 1 {
				s := stack[len(stack)-1]
				if s.raw != nil {
					text, isUTF16 := decodeTJArray(s.raw, currentFont.cmap)
					if strings.TrimSpace(text) != "" {
						isCID := currentFont.cmap != nil
						tokens = append(tokens, textToken{
							start:      s.start,
							end:        opEnd,
							text:       text,
							xPos:       textM[4],
							yPos:       textM[5],
							font:       currentFont,
							isUTF16:    isUTF16,
							isCIDFont:  isCID,
							renderMode: renderMode,
						})
					}
				}
			}
			stack = stack[:0]

		case "'":
			lineM[5] -= textLeading
			textM = lineM
			if inText && len(stack) >= 1 {
				s := stack[len(stack)-1]
				if s.raw != nil {
					text, isUTF16, isCID := decodeTextBytes(s.raw, currentFont.cmap)
					if strings.TrimSpace(text) != "" {
						tokens = append(tokens, textToken{
							start:      s.start,
							end:        opEnd,
							text:       text,
							xPos:       textM[4],
							yPos:       textM[5],
							font:       currentFont,
							isUTF16:    isUTF16,
							isCIDFont:  isCID,
							renderMode: renderMode,
						})
					}
				}
			}
			stack = stack[:0]

		case "\"":
			lineM[5] -= textLeading
			textM = lineM
			if inText && len(stack) >= 3 {
				s := stack[len(stack)-1]
				if s.raw != nil {
					text, isUTF16, isCID := decodeTextBytes(s.raw, currentFont.cmap)
					if strings.TrimSpace(text) != "" {
						tokens = append(tokens, textToken{
							start:      stack[len(stack)-3].start,
							end:        opEnd,
							text:       text,
							xPos:       textM[4],
							yPos:       textM[5],
							font:       currentFont,
							isUTF16:    isUTF16,
							isCIDFont:  isCID,
							renderMode: renderMode,
						})
					}
				}
			}
			stack = stack[:0]

		default:
			stack = stack[:0]
		}
	}

	return tokens
}

// groupIntoSegments groups tokens by Y proximity into segments.
func groupIntoSegments(pageIdx int, tokens []textToken) []segmentData {
	if len(tokens) == 0 {
		return nil
	}
	var segs []segmentData
	start := 0
	for i := 1; i < len(tokens); i++ {
		if math.Abs(tokens[i].yPos-tokens[start].yPos) > yProximity {
			segs = append(segs, makeSegment(pageIdx, tokens, start, i-1))
			start = i
		}
	}
	segs = append(segs, makeSegment(pageIdx, tokens, start, len(tokens)-1))
	return segs
}

func makeSegment(pageIdx int, tokens []textToken, start, end int) segmentData {
	var sb strings.Builder
	invisible := true
	for i := start; i <= end; i++ {
		t := tokens[i]
		if i > start && needsSpace(tokens[i-1], t) {
			sb.WriteByte(' ')
		}
		sb.WriteString(t.text)
		if !t.isInvisible() {
			invisible = false
		}
	}
	return segmentData{
		pageIdx:    pageIdx,
		tokenStart: start,
		tokenEnd:   end,
		text:       sb.String(),
		invisible:  invisible,
	}
}

// avgGlyphRatio approxime l'avance moyenne d'un glyphe latin, en fraction du
// corps. Grossier par nature — les métriques exactes demanderaient /Widths et
// les AFM — mais l'usage ne demande pas mieux : on cherche à savoir s'il reste
// un écart *après* le mot, pas à composer la ligne.
const avgGlyphRatio = 0.5

// spaceGapRatio est l'écart résiduel, en fraction du corps, au-delà duquel deux
// opérations de texte consécutives sont réputées séparées visuellement.
// En deçà, l'écart s'explique par le crénage ou l'imprécision de l'estimation.
const spaceGapRatio = 0.2

// needsSpace rapporte si une espace doit être insérée entre deux opérations de
// texte consécutives d'une même ligne.
//
// Un flux PDF ne porte pas nécessairement les espaces : beaucoup de générateurs
// positionnent chaque mot par un Td/Tm sans jamais émettre le caractère. Les
// concaténer tels quels produit « M.OlivierVANDAMME », que le modèle ne peut pas
// reconnaître comme une personne — c'est une fuite, et elle n'a rien à voir avec
// le découpage en segments.
//
// Le sens de l'erreur est choisi : sous-estimer l'avance ajoute une espace de
// trop, ce qui est bénin ; la surestimer recolle les mots, ce qui masque une
// entité.
func needsSpace(prev, next textToken) bool {
	if prev.text == "" || next.text == "" {
		return false
	}
	if strings.HasSuffix(prev.text, " ") || strings.HasPrefix(next.text, " ") {
		return false
	}

	// Retour en arrière : changement de colonne ou de cellule sur la même
	// ligne. Quelle que soit l'avance, les deux fragments sont disjoints.
	if next.xPos < prev.xPos {
		return true
	}

	size := prev.font.size
	if size <= 0 {
		size = 10
	}
	advance, exact := prev.font.metrics.advance(prev.text, size)
	if !exact {
		advance = avgGlyphRatio * size * float64(utf8.RuneCountInString(prev.text))
	}

	return next.xPos-(prev.xPos+advance) > spaceGapRatio*size
}

// ── Content stream scanning helpers ───────────────────────────────────────

func isWS(b byte) bool {
	return b == 0 || b == 9 || b == 10 || b == 12 || b == 13 || b == 32
}

func isDelimiter(b byte) bool {
	return b == '(' || b == ')' || b == '<' || b == '>' ||
		b == '[' || b == ']' || b == '{' || b == '}' || b == '/' || b == '%'
}

func isNumericStart(b byte) bool {
	return (b >= '0' && b <= '9') || b == '-' || b == '+' || b == '.'
}

func scanLiteralString(content []byte, pos int) ([]byte, int) {
	depth := 0
	i := pos + 1
	var buf bytes.Buffer
	for i < len(content) {
		b := content[i]
		if b == '\\' && i+1 < len(content) {
			buf.WriteByte(b)
			i++
			buf.WriteByte(content[i])
			i++
			continue
		}
		if b == '(' {
			depth++
		}
		if b == ')' {
			if depth == 0 {
				i++
				break
			}
			depth--
		}
		buf.WriteByte(b)
		i++
	}
	return buf.Bytes(), i
}

func scanHexString(content []byte, pos int) ([]byte, int) {
	i := pos + 1
	var buf bytes.Buffer
	for i < len(content) && content[i] != '>' {
		if !isWS(content[i]) {
			buf.WriteByte(content[i])
		}
		i++
	}
	if i < len(content) {
		i++
	}
	return buf.Bytes(), i
}

func scanArray(content []byte, pos int) ([]byte, int) {
	depth := 0
	i := pos
	start := pos
	for i < len(content) {
		if content[i] == '[' {
			depth++
		} else if content[i] == ']' {
			depth--
			if depth == 0 {
				i++
				break
			}
		} else if content[i] == '(' {
			_, end := scanLiteralString(content, i)
			i = end
			continue
		}
		i++
	}
	return content[start:i], i
}

func skipDict(content []byte, pos int) int {
	depth := 0
	i := pos
	for i < len(content) {
		if i+1 < len(content) && content[i] == '<' && content[i+1] == '<' {
			depth++
			i += 2
		} else if i+1 < len(content) && content[i] == '>' && content[i+1] == '>' {
			depth--
			i += 2
			if depth == 0 {
				break
			}
		} else {
			i++
		}
	}
	return i
}

func scanNumber(content []byte, pos int) ([]byte, int) {
	i := pos
	if i < len(content) && (content[i] == '-' || content[i] == '+') {
		i++
	}
	for i < len(content) && content[i] >= '0' && content[i] <= '9' {
		i++
	}
	if i < len(content) && content[i] == '.' {
		i++
		for i < len(content) && content[i] >= '0' && content[i] <= '9' {
			i++
		}
	}
	return content[pos:i], i
}

func scanName(content []byte, pos int) ([]byte, int) {
	// PDF names start with '/'; skip it and scan until next whitespace or delimiter.
	i := pos + 1
	for i < len(content) && !isWS(content[i]) && !isDelimiter(content[i]) {
		i++
	}
	return content[pos:i], i
}

func parseFloatBytes(raw []byte) float64 {
	if len(raw) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(string(raw), 64)
	return f
}

// ── Text encoding/decoding ─────────────────────────────────────────────────

// decodeTextBytes decodes a PDF string token (literal or hex) using CMap or fallback encodings.
// Returns decoded text, whether it was UTF-16BE, and whether a CMap was used.
func decodeTextBytes(raw []byte, cmap *toUnicodeMap) (text string, isUTF16 bool, isCIDFont bool) {
	if len(raw) == 0 {
		return "", false, false
	}

	// Hex strings: all bytes are hex digits
	if isHexBytes(raw) {
		decoded, err := hex.DecodeString(string(raw))
		if err == nil {
			// Try CMap first
			if cmap != nil {
				s := cmap.decode(decoded)
				if s != "" {
					return s, false, true
				}
			}
			// UTF-16BE with BOM
			if len(decoded) >= 2 && decoded[0] == 0xFE && decoded[1] == 0xFF {
				return decodeUTF16BE(decoded[2:]), true, false
			}
			// Raw bytes as Latin-1
			var sb strings.Builder
			for _, b := range decoded {
				sb.WriteRune(rune(b))
			}
			return sb.String(), false, false
		}
	}

	// Literal string: decode escape sequences + Latin-1
	return decodeLiteralString(raw), false, false
}

func isHexBytes(raw []byte) bool {
	if len(raw) == 0 || len(raw)%2 != 0 {
		return false
	}
	for _, b := range raw {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return false
		}
	}
	return true
}

func decodeLiteralString(raw []byte) string {
	var buf bytes.Buffer
	i := 0
	for i < len(raw) {
		if raw[i] != '\\' {
			buf.WriteRune(rune(raw[i]))
			i++
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		switch raw[i] {
		case 'n':
			buf.WriteByte('\n')
		case 'r':
			buf.WriteByte('\r')
		case 't':
			buf.WriteByte('\t')
		case 'b':
			buf.WriteByte('\b')
		case 'f':
			buf.WriteByte('\f')
		case '(', ')', '\\':
			buf.WriteByte(raw[i])
		case '\n', '\r':
			// line continuation
		default:
			if raw[i] >= '0' && raw[i] <= '7' {
				octal := []byte{raw[i]}
				i++
				for k := 0; k < 2 && i < len(raw) && raw[i] >= '0' && raw[i] <= '7'; k++ {
					octal = append(octal, raw[i])
					i++
				}
				v, _ := strconv.ParseUint(string(octal), 8, 8)
				buf.WriteRune(rune(v))
				continue
			}
			buf.WriteByte(raw[i])
		}
		i++
	}
	return buf.String()
}

func decodeUTF16BE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(u16))
}

// decodeTJArray decodes a TJ operand array `[…]` — concatenates all string elements.
func decodeTJArray(raw []byte, cmap *toUnicodeMap) (string, bool) {
	if len(raw) < 2 || raw[0] != '[' {
		return "", false
	}
	content := raw[1:]
	if len(content) > 0 && content[len(content)-1] == ']' {
		content = content[:len(content)-1]
	}
	var sb strings.Builder
	isUTF16 := false
	i := 0
	for i < len(content) {
		if isWS(content[i]) {
			i++
			continue
		}
		if content[i] == '(' {
			raw2, end := scanLiteralString(content, i)
			t, _, _ := decodeTextBytes(raw2, cmap)
			sb.WriteString(t)
			i = end
			continue
		}
		if content[i] == '<' {
			raw2, end := scanHexString(content, i)
			t, u, _ := decodeTextBytes(raw2, cmap)
			sb.WriteString(t)
			if u {
				isUTF16 = true
			}
			i = end
			continue
		}
		if isNumericStart(content[i]) {
			num, end := scanNumber(content, i)
			// Un nombre d'un tableau TJ déplace le texte de -n/1000 cadratin.
			// TeX et consorts encodent ainsi leurs espaces inter-mots, sans
			// jamais émettre le caractère : les ignorer collait « Free » et
			// « Forfait Mobile » en un seul mot, et le modèle ne pouvait plus
			// segmenter la ligne.
			if v := parseFloatBytes(content[i:end]); v <= -tjSpaceThreshold {
				sb.WriteByte(' ')
			}
			_ = num
			i = end
			continue
		}
		i++
	}
	return sb.String(), isUTF16
}

// tjSpaceThreshold sépare un espace inter-mots d'un simple crénage, en
// millièmes de cadratin.
//
// Une espace vaut 250 à 330 millièmes selon la police ; le crénage d'une paire
// serrée dépasse rarement 100. Le seuil est placé sous la première valeur
// plutôt qu'au-dessus de la seconde : une espace de trop se voit et se corrige,
// deux mots recollés masquent une entité et ne se voient plus.
const tjSpaceThreshold = 150

// substituteWinAnsi choisit le remplaçant d'une rune absente de Windows-1252.
//
// Une chaîne PDF littérale est relue octet par octet selon l'encodage de la
// police. Y écrire les octets UTF-8 d'un caractère absent de cet encodage ne le
// restitue pas : le lecteur affiche un octet par caractère. « █ » (U+2588, soit
// E2 96 88) ressortait ainsi en « â–ˆ », ce qui rendait illisibles les pages
// caviardées.
//
// Les caractères de bloc servent au caviardage et doivent rester opaques : ils
// deviennent des dièses. Le reste devient un point d'interrogation, qui signale
// la perte sans prétendre l'avoir évitée.
func substituteWinAnsi(r rune) byte {
	if r >= 0x2580 && r <= 0x259F { // Block Elements
		return '#'
	}
	return '?'
}

// win1252Extra maps Unicode code points that exist in Windows-1252 but not in Latin-1
// (the range U+0080–U+009F is remapped to printable characters in Windows-1252).
var win1252Extra = map[rune]byte{
	0x20AC: 0x80, // €
	0x201A: 0x82, // ‚
	0x0192: 0x83, // ƒ
	0x201E: 0x84, // „
	0x2026: 0x85, // …
	0x2020: 0x86, // †
	0x2021: 0x87, // ‡
	0x02C6: 0x88, // ˆ
	0x2030: 0x89, // ‰
	0x0160: 0x8A, // Š
	0x2039: 0x8B, // ‹
	0x0152: 0x8C, // Œ
	0x017D: 0x8E, // Ž
	0x2018: 0x91, // ' (guillemet simple gauche)
	0x2019: 0x92, // ' (apostrophe typographique / guillemet simple droit)
	0x201C: 0x93, // " (guillemet double gauche)
	0x201D: 0x94, // " (guillemet double droit)
	0x2022: 0x95, // • (puce)
	0x2013: 0x96, // – (tiret demi-cadratin)
	0x2014: 0x97, // — (tiret cadratin)
	0x02DC: 0x98, // ˜
	0x2122: 0x99, // ™
	0x0161: 0x9A, // š
	0x203A: 0x9B, // ›
	0x0153: 0x9C, // œ
	0x017E: 0x9E, // ž
	0x0178: 0x9F, // Ÿ
}

// encodePDFString encodes a Go string as a PDF literal string operand.
// If useUTF16 the result is a hex string with BOM; otherwise Windows-1252 literal
// (covers Latin-1 + common typographic characters like smart quotes and bullet).
func encodePDFString(s string, useUTF16 bool) []byte {
	if useUTF16 {
		u16 := utf16.Encode([]rune(s))
		var buf bytes.Buffer
		buf.WriteString("<FEFF")
		for _, u := range u16 {
			fmt.Fprintf(&buf, "%04X", u)
		}
		buf.WriteByte('>')
		return buf.Bytes()
	}
	var buf bytes.Buffer
	buf.WriteByte('(')
	for _, r := range s {
		switch {
		case r == '(':
			buf.WriteString(`\(`)
		case r == ')':
			buf.WriteString(`\)`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r < 128:
			buf.WriteByte(byte(r))
		default:
			if b, ok := win1252Extra[r]; ok {
				buf.WriteByte(b)
			} else if r < 256 {
				buf.WriteByte(byte(r))
			} else {
				buf.WriteByte(substituteWinAnsi(r))
			}
		}
	}
	buf.WriteByte(')')
	return buf.Bytes()
}

// writeHexString writes the glyph bytes as a PDF hex string <…>.
func writeHexString(out *bytes.Buffer, b []byte) {
	out.WriteByte('<')
	out.WriteString(hex.EncodeToString(b))
	out.WriteByte('>')
}
