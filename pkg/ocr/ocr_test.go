package ocr

import (
	"fmt"
	"strings"
	"testing"
)

// row assemble une ligne TSV. Les tabulations sont construites plutôt
// qu'écrites en dur : un espace glissé à leur place rendrait le fixture faux
// sans que rien ne le montre à la lecture.
func row(fields ...string) string { return strings.Join(fields, "\t") }

// tesseractOutput reproduit une sortie réelle de tesseract 5.5 : en-tête,
// lignes de hiérarchie (niveaux 1 à 4, sans texte, confiance -1), puis les mots
// au niveau 5.
func tesseractOutput() string {
	return strings.Join([]string{
		row("level", "page_num", "block_num", "par_num", "line_num", "word_num",
			"left", "top", "width", "height", "conf", "text"),
		row("1", "1", "0", "0", "0", "0", "0", "0", "1250", "500", "-1", ""),
		row("2", "1", "1", "0", "0", "0", "85", "165", "657", "110", "-1", ""),
		row("3", "1", "1", "1", "0", "0", "85", "165", "657", "110", "-1", ""),
		row("4", "1", "1", "1", "1", "0", "85", "165", "657", "110", "-1", ""),
		row("5", "1", "1", "1", "1", "1", "85", "165", "243", "88", "92.668922", "Jean"),
		row("5", "1", "1", "1", "1", "2", "378", "165", "364", "110", "90.992790", "Dupont"),
		row("4", "1", "1", "1", "2", "0", "85", "300", "400", "90", "-1", ""),
		row("5", "1", "1", "1", "2", "1", "85", "300", "400", "90", "88.5", "directeur"),
	}, "\n")
}

func TestParseTesseractTSV(t *testing.T) {
	words, err := ParseTesseractTSV(strings.NewReader(tesseractOutput()))
	if err != nil {
		t.Fatalf("ParseTesseractTSV: %v", err)
	}

	// Seuls les mots (niveau 5) sont retenus : les niveaux supérieurs décrivent
	// la hiérarchie et ne portent pas de texte.
	if len(words) != 3 {
		t.Fatalf("mots = %d, attendu 3 : %+v", len(words), words)
	}

	if got := words[0]; got.Text != "Jean" || got.BBox != (Rect{85, 165, 243, 88}) {
		t.Errorf("premier mot = %+v", got)
	}
	if got := words[0].Confidence; got < 0.92 || got > 0.93 {
		t.Errorf("confiance = %v, attendue ramenée sur 0–1", got)
	}

	// Les mots d'une même ligne partagent un identifiant, ceux d'une autre non.
	if words[0].Line != words[1].Line {
		t.Error("« Jean » et « Dupont » devraient partager la même ligne")
	}
	if words[2].Line == words[0].Line {
		t.Error("« directeur » est sur une autre ligne")
	}
}

// TestParseTesseractTSV_UnbalancedQuotes : tesseract n'échappe pas le champ
// texte. Un guillemet isolé ne doit pas faire échouer la lecture de la page.
func TestParseTesseractTSV_UnbalancedQuotes(t *testing.T) {
	input := strings.Join([]string{
		row("level", "page_num", "block_num", "par_num", "line_num", "word_num",
			"left", "top", "width", "height", "conf", "text"),
		row("5", "1", "1", "1", "1", "1", "10", "10", "50", "20", "80", `"Jean`),
		row("5", "1", "1", "1", "1", "2", "70", "10", "50", "20", "80", `l'"autre`),
	}, "\n")

	words, err := ParseTesseractTSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseTesseractTSV: %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("mots = %d, attendu 2 : %+v", len(words), words)
	}
}

// TestParseTesseractTSV_NegativeConfidence : une confiance non mesurée (-1) ne
// doit pas se propager, sous peine de fausser toute comparaison au seuil.
func TestParseTesseractTSV_NegativeConfidence(t *testing.T) {
	input := row("5", "1", "1", "1", "1", "1", "10", "10", "50", "20", "-1", "mot")

	words, err := ParseTesseractTSV(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseTesseractTSV: %v", err)
	}
	if len(words) != 1 || words[0].Confidence != 0 {
		t.Fatalf("confiance = %+v, attendue 0", words)
	}
}

