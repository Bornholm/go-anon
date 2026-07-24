package docprocessor

import (
	"fmt"
	"sort"
	"strings"
)

// Un document « anonymisé » ne se limite pas à son texte visible. Un DOCX porte
// l'auteur et le dernier modificateur dans docProps, du texte supprimé dans ses
// révisions, des commentaires ; un PDF porte un dictionnaire Info et des
// métadonnées XMP ; un ODT un meta.xml. Livrer un document dont le corps est
// pseudonymisé mais dont les propriétés nomment l'auteur est une non-conformité
// classique. Sanitizer traite ces surfaces non textuelles.

// SanitizePolicy pilote le traitement des surfaces cachées d'un document.
type SanitizePolicy struct {
	// StripMetadata purge les métadonnées identifiantes (auteur, société,
	// dernier modificateur, dates de révision…). Recommandé.
	StripMetadata bool
	// AcceptRevisions demande d'accepter les révisions (track changes) avant
	// anonymisation, pour que le texte « supprimé » disparaisse réellement.
	// Si le format ne sait pas le garantir, les révisions présentes sont
	// signalées comme non traitées (Unprocessed).
	AcceptRevisions bool
	// ProcessComments : true anonymise les commentaires via le pipeline ; false
	// (défaut) les supprime purement et simplement.
	ProcessComments bool
	// ProcessHeadersFeet étend le traitement aux en-têtes et pieds de page.
	ProcessHeadersFeet bool
	// Strict fait échouer la sanitisation si une surface connue reste non
	// traitée (Unprocessed non vide) ou si le format n'implémente pas Sanitizer.
	Strict bool
}

// DefaultSanitizePolicy retourne la politique recommandée : purge des
// métadonnées, acceptation des révisions, suppression des commentaires.
func DefaultSanitizePolicy() SanitizePolicy {
	return SanitizePolicy{
		StripMetadata:   true,
		AcceptRevisions: true,
		ProcessComments: false,
	}
}

// SanitizeReport rend compte des surfaces traitées et de celles qui ne l'ont pas
// été. Comme les rapports de vérification, il ne porte aucun contenu : seulement
// des comptes et des noms de surfaces.
type SanitizeReport struct {
	MetadataStripped bool
	RevisionsFound   int
	CommentsFound    int
	// Unprocessed liste les surfaces détectées mais non traitées (ex.
	// « revisions », « annotations PDF »). En mode strict, sa présence est une
	// erreur : le format ne peut pas garantir l'absence de fuite.
	Unprocessed []string
}

// OK indique qu'aucune surface connue n'est restée non traitée.
func (r *SanitizeReport) OK() bool { return r == nil || len(r.Unprocessed) == 0 }

// Sanitizer est implémentée par les Walkers capables de purger ou d'anonymiser
// les surfaces non textuelles de leur format. Un Walker qui ne l'implémente pas
// n'offre aucune garantie sur ses métadonnées : en mode strict, Sanitize le
// signale par une erreur.
type Sanitizer interface {
	Sanitize(policy SanitizePolicy) (SanitizeReport, error)
}

// ErrNoSanitizeGuarantee signale un format sans garantie de sanitisation en mode
// strict (le Walker n'implémente pas Sanitizer).
var ErrNoSanitizeGuarantee = fmt.Errorf("docprocessor: format sans garantie de sanitisation des métadonnées")

// ErrUnsanitizedSurface signale, en mode strict, des surfaces détectées mais non
// traitées. Le message énumère les surfaces (jamais leur contenu).
type ErrUnsanitizedSurface struct {
	Surfaces []string
}

func (e *ErrUnsanitizedSurface) Error() string {
	return "docprocessor: surfaces non traitées en mode strict : " + strings.Join(e.Surfaces, ", ")
}

// Sanitize applique la politique de sanitisation au walker s'il implémente
// Sanitizer. En mode strict :
//   - un walker sans Sanitizer → ErrNoSanitizeGuarantee ;
//   - un rapport avec des surfaces non traitées → ErrUnsanitizedSurface.
//
// Appeler Sanitize avant SaveTo, et — en strict — ne pas écrire le document si
// une erreur est retournée.
func Sanitize(walker Walker, policy SanitizePolicy) (SanitizeReport, error) {
	san, ok := walker.(Sanitizer)
	if !ok {
		if policy.Strict {
			return SanitizeReport{}, ErrNoSanitizeGuarantee
		}
		return SanitizeReport{}, nil
	}

	report, err := san.Sanitize(policy)
	if err != nil {
		return report, err
	}

	if policy.Strict && !report.OK() {
		surfaces := append([]string(nil), report.Unprocessed...)
		sort.Strings(surfaces)
		return report, &ErrUnsanitizedSurface{Surfaces: surfaces}
	}
	return report, nil
}
