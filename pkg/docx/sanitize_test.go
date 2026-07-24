package docx

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

const secretName = "NomTest"

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
	`<Override PartName="/word/comments.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"/>` +
	`</Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`</Relationships>`

const docRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rIdC" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/>` +
	`</Relationships>`

const coreXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/">` +
	`<dc:creator>` + secretName + `</dc:creator>` +
	`<cp:lastModifiedBy>` + secretName + `</cp:lastModifiedBy>` +
	`</cp:coreProperties>`

const commentsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:comment w:id="1" w:author="` + secretName + `" w:date="2020-01-01T00:00:00Z">` +
	`<w:p><w:r><w:t>un commentaire de ` + secretName + `</w:t></w:r></w:p></w:comment>` +
	`</w:comments>`

const revisionBlock = `<w:p><w:ins w:id="9" w:author="` + secretName + `" w:date="2020-01-01T00:00:00Z">` +
	`<w:r><w:t>texte insere</w:t></w:r></w:ins></w:p>`

func documentXML(withRevision bool) string {
	rev := ""
	if withRevision {
		rev = revisionBlock
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body><w:p><w:r><w:t>Bonjour tout le monde</w:t></w:r></w:p>` + rev + `</w:body></w:document>`
}

// buildDOCX écrit un DOCX minimal (mais ouvrable par godocx) dans un fichier
// temporaire et retourne son chemin.
func buildDOCX(t *testing.T, withRevision bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "in.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	parts := map[string]string{
		"[Content_Types].xml":          contentTypesXML,
		"_rels/.rels":                  rootRels,
		"word/document.xml":            documentXML(withRevision),
		"word/_rels/document.xml.rels": docRels,
		"docProps/core.xml":            coreXML,
		"word/comments.xml":            commentsXML,
	}
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func openWalker(t *testing.T, path string) *Walker {
	t.Helper()
	w, err := NewWalkerFromFile(path)
	if err != nil {
		t.Fatalf("NewWalkerFromFile: %v", err)
	}
	return w.(*Walker)
}

func TestDOCXSanitize_StripsMetadataAndComments(t *testing.T) {
	w := openWalker(t, buildDOCX(t, false))

	report, err := w.Sanitize(docprocessor.DefaultSanitizePolicy())
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if !report.MetadataStripped {
		t.Error("métadonnées non purgées")
	}
	if report.CommentsFound == 0 {
		t.Error("commentaire non détecté")
	}

	// Les parties portant le nom ne doivent plus le contenir.
	if core, ok := w.loadPart(partCore); ok && strings.Contains(string(core), secretName) {
		t.Error("core.xml contient encore le nom")
	}
	if com, ok := w.loadPart(partComments); ok && strings.Contains(string(com), secretName) {
		t.Error("comments.xml contient encore le nom")
	}
}

// TestGuarantee_DOCXStrictOutputHasNoHiddenName (6.T1) : en mode strict, un
// document sans révision produit une sortie où le nom n'apparaît dans AUCUNE
// partie du package OOXML.
func TestGuarantee_DOCXStrictOutputHasNoHiddenName(t *testing.T) {
	w := openWalker(t, buildDOCX(t, false))

	policy := docprocessor.DefaultSanitizePolicy()
	policy.Strict = true
	if _, err := docprocessor.Sanitize(w, policy); err != nil {
		t.Fatalf("sanitize strict: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.docx")
	if err := w.SaveTo(out); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	for name, content := range readAllParts(t, out) {
		if strings.Contains(content, secretName) {
			t.Errorf("le nom subsiste dans %s", name)
		}
	}
}

// TestGuarantee_DOCXStrictRefusesRevisions (6.T1, branche « erreur ») : un
// document avec révisions ne peut pas être garanti propre → le mode strict
// échoue au lieu de produire une sortie.
func TestGuarantee_DOCXStrictRefusesRevisions(t *testing.T) {
	w := openWalker(t, buildDOCX(t, true))

	policy := docprocessor.DefaultSanitizePolicy()
	policy.Strict = true
	_, err := docprocessor.Sanitize(w, policy)

	var unproc *docprocessor.ErrUnsanitizedSurface
	if !errors.As(err, &unproc) {
		t.Fatalf("attendu ErrUnsanitizedSurface pour un document à révisions, got %v", err)
	}
}

func readAllParts(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := make(map[string]string)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := rc.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		rc.Close()
		out[f.Name] = sb.String()
	}
	return out
}
