package docprocessor

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/ner"
)

// sliceWalker est un Walker de test qui émet des segments prédéfinis.
type sliceWalker struct {
	segments []string
	walked   int
	replaced []string
}

func (w *sliceWalker) Walk(fn func(Segment) error) error {
	for _, s := range w.segments {
		w.walked++
		if err := fn(Segment{Text: s, Replace: func(out string) {
			w.replaced = append(w.replaced, out)
		}}); err != nil {
			return err
		}
	}
	return nil
}

// emailRecognizer détecte les adresses e-mail par regex — suffisant pour tester
// l'orchestration sans charger de modèle CRF.
type emailRecognizer struct{}

func (emailRecognizer) Recognize(text string) ([]ner.Entity, error) {
	return ner.RegexEntityFilter(ner.BuiltinRegexPatterns)(text, nil), nil
}

func TestProcessWithReport_AggregatesLeaksPerSegment(t *testing.T) {
	// Passe défectueuse : elle restaure le texte source, donc l'e-mail.
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace,
		Passes: []anonymizer.AnonymizePass{func(original string, r *anonymizer.Result) string {
			return original
		}},
	})

	w := &sliceWalker{segments: []string{"rien à signaler", "écrire à jean@example.com"}}
	_, report, err := New(anon).ProcessWithReport(w, anonymizer.WithVerification())
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if report.Segments != 2 {
		t.Errorf("segments = %d, attendu 2", report.Segments)
	}
	if len(report.Leaks) == 0 {
		t.Fatal("la fuite du second segment aurait dû être rapportée")
	}
	for _, leak := range report.Leaks {
		if leak.Segment != 1 {
			t.Errorf("fuite attribuée au segment %d, attendu 1", leak.Segment)
		}
	}
}

