package docx

import (
	"github.com/bornholm/go-anon/pkg/docprocessor"
	godocx "github.com/gomutex/godocx"
	gdocx "github.com/gomutex/godocx/docx"
	"github.com/gomutex/godocx/wml/ctypes"
)

// Walker implémente docprocessor.Walker pour les fichiers DOCX.
// Chaque paragraphe est exposé comme un Segment ; le texte anonymisé
// est redistribué en écrasant le premier run et en vidant les suivants.
type Walker struct {
	rootDoc *gdocx.RootDoc
}

func NewWalker(rootDoc *gdocx.RootDoc) *Walker {
	return &Walker{rootDoc: rootDoc}
}

func NewWalkerFromFile(path string) (docprocessor.Walker, error) {
	rd, err := godocx.OpenDocument(path)
	if err != nil {
		return nil, err
	}
	return NewWalker(rd), nil
}

// Walk itère sur tous les paragraphes du document et appelle fn pour chacun.
func (w *Walker) Walk(fn func(docprocessor.Segment) error) error {
	if w.rootDoc.Document == nil || w.rootDoc.Document.Body == nil {
		return nil
	}
	for _, child := range w.rootDoc.Document.Body.Children {
		if child.Para == nil {
			continue
		}
		if err := w.walkParagraph(child.Para.GetCT(), fn); err != nil {
			return err
		}
	}
	return nil
}

type runRef struct {
	pIdx int // index dans ct.Children
	rIdx int // index dans Run.Children
}

func (w *Walker) walkParagraph(ct *ctypes.Paragraph, fn func(docprocessor.Segment) error) error {
	var refs []runRef
	var text string

	for pi, pChild := range ct.Children {
		if pChild.Run == nil {
			continue
		}
		for ri, rChild := range pChild.Run.Children {
			if rChild.Text == nil {
				continue
			}
			refs = append(refs, runRef{pi, ri})
			text += rChild.Text.Text
		}
	}

	if text == "" {
		return nil
	}

	seg := docprocessor.Segment{
		Text: text,
		Replace: func(anonymized string) {
			if len(refs) == 0 {
				return
			}
			// Écrire le texte anonymisé dans le premier run, vider les suivants.
			first := refs[0]
			ct.Children[first.pIdx].Run.Children[first.rIdx].Text = ctypes.TextFromString(anonymized)
			for _, ref := range refs[1:] {
				ct.Children[ref.pIdx].Run.Children[ref.rIdx].Text = ctypes.TextFromString("")
			}
		},
	}
	return fn(seg)
}

// SaveTo sauvegarde le document modifié vers le chemin indiqué.
func (w *Walker) SaveTo(outputPath string) error {
	return w.rootDoc.SaveTo(outputPath)
}
