package ocr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// TesseractExec pilote le binaire tesseract par exec, en lui passant l'image
// sur l'entrée standard et en lisant sa sortie TSV.
//
// Choix du défaut : pas de cgo, donc pas de rupture de la compilation croisée,
// et une mise à jour du moteur sans recompiler. Le coût est un fork par page,
// ce que la posture de couverture accepte.
type TesseractExec struct {
	// Binary est le chemin du binaire ; « tesseract » par défaut.
	Binary string
	// PSM est le mode de segmentation de page (--psm). 3 = automatique, adapté
	// à une page complète. 6 (bloc uniforme) donne parfois mieux sur un encart.
	PSM int
	// ExtraArgs est passé tel quel, après les options construites.
	ExtraArgs []string
}

// Modes de segmentation de page utiles ici.
const (
	// PSMAuto : analyse de mise en page complète. Défaut, meilleur rendement
	// sur un document ordinaire.
	PSMAuto = 3
	// PSMSparse : texte épars, sans analyse de mise en page.
	//
	// **À utiliser pour vérifier un document déjà caviardé.** Un large aplat
	// noir fait classer par l'analyse de mise en page la zone entière — et le
	// texte voisin avec elle — comme non textuelle : tesseract ne rend alors
	// plus rien du tout. Une vérification en PSMAuto conclurait donc « aucune
	// donnée lisible » sur une page où il en reste, c'est-à-dire un contrôle
	// qui passe toujours. C'est le pire défaut possible pour une vérification.
	PSMSparse = 11
)

// NewTesseractExec retourne le moteur avec ses réglages par défaut.
func NewTesseractExec() *TesseractExec {
	return &TesseractExec{Binary: "tesseract", PSM: PSMAuto}
}

// NewTesseractExecSparse retourne le moteur configuré pour relire un document
// caviardé (cf. PSMSparse).
func NewTesseractExecSparse() *TesseractExec {
	return &TesseractExec{Binary: "tesseract", PSM: PSMSparse}
}

func (e *TesseractExec) Name() string { return "tesseract-exec" }

func (e *TesseractExec) binary() string {
	if e.Binary != "" {
		return e.Binary
	}
	return "tesseract"
}

// Available vérifie la présence du binaire.
func (e *TesseractExec) Available() error {
	if _, err := exec.LookPath(e.binary()); err != nil {
		return fmt.Errorf("ocr: moteur %s indisponible : %w", e.Name(), err)
	}
	return nil
}

// Recognize encode l'image en PNG, la pousse dans tesseract et décode le TSV.
func (e *TesseractExec) Recognize(img image.Image, lang string) ([]Word, error) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return nil, fmt.Errorf("ocr: encodage PNG : %w", err)
	}

	args := []string{"stdin", "stdout", "--psm", strconv.Itoa(e.psm()), "-l", TesseractLang(lang)}
	args = append(args, e.ExtraArgs...)
	args = append(args, "tsv")

	cmd := exec.CommandContext(context.Background(), e.binary(), args...)
	cmd.Stdin = &encoded
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ocr: %s : %w : %s", e.Name(), err,
			strings.TrimSpace(stderr.String()))
	}

	return ParseTesseractTSV(&stdout)
}

func (e *TesseractExec) psm() int {
	if e.PSM <= 0 {
		return PSMAuto
	}
	return e.PSM
}

// TesseractLang traduit un code ISO 639-1 vers la nomenclature de tesseract,
// qui utilise l'ISO 639-2/T. Un code inconnu est rendu tel quel : les jeux de
// langues personnalisés (« fra+eng ») restent utilisables.
func TesseractLang(code string) string {
	switch strings.ToLower(code) {
	case "fr":
		return "fra"
	case "en":
		return "eng"
	case "es":
		return "spa"
	case "":
		return "eng"
	default:
		return code
	}
}

// Colonnes du TSV de tesseract, dans l'ordre de son en-tête.
const (
	tsvLevel = iota
	tsvPageNum
	tsvBlockNum
	tsvParNum
	tsvLineNum
	tsvWordNum
	tsvLeft
	tsvTop
	tsvWidth
	tsvHeight
	tsvConf
	tsvText
	tsvColumns
)

// tsvWordLevel est le niveau hiérarchique des mots ; les niveaux inférieurs
// décrivent page, bloc, paragraphe et ligne, et ne portent pas de texte.
const tsvWordLevel = 5

// ParseTesseractTSV décode la sortie TSV de tesseract.
//
// Exporté et découplé de l'exécution : c'est la partie qui peut se tromper
// (colonnes, guillemets, confiances négatives), et elle doit être testable sans
// binaire installé.
//
// Le découpage est un simple split sur tabulation, délibérément — et non
// `encoding/csv`. Tesseract n'échappe pas le champ texte : un mot commençant
// par un guillemet fait démarrer au lecteur CSV une chaîne quotée qui engloutit
// les lignes suivantes, y compris avec LazyQuotes. Un seul caractère mal placé
// sur un scan suffirait donc à faire disparaître la fin d'une page.
func ParseTesseractTSV(r io.Reader) ([]Word, error) {
	scanner := bufio.NewScanner(r)
	// Les lignes restent courtes, mais un scan bruité peut produire des champs
	// texte inattendus : la limite par défaut de 64 kio serait fatale.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var words []Word
	lineIDs := map[[3]int]int{}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		rec := strings.Split(line, "\t")

		if len(rec) < tsvColumns {
			continue
		}
		if rec[tsvLevel] == "level" {
			continue // en-tête
		}
		if atoi(rec[tsvLevel]) != tsvWordLevel {
			continue
		}

		text := strings.TrimSpace(rec[tsvText])
		if text == "" {
			continue
		}

		// Une confiance négative signale un champ non mesuré ; la ramener à 0
		// plutôt que de propager une valeur qui casserait les comparaisons.
		conf := atof(rec[tsvConf]) / 100
		if conf < 0 {
			conf = 0
		}

		key := [3]int{atoi(rec[tsvBlockNum]), atoi(rec[tsvParNum]), atoi(rec[tsvLineNum])}
		id, seen := lineIDs[key]
		if !seen {
			id = len(lineIDs)
			lineIDs[key] = id
		}

		words = append(words, Word{
			Text: text,
			BBox: Rect{
				X: atoi(rec[tsvLeft]), Y: atoi(rec[tsvTop]),
				W: atoi(rec[tsvWidth]), H: atoi(rec[tsvHeight]),
			},
			Confidence: conf,
			Line:       id,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ocr: lecture TSV : %w", err)
	}

	return words, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