// En mode strict, le parcours s'interrompt à la première fuite et aucun segment
// n'est réécrit après elle : l'appelant ne doit pas produire de document.
func TestProcessWithReport_StrictStopsBeforeWriting(t *testing.T) {
	anon := anonymizer.New(emailRecognizer{}, anonymizer.Config{
		Strategy: anonymizer.TagReplace,
		Passes: []anonymizer.AnonymizePass{func(original string, r *anonymizer.Result) string {
			return original
		}},
	})

	w := &sliceWalker{segments: []string{"écrire à jean@example.com", "second segment"}}
	_, _, err := New(anon).ProcessWithReport(w, anonymizer.WithStrictVerification())
	if err == nil {
		t.Fatal("le mode strict aurait dû échouer")
	}
	if !errors.Is(err, anonymizer.ErrVerificationFailed) {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !strings.Contains(err.Error(), "segment 0") {
		t.Errorf("l'erreur devrait localiser le segment fautif : %q", err.Error())
	}
	if w.walked != 1 {
		t.Errorf("segments parcourus = %d, attendu 1 (arrêt immédiat)", w.walked)
	}
	if len(w.replaced) != 0 {
		t.Errorf("aucun segment ne doit être réécrit, %d l'ont été", len(w.replaced))
	}
}

func TestSampleTextConcatenates(t *testing.T) {
	w := &sliceWalker{segments: []string{"Bonjour", "le", "monde"}}
	got, err := SampleText(w, 1000)
	if err != nil {
		t.Fatalf("SampleText: %v", err)
	}
	want := "Bonjour\nle\nmonde"
	if got != want {
		t.Errorf("texte = %q, attendu %q", got, want)
	}
}

func TestSampleTextStopsEarly(t *testing.T) {
	w := &sliceWalker{segments: []string{"aaaaa", "bbbbb", "ccccc", "ddddd"}}
	got, err := SampleText(w, 8)
	if err != nil {
		t.Fatalf("SampleText: %v", err)
	}
	// Après "aaaaa\nbbbbb" (11 octets) le seuil de 8 est atteint : on s'arrête.
	if want := "aaaaa\nbbbbb"; got != want {
		t.Errorf("texte = %q, attendu %q", got, want)
	}
	if w.walked != 2 {
		t.Errorf("segments parcourus = %d, attendu 2 (arrêt anticipé)", w.walked)
	}
}

// ── Vérification au niveau document ───────────────────────────────────────

// fullNameRecognizer ne reconnaît que la forme complète « Jean Dupont ».
// Il modélise le comportement d'un CRF face à une entité coupée par la
// segmentation : chaque moitié, seule dans son segment, ne ressemble à rien.
type fullNameRecognizer struct{}

func (fullNameRecognizer) Recognize(text string) ([]ner.Entity, error) {
	const name = "Jean Dupont"
	var entities []ner.Entity
	for pos := 0; ; {
		idx := strings.Index(text[pos:], name)
		if idx < 0 {
			break
		}
		start := pos + idx
		entities = append(entities, ner.Entity{
			Text: name, Type: ner.TypePER,
			Start: start, End: start + len(name), Confidence: 1,
		})
		pos = start + len(name)
	}
	return entities, nil
}

// upperCaseRecognizer tague toute suite de capitales — proxy grossier d'un
// modèle qui prendrait le contenu d'un placeholder pour un nom propre.
type upperCaseRecognizer struct{}

var upperRe = regexp.MustCompile(`[A-Z]{4,}`)

func (upperCaseRecognizer) Recognize(text string) ([]ner.Entity, error) {
	var entities []ner.Entity
	for _, loc := range upperRe.FindAllStringIndex(text, -1) {
		entities = append(entities, ner.Entity{
			Text: text[loc[0]:loc[1]], Type: ner.TypePER,
			Start: loc[0], End: loc[1], Confidence: 1,
		})
	}
	return entities, nil
}

func tagAnonymizer(rec anonymizer.Recognizer) *anonymizer.Anonymizer {
	return anonymizer.New(rec, anonymizer.Config{Strategy: anonymizer.TagReplace})
}

// Segments reproduisant une entité coupée en fin de ligne, cas typique du PDF
// et de l'OCR, qui segmentent à la ligne.
func splitNameSegments() []string {
	return []string{"a été reçu par Jean", "Dupont, directeur de cabinet"}
}

// TestDocumentVerification_SeesWhatSegmentsCannot (P3) : le contrôle par
// segment est structurellement aveugle à une entité coupée — aucune de ses
// moitiés n'est une entité. La recomposition la rend visible.
func TestDocumentVerification_SeesWhatSegmentsCannot(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})

	// Sans vérification document : le rapport est vide et le document serait
	// déclaré conforme. C'est exactement la faille que la phase mesure.
	w := &sliceWalker{segments: splitNameSegments()}
	_, report, err := New(anon).ProcessWithReport(w, anonymizer.WithVerification())
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if !report.OK() {
		t.Fatalf("prémisse invalide : la vérification par segment a déjà vu quelque chose (%v)", report.Leaks)
	}

	// Avec vérification document : l'entité réapparaît, à cheval sur les deux
	// segments.
	w = &sliceWalker{segments: splitNameSegments()}
	_, report, err = New(anon, WithDocumentVerification()).
		ProcessWithReport(w, anonymizer.WithVerification())
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(report.DocumentLeaks) != 1 {
		t.Fatalf("fuites document = %d, attendu 1 : %+v", len(report.DocumentLeaks), report.DocumentLeaks)
	}
	if report.OK() {
		t.Error("rapport déclaré conforme malgré une fuite document")
	}

	leak := report.DocumentLeaks[0]
	if want := []int{0, 1}; fmt.Sprint(leak.Segments) != fmt.Sprint(want) {
		t.Errorf("segments chevauchés = %v, attendu %v", leak.Segments, want)
	}
	if leak.Kind != anonymizer.LeakDocumentEntity {
		t.Errorf("nature = %v, attendu %v", leak.Kind, anonymizer.LeakDocumentEntity)
	}
	if leak.Type != ner.TypePER {
		t.Errorf("type = %v, attendu %v", leak.Type, ner.TypePER)
	}
}

// TestDocumentVerification_StrictFails : en strict, la fuite document refuse le
// document, avec la même sentinelle que la vérification par segment.
func TestDocumentVerification_StrictFails(t *testing.T) {
	w := &sliceWalker{segments: splitNameSegments()}
	_, _, err := New(tagAnonymizer(fullNameRecognizer{}), WithStrictDocumentVerification()).
		ProcessWithReport(w)
	if err == nil {
		t.Fatal("le mode strict aurait dû échouer")
	}
	if !errors.Is(err, anonymizer.ErrVerificationFailed) {
		t.Fatalf("erreur inattendue : %v", err)
	}
}

