package docprocessor

import (
	"fmt"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// Vérification visuelle du document produit.
//
// Tous les autres contrôles inspectent des chaînes : ils vérifient que le texte
// que le pipeline a manipulé ne porte plus de donnée personnelle. Celui-ci
// relit le fichier écrit **tel qu'un lecteur le voit** — rendu puis océrisé — et
// parle donc le même langage que le risque réel : *ce qu'un humain peut lire
// dans le document produit ne contient aucune donnée personnelle.*
//
// C'est le seul contrôle capable d'attraper le mode d'échec numéro un de ce
// type de pipeline : des boîtes mal alignées, par décalage d'origine, confusion
// points/pixels, ou axe Y inversé entre l'espace PDF et l'espace image.

// VisualVerifier est implémentée par les Walkers capables de relire un document
// écrit et d'en rendre le texte visible. Un walker qui ne l'implémente pas ne
// fournit tout simplement pas cette garantie.
type VisualVerifier interface {
	Walker
	// VisualText relit le fichier path et rend son texte visible, une région
	// par unité de localisation (page).
	VisualText(path string) ([]ReadOnlyRegion, error)
}

// VisualLeak signale une donnée restée lisible dans le document produit.
type VisualLeak struct {
	Region string
	anonymizer.Leak
}

// VerifyOutput relit le document écrit et signale ce qui y reste lisible.
//
// À appeler **après** l'écriture, forcément : c'est le fichier produit qui est
// contrôlé. En cas de fuite, l'appelant doit détruire ce fichier — un document
// dont on sait qu'il expose une donnée personnelle ne doit pas subsister.
//
// Deux contrôles complémentaires sur le texte relu :
//
//   - les formes de surface **connues de la session** — celles que le pipeline a
//     remplacées. Leur réapparition prouve qu'un remplacement ou un caviardage
//     a manqué sa cible. C'est le signal le plus fort, puisqu'il ne dépend
//     d'aucune redétection ;
//   - une **détection fraîche**, qui attrape ce que le pipeline n'avait jamais
//     vu : une donnée qu'un premier OCR avait mal lue et que la relecture
//     déchiffre.
//
// Les deux excluent les zones écrites par l'anonymiseur : relire une sortie
// revient à relire ses propres placeholders, et « ⟦PERSONNE_1_…⟧ » se laisse
// volontiers prendre pour un nom propre.
func (p *Processor) VerifyOutput(walker Walker, path string, session *anonymizer.Session) ([]VisualLeak, error) {
	vv, ok := walker.(VisualVerifier)
	if !ok {
		return nil, nil
	}

	regions, err := vv.VisualText(path)
	if err != nil {
		return nil, fmt.Errorf("relecture visuelle : %w", err)
	}

	var known map[string]string
	if session != nil {
		known = session.OriginalToPlaceholder
	}
	replacements := make([]string, 0, len(known))
	for _, replacement := range known {
		replacements = append(replacements, replacement)
	}

	var leaks []VisualLeak
	for _, region := range regions {
		if region.Text == "" {
			continue
		}

		// Contrôle 1 — réutilise la vérification de l'anonymiseur en lui
		// présentant le texte relu comme s'il s'agissait d'une sortie : elle
		// sait déjà chercher les formes connues et les identifiants structurés
		// hors des zones de remplacement.
		report := anonymizer.Verify("", &anonymizer.Result{
			Text:                  region.Text,
			OriginalToPlaceholder: known,
		})
		for _, leak := range report.Leaks {
			leaks = append(leaks, VisualLeak{Region: region.Label, Leak: leak})
		}

		// Contrôle 2 — détection fraîche.
		entities, err := p.anon.Detect(region.Text)
		if err != nil {
			return nil, fmt.Errorf("relecture de %s : %w", region.Label, err)
		}
		safe := anonymizer.SafeZones(region.Text, replacements)
		for _, ent := range entities {
			if anonymizer.Overlaps(safe, ent.Start, ent.End) {
				continue
			}
			leaks = append(leaks, VisualLeak{
				Region: region.Label,
				Leak: anonymizer.Leak{
					Kind:  anonymizer.LeakVisualResidue,
					Type:  ent.Type,
					Start: ent.Start,
					End:   ent.End,
				},
			})
		}
	}

	return leaks, nil
}
