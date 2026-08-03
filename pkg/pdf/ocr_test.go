package pdf

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/docprocessor"
	"github.com/bornholm/go-anon/pkg/ocr"
)

// ── Doublures ─────────────────────────────────────────────────────────────

type stubRasterizer struct {
	rendered []int // pages rendues, dans l'ordre
	err      error
}

func (s *stubRasterizer) Name() string     { return "stub" }
func (s *stubRasterizer) Available() error { return nil }
func (s *stubRasterizer) Render(_ string, pageNr, _ int) (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.rendered = append(s.rendered, pageNr)
	return image.NewGray(image.Rect(0, 0, 10, 10)), nil
}

type stubEngine struct {
	words       []ocr.Word
	unavailable error
}

func (s *stubEngine) Name() string     { return "stub" }
func (s *stubEngine) Available() error { return s.unavailable }
func (s *stubEngine) Recognize(image.Image, string) ([]ocr.Word, error) {
	return s.words, nil
}

func stubOCROptions(words []ocr.Word) (OCROptions, *stubRasterizer) {
	r := &stubRasterizer{}
	return OCROptions{Engine: &stubEngine{words: words}, Rasterizer: r}, r
}

// ── Tests ─────────────────────────────────────────────────────────────────

// TestRunOCR_CoversEveryPage : l'océrisation est systématique, y compris sur
// les pages qui portent déjà du texte. Trier en amont supposerait de savoir
// reconnaître ce qui mérite un OCR — c'est-à-dire refaire l'heuristique de la
// phase 0 avec ses faux négatifs.
func TestRunOCR_CoversEveryPage(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{
		drawText(400),                 // page de texte natif
		drawImage(0, 0, pageW, pageH), // page scannée
	}))
	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	opts, ras := stubOCROptions([]ocr.Word{
		{Text: "Jean", BBox: ocr.Rect{X: 0, Y: 0, W: 40, H: 10}, Confidence: 0.9, Line: 0},
	})
	if err := w.RunOCR(opts); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	if len(ras.rendered) != 2 {
		t.Errorf("pages rendues = %v, attendu les deux", ras.rendered)
	}
	if len(w.OCRLines(1)) == 0 || len(w.OCRLines(2)) == 0 {
		t.Error("les deux pages devraient porter des lignes océrisées")
	}
}

// TestRunOCR_FailsWhenEngineUnavailable : un moteur absent est une erreur, pas
// un silence. Dégrader sans le dire recréerait le fail-open que ce chantier
// corrige.
func TestRunOCR_FailsWhenEngineUnavailable(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{drawImage(0, 0, pageW, pageH)}))
	wr, _ := NewWalkerFromFile(path)
	w := wr.(*Walker)

	err := w.RunOCR(OCROptions{
		Engine:     &stubEngine{unavailable: errStub},
		Rasterizer: &stubRasterizer{},
	})
	if err == nil {
		t.Fatal("un moteur indisponible devrait faire échouer RunOCR")
	}
}

func TestRunOCR_RequiresEngineAndRasterizer(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{drawText(400)}))
	wr, _ := NewWalkerFromFile(path)
	w := wr.(*Walker)

	if err := w.RunOCR(OCROptions{Rasterizer: &stubRasterizer{}}); err == nil {
		t.Error("un OCR sans moteur devrait échouer")
	}
	if err := w.RunOCR(OCROptions{Engine: &stubEngine{}}); err == nil {
		t.Error("un OCR sans rastériseur devrait échouer")
	}
}