// TestDocumentVerification_NoFalsePositiveOnCleanDocument : une entité
// correctement anonymisée dans son segment ne doit pas ressortir.
func TestDocumentVerification_NoFalsePositiveOnCleanDocument(t *testing.T) {
	w := &sliceWalker{segments: []string{"Jean Dupont habite ici", "rien à signaler"}}
	_, report, err := New(tagAnonymizer(fullNameRecognizer{}), WithDocumentVerification()).
		ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(w.replaced) == 0 {
		t.Fatal("prémisse invalide : le nom n'a pas été remplacé")
	}
	if !report.OK() {
		t.Errorf("faux positif sur document sain : %+v", report.DocumentLeaks)
	}
}

// TestDocumentVerification_IgnoresOwnReplacements : le contrôle repasse sur une
// sortie qui contient les placeholders écrits par l'anonymiseur. Sans exclusion
// des zones sûres, il signalerait ses propres remplacements — un mode strict
// qui crie au loup finit désactivé.
func TestDocumentVerification_IgnoresOwnReplacements(t *testing.T) {
	w := &sliceWalker{segments: []string{"contacter ALPHABET ici", "puis BRAVOTEAM"}}
	_, report, err := New(tagAnonymizer(upperCaseRecognizer{}), WithDocumentVerification()).
		ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	// Prémisse : la sortie contient bien des placeholders en capitales, que le
	// recognizer redétecterait sans la protection.
	joined := strings.Join(w.replaced, " ")
	if !upperRe.MatchString(joined) {
		t.Fatalf("prémisse invalide : pas de capitales dans la sortie %q", joined)
	}
	if !report.OK() {
		t.Errorf("les remplacements ont été pris pour des fuites : %+v", report.DocumentLeaks)
	}
}

func TestSegmentsSpanning(t *testing.T) {
	// Trois segments de 5 octets, séparés par une espace : [0,5) [6,11) [12,17).
	bounds := [][2]int{{0, 5}, {6, 11}, {12, 17}}
	tests := []struct {
		name       string
		start, end int
		want       string
	}{
		{"interne au premier", 1, 4, "[0]"},
		{"à cheval sur deux", 3, 8, "[0 1]"},
		{"à cheval sur trois", 3, 14, "[0 1 2]"},
		{"interne au dernier", 13, 16, "[2]"},
	}
	for _, tt := range tests {
		if got := fmt.Sprint(segmentsSpanning(bounds, tt.start, tt.end)); got != tt.want {
			t.Errorf("%s : segments = %s, attendu %s", tt.name, got, tt.want)
		}
	}
}

// ── Détection multi-vues ──────────────────────────────────────────────────

// TestMultiView_AnonymizesCrossSegmentEntity (P3) : l'entité coupée est
// effectivement remplacée dans les deux segments, alors que la détection par
// segment ne la voit dans aucun.
func TestMultiView_AnonymizesCrossSegmentEntity(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})

	// Prémisse : sans multi-vues, rien n'est remplacé du tout.
	w := &sliceWalker{segments: splitNameSegments()}
	if _, _, err := New(anon).ProcessWithReport(w); err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(w.replaced) != 0 {
		t.Fatalf("prémisse invalide : %d segment(s) déjà remplacé(s)", len(w.replaced))
	}

	// Avec multi-vues : les deux moitiés sont anonymisées.
	w = &sliceWalker{segments: splitNameSegments()}
	if _, _, err := New(anon, WithMultiViewDetection()).ProcessWithReport(w); err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(w.replaced) != 2 {
		t.Fatalf("segments réécrits = %d, attendu 2 : %q", len(w.replaced), w.replaced)
	}
	for _, out := range w.replaced {
		if strings.Contains(out, "Jean") || strings.Contains(out, "Dupont") {
			t.Errorf("le nom subsiste dans la sortie : %q", out)
		}
	}
}

// TestMultiView_ClosesTheLeakSeenByDocumentVerification : les deux mécanismes se
// répondent — ce que la phase 2 signalait, la détection multi-vues le supprime.
func TestMultiView_ClosesTheLeakSeenByDocumentVerification(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})

	w := &sliceWalker{segments: splitNameSegments()}
	_, report, err := New(anon, WithMultiViewDetection(), WithDocumentVerification()).
		ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if !report.OK() {
		t.Errorf("fuite document persistante malgré la détection multi-vues : %+v",
			report.DocumentLeaks)
	}
}

