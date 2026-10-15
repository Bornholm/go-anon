package csv

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	"github.com/bornholm/go-anon/pkg/ner"
)

// regexRecognizer détecte les identifiants structurés (e-mail, téléphone) :
// assez pour tester le flux sans modèle.
type regexRecognizer struct{}

func (regexRecognizer) Recognize(text string) ([]ner.Entity, error) {
	return ner.RegexEntityFilter(ner.BuiltinRegexPatterns)(text, nil), nil
}

func processStream(t *testing.T, in string, opts ...StreamOption) (string, *Stream, *docprocessor.Report) {
	t.Helper()
	var out bytes.Buffer
	s, err := NewStream(strings.NewReader(in), &out, opts...)
	if err != nil {
		t.Fatal(err)
	}
	anon := anonymizer.New(regexRecognizer{}, anonymizer.Config{Strategy: anonymizer.TagReplace, ConsistentMap: true})
	session, report, err := docprocessor.New(anon).ProcessStream(s, anonymizer.WithVerification())
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	return out.String(), s, report
}

func TestStream_KeepsFormatAndHeader(t *testing.T) {
	in := bom + "nom;courriel;remarque\r\n" +
		"Dupont;jean.dupont@example.com;\"deux lignes;\r\navec séparateur\"\r\n" +
		"Martin;sophie.martin@example.com;\r\n"
	out, s, report := processStream(t, in)

	if !strings.HasPrefix(out, bom+"nom;courriel;remarque\r\n") {
		t.Errorf("BOM, en-tête ou CRLF perdus :\n%q", out)
	}
	if s.Separator() != ';' || len(s.Header()) != 3 {
		t.Errorf("séparateur %q, en-tête %v", s.Separator(), s.Header())
	}
	if strings.Contains(out, "@example.com") {
		t.Errorf("e-mail resté en clair :\n%s", out)
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(out, bom)))
	r.Comma = ';'
	records, err := r.ReadAll()
	if err != nil || len(records) != 3 || records[1][2] != "deux lignes;\navec séparateur" {
		t.Errorf("table modifiée : %v %q", err, records)
	}
	// Les cellules vides ne sont pas des segments.
	if report.Segments != 5 || report.Entities[ner.EntityType("EMAIL")] != 2 {
		t.Errorf("segments %d, entités %v", report.Segments, report.Entities)
	}
}

// Des lots d'une ligne donnent la même sortie, et une même valeur garde le
// même pseudonyme d'un lot à l'autre : la session couvre tout le fichier.
func TestStream_BatchesShareTheSession(t *testing.T) {
	in := "id,courriel\n1,a@example.com\n2,b@example.com\n3,a@example.com\n"
	out, _, report := processStream(t, in, WithBatchBytes(1))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || lines[0] != "id,courriel" {
		t.Fatalf("sortie :\n%s", out)
	}
	first, third := strings.SplitN(lines[1], ",", 2)[1], strings.SplitN(lines[3], ",", 2)[1]
	if first != third || first == strings.SplitN(lines[2], ",", 2)[1] {
		t.Errorf("pseudonymes incohérents entre lots :\n%s", out)
	}
	if report.Segments != 6 {
		t.Errorf("segments = %d, attendu 6", report.Segments)
	}
}

func TestStream_HeaderModes(t *testing.T) {
	in := "a@example.com,b@example.com\nc@example.com,d@example.com\n"
	out, s, _ := processStream(t, in)
	if s.Header() != nil || strings.Contains(out, "@") {
		t.Errorf("une ligne de données ne doit pas passer pour un en-tête :\n%s", out)
	}
	out, _, _ = processStream(t, "nom,ville\nDupont,Lyon\n", WithHeader(HeaderPresent))
	if !strings.HasPrefix(out, "nom,ville\n") {
		t.Errorf("HeaderPresent :\n%s", out)
	}
	_, s, report := processStream(t, "nom,tel\nDupont,0612345678\n", WithHeader(HeaderNone))
	if s.Header() != nil || report.Segments != 4 {
		t.Errorf("HeaderNone : en-tête %v, segments %d", s.Header(), report.Segments)
	}
}

// Une erreur interrompt le parcours avant l'écriture du lot en cours.
func TestStream_ErrorStopsBeforeWriting(t *testing.T) {
	var out bytes.Buffer
	s, err := NewStream(strings.NewReader("x,y\n1,secret\n2,secret\n"), &out, WithBatchBytes(1), WithHeader(HeaderPresent))
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	calls := 0
	err = s.WalkRows(func(rows []Row) error {
		if calls++; calls == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || out.String() != "x,y\n1,secret\n" {
		t.Errorf("err %v, sortie %q", err, out.String())
	}
	if err := s.WalkRows(func([]Row) error { return nil }); err == nil {
		t.Error("un second parcours doit échouer")
	}
}

func TestStream_Empty(t *testing.T) {
	out, s, report := processStream(t, "")
	if out != "" || s.Header() != nil || report.Segments != 0 {
		t.Errorf("sortie %q, segments %d", out, report.Segments)
	}
}

func TestLooksLikeHeader(t *testing.T) {
	for _, c := range []struct {
		first, second []string
		want          bool
	}{
		{[]string{"id", "nom"}, []string{"1", "Dupont"}, true},
		{[]string{"nom", "téléphone"}, []string{"Dupont", "06 12 34 56 78"}, true},
		{[]string{"nom", "courriel"}, []string{"Dupont", "jean@example.com"}, true},
		{[]string{"nom", "ville"}, []string{"Dupont", "Lyon"}, false},
		{[]string{"1", "Dupont"}, []string{"2", "Martin"}, false},
		{[]string{"nom", "nom"}, []string{"Dupont", "1"}, false},
		{[]string{"nom", ""}, []string{"Dupont", "1"}, false},
		{[]string{"nom"}, []string{"1"}, false},
	} {
		if got := LooksLikeHeader(c.first, c.second); got != c.want {
			t.Errorf("LooksLikeHeader(%q, %q) = %v", c.first, c.second, got)
		}
	}
}

func TestSampleStream(t *testing.T) {
	s, err := NewStream(strings.NewReader("id,texte\n1,Bonjour à tous\n2,Au revoir\n"), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	sample, err := docprocessor.SampleStream(s, 4)
	if err != nil || sample != "1\nBonjour à tous" {
		t.Errorf("échantillon %q, %v", sample, err)
	}
}
