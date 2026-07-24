package pdf

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/docprocessor"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcore "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const secretName = "NomTest"

// buildPDFWithInfo génère un PDF de démo dont le dictionnaire Info porte le nom.
func buildPDFWithInfo(t *testing.T) string {
	t.Helper()
	xref, err := pdfcore.CreateDemoXRef()
	if err != nil {
		t.Fatalf("CreateDemoXRef: %v", err)
	}
	// Ajouter une page (sinon l'écriture échoue : « missing indirect obj for pages dict »).
	page := model.Page{MediaBox: types.RectForFormat("A4"), Fm: model.FontMap{}, Buf: new(bytes.Buffer)}
	pdfcore.CreateTestPageContent(page)
	rootDict, err := xref.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if err := pdfcore.AddPageTreeWithSamplePage(xref, rootDict, page); err != nil {
		t.Fatalf("AddPageTreeWithSamplePage: %v", err)
	}

	infoDict := types.Dict(map[string]types.Object{
		"Author":  types.StringLiteral(secretName),
		"Title":   types.StringLiteral(secretName + " rapport"),
		"Creator": types.StringLiteral(secretName),
	})
	ir, err := xref.IndRefForNewObject(infoDict)
	if err != nil {
		t.Fatalf("IndRefForNewObject: %v", err)
	}
	xref.Info = ir

	ctx := pdfcore.CreateContext(xref, nil)
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := pdfapi.WriteContextFile(ctx, path); err != nil {
		t.Fatalf("WriteContextFile: %v", err)
	}
	return path
}

// infoValues retourne les valeurs du dictionnaire Info d'un PDF sur disque.
func infoValues(t *testing.T, path string) string {
	t.Helper()
	ctx, err := pdfapi.ReadContextFile(path)
	if err != nil {
		t.Fatalf("ReadContextFile: %v", err)
	}
	if ctx.Info == nil {
		return ""
	}
	d, err := ctx.DereferenceDict(*ctx.Info)
	if err != nil || d == nil {
		return ""
	}
	return fmt.Sprintf("%v", d)
}

// TestGuarantee_PDFStripsInfoMetadata (6.T2) : après sanitisation, le nom porté
// par le dictionnaire Info ne subsiste dans aucune valeur du PDF de sortie.
func TestGuarantee_PDFStripsInfoMetadata(t *testing.T) {
	in := buildPDFWithInfo(t)

	// Sanity : le nom est bien présent avant sanitisation.
	if before := infoValues(t, in); !strings.Contains(before, secretName) {
		t.Fatalf("le fixture ne porte pas le nom dans Info : %q", before)
	}

	wr, err := NewWalkerFromFile(in)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	w := wr.(*Walker)

	policy := docprocessor.DefaultSanitizePolicy()
	policy.Strict = true
	report, err := docprocessor.Sanitize(w, policy)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if !report.MetadataStripped {
		t.Error("métadonnées non purgées")
	}

	out := filepath.Join(t.TempDir(), "out.pdf")
	if err := w.SaveTo(out); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	if after := infoValues(t, out); strings.Contains(after, secretName) {
		t.Errorf("le nom subsiste dans Info après sanitisation : %q", after)
	}
}
