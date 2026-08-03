package pdf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// ── Fixtures ──────────────────────────────────────────────────────────────
//
// Les PDF sont écrits à la main plutôt que produits par pdfcpu : la détection
// raster porte précisément sur la géométrie du flux de contenu (cm / Do), qu'il
// faut pouvoir poser au point près.

const (
	pageW = 595.0 // A4 en points
	pageH = 842.0
)

// buildPDF assemble un PDF à partir du corps des objets 1..N, avec table xref
// et trailer corrects. Le premier objet doit être le catalogue.
func buildPDF(t *testing.T, objects []string) string {
	t.Helper()

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, len(objects)+1)
	for i, body := range objects {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}

	xrefPos := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objects)+1)
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, xrefPos)

	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// stream rend un objet flux avec un /Length correct.
func stream(extraDict, content string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream",
		extraDict, len(content), content)
}

// pageObjects construit un PDF d'une ou plusieurs pages partageant une image
// XObject et une fonte. contents contient un flux de contenu par page.
func pageObjects(contents []string) []string {
	kids := make([]string, len(contents))
	// Objets : 1 catalogue, 2 pages, 3 image, 4 fonte, puis page/contenu par paire.
	for i := range contents {
		kids[i] = fmt.Sprintf("%d 0 R", 5+2*i)
	}

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
			strings.Join(kids, " "), len(contents)),
		// Image 1×1 en niveaux de gris : le contenu importe peu, seule sa
		// géométrie de placement est mesurée.
		stream("/Type /XObject /Subtype /Image /Width 1 /Height 1 "+
			"/ColorSpace /DeviceGray /BitsPerComponent 8", "\x00"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	for i, c := range contents {
		objs = append(objs, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] "+
				"/Resources << /XObject << /Im0 3 0 R >> /Font << /F1 4 0 R >> >> "+
				"/Contents %d 0 R >>", pageW, pageH, 6+2*i))
		objs = append(objs, stream("", c))
	}
	return objs
}

// drawImage place l'image Im0 sur un rectangle de w × h points en (x, y).
func drawImage(x, y, w, h float64) string {
	return fmt.Sprintf("q %g 0 0 %g %g %g cm /Im0 Do Q\n", w, h, x, y)
}

// drawText émet un bloc texte de n caractères non blancs.
func drawText(n int) string {
	return fmt.Sprintf("BT /F1 12 Tf 72 700 Td (%s) Tj ET\n",
		strings.Repeat("Dupont", n/6+1))
}

func rasterPagesOf(t *testing.T, path string) []int {
	t.Helper()
	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	return wr.(*Walker).RasterPages()
}

// ── Tests ─────────────────────────────────────────────────────────────────

// TestRasterDetection couvre l'heuristique page par page : les deux conditions
// (couverture image et absence de texte) doivent être réunies.
func TestRasterDetection(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "scan pleine page sans texte",
			content: drawImage(0, 0, pageW, pageH),
			want:    true,
		},
		{
			name:    "page de texte sans image",
			content: drawText(400),
			want:    false,
		},
		{
			// Garde-fou faux positif : le risque dominant de l'heuristique.
			name:    "page de texte avec illustration",
			content: drawImage(72, 600, 200, 150) + drawText(400),
			want:    false,
		},
		{
			// Image majoritaire mais texte abondant : la page reste traitable.
			name:    "grande image et texte abondant",
			content: drawImage(0, 0, pageW, pageH*0.7) + drawText(400),
			want:    false,
		},
		{
			// Peu de texte mais image trop petite : rien à signaler.
			name:    "page quasi vide avec petit logo",
			content: drawImage(72, 700, 80, 80) + drawText(20),
			want:    false,
		},
		{
			// Un scan porte souvent un tampon OCR résiduel de quelques mots.
			name:    "scan pleine page avec quelques mots",
			content: drawImage(0, 0, pageW, pageH) + drawText(30),
			want:    true,
		},
		{
			// Le scan est fréquemment découpé en bandes par le pilote du scanner.
			name: "scan en deux bandes",
			content: drawImage(0, 0, pageW, pageH/2) +
				drawImage(0, pageH/2, pageW, pageH/2),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := buildPDF(t, pageObjects([]string{tt.content}))
			got := rasterPagesOf(t, path)
			if tt.want && len(got) == 0 {
				t.Errorf("page scannée non détectée")
			}
			if !tt.want && len(got) > 0 {
				t.Errorf("faux positif : pages %v signalées", got)
			}
		})
	}
}

