package pdf

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	"github.com/bornholm/go-anon/pkg/ner"
	"github.com/bornholm/go-anon/pkg/ocr"
)

// Test d'intégration du cas le plus dangereux : le PDF « searchable ».
//
// Un scan surmonté d'une couche texte invisible est le piège de P2 — le texte
// extractible est anonymisé, et lui seul ; toutes les vérifications textuelles
// passent au vert pendant que les pixels restent lisibles. C'est le seul
// scénario qui met en jeu les phases 1, 4′, 5′ et 6′ ensemble, et rien ne le
// couvrait de bout en bout.

// scanFixture décrit la page de test : petite, pour que l'image brute reste de
// taille raisonnable dans le PDF assemblé à la main.
const (
	scanW       = 340.0
	scanH       = 100.0
	scanDPI     = 150
	fixtureName = "Dupont"
	// fixtureKeep est le témoin : un mot qui n'est pas une donnée personnelle et
	// doit survivre. Sans lui, une page rendue blanche ferait passer le test à
	// vide — « le nom n'est plus lisible » serait vrai parce que plus rien ne
	// l'est.
	fixtureKeep = "Facture"
)

// buildSearchablePDF fabrique un PDF « searchable » : une image de texte
// occupant toute la page, surmontée d'une couche texte invisible (Tr 3) qui en
// reprend le contenu. C'est exactement ce que produit un scanner à OCR intégré.
func buildSearchablePDF(t *testing.T, ras Rasterizer) string {
	t.Helper()

	// 1. Une page de texte ordinaire, qui servira de source au rendu.
	src := buildSizedPDF(t, scanW, scanH,
		fmt.Sprintf("BT /F1 30 Tf 15 35 Td (%s %s) Tj ET\n", fixtureKeep, fixtureName))

	img, err := ras.Render(src, 1, scanDPI)
	if err != nil {
		t.Fatalf("rendu du fixture : %v", err)
	}

	// 2. La même page, mais en pixels — plus une couche texte invisible.
	// L'ordre importe : l'image d'abord, le texte invisible par-dessus.
	content := fmt.Sprintf("q %g 0 0 %g 0 0 cm /Im0 Do Q\n", scanW, scanH) +
		fmt.Sprintf("BT /F1 30 Tf 15 35 Td 3 Tr (%s %s) Tj 0 Tr ET\n", fixtureKeep, fixtureName)

	return buildImagePDF(t, scanW, scanH, img, content)
}

// buildSizedPDF assemble un PDF d'une page aux dimensions données.
func buildSizedPDF(t *testing.T, w, h float64, content string) string {
	t.Helper()
	return buildPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] "+
			"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", w, h),
		stream("", content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// buildImagePDF assemble un PDF d'une page portant img comme XObject /Im0.
// L'image est stockée en RGB non compressé : la taille importe peu ici, la
// lisibilité du fixture beaucoup.
func buildImagePDF(t *testing.T, w, h float64, img image.Image, content string) string {
	t.Helper()

	b := img.Bounds()
	rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bb>>8))
		}
	}

	return buildPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] "+
			"/Resources << /XObject << /Im0 5 0 R >> /Font << /F1 6 0 R >> >> "+
			"/Contents 4 0 R >>", w, h),
		stream("", content),
		stream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceRGB /BitsPerComponent 8", b.Dx(), b.Dy()), string(rgb)),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// nameRecognizer ne reconnaît que fixtureName, où qu'il apparaisse.
type nameRecognizer struct{}

func (nameRecognizer) Recognize(text string) ([]ner.Entity, error) {
	var entities []ner.Entity
	for pos := 0; ; {
		idx := strings.Index(text[pos:], fixtureName)
		if idx < 0 {
			break
		}
		start := pos + idx
		entities = append(entities, ner.Entity{
			Text: fixtureName, Type: ner.TypePER,
			Start: start, End: start + len(fixtureName), Confidence: 1,
		})
		pos = start + len(fixtureName)
	}
	return entities, nil
}

