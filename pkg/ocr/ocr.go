// Package ocr définit l'abstraction de reconnaissance optique utilisée pour
// atteindre le contenu qu'aucune couche texte ne décrit : pages scannées,
// encarts bitmap, tampons, signatures.
//
// Le contrat est volontairement minimal — une image, une langue, des mots
// positionnés — pour que plusieurs moteurs puissent l'implémenter sans
// contraindre l'appelant. La granularité **mot avec boîte englobante** est le
// minimum utile : une ligne entière ne permet pas de caviarder une entité en
// milieu de ligne, ce dont dépend le caviardage pixel.
package ocr

import (
	"image"
	"sort"
	"strings"
)

// Rect est une boîte en pixels dans l'image source, origine en haut à gauche
// (convention des moteurs OCR, inverse de l'espace PDF).
type Rect struct {
	X, Y, W, H int
}

// Right et Bottom bornent le rectangle.
func (r Rect) Right() int  { return r.X + r.W }
func (r Rect) Bottom() int { return r.Y + r.H }

// Union retourne le plus petit rectangle contenant r et o. Un rectangle de
// surface nulle est neutre, ce qui permet d'agréger sans cas particulier.
func (r Rect) Union(o Rect) Rect {
	if r.W == 0 || r.H == 0 {
		return o
	}
	if o.W == 0 || o.H == 0 {
		return r
	}
	x, y := min(r.X, o.X), min(r.Y, o.Y)
	right, bottom := max(r.Right(), o.Right()), max(r.Bottom(), o.Bottom())
	return Rect{X: x, Y: y, W: right - x, H: bottom - y}
}

// Word est un mot reconnu, avec sa position et la confiance du moteur.
type Word struct {
	Text       string
	BBox       Rect
	Confidence float64 // 0–1
	// Line identifie la ligne d'appartenance. Les moteurs raisonnent en
	// (bloc, paragraphe, ligne) ; on aplatit en un identifiant monotone, seul
	// l'ordre et l'égalité important pour reconstituer les lignes.
	Line int
}

// Engine est un moteur de reconnaissance optique.
type Engine interface {
	// Name identifie le moteur dans les rapports et les messages d'erreur.
	Name() string
	// Available rapporte si le moteur est utilisable ici — binaire présent,
	// bibliothèque chargée. Retourne nil quand il l'est.
	//
	// L'appelant doit vérifier au démarrage et échouer explicitement si aucun
	// moteur n'est disponible alors qu'une page bitmap est rencontrée :
	// dégrader en silence recréerait le fail-open que ce chantier corrige.
	Available() error
	// Recognize reconnaît le texte de img. lang est un code ISO 639-1
	// (« fr », « en », « es ») ; au moteur de le traduire dans sa propre
	// nomenclature.
	Recognize(img image.Image, lang string) ([]Word, error)
}

// Line est une ligne reconstituée à partir des mots d'un moteur.
type Line struct {
	Text  string
	Words []Word
	// Spans[i] borne le mot i dans Text, en octets.
	Spans [][2]int
	BBox  Rect
}

// BoxesFor retourne les boîtes des mots recouvrant l'intervalle [start, end)
// de Text.
//
// C'est le pont entre le monde du texte — où les entités sont détectées, avec
// des offsets — et celui des pixels, où il faut les effacer. Un caviardage qui
// se tromperait ici laisserait des glyphes dépasser.
func (l Line) BoxesFor(start, end int) []Rect {
	var boxes []Rect
	for i, span := range l.Spans {
		if start < span[1] && end > span[0] {
			boxes = append(boxes, l.Words[i].BBox)
		}
	}
	return boxes
}

// Lines reconstitue les lignes à partir d'une liste de mots.
//
// Les mots sont regroupés par identifiant de ligne, puis ordonnés
// horizontalement — un moteur peut les rendre dans un autre ordre, et un mot
// mal placé décalerait toutes les correspondances offset → boîte.
func Lines(words []Word) []Line {
	if len(words) == 0 {
		return nil
	}

	byLine := map[int][]Word{}
	var order []int
	for _, w := range words {
		if _, seen := byLine[w.Line]; !seen {
			order = append(order, w.Line)
		}
		byLine[w.Line] = append(byLine[w.Line], w)
	}
	sort.Ints(order)

	lines := make([]Line, 0, len(order))
	for _, id := range order {
		group := byLine[id]
		sort.SliceStable(group, func(i, j int) bool { return group[i].BBox.X < group[j].BBox.X })

		var b strings.Builder
		spans := make([][2]int, 0, len(group))
		var bbox Rect
		for i, w := range group {
			if i > 0 {
				b.WriteByte(' ')
			}
			start := b.Len()
			b.WriteString(w.Text)
			spans = append(spans, [2]int{start, b.Len()})
			bbox = bbox.Union(w.BBox)
		}

		lines = append(lines, Line{Text: b.String(), Words: group, Spans: spans, BBox: bbox})
	}
	return lines
}

// FilterConfidence retire les mots sous le seuil.
//
// À utiliser avec parcimonie : sous une posture de couverture maximale, un mot
// douteux vaut mieux qu'un mot manquant, et le coût d'un faux positif est un
// caviardage de trop. Le seuil sert surtout à écarter le bruit de fond que les
// moteurs produisent sur les scans dégradés (traits, poussières).
func FilterConfidence(words []Word, min float64) []Word {
	if min <= 0 {
		return words
	}
	kept := make([]Word, 0, len(words))
	for _, w := range words {
		if w.Confidence >= min {
			kept = append(kept, w)
		}
	}
	return kept
}
