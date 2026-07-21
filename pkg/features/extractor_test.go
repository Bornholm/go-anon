package features_test

import (
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/lang"
)

// assertFeature vérifie qu'une feature a la valeur attendue.
// Si la clé est absente, la valeur est considérée comme 0.0.
func assertFeature(t *testing.T, f map[string]float64, key string, want float64) {
	t.Helper()
	got, ok := f[key]
	if !ok {
		got = 0.0
	}
	if got != want {
		t.Errorf("feature %q : got %v, want %v", key, got, want)
	}
}

// simpleExtractor crée un extracteur minimal sans gazetteers ni profil langue.
func simpleExtractor() *features.FeatureExtractor {
	return &features.FeatureExtractor{WindowSize: 2}
}

// --- Features morphologiques ---

func TestBias(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"hello"}, 0)
	assertFeature(t, f, "bias", 1.0)
}

func TestLowercase(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"John"}, 0)
	assertFeature(t, f, "word.lower=john", 1.0)
}

func TestSuffix2(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "word.suffix2=is", 1.0)
}

func TestSuffix3(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "word.suffix3=ris", 1.0)
}

func TestPrefix2(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "word.prefix2=Pa", 1.0)
}

func TestPrefix3(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "word.prefix3=Par", 1.0)
}

func TestSuffixShortWord(t *testing.T) {
	// Mot plus court que n : retourne le mot entier.
	fe := simpleExtractor()
	f := fe.Features([]string{"Go"}, 0)
	assertFeature(t, f, "word.suffix3=Go", 1.0)
	assertFeature(t, f, "word.prefix3=Go", 1.0)
}

func TestSuffixUTF8(t *testing.T) {
	// suffix/prefix doivent compter les runes, pas les bytes.
	// "très" : t, r, è, s → suffix2 = "ès", prefix2 = "tr"
	fe := simpleExtractor()
	f := fe.Features([]string{"très"}, 0)
	assertFeature(t, f, "word.suffix2=ès", 1.0)
	assertFeature(t, f, "word.prefix2=tr", 1.0)
}

// --- Features orthographiques ---

func TestIsTitle(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "word.isTitle", 1.0)
}

func TestIsNotTitleAllLower(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"paris"}, 0)
	assertFeature(t, f, "word.isTitle", 0.0)
}

func TestIsNotTitleAllUpper(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"PARIS"}, 0)
	assertFeature(t, f, "word.isTitle", 0.0)
}

func TestIsUpper(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"PARIS"}, 0)
	assertFeature(t, f, "word.isUpper", 1.0)
}

func TestIsNotUpperMixed(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "word.isUpper", 0.0)
}

func TestIsDigit(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"42"}, 0)
	assertFeature(t, f, "word.isDigit", 1.0)
}

func TestIsNotDigitAlphanumeric(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"42ème"}, 0)
	assertFeature(t, f, "word.isDigit", 0.0)
}

func TestHasHyphen(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"peut-être"}, 0)
	assertFeature(t, f, "word.hasHyphen", 1.0)
}

func TestHasApostrophe(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"l'homme"}, 0)
	assertFeature(t, f, "word.hasApostrophe", 1.0)
}

// --- Features de forme ---

func TestWordShapeTitleCase(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"John"}, 0)
	assertFeature(t, f, "word.shape=Xxxx", 1.0)
}

func TestWordShapeAllUpper(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"US"}, 0)
	assertFeature(t, f, "word.shape=XX", 1.0)
}

func TestWordShapeWithHyphenAndDigit(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"US-2"}, 0)
	assertFeature(t, f, "word.shape=XX-d", 1.0)
}

func TestWordShapeWithApostrophe(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"l'homme"}, 0)
	assertFeature(t, f, "word.shape=x'xxxxx", 1.0)
}

func TestShortShape(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"John"}, 0)
	// "Xxxx" → "Xx"
	assertFeature(t, f, "word.shortShape=Xx", 1.0)
}

func TestShortShapeAllSame(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"1999"}, 0)
	// "dddd" → "d"
	assertFeature(t, f, "word.shortShape=d", 1.0)
}

// --- Features de contexte ---

func TestContextBOS(t *testing.T) {
	// Premier token : w[-1] = BOS, w[-2] = BOS
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "w[-1].BOS", 1.0)
	assertFeature(t, f, "w[-2].BOS", 1.0)
}