// TestIntegration_SearchablePDF suit le cas hybride de bout en bout : détection
// du piège, océrisation, anonymisation de la couche texte, caviardage des
// pixels, puis vérification visuelle du document produit.
func TestIntegration_SearchablePDF(t *testing.T) {
	engine := ocr.NewTesseractExec()
	ras := NewPdftoppmRasterizer()
	if err := engine.Available(); err != nil {
		t.Skipf("tesseract absent : %v", err)
	}
	if err := ras.Available(); err != nil {
		t.Skipf("pdftoppm absent : %v", err)
	}

	path := buildSearchablePDF(t, ras)

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	// Phase 1 — le piège doit être reconnu comme tel : une page image portant
	// une couche texte invisible, et non une page de texte ordinaire.
	if got := w.HybridPages(); len(got) != 1 {
		t.Fatalf("pages hybrides = %v, attendu [1]", got)
	}
	if got := w.RasterPages(); len(got) != 0 {
		t.Errorf("la page ne devrait pas être classée raster nu : %v", got)
	}

	// Phase 4′ — océrisation.
	ocrOpts := OCROptions{
		Engine:       engine,
		Rasterizer:   ras,
		Lang:         "fr",
		DPI:          scanDPI,
		VerifyEngine: ocr.NewTesseractExecSparse(),
	}
	if err := w.RunOCR(ocrOpts); err != nil {
		t.Fatalf("RunOCR: %v", err)
	}
	regions := w.ReadOnlyText()
	if len(regions) == 0 || !strings.Contains(regions[0].Text, fixtureName) {
		t.Skipf("OCR trop imprécis sur ce fixture : %+v", regions)
	}

	// Anonymisation : la couche invisible passe par les segments, les pixels
	// par le caviardage.
	anon := anonymizer.New(nameRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace, ConsistentMap: true,
	})
	proc := docprocessor.New(anon, docprocessor.WithMultiViewDetection())

	session, report, err := proc.ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if report.RedactedZones == 0 {
		t.Error("aucune zone bitmap caviardée alors que l'OCR a lu le nom")
	}
	if report.Entities[ner.TypePER] == 0 {
		t.Error("aucune entité comptée")
	}
	// La couche invisible est du texte : elle doit avoir été remplacée elle
	// aussi. L'anonymiser ne suffit pas, mais l'omettre laisserait la donnée
	// extractible par un simple copier-coller.
	if len(session.OriginalToPlaceholder) == 0 {
		t.Error("la couche texte invisible n'a pas été anonymisée")
	}

	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := w.SaveTo(out); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	// Phase 6′ — vérification visuelle : plus rien de lisible dans la sortie.
	leaks, err := proc.VerifyOutput(w, out, session)
	if err != nil {
		t.Fatalf("VerifyOutput : %v", err)
	}
	if len(leaks) > 0 {
		t.Errorf("la vérification visuelle signale %d fuite(s) : %+v", len(leaks), leaks)
	}

	// Et le contrôle direct, indépendant du chemin de vérification : ni la
	// couche texte ni les pixels ne doivent rendre le nom.
	assertNameGone(t, out, ocrOpts)
}

// assertNameGone vérifie qu'aucun chemin de lecture ne rend le nom : ni
// l'extraction de texte, ni la relecture visuelle.
func assertNameGone(t *testing.T, path string, ocrOpts OCROptions) {
	t.Helper()

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("relecture : %v", err)
	}
	w := wr.(*Walker)

	var extracted strings.Builder
	if err := w.Walk(func(seg docprocessor.Segment) error {
		extracted.WriteString(seg.Text)
		extracted.WriteByte('\n')
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if strings.Contains(extracted.String(), fixtureName) {
		t.Errorf("le nom reste extractible : %q", extracted.String())
	}

	if err := w.RunOCR(ocrOpts); err != nil {
		t.Fatalf("ré-OCR : %v", err)
	}
	var visible strings.Builder
	for _, region := range w.ReadOnlyText() {
		visible.WriteString(region.Text)
		if strings.Contains(region.Text, fixtureName) {
			t.Errorf("le nom reste LISIBLE dans %s : %q", region.Label, region.Text)
		}
	}
	// Sans ce témoin, le contrôle ci-dessus serait satisfait par une page
	// devenue illisible dans son ensemble — un caviardage qui déborde.
	if !strings.Contains(visible.String(), fixtureKeep) {
		t.Errorf("le caviardage a débordé sur le reste de la page : %q", visible.String())
	}
}
