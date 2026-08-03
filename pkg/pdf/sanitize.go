package pdf

import (
	"fmt"

	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// Surfaces cachées d'un PDF :
//   - dictionnaire Info (Author, Title, Subject, Keywords, Creator, Producer…)
//     et métadonnées XMP dans le catalogue → purgés ;
//   - annotations de page (commentaires, champs de formulaire) et pièces jointes
//     (EmbeddedFiles) → détectées et signalées. Le pipeline ne les traite pas :
//     en mode strict, leur présence est une erreur, à charge de l'appelant de
//     les retirer en amont ;
//   - pages scannées (image pleine page sans couche texte) → détectées et
//     signalées. Leur contenu échappe intégralement à l'anonymisation : sans ce
//     signalement, le document ressortirait intact et déclaré conforme.

// Sanitize purge les métadonnées du PDF et signale les surfaces non traitées.
func (w *Walker) Sanitize(policy docprocessor.SanitizePolicy) (docprocessor.SanitizeReport, error) {
	var report docprocessor.SanitizeReport

	if policy.StripMetadata {
		w.stripInfoDict()
		w.stripXMP()
		report.MetadataStripped = true
	}

	if n := w.countAnnotations(); n > 0 {
		// Les annotations peuvent porter du texte (commentaires, valeurs de
		// champ) que le pipeline n'anonymise pas.
		report.Unprocessed = append(report.Unprocessed, "annotations PDF")
	}
	if w.hasEmbeddedFiles() {
		report.Unprocessed = append(report.Unprocessed, "pièces jointes PDF")
	}
	if n := len(w.rasterPages); n > 0 {
		report.Unprocessed = append(report.Unprocessed, fmt.Sprintf(
			"%d page(s) image sans couche texte (contenu scanné) : %s",
			n, formatPageList(w.rasterPages)))
	}
	if n := len(w.hybridPages); n > 0 {
		report.Unprocessed = append(report.Unprocessed, fmt.Sprintf(
			"%d page(s) scannée(s) à couche texte invisible : le texte extrait est "+
				"anonymisé mais les pixels rendus au lecteur restent inchangés : %s",
			n, formatPageList(w.hybridPages)))
	}
	if pages := w.modifiedInvisiblePages(); len(pages) > 0 {
		report.Unprocessed = append(report.Unprocessed, fmt.Sprintf(
			"%d page(s) où du texte invisible a été anonymisé sans que le rendu "+
				"visible soit modifié : %s",
			len(pages), formatPageList(pages)))
	}

	return report, nil
}

// stripInfoDict vide le dictionnaire Info de toute entrée identifiante.
func (w *Walker) stripInfoDict() {
	if w.ctx.Info == nil {
		return
	}
	d, err := w.ctx.DereferenceDict(*w.ctx.Info)
	if err != nil || d == nil {
		return
	}
	for k := range d {
		delete(d, k)
	}
}

// stripXMP retire le flux de métadonnées XMP référencé par le catalogue.
func (w *Walker) stripXMP() {
	cat, err := w.ctx.Catalog()
	if err != nil || cat == nil {
		return
	}
	delete(cat, "Metadata")
}

// countAnnotations somme les annotations présentes sur toutes les pages.
func (w *Walker) countAnnotations() int {
	total := 0
	for pageNr := 1; pageNr <= w.ctx.PageCount; pageNr++ {
		d, _, _, err := w.ctx.PageDict(pageNr, false)
		if err != nil || d == nil {
			continue
		}
		obj, found := d.Find("Annots")
		if !found || obj == nil {
			continue
		}
		arr, err := w.ctx.DereferenceArray(obj)
		if err != nil {
			continue
		}
		total += len(arr)
	}
	return total
}

// hasEmbeddedFiles rapporte la présence de pièces jointes (arbre de noms
// EmbeddedFiles dans le catalogue).
func (w *Walker) hasEmbeddedFiles() bool {
	names, err := w.ctx.NamesDict()
	if err != nil || names == nil {
		return false
	}
	obj, found := names.Find("EmbeddedFiles")
	return found && obj != nil
}