// TestRasterDetection_MixedDocument : dans un document mixte, seules les pages
// effectivement scannées sont signalées, avec leur numéro.
func TestRasterDetection_MixedDocument(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{
		drawText(400),                                // page 1 : texte
		drawImage(0, 0, pageW, pageH),                // page 2 : scan
		drawImage(0, 0, pageW, pageH),                // page 3 : scan
		drawImage(72, 600, 200, 150) + drawText(400), // page 4 : texte illustré
	}))

	got := rasterPagesOf(t, path)
	want := []int{2, 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pages raster = %v, attendu %v", got, want)
	}
}

// TestGuarantee_RasterPageFailsClosed (P1) : un PDF scanné ne doit jamais être
// déclaré conforme. Sans ce signalement, Walk n'itère sur rien, le rapport est
// vide et le document ressort intact avec un code de sortie nul.
func TestGuarantee_RasterPageFailsClosed(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{drawImage(0, 0, pageW, pageH)}))

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	// Sanity : le pipeline texte n'a effectivement aucune prise sur ce document.
	segments := 0
	if err := w.Walk(func(docprocessor.Segment) error { segments++; return nil }); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if segments != 0 {
		t.Fatalf("le fixture n'est pas un scan pur : %d segment(s)", segments)
	}

	policy := docprocessor.DefaultSanitizePolicy()
	policy.Strict = true

	report, err := docprocessor.Sanitize(w, policy)

	var unsan *docprocessor.ErrUnsanitizedSurface
	if !errors.As(err, &unsan) {
		t.Fatalf("mode strict : erreur attendue sur page scannée, obtenu %v", err)
	}
	if report.OK() {
		t.Error("rapport déclaré conforme malgré une page scannée")
	}
	if !strings.Contains(strings.Join(unsan.Surfaces, " "), "scanné") {
		t.Errorf("surface signalée peu explicite : %v", unsan.Surfaces)
	}
}

// TestGuarantee_TextPDFNotFlagged : non-régression sur le chemin nominal — un
// PDF texte reste traitable en mode strict.
func TestGuarantee_TextPDFNotFlagged(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{drawText(400)}))

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	policy := docprocessor.DefaultSanitizePolicy()
	policy.Strict = true
	if _, err := docprocessor.Sanitize(w, policy); err != nil {
		t.Fatalf("PDF texte refusé en mode strict : %v", err)
	}
}

func TestFormatPageList(t *testing.T) {
	tests := []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{3}, "3"},
		{[]int{1, 2, 3}, "1-3"},
		{[]int{1, 2, 3, 7, 9, 10}, "1-3, 7, 9-10"},
		{[]int{2, 4, 6}, "2, 4, 6"},
	}
	for _, tt := range tests {
		if got := formatPageList(tt.in); got != tt.want {
			t.Errorf("formatPageList(%v) = %q, attendu %q", tt.in, got, tt.want)
		}
	}
}

// ── Phase 1 : mode de rendu texte et cas hybride ──────────────────────────

// drawInvisibleText émet une couche texte non rendue (Tr 3), telle qu'en
// déposent les scanners et les OCR produisant un PDF « searchable ».
func drawInvisibleText(n int) string {
	return fmt.Sprintf("BT /F1 12 Tf 72 700 Td 3 Tr (%s) Tj 0 Tr ET\n",
		strings.Repeat("Dupont", n/6+1))
}

// TestRenderMode_GraphicsState vérifie la sémantique du mode de rendu : il
// appartient à l'état graphique (sauvegardé par q, restauré par Q) et n'est
// PAS réinitialisé par BT.
func TestRenderMode_GraphicsState(t *testing.T) {
	content := []byte(
		"BT 3 Tr (a) Tj ET " + // posé explicitement
			"BT (b) Tj ET " + // BT ne réinitialise pas le mode
			"q 0 Tr BT (c) Tj ET Q " + // rendu visible dans l'état empilé
			"BT (d) Tj ET") // Q a restauré le mode 3

	tokens := extractTextTokens(content, nil)
	if len(tokens) != 4 {
		t.Fatalf("tokens = %d, attendu 4 : %+v", len(tokens), tokens)
	}

	want := map[string]bool{"a": true, "b": true, "c": false, "d": true}
	for _, tok := range tokens {
		if got := tok.isInvisible(); got != want[tok.text] {
			t.Errorf("token %q : invisible = %v, attendu %v (Tr=%d)",
				tok.text, got, want[tok.text], tok.renderMode)
		}
	}
}

