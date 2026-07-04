package langdetect

import "testing"

func TestWhatlangDetectSupportedLanguages(t *testing.T) {
	det := NewWhatlangDetector("fr", "en", "es")

	cases := []struct {
		name string
		text string
		want string
	}{
		{"français", "Jean Dupont habite à Paris et travaille chez Renault depuis dix ans.", "fr"},
		{"anglais", "John Smith lives in London and works for a large company downtown.", "en"},
		{"espagnol", "Juan García vive en Madrid y trabaja en una empresa desde hace años.", "es"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := det.Detect(tc.text)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if res.Lang != tc.want {
				t.Errorf("langue = %q, attendu %q (confiance %.3f, reliable %v)", res.Lang, tc.want, res.Confidence, res.Reliable)
			}
			if !res.Reliable {
				t.Errorf("détection jugée non fiable pour un texte clair (confiance %.3f)", res.Confidence)
			}
		})
	}
}

func TestWhatlangDetectUnreliableOnEmpty(t *testing.T) {
	det := NewWhatlangDetector("fr", "en", "es")
	res, err := det.Detect("")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Reliable {
		t.Errorf("un texte vide ne devrait pas être jugé fiable (langue %q, confiance %.3f)", res.Lang, res.Confidence)
	}
}

func TestWhatlangWhitelistRestriction(t *testing.T) {
	// Restreint à l'anglais uniquement : un texte français doit être ramené à en.
	det := NewWhatlangDetector("en")
	res, err := det.Detect("Jean Dupont habite à Paris et travaille chez Renault depuis dix ans.")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if res.Lang != "en" {
		t.Errorf("avec whitelist {en}, langue = %q, attendu en", res.Lang)
	}
}
