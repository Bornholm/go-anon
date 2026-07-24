package ner_test

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/ner"
)

// TestGuarantee_KnownLeaksRecall rejoue le corpus des fuites connues
// (testdata/known_leaks_fr.txt) : chaque forme de surface attendue doit être
// couverte par au moins une entité détectée. Ce test matérialise la garantie de
// non-régression du rappel (S9.4) — un commit qui refait manquer un cas
// historique casse la CI.
//
// Il exige un modèle réel, fourni via les variables d'environnement :
//
//	GOANON_TEST_MODEL_FR=/chemin/model_fr.crf.gz \
//	GOANON_TEST_FIRSTNAMES_FR=/chemin/eu_prenoms.txt \  (optionnel, active le preset high-recall)
//	go test ./pkg/ner/ -run TestGuarantee_KnownLeaksRecall
//
// Absent le modèle, le test est ignoré : la CI publique n'embarque pas les
// modèles, mais la garantie reste vérifiable localement et en pré-release.
func TestGuarantee_KnownLeaksRecall(t *testing.T) {
	modelPath := os.Getenv("GOANON_TEST_MODEL_FR")
	if modelPath == "" {
		t.Skip("GOANON_TEST_MODEL_FR non défini : test de rappel des fuites connues ignoré")
	}

	mf, err := os.Open(modelPath)
	if err != nil {
		t.Fatalf("ouverture modèle %q : %v", modelPath, err)
	}
	defer mf.Close()

	m, err := ner.LoadModel(mf)
	if err != nil {
		t.Fatalf("chargement modèle : %v", err)
	}

	opts := []ner.RecognizerOption{ner.WithLanguage("fr")}

	// Le preset high-recall n'a d'effet qu'avec le gazetteer de prénoms.
	var firstNames *features.Gazetteer
	if gazPath := os.Getenv("GOANON_TEST_FIRSTNAMES_FR"); gazPath != "" {
		gf, err := os.Open(gazPath)
		if err != nil {
			t.Fatalf("ouverture gazetteer %q : %v", gazPath, err)
		}
		firstNames, err = features.LoadGazetteer("firstnames", gf)
		gf.Close()
		if err != nil {
			t.Fatalf("chargement gazetteer : %v", err)
		}
		opts = append(opts, ner.WithGazetteers(map[string]*features.Gazetteer{"firstnames": firstNames}))
	}
	opts = append(opts, ner.HighRecall(firstNames)...)

	rec, err := ner.New(m, opts...)
	if err != nil {
		t.Fatalf("initialisation recognizer : %v", err)
	}

	cases, err := loadKnownLeaks("testdata/known_leaks_fr.txt")
	if err != nil {
		t.Fatalf("chargement corpus fuites : %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("corpus de fuites vide")
	}

	for _, tc := range cases {
		entities, err := rec.Recognize(tc.text)
		if err != nil {
			t.Errorf("Recognize(%q) : %v", tc.text, err)
			continue
		}
		for _, want := range tc.surfaces {
			if !surfaceCovered(tc.text, want, entities) {
				// On ne logge PAS la sortie détaillée : le texte est du corpus
				// de test, mais on garde la discipline « pas de contenu superflu ».
				t.Errorf("fuite : forme %q non détectée dans %q", want, tc.text)
			}
		}
	}
}

type leakCase struct {
	text     string
	surfaces []string
}

func loadKnownLeaks(path string) ([]leakCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cases []leakCase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		cases = append(cases, leakCase{
			text:     parts[0],
			surfaces: strings.Split(parts[1], "|"),
		})
	}
	return cases, sc.Err()
}

// surfaceCovered retourne true si want apparaît dans le texte à une position
// couverte (même partiellement) par une entité détectée. La couverture partielle
// suffit : l'anonymiseur remplace le span de l'entité, donc dès qu'une entité
// chevauche l'occurrence de want, la forme ne part pas en clair.
func surfaceCovered(text, want string, entities []ner.Entity) bool {
	from := 0
	for {
		idx := strings.Index(text[from:], want)
		if idx < 0 {
			return false
		}
		start := from + idx
		end := start + len(want)
		for _, e := range entities {
			if e.Start < end && e.End > start {
				return true
			}
		}
		from = start + 1
	}
}
