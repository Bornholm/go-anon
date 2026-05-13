package odt

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// nodeKind distingue les types de nœuds de l'arbre XML.
type nodeKind int

const (
	kindElement  nodeKind = iota
	kindText              // char-data modifiable en-place
	kindProcInst          // <?target inst?>
	kindComment           // <!--...-->
)

// xmlNode est un nœud générique de l'arbre XML.
// Pour kindText, seul le champ text est utilisé (rawName vide).
// Pour kindElement, rawName contient le nom avec préfixe ("text:p").
type xmlNode struct {
	kind     nodeKind
	rawName  string     // nom avec préfixe namespace, ex: "text:p"
	attrs    []xml.Attr // attributs de l'élément (inclut les xmlns:...)
	children []*xmlNode
	text     string // char-data, instruction PI, ou contenu commentaire
}

// Walker implémente docprocessor.Walker pour les fichiers ODT.
// L'arbre XML de content.xml est modifié en-place lors du Replace ;
// SaveTo recrée le fichier ZIP avec le content.xml mis à jour.
type Walker struct {
	contentXML *xmlNode
	otherFiles map[string]zipEntry // tous les fichiers ZIP hors content.xml
}

type zipEntry struct {
	data   []byte
	method uint16
}

// NewWalkerFromFile ouvre un fichier ODT et retourne un Walker prêt à l'emploi.
func NewWalkerFromFile(path string) (docprocessor.Walker, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("ouverture ODT %q : %w", path, err)
	}
	defer zr.Close()

	w := &Walker{
		otherFiles: make(map[string]zipEntry),
	}

	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("lecture entrée %s : %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("lecture entrée %s : %w", f.Name, err)
		}

		if f.Name == "content.xml" {
			n, err := parseXML(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("parsing content.xml : %w", err)
			}
			w.contentXML = n
		} else {
			w.otherFiles[f.Name] = zipEntry{data: data, method: f.Method}
		}
	}

	if w.contentXML == nil {
		return nil, fmt.Errorf("content.xml absent du fichier ODT")
	}
	return w, nil
}

// Walk itère sur tous les paragraphes (text:p) et titres (text:h) du document.
func (w *Walker) Walk(fn func(docprocessor.Segment) error) error {
	return walkParagraphs(w.contentXML, fn)
}