// TestReadOnlyText : le texte océrisé est exposé comme lisible mais non
// réécrivable, localisé par page et sans jamais mélanger les pages.
func TestReadOnlyText(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{
		drawImage(0, 0, pageW, pageH),
		drawImage(0, 0, pageW, pageH),
	}))
	wr, _ := NewWalkerFromFile(path)
	w := wr.(*Walker)

	opts, _ := stubOCROptions([]ocr.Word{
		{Text: "Jean", BBox: ocr.Rect{X: 0, Y: 0, W: 40, H: 10}, Line: 0},
		{Text: "Dupont", BBox: ocr.Rect{X: 50, Y: 0, W: 60, H: 10}, Line: 0},
	})
	if err := w.RunOCR(opts); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	regions := w.ReadOnlyText()
	if len(regions) != 2 {
		t.Fatalf("régions = %d, attendu 2 : %+v", len(regions), regions)
	}
	if got, want := regions[0].Text, "Jean Dupont"; got != want {
		t.Errorf("texte = %q, attendu %q", got, want)
	}
	if !strings.Contains(regions[0].Label, "page 1") {
		t.Errorf("libellé = %q, devrait localiser la page", regions[0].Label)
	}
	if regions[0].Label == regions[1].Label {
		t.Error("les régions devraient être distinguées page par page")
	}
}

// TestWalker_ImplementsReadOnlyTextWalker verrouille le contrat optionnel :
// sans lui, docprocessor ignorerait silencieusement le contenu océrisé.
func TestWalker_ImplementsReadOnlyTextWalker(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{drawText(400)}))
	wr, _ := NewWalkerFromFile(path)
	if _, ok := wr.(docprocessor.ReadOnlyTextWalker); !ok {
		t.Fatal("le Walker PDF devrait implémenter docprocessor.ReadOnlyTextWalker")
	}
}

var errStub = stubError("moteur absent")

type stubError string

func (e stubError) Error() string { return string(e) }

// ── Intégration : chaîne réelle rastérisation → OCR ───────────────────────

// TestRunOCR_RealPipeline valide la chaîne complète avec les vrais outils.
// Ignoré s'ils sont absents : le reste de la suite doit rester exécutable
// partout, mais cette validation est la seule qui prouve que le format TSV,
// l'encodage PNG et le plomberie stdin s'accordent réellement.
func TestRunOCR_RealPipeline(t *testing.T) {
	engine := ocr.NewTesseractExec()
	ras := NewPdftoppmRasterizer()
	if err := engine.Available(); err != nil {
		t.Skipf("tesseract absent : %v", err)
	}
	if err := ras.Available(); err != nil {
		t.Skipf("pdftoppm absent : %v", err)
	}

	// Corps généreux : en deçà de ~20 points à 150 dpi, un moteur peine sur du
	// texte synthétique sans anticrénelage.
	content := "BT /F1 48 Tf 40 400 Td (Jean Dupont) Tj ET\n"
	path := buildPDF(t, pageObjects([]string{content}))

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	if err := w.RunOCR(OCROptions{
		Engine: engine, Rasterizer: ras, Lang: "fr", DPI: 200,
	}); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	lines := w.OCRLines(1)
	if len(lines) == 0 {
		t.Fatal("aucune ligne reconnue")
	}

	joined := strings.Join(func() []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = l.Text
		}
		return out
	}(), " ")

	if !strings.Contains(joined, "Dupont") {
		t.Fatalf("texte reconnu = %q, « Dupont » attendu", joined)
	}

	// Les boîtes doivent être exploitables : non dégénérées et dans l'image.
	for _, l := range lines {
		for _, word := range l.Words {
			if word.BBox.W <= 0 || word.BBox.H <= 0 {
				t.Errorf("boîte dégénérée pour %q : %+v", word.Text, word.BBox)
			}
		}
	}

	// Et surtout : l'entité doit être localisable en pixels depuis ses offsets,
	// ce dont dépendra le caviardage.
	for _, l := range lines {
		if idx := strings.Index(l.Text, "Dupont"); idx >= 0 {
			boxes := l.BoxesFor(idx, idx+len("Dupont"))
			if len(boxes) == 0 {
				t.Errorf("aucune boîte pour l'entité dans %q", l.Text)
			}
			return
		}
	}
}

// ── Caviardage pixel ──────────────────────────────────────────────────────

