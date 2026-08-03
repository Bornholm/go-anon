package docprocessor

import (
	"fmt"

	"github.com/bornholm/go-anon/pkg/anonymizer"
)

// ReadOnlyRegion est du texte présent dans le document mais qu'aucun segment ne
// représente : le pipeline sait l'analyser, pas le réécrire. Typiquement le
// contenu océrisé d'une page bitmap.
type ReadOnlyRegion struct {
	// Label localise la région pour le rapport, ex. « page 3 (OCR) ». Il ne
	// porte jamais de contenu.
	Label string
	Text  string
}

// ReadOnlyTextWalker est implémentée par les Walkers capables d'exposer un tel
// texte. Un walker qui ne l'implémente pas ne change simplement rien.
type ReadOnlyTextWalker interface {
	Walker
	ReadOnlyText() []ReadOnlyRegion
}

// RedactingWalker est implémentée par les Walkers sachant en plus **effacer**
// une portion de leur contenu non textuel — typiquement noircir des pixels.
//
// La séparation des rôles est délibérée : le processeur détecte et désigne par
// offsets, le walker seul sait traduire ces offsets dans la géométrie de son
// format. Un walker qui expose du texte sans implémenter cette interface se
// contente du signalement.
type RedactingWalker interface {
	ReadOnlyTextWalker
	// MarkRedaction désigne [start, end) dans le texte de la région d'index
	// region, dans l'ordre rendu par ReadOnlyText.
	MarkRedaction(region, start, end int)
}

// RegionLeak signale une entité détectée dans une région non réécrivable.
//
// La distinction avec SegmentLeak est essentielle : ce n'est pas un
// remplacement qui a échoué, c'est du contenu que le pipeline **n'a aucun moyen
// de retirer**. Le document sortira avec cette donnée en clair, quelle que soit
// la qualité du reste du traitement. Comme les autres, ne porte que des offsets
// et des types.
type RegionLeak struct {
	Region string
	anonymizer.Leak
}

// scanReadOnlyRegions analyse les régions non réécrivables et signale toute
// entité qui s'y trouve.
//
// Le contrôle est systématique dès qu'un walker en expose : le coût d'une passe
// de détection est négligeable devant celui de l'OCR qui l'a produit, et un
// document dont on sait qu'il contient un nom illisible par le pipeline ne doit
// jamais être déclaré conforme.
func (p *Processor) scanReadOnlyRegions(walker Walker, report *Report) ([]RegionLeak, error) {
	rw, ok := walker.(ReadOnlyTextWalker)
	if !ok {
		return nil, nil
	}

	redactor, canRedact := walker.(RedactingWalker)

	var leaks []RegionLeak
	for i, region := range rw.ReadOnlyText() {
		if region.Text == "" {
			continue
		}
		entities, err := p.anon.Detect(region.Text)
		if err != nil {
			return nil, fmt.Errorf("analyse de %s : %w", region.Label, err)
		}
		for _, ent := range entities {
			if canRedact {
				// Le walker sait effacer : la zone est marquée et disparaîtra à
				// l'écriture. La fuite n'est alors plus signalée — elle n'existe
				// plus dans le document produit.
				redactor.MarkRedaction(i, ent.Start, ent.End)
				report.countEntity(ent.Type)
				report.RedactedZones++
				continue
			}
			leaks = append(leaks, RegionLeak{
				Region: region.Label,
				Leak: anonymizer.Leak{
					Kind:  anonymizer.LeakUnwritableRegion,
					Type:  ent.Type,
					Start: ent.Start,
					End:   ent.End,
				},
			})
		}
	}
	return leaks, nil
}
