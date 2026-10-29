package anonymizer

import (
	"errors"
	"regexp"
	"strconv"
	"testing"

	"github.com/bornholm/go-anon/pkg/ner"
)

var testIndexKey = HashKey("0123456789abcdef0123456789abcdef")

// persons construit un recognizer qui trouve les noms donnés, dans cet ordre,
// dans le texte qui les réunit.
func persons(names ...string) (*mockRecognizer, string) {
	rec := &mockRecognizer{}
	text := ""
	for i, name := range names {
		if i > 0 {
			text += " et "
		}
		rec.entities = append(rec.entities, ner.Entity{Text: name, Type: ner.TypePER, Start: len(text), End: len(text) + len(name), Confidence: 1.0})
		text += name
	}
	return rec, text
}

func anonymizeWithIndexKey(t *testing.T, names ...string) *Result {
	t.Helper()
	rec, text := persons(names...)
	sess, err := NewSessionFromState(SessionState{Version: SessionStateVersion, Nonce: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := New(rec, Config{Strategy: TagReplace}).Anonymize(text, WithSession(sess), WithIndexKey(testIndexKey))
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	return res
}

func TestWithIndexKey_SameEntitySamePlaceholderWhateverTheOrder(t *testing.T) {
	first := anonymizeWithIndexKey(t, "Jean Dupont", "Marie Martin")
	second := anonymizeWithIndexKey(t, "Marie Martin", "Paul Durand", "jean dupont")

	for _, name := range []string{"Jean Dupont", "Marie Martin"} {
		p := first.OriginalToPlaceholder[name]
		if p == "" {
			t.Fatalf("aucun placeholder pour %q dans %v", name, first.OriginalToPlaceholder)
		}
		want := name
		if name == "Jean Dupont" {
			want = "jean dupont"
		}
		if got := second.OriginalToPlaceholder[want]; got != p {
			t.Errorf("%q : %q au premier appel, %q au second : attendu le même placeholder quel que soit l'ordre", name, p, got)
		}
	}
}

func TestWithIndexKey_AnEarlierPlaceholderDoesNotNameAnotherEntity(t *testing.T) {
	jean := anonymizeWithIndexKey(t, "Jean Dupont").OriginalToPlaceholder["Jean Dupont"]

	later := anonymizeWithIndexKey(t, "Marie Martin")
	if owner, ok := later.Mapping[jean]; ok {
		t.Errorf("le placeholder de Jean Dupont désigne %q dans un texte qui ne le contient pas", owner)
	}
}

func TestWithIndexKey_IndexesStayWithinSixDigits(t *testing.T) {
	pattern := regexp.MustCompile(`^⟦PERSON_([0-9]+)_abc123⟧$`)
	for p := range anonymizeWithIndexKey(t, "Jean Dupont", "Marie Martin", "Paul Durand").Mapping {
		m := pattern.FindStringSubmatch(p)
		if m == nil {
			t.Fatalf("le placeholder %q n'a pas la forme d'un tag", p)
		}
		if index, _ := strconv.Atoi(m[1]); index < 1 || index > maxKeyedIndex {
			t.Errorf("index %d hors de [1, %d]", index, maxKeyedIndex)
		}
	}
}

func TestWithIndexKey_ACollisionTakesTheNextFreeIndex(t *testing.T) {
	anon := New(&mockRecognizer{}, Config{Strategy: TagReplace})
	params := &anonymizeParams{nonce: "abc123", indexKey: testIndexKey}
	ent := ner.Entity{Text: "Jean Dupont", Type: ner.TypePER}
	index := keyedIndex(testIndexKey, ner.TypePER, "jean dupont")

	taken := params.tagPlaceholder("PERSON", index)
	got, err := anon.keyedReplacement(ent, params, map[string]string{taken: "Marie Martin"})
	if err != nil {
		t.Fatal(err)
	}
	if want := params.tagPlaceholder("PERSON", index%maxKeyedIndex+1); got != want {
		t.Errorf("placeholder = %q, attendu %q, l'index suivant", got, want)
	}

	got, err = anon.keyedReplacement(ent, params, map[string]string{taken: "JEAN DUPONT"})
	if err != nil {
		t.Fatal(err)
	}
	if got != taken {
		t.Errorf("placeholder = %q, attendu %q : l'index désigne déjà la même entité", got, taken)
	}
}

func TestWithIndexKey_RequiresAValidKey(t *testing.T) {
	rec, text := persons("Jean Dupont")
	_, err := New(rec, Config{Strategy: TagReplace}).Anonymize(text, WithIndexKey(HashKey("short")))
	if !errors.Is(err, ErrHashKeyTooShort) {
		t.Errorf("err = %v, attendu ErrHashKeyTooShort", err)
	}
}

func TestWithoutIndexKey_IndexesFollowTheOrderOfAppearance(t *testing.T) {
	rec, text := persons("Jean Dupont", "Marie Martin")
	res, err := New(rec, Config{Strategy: TagReplace}).Anonymize(text)
	if err != nil {
		t.Fatal(err)
	}
	if p := res.OriginalToPlaceholder["Jean Dupont"]; !regexp.MustCompile(`^⟦PERSON_1_`).MatchString(p) {
		t.Errorf("Jean Dupont = %q, attendu PERSON_1", p)
	}
}

func TestWithIndexKey_LeavesTheHashStrategyAlone(t *testing.T) {
	rec, text := persons("Jean Dupont")
	plain, err := New(rec, Config{Strategy: Hash}).Anonymize(text, WithHashKey(testIndexKey))
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := New(rec, Config{Strategy: Hash}).Anonymize(text, WithHashKey(testIndexKey), WithIndexKey(testIndexKey))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Text != keyed.Text {
		t.Errorf("le placeholder hash change avec une clé d'index : %q, attendu %q", keyed.Text, plain.Text)
	}
}