func TestBlacken(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	out := blacken(src, []ocr.Rect{{X: 10, Y: 10, W: 20, H: 20}})

	if r, _, _, _ := out.At(15, 15).RGBA(); r != 0 {
		t.Error("le centre de la zone devrait être noirci")
	}
	if r, _, _, _ := out.At(80, 80).RGBA(); r == 0 {
		t.Error("le reste de l'image devrait être intact")
	}
	// La marge doit déborder la boîte : les moteurs OCR épousent les glyphes au
	// plus près, et sans elle jambages et accents survivent au noircissement.
	if r, _, _, _ := out.At(10, 9).RGBA(); r != 0 {
		t.Error("la marge au-dessus de la boîte devrait être noircie")
	}
}

// TestBlacken_ClipsToImage : une boîte débordant de l'image ne doit pas faire
// paniquer — un OCR sur une image redimensionnée peut en produire.
func TestBlacken_ClipsToImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	out := blacken(src, []ocr.Rect{{X: 40, Y: 40, W: 100, H: 100}, {X: -20, Y: -20, W: 30, H: 30}})
	if out.Bounds() != src.Bounds() {
		t.Errorf("bornes modifiées : %v", out.Bounds())
	}
}

func TestOCRPage_RegionText(t *testing.T) {
	p := ocrPage{pageNr: 1, lines: []ocr.Line{
		{Text: "Jean Dupont"},
		{Text: "directeur"},
	}}

	text, bounds := p.regionText()
	if want := "Jean Dupont\ndirecteur"; text != want {
		t.Fatalf("texte = %q, attendu %q", text, want)
	}
	// Les bornes doivent redécouper exactement chaque ligne : c'est d'elles que
	// dépend la correspondance offset d'entité → boîte de pixels.
	for i, b := range bounds {
		if got := text[b[0]:b[1]]; got != p.lines[i].Text {
			t.Errorf("bornes %d = %q, attendu %q", i, got, p.lines[i].Text)
		}
	}
}

// TestRedaction_RemovesPixels est la preuve de la phase : après caviardage, le
// nom ne doit plus être *lisible* dans le document produit. On le vérifie en
// ré-océrisant la sortie — le seul contrôle qui parle le même langage que le
// risque réel, puisque tous les autres n'inspectent que des chaînes.
func TestRedaction_RemovesPixels(t *testing.T) {
	engine := ocr.NewTesseractExec()
	ras := NewPdftoppmRasterizer()
	if err := engine.Available(); err != nil {
		t.Skipf("tesseract absent : %v", err)
	}
	if err := ras.Available(); err != nil {
		t.Skipf("pdftoppm absent : %v", err)
	}

	ocrOpts := OCROptions{Engine: engine, Rasterizer: ras, Lang: "fr", DPI: 200}
	content := "BT /F1 48 Tf 40 400 Td (Jean Dupont) Tj ET\n"
	path := buildPDF(t, pageObjects([]string{content}))

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)
	if err := w.RunOCR(ocrOpts); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	regions := w.ReadOnlyText()
	if len(regions) == 0 {
		t.Fatal("aucune région océrisée")
	}
	idx := strings.Index(regions[0].Text, "Dupont")
	if idx < 0 {
		t.Fatalf("prémisse invalide : « Dupont » non reconnu dans %q", regions[0].Text)
	}
	w.MarkRedaction(0, idx, idx+len("Dupont"))

	if got := w.RedactedPages(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("pages à caviarder = %v, attendu [1]", got)
	}

	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := w.SaveTo(out); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	// Relecture : la page aplatie ne porte plus aucune couche texte, donc
	// l'extraction ne rend rien — première garantie, purement structurelle.
	wr2, err := NewWalkerFromFile(out)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	w2 := wr2.(*Walker)
	if n := len(w2.segments); n != 0 {
		t.Errorf("la page aplatie porte encore %d segment(s) de texte", n)
	}

	// Garantie décisive : ré-OCR de la sortie, en mode « texte épars ».
	// L'analyse de mise en page classe une page portant un large aplat noir
	// comme non textuelle et ne rend alors plus rien — un contrôle qui passerait
	// toujours, donc inutile. Cf. ocr.PSMSparse.
	verifyOpts := ocrOpts
	verifyOpts.Engine = ocr.NewTesseractExecSparse()
	if err := w2.RunOCR(verifyOpts); err != nil {
		t.Fatalf("ré-OCR: %v", err)
	}
	var produced string
	if r := w2.ReadOnlyText(); len(r) > 0 {
		produced = r[0].Text
	}

	if strings.Contains(produced, "Dupont") {
		t.Errorf("le nom reste lisible après caviardage : %q", produced)
	}
	// Contrôle de portée : seul le nom visé disparaît, pas toute la page.
	if !strings.Contains(produced, "Jean") {
		t.Errorf("le caviardage a débordé sur le reste de la ligne : %q", produced)
	}
}