// TestMultiView_StrictAcceptsWhatItRepairs : bout en bout, le document coupé
// passe désormais le mode strict, qu'il échouait auparavant.
func TestMultiView_StrictAcceptsWhatItRepairs(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})

	w := &sliceWalker{segments: splitNameSegments()}
	if _, _, err := New(anon, WithStrictDocumentVerification()).ProcessWithReport(w); err == nil {
		t.Fatal("prémisse invalide : le strict aurait dû refuser le document non réparé")
	}

	w = &sliceWalker{segments: splitNameSegments()}
	if _, _, err := New(anon, WithMultiViewDetection(), WithStrictDocumentVerification()).
		ProcessWithReport(w); err != nil {
		t.Errorf("le document réparé devrait passer le strict : %v", err)
	}
}

// TestMultiView_NoRegressionOnUncutEntity : une entité entière dans son segment
// reste anonymisée une seule fois, sans doublon ni corruption d'offsets.
func TestMultiView_NoRegressionOnUncutEntity(t *testing.T) {
	w := &sliceWalker{segments: []string{"Jean Dupont habite ici", "rien à signaler"}}
	_, _, err := New(tagAnonymizer(fullNameRecognizer{}), WithMultiViewDetection()).
		ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(w.replaced) != 1 {
		t.Fatalf("segments réécrits = %d, attendu 1 : %q", len(w.replaced), w.replaced)
	}
	if got, want := w.replaced[0], "habite ici"; !strings.HasSuffix(got, want) {
		t.Errorf("sortie = %q, le reste du segment devrait être intact", got)
	}
	if strings.Contains(w.replaced[0], "Jean") {
		t.Errorf("le nom subsiste : %q", w.replaced[0])
	}
}

// TestMultiView_DetectionRunsOnSourceText : les vues doivent être construites
// avant toute réécriture. Un walker qui renverrait le texte déjà anonymisé au
// second parcours ferait détecter des placeholders au lieu d'entités.
func TestMultiView_DetectionRunsOnSourceText(t *testing.T) {
	w := &sliceWalker{segments: splitNameSegments()}
	if _, _, err := New(tagAnonymizer(fullNameRecognizer{}), WithMultiViewDetection()).
		ProcessWithReport(w); err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	// Trois parcours attendus : collecte, puis anonymisation (2 segments chacun).
	if w.walked != 2*len(splitNameSegments()) {
		t.Errorf("segments parcourus = %d, attendu %d (collecte + anonymisation)",
			w.walked, 2*len(splitNameSegments()))
	}
}

func TestJoinSegments(t *testing.T) {
	texts := []string{"abc", "de", "fghi"}

	joined, bounds := joinSegments(texts, []int{0, 2})
	if want := "abc fghi"; joined != want {
		t.Errorf("joined = %q, attendu %q", joined, want)
	}
	if want := "[[0 3] [4 8]]"; fmt.Sprint(bounds) != want {
		t.Errorf("bounds = %v, attendu %s", bounds, want)
	}

	// Les bornes doivent délimiter exactement le texte de chaque segment.
	for k, idx := range []int{0, 2} {
		if got := joined[bounds[k][0]:bounds[k][1]]; got != texts[idx] {
			t.Errorf("bornes %d : %q, attendu %q", k, got, texts[idx])
		}
	}
}

func TestProjectEntity_SplitsAtSegmentBoundary(t *testing.T) {
	// "abc de" : segment 5 = "abc" [0,3), segment 9 = "de" [4,6).
	joined := "abc de"
	bounds := [][2]int{{0, 3}, {4, 6}}
	indices := []int{5, 9}

	out := map[int][]ner.Entity{}
	projectEntity(joined, bounds, indices, ner.Entity{
		Text: "abc de", Type: ner.TypePER, Start: 0, End: 6,
	}, out)

	if len(out) != 2 {
		t.Fatalf("segments touchés = %d, attendu 2 : %+v", len(out), out)
	}
	if got := out[5][0]; got.Start != 0 || got.End != 3 || got.Text != "abc" {
		t.Errorf("segment 5 : %+v, attendu [0,3) \"abc\"", got)
	}
	// Les offsets du second segment doivent être relatifs à ce segment, pas à
	// la recomposition : c'est là que se glissent les erreurs de projection.
	if got := out[9][0]; got.Start != 0 || got.End != 2 || got.Text != "de" {
		t.Errorf("segment 9 : %+v, attendu [0,2) \"de\"", got)
	}
}

// ── Vue « bloc » et césure ────────────────────────────────────────────────

// blockSliceWalker ajoute à sliceWalker le contrat optionnel BlockWalker.
type blockSliceWalker struct {
	sliceWalker
	blocks [][]int
}