// TestInlineImage_DoesNotSwallowText : les octets bruts d'une image en ligne
// doivent être sautés. Une parenthèse ouvrante dans les pixels ferait sinon
// consommer tout le reste du flux comme une chaîne littérale, et le texte
// réel disparaîtrait silencieusement de l'extraction.
func TestInlineImage_DoesNotSwallowText(t *testing.T) {
	content := []byte(
		"BI /W 4 /H 1 /CS /G /BPC 8 ID (((( EI\n" +
			"BT /F1 12 Tf 72 700 Td (TexteVisible) Tj ET")

	tokens := extractTextTokens(content, nil)
	if len(tokens) != 1 || tokens[0].text != "TexteVisible" {
		t.Fatalf("texte suivant une image en ligne perdu : %+v", tokens)
	}
}

// TestHybridPageDetection (P2) : un scan surmonté d'une couche OCR invisible
// est classé hybride, pas raster — c'est le cas dangereux, celui où toutes les
// vérifications textuelles passent au vert alors que les pixels restent lisibles.
func TestHybridPageDetection(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantRaster bool
		wantHybrid bool
	}{
		{
			name:       "scan nu",
			content:    drawImage(0, 0, pageW, pageH),
			wantRaster: true,
		},
		{
			name:       "scan avec couche OCR invisible",
			content:    drawImage(0, 0, pageW, pageH) + drawInvisibleText(400),
			wantHybrid: true,
		},
		{
			// Le texte invisible ne doit pas être compté comme visible : sans le
			// suivi de Tr, cette page passerait pour une page de texte ordinaire.
			name:       "scan avec courte couche OCR",
			content:    drawImage(0, 0, pageW, pageH) + drawInvisibleText(30),
			wantHybrid: true,
		},
		{
			// Image de fond surmontée de vrai texte : rien à signaler.
			name:    "fond de page avec texte visible",
			content: drawImage(0, 0, pageW, pageH) + drawText(400),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := buildPDF(t, pageObjects([]string{tt.content}))
			wr, err := NewWalkerFromFile(path)
			if err != nil {
				t.Fatalf("NewWalkerFromFile: %v", err)
			}
			w := wr.(*Walker)

			if got := len(w.RasterPages()) > 0; got != tt.wantRaster {
				t.Errorf("raster = %v, attendu %v", got, tt.wantRaster)
			}
			if got := len(w.HybridPages()) > 0; got != tt.wantHybrid {
				t.Errorf("hybride = %v, attendu %v", got, tt.wantHybrid)
			}
		})
	}
}

// TestGuarantee_HybridPageFailsClosed (P2) : le mode strict refuse un PDF
// searchable. Sans ce contrôle, le document sortirait avec un texte
// irréprochable et des pixels en clair.
func TestGuarantee_HybridPageFailsClosed(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{
		drawImage(0, 0, pageW, pageH) + drawInvisibleText(400),
	}))

	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	// Sanity : contrairement au scan nu, le pipeline texte « voit » du contenu
	// ici — c'est précisément ce qui rend le cas trompeur.
	segments := 0
	if err := w.Walk(func(docprocessor.Segment) error { segments++; return nil }); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if segments == 0 {
		t.Fatal("le fixture ne porte pas de couche texte")
	}

	policy := docprocessor.DefaultSanitizePolicy()
	policy.Strict = true

	var unsan *docprocessor.ErrUnsanitizedSurface
	if _, err := docprocessor.Sanitize(w, policy); !errors.As(err, &unsan) {
		t.Fatalf("mode strict : erreur attendue sur page hybride, obtenu %v", err)
	}
	if !strings.Contains(strings.Join(unsan.Surfaces, " "), "invisible") {
		t.Errorf("surface signalée peu explicite : %v", unsan.Surfaces)
	}
}

