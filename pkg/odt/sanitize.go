package odt

import (
	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// Surfaces cachées d'un ODT :
//   - meta.xml : auteur, société, statistiques, dates → purgé ;
//   - office:annotation dans content.xml : commentaires (texte + auteur) ;
//   - text:tracked-changes dans content.xml : texte supprimé archivé, plus les
//     jalons text:change* inline.
//
// Contrairement au DOCX, l'arbre XML d'ODT est entièrement sous notre contrôle
// (cf. Walker.contentXML) : on peut réellement retirer ces nœuds.

// emptyMeta est un meta.xml minimal valide, sans aucune donnée identifiante.
const emptyMeta = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<office:document-meta ` +
	`xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
	`xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0">` +
	`<office:meta></office:meta></office:document-meta>`

// Sanitize purge les surfaces non textuelles de l'ODT selon policy.
func (w *Walker) Sanitize(policy docprocessor.SanitizePolicy) (docprocessor.SanitizeReport, error) {
	var report docprocessor.SanitizeReport

	if policy.StripMetadata {
		if _, ok := w.otherFiles["meta.xml"]; ok {
			w.otherFiles["meta.xml"] = zipEntry{data: []byte(emptyMeta), method: 8 /* Deflate */}
			report.MetadataStripped = true
		}
	}

	// Commentaires (office:annotation, office:annotation-end).
	report.CommentsFound = countElements(w.contentXML, map[string]bool{"annotation": true})
	if report.CommentsFound > 0 {
		if policy.ProcessComments {
			// L'anonymisation ciblée des commentaires ODT n'est pas gérée.
			report.Unprocessed = append(report.Unprocessed, "commentaires (anonymisation non gérée)")
		} else {
			removeElements(w.contentXML, map[string]bool{
				"annotation": true, "annotation-end": true,
			})
		}
	}

	// Révisions suivies : la réserve text:tracked-changes contient le texte
	// supprimé ; les jalons text:change* marquent les emplacements dans le corps.
	report.RevisionsFound = countElements(w.contentXML, map[string]bool{"tracked-changes": true})
	if report.RevisionsFound > 0 {
		if policy.AcceptRevisions {
			// Accepter : retirer la réserve (texte supprimé) et les jalons inline.
			// Le texte inséré, lui, reste dans le flux — il a déjà été anonymisé.
			removeElements(w.contentXML, map[string]bool{
				"tracked-changes": true,
				"change":          true,
				"change-start":    true,
				"change-end":      true,
			})
		} else {
			report.Unprocessed = append(report.Unprocessed, "révisions ODT (AcceptRevisions désactivé)")
		}
	}

	return report, nil
}

// countElements compte récursivement les éléments dont le nom local figure dans
// names (sans les retirer).
func countElements(n *xmlNode, names map[string]bool) int {
	if n == nil || n.kind != kindElement {
		return 0
	}
	count := 0
	if names[xmlLocalName(n.rawName)] {
		count++
	}
	for _, child := range n.children {
		count += countElements(child, names)
	}
	return count
}

// removeElements retire récursivement de l'arbre tous les éléments dont le nom
// local figure dans names, et retourne le nombre de nœuds retirés.
func removeElements(n *xmlNode, names map[string]bool) int {
	if n == nil || n.kind != kindElement {
		return 0
	}
	removed := 0
	kept := n.children[:0]
	for _, child := range n.children {
		if child.kind == kindElement && names[xmlLocalName(child.rawName)] {
			removed++
			continue
		}
		removed += removeElements(child, names)
		kept = append(kept, child)
	}
	n.children = kept
	return removed
}