func (w *blockSliceWalker) Blocks() [][]int { return w.blocks }

// spyRecognizer enregistre chaque texte soumis à la détection, pour vérifier
// quelles recompositions ont réellement été construites.
type spyRecognizer struct {
	seen []string
}

func (s *spyRecognizer) Recognize(text string) ([]ner.Entity, error) {
	s.seen = append(s.seen, text)
	return nil, nil
}

func (s *spyRecognizer) sawExactly(text string) bool {
	for _, t := range s.seen {
		if t == text {
			return true
		}
	}
	return false
}

// TestMultiView_UsesBlockView : un walker exposant BlockWalker doit voir sa vue
// « bloc » construite et soumise à la détection. Sans ce branchement, le PDF
// resterait analysé ligne par ligne, ce qui est le fond du problème — le modèle
// est entraîné sur des phrases.
func TestMultiView_UsesBlockView(t *testing.T) {
	spy := &spyRecognizer{}
	w := &blockSliceWalker{
		sliceWalker: sliceWalker{segments: []string{"une", "deux", "trois", "quatre"}},
		blocks:      [][]int{{0, 1, 2}},
	}

	if _, _, err := New(tagAnonymizer(spy), WithMultiViewDetection()).ProcessWithReport(w); err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	if !spy.sawExactly("une deux trois") {
		t.Errorf("la vue « bloc » n'a pas été soumise à la détection : %q", spy.seen)
	}
}

// TestMultiView_IgnoresBlocksWhenUnsupported : un walker sans BlockWalker perd
// la vue « bloc » sans que rien d'autre ne change.
func TestMultiView_IgnoresBlocksWhenUnsupported(t *testing.T) {
	spy := &spyRecognizer{}
	w := &sliceWalker{segments: []string{"une", "deux", "trois"}}

	if _, _, err := New(tagAnonymizer(spy), WithMultiViewDetection()).ProcessWithReport(w); err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	// Les vues « paires » et « document » restent construites.
	for _, want := range []string{"une deux", "deux trois", "une deux trois"} {
		if !spy.sawExactly(want) {
			t.Errorf("vue %q absente : %q", want, spy.seen)
		}
	}
}

func TestJoinSegments_Hyphenation(t *testing.T) {
	tests := []struct {
		name  string
		texts []string
		want  string
	}{
		{
			// Le cas qui compte : une césure cache un nom au modèle.
			name:  "cesure recollee",
			texts: []string{"a été reçu par Du-", "pont hier"},
			want:  "a été reçu par Dupont hier",
		},
		{
			// Nom propre composé : la majuscule suivante protège le trait d'union.
			name:  "nom compose preserve",
			texts: []string{"il vit à Saint-", "Étienne depuis"},
			want:  "il vit à Saint- Étienne depuis",
		},
		{
			// Un tiret isolé n'est pas une césure.
			name:  "tiret sans lettre avant",
			texts: []string{"liste -", "puis suite"},
			want:  "liste - puis suite",
		},
		{
			name:  "sans cesure",
			texts: []string{"première ligne", "seconde ligne"},
			want:  "première ligne seconde ligne",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			joined, bounds := joinSegments(tt.texts, []int{0, 1})
			if joined != tt.want {
				t.Errorf("joined = %q, attendu %q", joined, tt.want)
			}

			// Invariant dont dépend projectEntity : la portion contribuée par un
			// segment doit rester un préfixe de son texte, sinon les offsets
			// projetés ne sont plus des indices valides dans ce segment.
			for k := range tt.texts {
				contributed := joined[bounds[k][0]:bounds[k][1]]
				if !strings.HasPrefix(tt.texts[k], contributed) {
					t.Errorf("segment %d : %q n'est pas un préfixe de %q",
						k, contributed, tt.texts[k])
				}
			}
		})
	}
}

// TestMultiView_AnonymizesHyphenatedName : bout en bout, un nom coupé par une
// césure est bien anonymisé dans les deux segments.
func TestMultiView_AnonymizesHyphenatedName(t *testing.T) {
	// Le recognizer ne connaît que « Dupont » : sans recollage de la césure, la
	// forme n'existe dans aucune vue.
	w := &sliceWalker{segments: []string{"reçu par Du-", "pont hier"}}
	_, _, err := New(tagAnonymizer(surnameRecognizer{}), WithMultiViewDetection()).
		ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(w.replaced) != 2 {
		t.Fatalf("segments réécrits = %d, attendu 2 : %q", len(w.replaced), w.replaced)
	}
	for _, out := range w.replaced {
		if strings.Contains(out, "Du-") || strings.HasPrefix(out, "pont") {
			t.Errorf("la forme césurée subsiste : %q", out)
		}
	}
}

