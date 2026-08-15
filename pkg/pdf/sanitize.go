package pdf

import (
	"fmt"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"

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

	if n := w.countTextAnnotations(); n > 0 {
		// Les annotations peuvent porter du texte (commentaires, valeurs de
		// champ) que le pipeline n'anonymise pas.
		report.Unprocessed = append(report.Unprocessed, fmt.Sprintf(
			"%d annotation(s) PDF porteuse(s) de texte", n))
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

// annotationTextKeys liste les entrées d'un dictionnaire d'annotation qui
// peuvent contenir du texte saisi : commentaire, nom et valeur de champ de
// formulaire, infobulle, variante enrichie.
var annotationTextKeys = []string{"Contents", "T", "V", "TU", "RC"}

// countTextAnnotations compte les annotations susceptibles de porter une donnée
// personnelle.
//
// Compter toutes les annotations rendait l'alerte inutilisable : un PDF de
// facture porte des /Link sur ses URL de contact, sans une ligne de texte
// saisi. Signaler ces liens comme surface non traitée noyait les vraies
// annotations à risque, et faisait échouer le mode strict sans motif.
//
// Une annotation compte si elle porte un champ texte non vide, ou une action
// /URI — une adresse peut contenir un identifiant client ou un courriel.
func (w *Walker) countTextAnnotations() int {
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
		for _, a := range arr {
			if w.annotationCarriesText(a) {
				total++
			}
		}
	}
	return total
}

// annotationCarriesText rapporte si une annotation porte du texte saisi ou une
// URI. En cas de doute — dictionnaire illisible — elle compte : mieux vaut une
// alerte de trop qu'une donnée passée sous silence.
func (w *Walker) annotationCarriesText(a types.Object) bool {
	ad, err := w.ctx.DereferenceDict(a)
	if err != nil || ad == nil {
		return true
	}
	for _, k := range annotationTextKeys {
		v, ok := ad.Find(k)
		if !ok {
			continue
		}
		if s, err := w.ctx.DereferenceStringOrHexLiteral(v, 0, nil); err == nil && strings.TrimSpace(s) != "" {
			return true
		}
	}
	if action, ok := ad.Find("A"); ok {
		if actionDict, err := w.ctx.DereferenceDict(action); err == nil && actionDict != nil {
			if uri, ok := actionDict.Find("URI"); ok {
				if s, err := w.ctx.DereferenceStringOrHexLiteral(uri, 0, nil); err == nil && uriCarriesData(s) {
					return true
				}
			}
		}
	}
	return false
}

// uriCarriesData rapporte si une URI peut transporter une donnée personnelle.
//
// Une facture porte des liens vers le site de son émetteur : les signaler
// revient à alerter sur chaque document, ce qui apprend à ignorer l'alerte. Une
// adresse de courriel ou des paramètres de requête, en revanche, portent
// couramment un identifiant client ou un jeton de session.
func uriCarriesData(uri string) bool {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(uri), "mailto:") {
		return true
	}
	return strings.ContainsAny(uri, "?=@")
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
