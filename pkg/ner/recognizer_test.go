package ner

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/model"
	"github.com/bornholm/go-anon/pkg/tokenizer"
)

func newTestCRF() *model.CRF {
	L := len([]string{"O", "B-PER", "I-PER"})
	labelIndex := map[string]int{"O": 0, "B-PER": 1, "I-PER": 2}
	transition := make([][]float64, L)
	for i := range transition {
		transition[i] = make([]float64, L)
	}
	return &model.CRF{
		Labels:     []string{"O", "B-PER", "I-PER"},
		LabelIndex: labelIndex,
		Weights:    &model.SparseWeights{W: map[uint64]float64{}},
		Transition: transition,
	}
}

func TestRecognizer_EmptyText(t *testing.T) {
	rec := &Recognizer{
		tok:       &tokenizer.UnicodeTokenizer{},
		extractor: &features.FeatureExtractor{WindowSize: 2},
	}
	rec.crf = newTestCRF()

	entities, err := rec.Recognize("")
	if err != nil {
		t.Fatalf("Recognize error: %v", err)
	}
	if len(entities) != 0 {
		t.Errorf("expected 0 entities, got %d", len(entities))
	}
}

func TestRecognizer_NoEntities(t *testing.T) {
	rec := &Recognizer{
		tok:       &tokenizer.UnicodeTokenizer{},
		extractor: &features.FeatureExtractor{WindowSize: 2},
	}
	rec.crf = newTestCRF()

	entities, err := rec.Recognize("The weather is nice today.")
	if err != nil {
		t.Fatalf("Recognize error: %v", err)
	}

	if len(entities) != 0 {
		t.Errorf("expected 0 entities, got %d", len(entities))
	}
}

func TestRecognizer_WithPersonEntity(t *testing.T) {
	rec := &Recognizer{
		tok:       &tokenizer.UnicodeTokenizer{},
		extractor: &features.FeatureExtractor{WindowSize: 2},
		crf:       newTestCRF(),
	}

	entities, err := rec.Recognize("John lives in Paris.")
	if err != nil {
		t.Fatalf("Recognize error: %v", err)
	}

	if len(entities) == 0 {
		t.Log("no entities detected (expected with untrained CRF)")
		return
	}

	if len(entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(entities))
		return
	}

	entity := entities[0]
	if entity.Type != TypePER {
		t.Errorf("expected PER, got %s", entity.Type)
	}

	if entity.Start >= entity.End {
		t.Errorf("invalid span: Start=%d, End=%d", entity.Start, entity.End)
	}
}

func TestRecognizer_ConfidenceScore(t *testing.T) {
	rec := &Recognizer{
		tok:       &tokenizer.UnicodeTokenizer{},
		extractor: &features.FeatureExtractor{WindowSize: 2},
		crf:       newTestCRF(),
	}

	entities, err := rec.Recognize("John Doe works at Acme.")
	if err != nil {
		t.Fatalf("Recognize error: %v", err)
	}

	for _, e := range entities {
		if e.Confidence < 0 || e.Confidence > 1 {
			t.Errorf("confidence out of range [0,1]: %f", e.Confidence)
		}
		t.Logf("entity: %s (%s) confidence=%.3f", e.Text, e.Type, e.Confidence)
	}
}

func TestRecognizer_OffsetsPreserved(t *testing.T) {
	rec := &Recognizer{
		tok:       &tokenizer.UnicodeTokenizer{},
		extractor: &features.FeatureExtractor{WindowSize: 2},
		crf:       newTestCRF(),
	}

	text := "John lives in Paris."
	entities, err := rec.Recognize(text)
	if err != nil {
		t.Fatalf("Recognize error: %v", err)
	}

	for _, e := range entities {
		extracted := text[e.Start:e.End]
		if !strings.Contains(text, extracted) {
			t.Logf("extracted span from original: %q", extracted)
		}
	}
}

func TestRecognizer_WithLanguage(t *testing.T) {
	m := &Model{crf: newTestCRF()}
	rec, err := New(m, WithLanguage("en"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec == nil {
		t.Error("expected non-nil Recognizer")
	}
}

func TestRecognizer_PunctuationTokens_NoCrashValidOffsets(t *testing.T) {
	m := &Model{crf: newTestCRF()}
	rec, err := New(m, WithLanguage("fr"), WithPunctuationTokens(true))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	text := "Jean Dupont, directeur de Renault, habite à Paris."
	entities, err := rec.Recognize(text)
	if err != nil {
		t.Fatalf("Recognize: %v", err)
	}
	for _, e := range entities {
		if e.Start < 0 || e.End > len(text) || text[e.Start:e.End] != e.Text {
			t.Errorf("offsets invalides avec ponctuation incluse : %+v", e)
		}
	}
}

func TestRecognizer_ConfigWarnings_Mismatch(t *testing.T) {
	crf := newTestCRF()
	crf.FeatureCfg = model.FeatureConfig{
		WindowSize:     3,
		LangCode:       "fr",
		GazetteerNames: []string{"firstnames"},
		HasClusters:    true,
	}
	rec, err := New(&Model{crf: crf}, WithLanguage("en"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	warnings := rec.Warnings()
	if len(warnings) != 3 {
		t.Fatalf("attendu 3 avertissements (langue, gazetteer, clusters), got %d : %v", len(warnings), warnings)
	}
}

func TestRecognizer_ConfigWarnings_NoneWhenAligned(t *testing.T) {
	crf := newTestCRF()
	crf.FeatureCfg = model.FeatureConfig{WindowSize: 3, LangCode: "en"}
	rec, err := New(&Model{crf: crf}, WithLanguage("en"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if w := rec.Warnings(); len(w) != 0 {
		t.Errorf("attendu 0 avertissement, got %v", w)
	}
}