// surnameRecognizer ne reconnaît que « Dupont », jamais ses fragments.
type surnameRecognizer struct{}

func (surnameRecognizer) Recognize(text string) ([]ner.Entity, error) {
	const name = "Dupont"
	var entities []ner.Entity
	for pos := 0; ; {
		idx := strings.Index(text[pos:], name)
		if idx < 0 {
			break
		}
		start := pos + idx
		entities = append(entities, ner.Entity{
			Text: name, Type: ner.TypePER, Start: start, End: start + len(name), Confidence: 1,
		})
		pos = start + len(name)
	}
	return entities, nil
}

// ── Régions non réécrivables (contenu océrisé) ────────────────────────────

// readOnlyWalker expose du texte que le pipeline peut lire sans le réécrire.
type readOnlyWalker struct {
	sliceWalker
	regions []ReadOnlyRegion
}

func (w *readOnlyWalker) ReadOnlyText() []ReadOnlyRegion { return w.regions }

// TestReadOnlyRegions_ReportedAsLeaks : une entité présente dans du contenu
// bitmap océrisé sortira en clair quoi qu'il arrive. Le document ne doit jamais
// être déclaré conforme dans ce cas.
func TestReadOnlyRegions_ReportedAsLeaks(t *testing.T) {
	w := &readOnlyWalker{
		sliceWalker: sliceWalker{segments: []string{"rien à signaler"}},
		regions: []ReadOnlyRegion{
			{Label: "page 3 (OCR)", Text: "signé Jean Dupont le 4 mars"},
		},
	}

	_, report, err := New(tagAnonymizer(fullNameRecognizer{})).ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	if len(report.RegionLeaks) != 1 {
		t.Fatalf("fuites région = %d, attendu 1 : %+v", len(report.RegionLeaks), report.RegionLeaks)
	}
	if report.OK() {
		t.Error("rapport déclaré conforme malgré une entité irrécupérable")
	}

	leak := report.RegionLeaks[0]
	if leak.Region != "page 3 (OCR)" {
		t.Errorf("région = %q, devrait localiser la page", leak.Region)
	}
	if leak.Kind != anonymizer.LeakUnwritableRegion {
		t.Errorf("nature = %v, attendu %v", leak.Kind, anonymizer.LeakUnwritableRegion)
	}
}

// TestReadOnlyRegions_AlwaysScanned : le contrôle ne dépend d'aucune option.
// Une entité qu'on sait présente et qu'on ne sait pas retirer doit être
// signalée quelle que soit la configuration.
func TestReadOnlyRegions_AlwaysScanned(t *testing.T) {
	newWalker := func() *readOnlyWalker {
		return &readOnlyWalker{
			sliceWalker: sliceWalker{segments: []string{"rien"}},
			regions:     []ReadOnlyRegion{{Label: "page 1 (OCR)", Text: "Jean Dupont"}},
		}
	}

	for _, opts := range [][]Option{nil, {WithMultiViewDetection()}, {WithDocumentVerification()}} {
		_, report, err := New(tagAnonymizer(fullNameRecognizer{}), opts...).
			ProcessWithReport(newWalker())
		if err != nil {
			t.Fatalf("ProcessWithReport : %v", err)
		}
		if len(report.RegionLeaks) == 0 {
			t.Errorf("fuite région non signalée avec %d option(s)", len(opts))
		}
	}
}

// TestReadOnlyRegions_StrictFails : en strict, la présence d'une entité
// irrécupérable refuse le document.
func TestReadOnlyRegions_StrictFails(t *testing.T) {
	w := &readOnlyWalker{
		sliceWalker: sliceWalker{segments: []string{"rien"}},
		regions:     []ReadOnlyRegion{{Label: "page 1 (OCR)", Text: "Jean Dupont"}},
	}

	_, _, err := New(tagAnonymizer(fullNameRecognizer{}), WithStrictDocumentVerification()).
		ProcessWithReport(w)
	if err == nil {
		t.Fatal("le mode strict aurait dû refuser le document")
	}
	if !errors.Is(err, anonymizer.ErrVerificationFailed) {
		t.Fatalf("erreur inattendue : %v", err)
	}
}

