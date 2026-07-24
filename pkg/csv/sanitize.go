package csv

import (
	"github.com/bornholm/go-anon/pkg/docprocessor"
)

// Sanitize est un no-op pour CSV/TSV : le fichier EST son texte visible, il n'y
// a ni métadonnées, ni commentaires, ni révisions cachées. Implémenter
// l'interface Sanitizer permet néanmoins au mode strict de considérer le format
// comme garanti (plutôt que de le refuser comme « format sans garantie »).
func (w *Walker) Sanitize(policy docprocessor.SanitizePolicy) (docprocessor.SanitizeReport, error) {
	return docprocessor.SanitizeReport{}, nil
}