func TestLines(t *testing.T) {
	words, err := ParseTesseractTSV(strings.NewReader(tesseractOutput()))
	if err != nil {
		t.Fatalf("ParseTesseractTSV: %v", err)
	}

	lines := Lines(words)
	if len(lines) != 2 {
		t.Fatalf("lignes = %d, attendu 2", len(lines))
	}
	if got, want := lines[0].Text, "Jean Dupont"; got != want {
		t.Errorf("ligne 0 = %q, attendu %q", got, want)
	}

	// La boîte de la ligne englobe celles de ses mots.
	if got, want := lines[0].BBox, (Rect{85, 165, 657, 110}); got != want {
		t.Errorf("bbox ligne = %+v, attendu %+v", got, want)
	}

	// Les spans doivent redécouper exactement le texte de la ligne.
	for i, span := range lines[0].Spans {
		if got := lines[0].Text[span[0]:span[1]]; got != lines[0].Words[i].Text {
			t.Errorf("span %d = %q, attendu %q", i, got, lines[0].Words[i].Text)
		}
	}
}

// TestLines_OrdersWordsHorizontally : un moteur peut rendre les mots dans un
// autre ordre que celui de lecture. Un mot mal placé décalerait toutes les
// correspondances offset → boîte.
func TestLines_OrdersWordsHorizontally(t *testing.T) {
	lines := Lines([]Word{
		{Text: "Dupont", BBox: Rect{X: 378, Y: 165, W: 364, H: 110}, Line: 0},
		{Text: "Jean", BBox: Rect{X: 85, Y: 165, W: 243, H: 88}, Line: 0},
	})
	if len(lines) != 1 || lines[0].Text != "Jean Dupont" {
		t.Fatalf("ligne = %+v, attendu « Jean Dupont »", lines)
	}
}

// TestBoxesFor est le pont entre offsets et pixels : c'est lui qui dira quelles
// zones noircir. Une erreur ici laisserait des glyphes dépasser du caviardage.
func TestBoxesFor(t *testing.T) {
	line := Lines([]Word{
		{Text: "Jean", BBox: Rect{85, 165, 243, 88}, Line: 0},
		{Text: "Dupont", BBox: Rect{378, 165, 364, 110}, Line: 0},
		{Text: "habite", BBox: Rect{760, 165, 300, 90}, Line: 0},
	})[0]

	tests := []struct {
		name       string
		start, end int
		want       string
	}{
		{"premier mot seul", 0, 4, "[{85 165 243 88}]"},
		{"second mot seul", 5, 11, "[{378 165 364 110}]"},
		{"entité à cheval sur deux mots", 0, 11, "[{85 165 243 88} {378 165 364 110}]"},
		{"fragment intérieur d'un mot", 6, 8, "[{378 165 364 110}]"},
		{"hors de toute portée", 30, 40, "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmt.Sprint(line.BoxesFor(tt.start, tt.end)); got != tt.want {
				t.Errorf("= %s, attendu %s", got, tt.want)
			}
		})
	}
}

func TestRectUnion(t *testing.T) {
	a := Rect{10, 10, 20, 20}
	b := Rect{40, 5, 10, 10}

	if got, want := a.Union(b), (Rect{10, 5, 40, 25}); got != want {
		t.Errorf("union = %+v, attendu %+v", got, want)
	}
	// Le rectangle vide est neutre : permet d'agréger sans cas particulier.
	if got := (Rect{}).Union(a); got != a {
		t.Errorf("union avec vide = %+v, attendu %+v", got, a)
	}
	if got := a.Union(Rect{}); got != a {
		t.Errorf("union avec vide = %+v, attendu %+v", got, a)
	}
}

func TestTesseractLang(t *testing.T) {
	cases := map[string]string{
		"fr": "fra", "en": "eng", "es": "spa", "": "eng",
		"fra+eng": "fra+eng", // jeu personnalisé rendu tel quel
	}
	for in, want := range cases {
		if got := TesseractLang(in); got != want {
			t.Errorf("TesseractLang(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestFilterConfidence(t *testing.T) {
	words := []Word{
		{Text: "sûr", Confidence: 0.9},
		{Text: "bruit", Confidence: 0.1},
	}
	if got := FilterConfidence(words, 0.5); len(got) != 1 || got[0].Text != "sûr" {
		t.Errorf("filtrage = %+v", got)
	}
	// Seuil nul : aucun filtrage, la slice est rendue telle quelle.
	if got := FilterConfidence(words, 0); len(got) != 2 {
		t.Errorf("seuil nul devrait tout conserver : %+v", got)
	}
}