// SaveTo recrée le fichier ODT avec le content.xml modifié.
// Utilise CreateRaw avec CRC32 et tailles pré-calculés pour éviter les
// data descriptors (flag bit 3), rejetés par LibreOffice.
func (w *Walker) SaveTo(outputPath string) error {
	var xmlBuf bytes.Buffer
	if err := serializeXML(&xmlBuf, w.contentXML); err != nil {
		return fmt.Errorf("sérialisation content.xml : %w", err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	// mimetype en premier, Store, pas de data descriptor (exigence ODF 1.2 §2.2.1)
	if mt, ok := w.otherFiles["mimetype"]; ok {
		if err := writeZipEntry(zw, "mimetype", zip.Store, mt.data); err != nil {
			return err
		}
	}

	// content.xml avec le texte anonymisé
	if err := writeZipEntry(zw, "content.xml", zip.Deflate, xmlBuf.Bytes()); err != nil {
		return err
	}

	// reste des fichiers
	for name, entry := range w.otherFiles {
		if name == "mimetype" {
			continue
		}
		if err := writeZipEntry(zw, name, entry.method, entry.data); err != nil {
			return fmt.Errorf("écrire %s : %w", name, err)
		}
	}
	return nil
}

// writeZipEntry écrit une entrée ZIP sans data descriptor.
// Pour Deflate, compresse dans un buffer pour connaître la taille à l'avance.
// Pour Store, écrit directement avec CRC32 et taille pré-calculés.
func writeZipEntry(zw *zip.Writer, name string, method uint16, data []byte) error {
	var payload []byte
	if method == zip.Deflate && len(data) > 0 {
		var buf bytes.Buffer
		fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			return err
		}
		if _, err := fw.Write(data); err != nil {
			return err
		}
		if err := fw.Close(); err != nil {
			return err
		}
		payload = buf.Bytes()
	} else {
		payload = data
	}

	fh := &zip.FileHeader{
		Name:               name,
		Method:             method,
		Flags:              0,
		CompressedSize64:   uint64(len(payload)),
		UncompressedSize64: uint64(len(data)),
		CRC32:              crc32.ChecksumIEEE(data),
	}
	w, err := zw.CreateRaw(fh)
	if err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// --- parsing XML ---

func parseXML(r io.Reader) (*xmlNode, error) {
	dec := xml.NewDecoder(r)
	root := &xmlNode{kind: kindElement, rawName: "__root__"}
	if err := parseChildren(dec, root); err != nil && err != io.EOF {
		return nil, err
	}
	return root, nil
}

func parseChildren(dec *xml.Decoder, parent *xmlNode) error {
	for {
		tok, err := dec.RawToken()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child := &xmlNode{
				kind:    kindElement,
				rawName: xmlRawName(t.Name),
				attrs:   append([]xml.Attr(nil), t.Attr...),
			}
			if err := parseChildren(dec, child); err != nil && err != io.EOF {
				return err
			}
			parent.children = append(parent.children, child)
		case xml.EndElement:
			return nil
		case xml.CharData:
			if len(t) > 0 {
				parent.children = append(parent.children, &xmlNode{
					kind: kindText,
					text: string(t),
				})
			}
		case xml.ProcInst:
			parent.children = append(parent.children, &xmlNode{
				kind:    kindProcInst,
				rawName: t.Target,
				text:    string(t.Inst),
			})
		case xml.Comment:
			parent.children = append(parent.children, &xmlNode{
				kind: kindComment,
				text: string(t),
			})
		}
	}
}

// xmlRawName reconstruit le nom qualifié avec préfixe depuis xml.Name.
// RawToken() met le préfixe dans Space et le nom local dans Local.
func xmlRawName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// --- sérialisation XML ---

func serializeXML(w io.Writer, n *xmlNode) error {
	switch n.kind {
	case kindProcInst:
		if n.text != "" {
			_, err := fmt.Fprintf(w, "<?%s %s?>", n.rawName, n.text)
			return err
		}
		_, err := fmt.Fprintf(w, "<?%s?>", n.rawName)
		return err
	case kindComment:
		_, err := fmt.Fprintf(w, "<!--%s-->", n.text)
		return err
	case kindText:
		return writeEscapedText(w, n.text)
	case kindElement:
		if n.rawName == "__root__" {
			for _, child := range n.children {
				if err := serializeXML(w, child); err != nil {
					return err
				}
			}
			return nil
		}
		if _, err := fmt.Fprintf(w, "<%s", n.rawName); err != nil {
			return err
		}
		for _, attr := range n.attrs {
			if err := writeAttr(w, xmlRawName(attr.Name), attr.Value); err != nil {
				return err
			}
		}
		if len(n.children) == 0 {
			_, err := fmt.Fprint(w, "/>")
			return err
		}
		if _, err := fmt.Fprint(w, ">"); err != nil {
			return err
		}
		for _, child := range n.children {
			if err := serializeXML(w, child); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(w, "</%s>", n.rawName)
		return err
	}
	return nil
}

// --- parcours des paragraphes ---

// walkParagraphs traverse récursivement l'arbre XML et appelle fn
// pour chaque nœud text:p ou text:h (paragraphes et titres ODT).
// Gère les paragraphes imbriqués (cellules de tableaux, cadres, etc.).
func walkParagraphs(n *xmlNode, fn func(docprocessor.Segment) error) error {
	if n.kind != kindElement {
		return nil
	}
	local := xmlLocalName(n.rawName)
	if local == "p" || local == "h" {
		return walkParagraph(n, fn)
	}
	for _, child := range n.children {
		if err := walkParagraphs(child, fn); err != nil {
			return err
		}
	}
	return nil
}

// walkParagraph crée un Segment par nœud texte non-vide dans le paragraphe.
// Chaque remplacement est appliqué directement sur le nœud d'origine,
// ce qui préserve la position des spans et leur mise en forme.
func walkParagraph(n *xmlNode, fn func(docprocessor.Segment) error) error {
	var refs []*xmlNode
	collectTextNodes(n, &refs)

	for _, ref := range refs {
		localRef := ref
		seg := docprocessor.Segment{
			Text: localRef.text,
			Replace: func(anonymized string) {
				localRef.text = anonymized
			},
		}
		if err := fn(seg); err != nil {
			return err
		}
	}
	return nil
}

// collectTextNodes collecte récursivement tous les nœuds kindText non-vides.
func collectTextNodes(n *xmlNode, refs *[]*xmlNode) {
	if n.kind == kindText && n.text != "" {
		*refs = append(*refs, n)
		return
	}
	for _, child := range n.children {
		collectTextNodes(child, refs)
	}
}

// writeAttr écrit un attribut XML : name="value" avec échappement XML correct.
// Encode &, <, >, " et ' (→ &apos;) pour être conforme à LibreOffice.
func writeAttr(w io.Writer, name, value string) error {
	if _, err := fmt.Fprintf(w, ` %s="`, name); err != nil {
		return err
	}
	b := []byte(value)
	last := 0
	for i := 0; i < len(b); i++ {
		var repl string
		switch b[i] {
		case '&':
			repl = "&amp;"
		case '<':
			repl = "&lt;"
		case '>':
			repl = "&gt;"
		case '"':
			repl = "&quot;"
		case '\'':
			repl = "&apos;"
		default:
			continue
		}
		if _, err := w.Write(b[last:i]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, repl); err != nil {
			return err
		}
		last = i + 1
	}
	if _, err := w.Write(b[last:]); err != nil {
		return err
	}
	_, err := io.WriteString(w, `"`)
	return err
}

// writeEscapedText écrit s dans w en n'échappant que &, < et >.
// Contrairement à xml.EscapeText, les sauts de ligne et retours chariot
// sont écrits littéralement pour préserver le contenu original du document.
func writeEscapedText(w io.Writer, s string) error {
	b := []byte(s)
	last := 0
	for i := 0; i < len(b); i++ {
		var repl string
		switch b[i] {
		case '&':
			repl = "&amp;"
		case '<':
			repl = "&lt;"
		case '>':
			repl = "&gt;"
		default:
			continue
		}
		if _, err := w.Write(b[last:i]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, repl); err != nil {
			return err
		}
		last = i + 1
	}
	_, err := w.Write(b[last:])
	return err
}

// xmlLocalName retourne la partie locale d'un nom qualifié ("text:p" → "p").
func xmlLocalName(rawName string) string {
	if i := strings.LastIndex(rawName, ":"); i >= 0 {
		return rawName[i+1:]
	}
	return rawName
}