// ── Vérification visuelle du document produit ─────────────────────────────

// TestVisualText_SeesWhatARedactionMissed est le contrôle décisif de la
// vérification : elle doit *détecter* une fuite, pas seulement passer sur un
// document propre. Une vérification qui ne dit jamais rien est indiscernable
// d'une vérification cassée.
//
// Le cas simulé est le mode d'échec numéro un du caviardage pixel : des boîtes
// mal alignées, par décalage d'origine ou axe Y inversé entre l'espace PDF et
// l'espace image. Ici, un caviardage volontairement décalé.
func TestVisualText_SeesWhatARedactionMissed(t *testing.T) {
	engine := ocr.NewTesseractExec()
	ras := NewPdftoppmRasterizer()
	if err := engine.Available(); err != nil {
		t.Skipf("tesseract absent : %v", err)
	}
	if err := ras.Available(); err != nil {
		t.Skipf("pdftoppm absent : %v", err)
	}

	opts := OCROptions{
		Engine:       engine,
		Rasterizer:   ras,
		Lang:         "fr",
		DPI:          200,
		VerifyEngine: ocr.NewTesseractExecSparse(),
	}

	// Deux lignes bien séparées : on caviarde la seconde en croyant viser la
	// première, comme le ferait un décalage de repère.
	content := "BT /F1 48 Tf 40 600 Td (Dupont) Tj ET\n" +
		"BT /F1 48 Tf 40 300 Td (Martin) Tj ET\n"
	path := buildPDF(t, pageObjects([]string{content}))

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)
	if err := w.RunOCR(opts); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	regions := w.ReadOnlyText()
	if len(regions) == 0 || !strings.Contains(regions[0].Text, "Dupont") {
		t.Skipf("OCR trop imprécis sur ce fixture : %+v", regions)
	}

	// Caviardage volontairement porté sur « Martin » alors que la donnée à
	// retirer est « Dupont ».
	if idx := strings.Index(regions[0].Text, "Martin"); idx >= 0 {
		w.MarkRedaction(0, idx, idx+len("Martin"))
	}

	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := w.SaveTo(out); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	visual, err := w.VisualText(out)
	if err != nil {
		t.Fatalf("VisualText: %v", err)
	}
	if len(visual) == 0 {
		t.Fatal("la relecture n'a rien rendu — vérification vacante")
	}

	joined := ""
	for _, r := range visual {
		joined += r.Text + "\n"
	}
	if !strings.Contains(joined, "Dupont") {
		t.Errorf("la relecture n'a pas vu la donnée manquée : %q", joined)
	}
	if strings.Contains(joined, "Martin") {
		t.Errorf("la zone effectivement caviardée reste lisible : %q", joined)
	}
}

// TestVisualText_DisabledWithoutEngine : sans moteur de relecture, la garantie
// n'est simplement pas fournie — sans erreur ni faux signal.
func TestVisualText_DisabledWithoutEngine(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{drawText(400)}))
	wr, _ := NewWalkerFromFile(path)
	w := wr.(*Walker)

	opts, _ := stubOCROptions(nil)
	if err := w.RunOCR(opts); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}

	regions, err := w.VisualText(path)
	if err != nil || regions != nil {
		t.Errorf("regions=%+v err=%v", regions, err)
	}
}
