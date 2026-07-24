package odt

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// elem construit un nœud élément ODT avec le nom qualifié donné.
func elem(rawName string, children ...*xmlNode) *xmlNode {
	return &xmlNode{kind: kindElement, rawName: rawName, children: children}
}

func text(s string) *xmlNode { return &xmlNode{kind: kindText, text: s} }

// buildODTTree fabrique un content.xml minimal avec un paragraphe, un
// commentaire (office:annotation) et une révision (text:tracked-changes).
func buildODTTree() *xmlNode {
	return elem("__root__",
		elem("office:document-content",
			elem("office:body",
				elem("office:text",
					elem("text:tracked-changes",
						elem("text:changed-region",
							elem("text:deletion",
								elem("text:p", text("NomTest supprimé")))),
					),
					elem("text:p",
						text("Bonjour "),
						elem("office:annotation",
							elem("dc:creator", text("NomTest")),
							elem("text:p", text("un commentaire de NomTest"))),
						text("le monde"),
						elem("text:change"), // jalon inline
					),
				),
			),
		),
	)
}

func newTestWalker() *Walker {
	return &Walker{
		contentXML: buildODTTree(),
		otherFiles: map[string]zipEntry{
			"meta.xml": {data: []byte(`<office:document-meta><office:meta><dc:creator>NomTest</dc:creator></office:meta></office:document-meta>`)},
		},
	}
}

func serialize(n *xmlNode) string {
	var sb strings.Builder
	_ = serializeXML(&sb, n)
	return sb.String()
}

func TestODTSanitize_RemovesCommentsRevisionsAndMetadata(t *testing.T) {
	w := newTestWalker()

	report, err := w.Sanitize(docprocessor.DefaultSanitizePolicy())
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if !report.MetadataStripped {
		t.Error("meta.xml non purgé")
	}
	if report.CommentsFound == 0 {
		t.Error("commentaire non détecté")
	}
	if report.RevisionsFound == 0 {
		t.Error("révision non détectée")
	}
	if !report.OK() {
		t.Errorf("surfaces non traitées inattendues : %v", report.Unprocessed)
	}

	// Le contenu ne doit plus porter le nom (commentaire + texte supprimé retirés).
	if got := serialize(w.contentXML); strings.Contains(got, "NomTest") {
		t.Errorf("le nom subsiste dans content.xml : %s", got)
	}
	// meta.xml purgé.
	if got := string(w.otherFiles["meta.xml"].data); strings.Contains(got, "NomTest") {
		t.Errorf("le nom subsiste dans meta.xml : %s", got)
	}
	// Le texte légitime reste.
	if got := serialize(w.contentXML); !strings.Contains(got, "Bonjour") || !strings.Contains(got, "le monde") {
		t.Errorf("le texte légitime a été altéré : %s", got)
	}
}

// TestODTSanitize_StrictRefusesUnhandledComments : avec ProcessComments actif
// (anonymisation ciblée non gérée), le mode strict refuse.
func TestODTSanitize_StrictRefusesUnhandledComments(t *testing.T) {
	w := newTestWalker()
	policy := docprocessor.DefaultSanitizePolicy()
	policy.ProcessComments = true // demande l'anonymisation, non gérée → Unprocessed
	policy.Strict = true

	_, err := docprocessor.Sanitize(w, policy)
	var unproc *docprocessor.ErrUnsanitizedSurface
	if err == nil {
		t.Fatal("attendu une erreur en mode strict avec commentaires non anonymisables")
	}
	_ = unproc
}
