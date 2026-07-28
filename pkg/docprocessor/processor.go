package docprocessor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/ner"
)

// errSampleDone est une sentinelle interne utilisée pour interrompre un Walk
// une fois assez de texte accumulé.
var errSampleDone = errors.New("docprocessor: sample done")

// SampleText parcourt le walker et accumule le texte des segments (séparés par
// un saut de ligne) jusqu'à atteindre maxChars caractères, puis s'arrête.
// Lecture seule : Segment.Replace n'est jamais appelé. Utile pour détecter la
// langue d'un document avant de choisir le modèle et de construire le pipeline.
func SampleText(walker Walker, maxChars int) (string, error) {
	var b strings.Builder
	err := walker.Walk(func(seg Segment) error {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(seg.Text)
		if b.Len() >= maxChars {
			return errSampleDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSampleDone) {
		return "", err
	}
	return b.String(), nil
}

// Segment représente une unité de texte extraite d'un document.
// Replace est le callback qui écrit le texte anonymisé en retour dans le document.
type Segment struct {
	Text    string
	Replace func(anonymized string)
}

// Walker parcourt les segments de texte d'un document, indépendamment de son format.
type Walker interface {
	Walk(func(Segment) error) error
}

// Processor orchestre l'anonymisation d'un document via un Walker.
type Processor struct {
	anon      *anonymizer.Anonymizer
	verifyDoc bool
	strictDoc bool
	multiView bool
}

// Option configure un Processor.
type Option func(*Processor)

// WithDocumentVerification active la vérification au niveau document : après le
// parcours, le texte de sortie est recomposé et repassé au recognizer.
//
// Motivation — la vérification par segment ne peut pas voir une entité coupée
// par la segmentation du format : « Jean » en fin de ligne et « Dupont » au
// début de la suivante ne sont, isolément, des entités pour personne. La règle
// générale est que la vérification doit opérer à une granularité au moins aussi
// grossière que l'unité qu'un lecteur peut reconstituer — et un lecteur lit à
// travers les sauts de ligne.
//
// Coût : une passe de détection supplémentaire sur l'intégralité du document.
// D'où l'activation explicite plutôt que par défaut.
func WithDocumentVerification() Option {
	return func(p *Processor) { p.verifyDoc = true }
}

// WithStrictDocumentVerification active la vérification document et fait
// échouer ProcessWithReport si elle trouve quoi que ce soit. L'appelant ne doit
// alors pas écrire de document de sortie.
func WithStrictDocumentVerification() Option {
	return func(p *Processor) { p.verifyDoc, p.strictDoc = true, true }
}

// WithMultiViewDetection recompose le document sous plusieurs angles avant
// d'anonymiser, et unione les entités trouvées par chacun.
//
// Motivation — la détection par segment est structurellement aveugle à une
// entité coupée par la segmentation du format : « Jean » en fin de ligne et
// « Dupont » au début de la suivante ne sont, isolément, des entités pour
// personne. Plus largement, le modèle est entraîné sur des phrases, alors que
// le PDF lui livre des fragments de ligne : c'est la détection sur *toutes* les
// entités de la page qui souffre du découpage, pas seulement sur celles qui
// tombent sur une coupure.
//
// L'union est monotone en rappel — aucune vue ne peut retirer ce qu'une autre a
// trouvé — au prix d'environ trois passes de détection supplémentaires sur
// l'ensemble du document, plus une lecture préalable du walker.
//
// À combiner avec WithDocumentVerification, qui contrôle la sortie là où
// celle-ci améliore l'entrée.
func WithMultiViewDetection() Option {
	return func(p *Processor) { p.multiView = true }
}

func New(anon *anonymizer.Anonymizer, opts ...Option) *Processor {
	p := &Processor{anon: anon}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SegmentLeak localise une fuite dans un document : le rang du segment dans le
// parcours, plus les offsets dans ce segment. Comme anonymizer.Leak, il ne porte
// jamais de contenu.
type SegmentLeak struct {
	Segment int
	anonymizer.Leak
}

// DocumentLeak localise une fuite détectée sur le texte recomposé du document.
//
// Les offsets sont relatifs à cette recomposition, pas à un segment : une entité
// coupée par la segmentation n'appartient en propre à aucun d'eux. Segments
// donne les rangs des segments qu'elle chevauche — une longueur supérieure à 1
// identifie précisément une entité cross-segment. Comme Leak, ne porte jamais
// de contenu.
type DocumentLeak struct {
	Segments []int
	anonymizer.Leak
}

// Report agrège les rapports de vérification de tous les segments d'un document.
type Report struct {
	Segments int
	Leaks    []SegmentLeak
	// DocumentLeaks est renseigné par WithDocumentVerification : entités
	// redevenues détectables une fois le document recomposé.
	DocumentLeaks []DocumentLeak
	// RegionLeaks liste les entités trouvées dans des portions que le pipeline
	// ne sait pas réécrire (contenu bitmap océrisé).
	RegionLeaks []RegionLeak

	// Entities compte les occurrences anonymisées par type. Le total ne peut
	// pas se déduire de la taille du mapping : la stratégie Redact n'en produit
	// aucun, étant irréversible par construction.
	//
	// Sert à surveiller la **précision**, angle mort d'une configuration réglée
	// pour le rappel : un type qui s'emballe (des LOC partout, des PER sur des
	// noms communs) se voit ici avant de rendre les documents inexploitables.
	// Compte les occurrences, pas les entités distinctes — c'est ce que le
	// lecteur du document constate.
	Entities map[ner.EntityType]int
	// RedactedZones compte les zones effacées dans du contenu non textuel.
	RedactedZones int
}

// countEntity incrémente le compteur d'un type.
func (r *Report) countEntity(t ner.EntityType) {
	if r.Entities == nil {
		r.Entities = map[ner.EntityType]int{}
	}
	r.Entities[t]++
}

// TotalEntities somme les occurrences anonymisées, tous types confondus.
func (r *Report) TotalEntities() int {
	total := 0
	for _, n := range r.Entities {
		total += n
	}
	return total
}

// OK indique qu'aucune fuite n'a été détectée : ni par segment, ni à l'échelle
// du document, ni dans une région non réécrivable.
func (r *Report) OK() bool {
	return r == nil ||
		(len(r.Leaks) == 0 && len(r.DocumentLeaks) == 0 && len(r.RegionLeaks) == 0)
}

// Process parcourt les segments via walker, anonymise chacun avec une session partagée,
// et appelle Segment.Replace pour chaque segment dont le texte a changé.
// Les opts additionnelles sont ajoutées après WithSession (qui est toujours injecté).
func (p *Processor) Process(walker Walker, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, error) {
	session, _, err := p.ProcessWithReport(walker, opts...)
	return session, err
}

// ProcessWithReport se comporte comme Process et agrège en plus les rapports de
// vérification par segment (WithVerification).
//
// En mode strict (WithStrictVerification), la première fuite interrompt le
// parcours et retourne une erreur : l'appelant ne doit alors pas écrire de
// document de sortie, sous peine de produire un fichier partiellement anonymisé.
func (p *Processor) ProcessWithReport(walker Walker, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, *Report, error) {
	session := anonymizer.NewSession()
	allOpts := append([]anonymizer.AnonymizeOption{anonymizer.WithSession(session)}, opts...)

	// La détection multi-vues demande une lecture préalable du document : les
	// vues se construisent sur le texte source, avant toute réécriture.
	var extras map[int][]ner.Entity
	if p.multiView {
		texts, err := collectTexts(walker)
		if err != nil {
			return session, nil, err
		}
		extras, err = p.detectViews(walker, texts)
		if err != nil {
			return session, nil, err
		}
	}

	report := &Report{}
	index := 0
	var outputs []string
	var replacements []string

	err := walker.Walk(func(seg Segment) error {
		i := index
		index++
		report.Segments = index

		segOpts := allOpts
		if found := extras[i]; len(found) > 0 {
			segOpts = append(append([]anonymizer.AnonymizeOption{}, allOpts...),
				anonymizer.WithAdditionalEntities(found))
		}

		result, err := p.anon.Anonymize(seg.Text, segOpts...)
		if err != nil {
			var verr *anonymizer.VerificationError
			if errors.As(err, &verr) {
				return fmt.Errorf("segment %d : %w", i, err)
			}
			return err
		}
		if result.Verification != nil {
			for _, leak := range result.Verification.Leaks {
				report.Leaks = append(report.Leaks, SegmentLeak{Segment: i, Leak: leak})
			}
		}
		for _, ent := range result.Entities {
			report.countEntity(ent.Type)
		}
		if result.Text != seg.Text {
			seg.Replace(result.Text)
		}

		if p.verifyDoc {
			outputs = append(outputs, result.Text)
			for _, replacement := range result.OriginalToPlaceholder {
				replacements = append(replacements, replacement)
			}
		}
		return nil
	})
	if err != nil {
		return session, report, err
	}

	if p.verifyDoc {
		leaks, err := p.verifyDocument(outputs, replacements)
		if err != nil {
			return session, report, err
		}
		report.DocumentLeaks = leaks
		if p.strictDoc && len(leaks) > 0 {
			return session, report, fmt.Errorf(
				"document : %w : %d entité(s) redétectée(s) après recomposition",
				anonymizer.ErrVerificationFailed, len(leaks))
		}
	}

	// Systématique : ne dépend d'aucune option, seulement de ce que le walker
	// expose. Une entité qu'on sait présente et qu'on ne sait pas retirer doit
	// être signalée quelle que soit la configuration.
	regionLeaks, err := p.scanReadOnlyRegions(walker, report)
	if err != nil {
		return session, report, err
	}
	report.RegionLeaks = regionLeaks
	if p.strictDoc && len(regionLeaks) > 0 {
		return session, report, fmt.Errorf(
			"%w : %d entité(s) dans une portion non réécrivable du document",
			anonymizer.ErrVerificationFailed, len(regionLeaks))
	}

	return session, report, nil
}

// verifyDocument recompose la sortie et y relance une passe de détection.
//
// Le séparateur est une espace, et c'est le cœur du contrôle : `pkg/ner`
// découpe d'abord par ligne, donc joindre par « \n » reproduirait exactement la
// segmentation du walker et ne trouverait jamais rien. L'espace recolle les
// segments en un flux continu, où le CRF revoit « Jean Dupont » que la coupure
// lui avait caché.
//
// Contrepartie assumée : deux segments voisins mais sans rapport (deux cellules,
// deux colonnes) deviennent adjacents et peuvent produire un faux positif. Pour
// un contrôle dont la sortie est « regarde ici », c'est le bon sens d'erreur.
func (p *Processor) verifyDocument(outputs, replacements []string) ([]DocumentLeak, error) {
	if len(outputs) == 0 {
		return nil, nil
	}

	const sep = " "
	bounds := make([][2]int, len(outputs))
	pos := 0
	for i, out := range outputs {
		bounds[i] = [2]int{pos, pos + len(out)}
		pos += len(out) + len(sep)
	}
	joined := strings.Join(outputs, sep)

	entities, err := p.anon.Detect(joined)
	if err != nil {
		return nil, fmt.Errorf("vérification document : %w", err)
	}

	safe := anonymizer.SafeZones(joined, replacements)

	var leaks []DocumentLeak
	for _, ent := range entities {
		if anonymizer.Overlaps(safe, ent.Start, ent.End) {
			continue
		}
		leaks = append(leaks, DocumentLeak{
			Segments: segmentsSpanning(bounds, ent.Start, ent.End),
			Leak: anonymizer.Leak{
				Kind:  anonymizer.LeakDocumentEntity,
				Type:  ent.Type,
				Start: ent.Start,
				End:   ent.End,
			},
		})
	}
	return leaks, nil
}

// segmentsSpanning retourne les rangs des segments chevauchés par [start, end).
func segmentsSpanning(bounds [][2]int, start, end int) []int {
	var spanning []int
	for i, b := range bounds {
		if start < b[1] && end > b[0] {
			spanning = append(spanning, i)
		}
	}
	return spanning
}