func TestContextEOS(t *testing.T) {
	// Dernier token : w[+1] = EOS, w[+2] = EOS
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 3)
	assertFeature(t, f, "w[+1].EOS", 1.0)
	assertFeature(t, f, "w[+2].EOS", 1.0)
}

func TestContextWindowLeft(t *testing.T) {
	// "Paris" en position 3 : w[-1] = "in", w[-2] = "lives"
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 3)
	assertFeature(t, f, "w[-1].lower=in", 1.0)
	assertFeature(t, f, "w[-2].lower=lives", 1.0)
}

func TestContextWindowRight(t *testing.T) {
	// "John" en position 0 : w[+1] = "lives"
	fe := &features.FeatureExtractor{WindowSize: 1}
	tokens := []string{"John", "lives"}
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "w[+1].lower=lives", 1.0)
}

func TestContextTitleInWindow(t *testing.T) {
	// "Paris" à droite → w[+1].isTitle = 1.0
	fe := &features.FeatureExtractor{WindowSize: 1}
	tokens := []string{"in", "Paris"}
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "w[+1].isTitle", 1.0)
}

func TestContextWindowZero(t *testing.T) {
	// WindowSize=0 : aucune feature de contexte générée.
	fe := &features.FeatureExtractor{WindowSize: 0}
	tokens := []string{"hello"}
	f := fe.Features(tokens, 0)
	for k := range f {
		if len(k) > 2 && k[:2] == "w[" {
			t.Errorf("feature contexte inattendue avec WindowSize=0 : %q", k)
		}
	}
}

// --- Gazetteers ---

func TestGazetteerHit(t *testing.T) {
	gaz, _ := features.LoadGazetteer("cities", strings.NewReader("Paris\nLyon\n"))
	fe := &features.FeatureExtractor{
		WindowSize: 0,
		Gazetteers: map[string]*features.Gazetteer{"cities": gaz},
	}
	f := fe.Features([]string{"Paris"}, 0)
	assertFeature(t, f, "gaz.cities", 1.0)
}

func TestGazetteerMiss(t *testing.T) {
	gaz, _ := features.LoadGazetteer("cities", strings.NewReader("Paris\n"))
	fe := &features.FeatureExtractor{
		WindowSize: 0,
		Gazetteers: map[string]*features.Gazetteer{"cities": gaz},
	}
	f := fe.Features([]string{"Berlin"}, 0)
	if _, ok := f["gaz.cities"]; ok {
		t.Error("gaz.cities ne doit pas être dans les features pour un mot absent")
	}
}

// --- LangProfile ---

func TestLangProfileStopWord(t *testing.T) {
	fe := &features.FeatureExtractor{
		WindowSize:  0,
		LangProfile: lang.NewFrenchProfile(),
	}
	f := fe.Features([]string{"le"}, 0)
	assertFeature(t, f, "lang.isStopWord", 1.0)
}

func TestLangProfileNoFeatureWithoutProfile(t *testing.T) {
	fe := simpleExtractor() // LangProfile = nil
	f := fe.Features([]string{"le"}, 0)
	if _, ok := f["lang.isStopWord"]; ok {
		t.Error("lang.isStopWord ne doit pas apparaître sans LangProfile")
	}
}

// --- Indépendance des maps retournées ---

func TestNoMutation(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"Paris"}
	f1 := fe.Features(tokens, 0)
	f1["injected"] = 99.0

	f2 := fe.Features(tokens, 0)
	if _, ok := f2["injected"]; ok {
		t.Error("la map retournée par Features doit être indépendante entre appels")
	}
}

// --- Bigram features ---

func TestBigramFeatures(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	// Bigram w[-1]+w[0] pour "lives" = "john lives"
	f := fe.Features(tokens, 1)
	assertFeature(t, f, "bigram.w[-1]+w[0]=john lives", 1.0)
	assertFeature(t, f, "bigram.w[0]+w[+1]=lives in", 1.0)
}

func TestBigramFeaturesBOS(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives"}
	// Premier token n'a pas de w[-1]
	f := fe.Features(tokens, 0)
	if _, ok := f["bigram.w[-1]+w[0]="]; ok {
		t.Error("bigram ne doit pas avoir de feature BOS")
	}
}