// TestReadOnlyRegions_CleanRegionIsSilent : du contenu bitmap sans donnée
// personnelle ne doit rien déclencher.
func TestReadOnlyRegions_CleanRegionIsSilent(t *testing.T) {
	w := &readOnlyWalker{
		sliceWalker: sliceWalker{segments: []string{"rien"}},
		regions:     []ReadOnlyRegion{{Label: "page 1 (OCR)", Text: "facture acquittée"}},
	}

	_, report, err := New(tagAnonymizer(fullNameRecognizer{})).ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if !report.OK() {
		t.Errorf("faux positif sur région saine : %+v", report.RegionLeaks)
	}
}

// ── Vérification visuelle du document produit ─────────────────────────────

// visualWalker simule un format capable de relire le document écrit.
type visualWalker struct {
	sliceWalker
	visual []ReadOnlyRegion
	seen   string // chemin passé à VisualText
}

func (w *visualWalker) VisualText(path string) ([]ReadOnlyRegion, error) {
	w.seen = path
	return w.visual, nil
}

// TestVerifyOutput_DetectsSurvivingSurface : une forme de surface connue de la
// session qui réapparaît à la relecture prouve qu'un remplacement ou un
// caviardage a manqué sa cible. C'est le signal le plus fort du contrôle,
// puisqu'il ne dépend d'aucune redétection.
func TestVerifyOutput_DetectsSurvivingSurface(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})
	w := &visualWalker{
		sliceWalker: sliceWalker{segments: []string{"signé Jean Dupont"}},
	}

	proc := New(anon)
	session, _, err := proc.ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}
	if len(session.OriginalToPlaceholder) == 0 {
		t.Fatal("prémisse invalide : rien n'a été remplacé")
	}

	// Le rendu relu porte encore le nom : le caviardage a raté sa cible.
	w.visual = []ReadOnlyRegion{{Label: "page 1 (relecture)", Text: "signé Jean Dupont"}}

	leaks, err := proc.VerifyOutput(w, "/tmp/sortie.pdf", session)
	if err != nil {
		t.Fatalf("VerifyOutput : %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("le nom resté lisible aurait dû être signalé")
	}
	if w.seen != "/tmp/sortie.pdf" {
		t.Errorf("le contrôle doit porter sur le fichier produit, pas %q", w.seen)
	}
}

// TestVerifyOutput_IgnoresPlaceholders : la relecture voit forcément les
// placeholders écrits par l'anonymiseur, et « ⟦PERSONNE_1_…⟧ » se laisse
// volontiers prendre pour un nom propre. Les signaler rendrait le contrôle
// inutilisable.
func TestVerifyOutput_IgnoresPlaceholders(t *testing.T) {
	anon := tagAnonymizer(upperCaseRecognizer{})
	w := &visualWalker{sliceWalker: sliceWalker{segments: []string{"contacter ALPHABET ici"}}}

	proc := New(anon)
	session, _, err := proc.ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	// La relecture rend exactement la sortie produite.
	w.visual = []ReadOnlyRegion{{Label: "page 1 (relecture)", Text: w.replaced[0]}}
	if !upperRe.MatchString(w.replaced[0]) {
		t.Fatalf("prémisse invalide : pas de capitales dans %q", w.replaced[0])
	}

	leaks, err := proc.VerifyOutput(w, "out.pdf", session)
	if err != nil {
		t.Fatalf("VerifyOutput : %v", err)
	}
	if len(leaks) != 0 {
		t.Errorf("les placeholders ont été pris pour des fuites : %+v", leaks)
	}
}

// TestVerifyOutput_DetectsNeverSeenEntity : la détection fraîche attrape ce que
// le pipeline n'avait jamais vu — une donnée qu'un premier OCR avait mal lue et
// que la relecture déchiffre.
func TestVerifyOutput_DetectsNeverSeenEntity(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})
	w := &visualWalker{
		sliceWalker: sliceWalker{segments: []string{"rien à signaler"}},
		visual: []ReadOnlyRegion{
			{Label: "page 2 (relecture)", Text: "tampon : Jean Dupont"},
		},
	}

	proc := New(anon)
	session, _, err := proc.ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	leaks, err := proc.VerifyOutput(w, "out.pdf", session)
	if err != nil {
		t.Fatalf("VerifyOutput : %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("l'entité jamais vue par le pipeline aurait dû être signalée")
	}
	if leaks[0].Kind != anonymizer.LeakVisualResidue {
		t.Errorf("nature = %v, attendu %v", leaks[0].Kind, anonymizer.LeakVisualResidue)
	}
	if leaks[0].Region != "page 2 (relecture)" {
		t.Errorf("région = %q, devrait localiser la page", leaks[0].Region)
	}
}