// TestModifiedInvisibleText_Reported : un bloc scanné minoritaire (signature,
// tampon) surmonté de sa couche OCR reste sous le seuil de couverture, mais
// anonymiser ce texte invisible produit la même illusion. Le signalement doit
// donc aussi porter sur ce qui a été effectivement modifié.
func TestModifiedInvisibleText_Reported(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantReport bool
	}{
		{
			name:       "texte invisible modifié",
			content:    drawImage(72, 600, 200, 150) + drawInvisibleText(400),
			wantReport: true,
		},
		{
			name:    "texte visible modifié",
			content: drawImage(72, 600, 200, 150) + drawText(400),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := buildPDF(t, pageObjects([]string{tt.content}))
			wr, err := NewWalkerFromFile(path)
			if err != nil {
				t.Fatalf("NewWalkerFromFile: %v", err)
			}
			w := wr.(*Walker)

			// Le seuil de couverture ne doit pas être atteint : c'est bien la
			// modification, et non la géométrie, qui déclenche le signalement.
			if len(w.HybridPages()) > 0 || len(w.RasterPages()) > 0 {
				t.Fatalf("fixture mal calibré : raster=%v hybride=%v",
					w.RasterPages(), w.HybridPages())
			}

			if err := w.Walk(func(s docprocessor.Segment) error {
				s.Replace("[ANONYME]")
				return nil
			}); err != nil {
				t.Fatalf("Walk: %v", err)
			}

			report, err := w.Sanitize(docprocessor.DefaultSanitizePolicy())
			if err != nil {
				t.Fatalf("Sanitize: %v", err)
			}
			got := strings.Contains(strings.Join(report.Unprocessed, " "), "texte invisible")
			if got != tt.wantReport {
				t.Errorf("signalement = %v, attendu %v (surfaces : %v)",
					got, tt.wantReport, report.Unprocessed)
			}
		})
	}
}

// ── Regroupement en blocs (vue « bloc ») ──────────────────────────────────

// drawLine émet une ligne de texte à l'ordonnée y.
func drawLine(y float64, text string) string {
	return fmt.Sprintf("BT /F1 12 Tf 72 %g Td (%s) Tj ET\n", y, text)
}

// lines assemble des lignes régulièrement espacées à partir de y0.
func lines(y0, leading float64, texts ...string) string {
	var b strings.Builder
	for i, t := range texts {
		b.WriteString(drawLine(y0-float64(i)*leading, t))
	}
	return b.String()
}

func blocksOf(t *testing.T, contents []string) [][]int {
	t.Helper()
	path := buildPDF(t, pageObjects(contents))
	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	return wr.(*Walker).Blocks()
}

func TestBlocks(t *testing.T) {
	tests := []struct {
		name     string
		contents []string
		want     string
	}{
		{
			name:     "interligne régulier : un seul bloc",
			contents: []string{lines(700, 14, "premiere", "deuxieme", "troisieme")},
			want:     "[[0 1 2]]",
		},
		{
			// Rupture verticale franche : fin de paragraphe.
			name: "saut de paragraphe : deux blocs",
			contents: []string{
				lines(700, 14, "premiere", "deuxieme") +
					lines(640, 14, "troisieme", "quatrieme"),
			},
			want: "[[0 1] [2 3]]",
		},
		{
			// Le texte repart vers le haut : changement de colonne. C'est le
			// pire cas — fusionner deux colonnes produirait du charabia.
			name: "remontee : nouvelle colonne",
			contents: []string{
				lines(700, 14, "colonneA1", "colonneA2") +
					lines(700, 14, "colonneB1", "colonneB2"),
			},
			want: "[[0 1] [2 3]]",
		},
		{
			// Un bloc d'une seule ligne n'apporte rien : le segment est de
			// toute façon analysé tel quel.
			name:     "ligne isolee : aucun bloc",
			contents: []string{drawLine(700, "seule")},
			want:     "[]",
		},
		{
			// Un bloc ne traverse jamais une frontière de page.
			name: "pages distinctes : blocs distincts",
			contents: []string{
				lines(700, 14, "pageUnA", "pageUnB"),
				lines(700, 14, "pageDeuxA", "pageDeuxB"),
			},
			want: "[[0 1] [2 3]]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmt.Sprint(blocksOf(t, tt.contents)); got != tt.want {
				t.Errorf("Blocks() = %s, attendu %s", got, tt.want)
			}
		})
	}
}

// TestBlocks_ImplementsBlockWalker verrouille le contrat optionnel : sans lui,
// la vue « bloc » est silencieusement ignorée par docprocessor.
func TestBlocks_ImplementsBlockWalker(t *testing.T) {
	path := buildPDF(t, pageObjects([]string{lines(700, 14, "une", "deux")}))
	wr, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	if _, ok := wr.(docprocessor.BlockWalker); !ok {
		t.Fatal("le Walker PDF devrait implémenter docprocessor.BlockWalker")
	}
}