func TestBigramFeaturesEOS(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives"}
	// Dernier token n'a pas de w[+1]
	f := fe.Features(tokens, 1)
	if _, ok := f["bigram.w[0]+w[+1]="]; ok {
		t.Error("bigram ne doit pas avoir de feature EOS")
	}
}

// --- Morphological features ---

func TestWordLength(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Hello"}, 0)
	assertFeature(t, f, "word.len=5", 1.0)
}

func TestContainsDigit(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Test123"}, 0)
	assertFeature(t, f, "word.hasDigit", 1.0)
}

func TestNoDigit(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"Hello"}, 0)
	assertFeature(t, f, "word.hasDigit", 0.0)
}

func TestAllCapsRatio(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"ABCdef"}, 0)
	assertFeature(t, f, "word.allCapsRatio", 0.5)
}

func TestAllCapsRatioLower(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"abc"}, 0)
	assertFeature(t, f, "word.allCapsRatio", 0.0)
}

func TestAllCapsRatioUpper(t *testing.T) {
	fe := simpleExtractor()
	f := fe.Features([]string{"ABC"}, 0)
	assertFeature(t, f, "word.allCapsRatio", 1.0)
}

// --- Multi-word gazetteer features ---

func TestGazetteerSequence(t *testing.T) {
	gaz, _ := features.LoadGazetteer("cities", strings.NewReader("new york\nparis\n"))
	fe := &features.FeatureExtractor{
		WindowSize: 0,
		Gazetteers: map[string]*features.Gazetteer{"cities": gaz},
	}
	tokens := []string{"New", "York"}
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "gazseq.cities", 1.0)
}

func TestGazetteerSequenceNegative(t *testing.T) {
	gaz, _ := features.LoadGazetteer("cities", strings.NewReader("paris\n"))
	fe := &features.FeatureExtractor{
		WindowSize: 0,
		Gazetteers: map[string]*features.Gazetteer{"cities": gaz},
	}
	tokens := []string{"New", "York"}
	f := fe.Features(tokens, 0)
	if _, ok := f["gazseq.cities"]; ok {
		t.Error("gazseq ne doit pas être présent pour une séquence absente")
	}
}

// --- Context shape features ---

func TestContextShapeBOS(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "wshape[-1]=BOS", 1.0)
}

func TestContextShapeEOS(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 3)
	assertFeature(t, f, "wshape[+1]=EOS", 1.0)
}

func TestContextShapeWindow(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 2)
	// "lives" = "xxxxx" (5 letters), "Paris" = "Xxxx" (5 letters)
	assertFeature(t, f, "wshape[-1]=xxxxx", 1.0)
	assertFeature(t, f, "wshape[+1]=Xxxxx", 1.0)
}

// --- Title Bigram features ---

func TestTitleBigram(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "Smith", "lives", "in", "Paris"}
	// "Smith" a "John" titlecase à gauche
	f := fe.Features(tokens, 1)
	assertFeature(t, f, "titleBigram", 1.0)
}

func TestTitleBigramRight(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "Smith", "lives", "in", "Paris"}
	// "John" a "Smith" titlecase à droite
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "titleBigramRight", 1.0)
}

func TestNoTitleBigram(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"the", "dog", "runs"}
	f := fe.Features(tokens, 1)
	if _, ok := f["titleBigram"]; ok {
		t.Error("titleBigram ne doit pas être présent pour 'dog'")
	}
}

// --- Title Run features ---

func TestTitleRunTwo(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "Smith", "lives"}
	// "John Smith" = run de 2 titlecase
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "titleRun=2", 1.0)
}

func TestTitleRunThree(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "Michael", "Smith", "lives"}
	// run de 3 titlecase
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "titleRun=3", 1.0)
}

func TestTitleRunFivePlus(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "Michael", "David", "Smith", "Jr.", "lives"}
	// run de 5+ titlecase
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "titleRun=5+", 1.0)
}

func TestTitleRunSingle(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"John", "lives", "in", "Paris"}
	f := fe.Features(tokens, 0)
	if _, ok := f["titleRun=1"]; ok {
		t.Error("titleRun ne doit pas être présent pour run de 1")
	}
}

// --- Position features ---

