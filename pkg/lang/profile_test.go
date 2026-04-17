package lang_test

import (
	"testing"

	"github.com/bornholm/go-anon/pkg/lang"
)

func assertFeature(t *testing.T, features map[string]float64, key string, want float64) {
	t.Helper()
	got, ok := features[key]
	if !ok {
		got = 0.0
	}
	if got != want {
		t.Errorf("feature %q : got %v, want %v", key, got, want)
	}
}

// --- Profil français ---

func TestFrenchStopWord(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("le")
	assertFeature(t, f, "lang.isStopWord", 1.0)
}

func TestFrenchStopWordUpperVariant(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("Les")
	assertFeature(t, f, "lang.isStopWord", 1.0)
}

func TestFrenchNominalParticle(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("de")
	assertFeature(t, f, "lang.isNominalParticle", 1.0)
}

func TestFrenchNominalParticleUpperVariant(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("Von")
	assertFeature(t, f, "lang.isNominalParticle", 1.0)
}

func TestFrenchAbbreviation(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("M.")
	assertFeature(t, f, "lang.isAbbreviation", 1.0)
}

func TestFrenchAbbreviationDr(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("Dr.")
	assertFeature(t, f, "lang.isAbbreviation", 1.0)
}

func TestFrenchNormalWordNoFeature(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("chat")
	assertFeature(t, f, "lang.isStopWord", 0.0)
	assertFeature(t, f, "lang.isNominalParticle", 0.0)
	assertFeature(t, f, "lang.isAbbreviation", 0.0)
}

// --- Profil anglais ---

func TestEnglishStopWord(t *testing.T) {
	en := lang.NewEnglishProfile()
	f := en.Features("the")
	assertFeature(t, f, "lang.isStopWord", 1.0)
}

func TestEnglishStopWordUpperVariant(t *testing.T) {
	en := lang.NewEnglishProfile()
	f := en.Features("The")
	assertFeature(t, f, "lang.isStopWord", 1.0)
}

func TestEnglishNominalParticle(t *testing.T) {
	en := lang.NewEnglishProfile()
	f := en.Features("van")
	assertFeature(t, f, "lang.isNominalParticle", 1.0)
}

func TestEnglishAbbreviation(t *testing.T) {
	en := lang.NewEnglishProfile()
	f := en.Features("Dr.")
	assertFeature(t, f, "lang.isAbbreviation", 1.0)
}

func TestEnglishNormalWordNoFeature(t *testing.T) {
	en := lang.NewEnglishProfile()
	f := en.Features("hello")
	assertFeature(t, f, "lang.isStopWord", 0.0)
	assertFeature(t, f, "lang.isNominalParticle", 0.0)
	assertFeature(t, f, "lang.isAbbreviation", 0.0)
}

// --- Cas limites ---

func TestEmptyWord(t *testing.T) {
	fr := lang.NewFrenchProfile()
	f := fr.Features("")
	// Aucune feature ne doit planter ni retourner de valeur incorrecte.
	if len(f) != 0 {
		t.Errorf("mot vide : attendu 0 feature, got %d", len(f))
	}
}

func TestAbbreviationIsCaseSensitive(t *testing.T) {
	// Les abréviations sont sensibles à la casse : "m." ≠ "M."
	fr := lang.NewFrenchProfile()
	f := fr.Features("m.")
	assertFeature(t, f, "lang.isAbbreviation", 0.0)
}