// TestVerifyOutput_CleanDocumentIsSilent : un document réellement propre ne
// doit rien déclencher — un contrôle qui crie au loup finit désactivé.
func TestVerifyOutput_CleanDocumentIsSilent(t *testing.T) {
	anon := tagAnonymizer(fullNameRecognizer{})
	w := &visualWalker{
		sliceWalker: sliceWalker{segments: []string{"facture acquittée"}},
		visual:      []ReadOnlyRegion{{Label: "page 1 (relecture)", Text: "facture acquittée"}},
	}

	proc := New(anon)
	session, _, _ := proc.ProcessWithReport(w)
	leaks, err := proc.VerifyOutput(w, "out.pdf", session)
	if err != nil {
		t.Fatalf("VerifyOutput : %v", err)
	}
	if len(leaks) != 0 {
		t.Errorf("faux positif sur document propre : %+v", leaks)
	}
}

// TestVerifyOutput_SkipsWalkersWithoutSupport : un format qui ne sait pas se
// relire ne fournit pas la garantie, sans erreur ni faux signal.
func TestVerifyOutput_SkipsWalkersWithoutSupport(t *testing.T) {
	w := &sliceWalker{segments: []string{"Jean Dupont"}}
	proc := New(tagAnonymizer(fullNameRecognizer{}))
	session, _, _ := proc.ProcessWithReport(w)

	leaks, err := proc.VerifyOutput(w, "out.pdf", session)
	if err != nil || len(leaks) != 0 {
		t.Errorf("leaks=%+v err=%v", leaks, err)
	}
}

// ── Compteurs de précision ────────────────────────────────────────────────

// TestReport_CountsEntitiesByType : le décompte par type est le seul retour
// immédiat sur la précision d'une configuration réglée pour le rappel. Il
// compte les **occurrences**, pas les entités distinctes — c'est ce qu'un
// lecteur du document constate.
func TestReport_CountsEntitiesByType(t *testing.T) {
	w := &sliceWalker{segments: []string{
		"Jean Dupont habite ici",
		"Jean Dupont revient",
		"rien à signaler",
	}}

	_, report, err := New(tagAnonymizer(fullNameRecognizer{})).ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	if got := report.Entities[ner.TypePER]; got != 2 {
		t.Errorf("occurrences PER = %d, attendu 2 (une par mention)", got)
	}
	if got := report.TotalEntities(); got != 2 {
		t.Errorf("total = %d, attendu 2", got)
	}
}

// TestReport_CountsRedactedZones : les zones effacées dans du contenu bitmap
// comptent aussi — elles sont invisibles du décompte des segments.
func TestReport_CountsRedactedZones(t *testing.T) {
	w := &redactingWalker{
		readOnlyWalker: readOnlyWalker{
			sliceWalker: sliceWalker{segments: []string{"rien"}},
			regions:     []ReadOnlyRegion{{Label: "page 1 (OCR)", Text: "signé Jean Dupont"}},
		},
	}

	_, report, err := New(tagAnonymizer(fullNameRecognizer{})).ProcessWithReport(w)
	if err != nil {
		t.Fatalf("ProcessWithReport : %v", err)
	}

	if report.RedactedZones != 1 {
		t.Errorf("zones caviardées = %d, attendu 1", report.RedactedZones)
	}
	if got := report.Entities[ner.TypePER]; got != 1 {
		t.Errorf("l'entité caviardée devrait être comptée : %d", got)
	}
	// Caviardée, donc plus signalée : elle ne sera pas dans le document produit.
	if len(report.RegionLeaks) != 0 {
		t.Errorf("une zone effacée ne devrait plus être une fuite : %+v", report.RegionLeaks)
	}
	if !report.OK() {
		t.Error("le rapport devrait être conforme une fois la zone caviardée")
	}
}

// redactingWalker sait en plus effacer les zones désignées.
type redactingWalker struct {
	readOnlyWalker
	marked [][3]int
}

func (w *redactingWalker) MarkRedaction(region, start, end int) {
	w.marked = append(w.marked, [3]int{region, start, end})
}