func TestPosFirst(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"Hello", "world"}
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "pos.first", 1.0)
	assertFeature(t, f, "pos.second", 0.0)
	assertFeature(t, f, "pos.last", 0.0)
}

func TestPosSecond(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"Hello", "world", "test"}
	f := fe.Features(tokens, 1)
	assertFeature(t, f, "pos.first", 0.0)
	assertFeature(t, f, "pos.second", 1.0)
}

func TestPosLast(t *testing.T) {
	fe := simpleExtractor()
	tokens := []string{"Hello", "world"}
	f := fe.Features(tokens, 1)
	assertFeature(t, f, "pos.first", 0.0)
	assertFeature(t, f, "pos.last", 1.0)
}

// --- Context suffix features ---

func TestContextSuffix3(t *testing.T) {
	fe := &features.FeatureExtractor{WindowSize: 1}
	tokens := []string{"John", "lives", "in"}
	// "lives" a "John" à gauche, suffixe "ohn"
	f := fe.Features(tokens, 1)
	assertFeature(t, f, "w[-1].suf3=ohn", 1.0)
}

func TestContextSuffix3Right(t *testing.T) {
	fe := &features.FeatureExtractor{WindowSize: 1}
	tokens := []string{"lives", "Paris"}
	// "lives" a "Paris" à droite, suffixe "ris"
	f := fe.Features(tokens, 0)
	assertFeature(t, f, "w[+1].suf3=ris", 1.0)
}

// --- Schéma de features v1 ---

func TestFeatureSchema_WordLen(t *testing.T) {
	tokens := []string{"anticonstitutionnellement"} // 25 runes
	legacy := &features.FeatureExtractor{WindowSize: 1, Schema: features.SchemaLegacy}
	v1 := &features.FeatureExtractor{WindowSize: 1, Schema: features.SchemaV1}

	fLegacy := legacy.Features(tokens, 0)
	fV1 := v1.Features(tokens, 0)

	// Schéma 0 gelé : itos(25) = '0'+25 = 'I'.
	if _, ok := fLegacy["word.len=I"]; !ok {
		t.Errorf("schéma 0 : feature word.len historique absente, features: %v", keysWithPrefix(fLegacy, "word.len"))
	}
	if _, ok := fV1["word.len=25"]; !ok {
		t.Errorf("schéma 1 : word.len=25 absente, features: %v", keysWithPrefix(fV1, "word.len"))
	}
}

func TestFeatureSchema_GazseqBI(t *testing.T) {
	gaz, _ := features.LoadGazetteer("cities", strings.NewReader("new york\n"))
	tokens := []string{"in", "New", "York", "today"}

	legacy := &features.FeatureExtractor{WindowSize: 1, Schema: features.SchemaLegacy, Gazetteers: map[string]*features.Gazetteer{"cities": gaz}}
	v1 := &features.FeatureExtractor{WindowSize: 1, Schema: features.SchemaV1, Gazetteers: map[string]*features.Gazetteer{"cities": gaz}}

	// Schéma 0 : seul le token de départ ("New", idx 1) est marqué.
	fNew := legacy.Features(tokens, 1)
	fYork := legacy.Features(tokens, 2)
	if _, ok := fNew["gazseq.cities"]; !ok {
		t.Error("schéma 0 : gazseq.cities attendu sur le token de départ")
	}
	if _, ok := fYork["gazseq.cities"]; ok {
		t.Error("schéma 0 : York ne doit pas être marqué (comportement historique gelé)")
	}

	// Schéma 1 : B sur "New", I sur "York".
	fNew = v1.Features(tokens, 1)
	fYork = v1.Features(tokens, 2)
	if _, ok := fNew["gazseq.cities.B"]; !ok {
		t.Errorf("schéma 1 : gazseq.cities.B attendu sur New, got %v", keysWithPrefix(fNew, "gazseq"))
	}
	if _, ok := fYork["gazseq.cities.I"]; !ok {
		t.Errorf("schéma 1 : gazseq.cities.I attendu sur York, got %v", keysWithPrefix(fYork, "gazseq"))
	}
	if _, ok := fNew["gazseq.cities.I"]; ok {
		t.Error("schéma 1 : New ne doit pas porter .I")
	}
}

func keysWithPrefix(f map[string]float64, prefix string) []string {
	var out []string
	for k := range f {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}
