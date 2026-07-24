package docx

import (
	"archive/zip"
	"io"
	"strings"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// Surfaces cachées d'un DOCX traitées ici :
//   - docProps/core.xml, app.xml, custom.xml : auteur, société, dernier
//     modificateur, révision… → purgées ;
//   - word/comments.xml : texte et auteur des commentaires → supprimés ;
//   - word/document.xml : révisions (w:ins/w:del) → détectées. godocx ne sait
//     pas les accepter de façon garantie, donc en mode strict leur présence est
//     une erreur plutôt qu'un pari silencieux.
//
// core.xml / app.xml / custom.xml et comments.xml vivent dans rootDoc.FileMap
// (godocx les recopie verbatim à la sauvegarde) : on les réécrit en place.
// document.xml est reconstruit par godocx à partir de son modèle ; pour détecter
// les révisions on relit donc les octets d'origine depuis le fichier source.

const (
	partCore     = "docProps/core.xml"
	partApp      = "docProps/app.xml"
	partCustom   = "docProps/custom.xml"
	partComments = "word/comments.xml"
	partDocument = "word/document.xml"
)

// emptyCore est un core.xml minimal valide, sans aucun champ identifiant.
const emptyCore = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<cp:coreProperties ` +
	`xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
	`xmlns:dcterms="http://purl.org/dc/terms/" ` +
	`xmlns:dcmitype="http://purl.org/dc/dcmitype/" ` +
	`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></cp:coreProperties>`

// emptyApp est un app.xml minimal valide, sans société ni auteur.
const emptyApp = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Properties ` +
	`xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" ` +
	`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"></Properties>`

// emptyCustom est un custom.xml minimal valide, sans propriété.
const emptyCustom = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Properties ` +
	`xmlns="http://schemas.openxmlformats.org/officeDocument/2006/custom-properties" ` +
	`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"></Properties>`

// emptyComments est un comments.xml vide : les anchors résiduels dans le corps
// deviennent orphelins (inoffensifs), mais aucun texte ni auteur de commentaire
// ne subsiste.
const emptyComments = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"></w:comments>`

// Sanitize purge les surfaces non textuelles du DOCX selon policy.
func (w *Walker) Sanitize(policy docprocessor.SanitizePolicy) (docprocessor.SanitizeReport, error) {
	var report docprocessor.SanitizeReport

	if policy.StripMetadata {
		w.replacePartIfPresent(partCore, emptyCore)
		w.replacePartIfPresent(partApp, emptyApp)
		w.replacePartIfPresent(partCustom, emptyCustom)
		report.MetadataStripped = true
	}

	// Commentaires : compte dans les octets bruts, puis suppression (le modèle
	// godocx ne les expose pas pour anonymisation → politique = supprimer).
	if raw, ok := w.loadPart(partComments); ok {
		report.CommentsFound = strings.Count(string(raw), "<w:comment ")
		if report.CommentsFound > 0 {
			if policy.ProcessComments {
				// L'anonymisation ciblée des commentaires n'est pas encore gérée
				// pour DOCX ; on le signale plutôt que de laisser passer.
				report.Unprocessed = append(report.Unprocessed, "commentaires (anonymisation non gérée)")
			} else {
				w.storePart(partComments, []byte(emptyComments))
			}
		}
	}

	// Révisions (track changes) : détectées dans le document.xml d'origine.
	if n := w.countRevisions(); n > 0 {
		report.RevisionsFound = n
		// godocx ne garantit pas l'acceptation des révisions : le texte supprimé
		// (w:delText) pourrait survivre ou disparaître de façon non maîtrisée.
		// On ne peut donc pas honorer AcceptRevisions → surface non traitée.
		report.Unprocessed = append(report.Unprocessed,
			"révisions DOCX (acceptation non garantie)")
	}

	return report, nil
}

// replacePartIfPresent remplace le contenu d'une partie ZIP si elle existe.
func (w *Walker) replacePartIfPresent(name, content string) {
	if _, ok := w.loadPart(name); ok {
		w.storePart(name, []byte(content))
	}
}

// loadPart lit une partie depuis la FileMap de godocx.
func (w *Walker) loadPart(name string) ([]byte, bool) {
	v, ok := w.rootDoc.FileMap.Load(name)
	if !ok {
		return nil, false
	}
	b, ok := v.([]byte)
	return b, ok
}

// storePart écrit une partie dans la FileMap (recopiée verbatim à la sauvegarde).
func (w *Walker) storePart(name string, content []byte) {
	w.rootDoc.FileMap.Store(name, content)
}

// countRevisions relit word/document.xml dans le fichier source et compte les
// balises d'insertion et de suppression suivies (track changes). Retourne 0 si
// le fichier source n'est pas disponible (Walker construit depuis un RootDoc).
func (w *Walker) countRevisions() int {
	if w.srcPath == "" {
		return 0
	}
	raw, err := readZipPart(w.srcPath, partDocument)
	if err != nil {
		return 0
	}
	s := string(raw)
	return strings.Count(s, "<w:ins ") + strings.Count(s, "<w:del ")
}

// readZipPart lit une entrée nommée depuis une archive ZIP.
func readZipPart(path, name string) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.ReplaceAll(f.Name, "\\", "/") == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, io.EOF
}
