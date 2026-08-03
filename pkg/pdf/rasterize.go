package pdf

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultDPI est la résolution de rastérisation par défaut. 300 dpi est le
// plancher usuel pour l'OCR d'un document bureautique : en deçà, les moteurs
// perdent les corps de texte de 8–9 points, courants dans les mentions légales
// et les bas de page — précisément là où se logent les données personnelles.
const DefaultDPI = 300

// Rasterizer rend une page de PDF en image.
//
// Abstrait derrière une interface parce que le rendu est le seul point du
// pipeline qui exige un moteur de rendu complet : il n'est pas question de le
// réimplémenter, et l'outil disponible varie selon l'environnement.
type Rasterizer interface {
	Name() string
	// Available rapporte si le rastériseur est utilisable ici.
	Available() error
	// Render rend la page pageNr (base 1) du fichier path à la résolution dpi.
	Render(path string, pageNr, dpi int) (image.Image, error)
}

// PdftoppmRasterizer pilote pdftoppm (poppler-utils) par exec.
//
// pdftoppm écrit le PNG sur la sortie standard lorsqu'aucun préfixe de fichier
// ne lui est donné : rien ne transite par le disque, ce qui évite de laisser
// traîner des images en clair d'un document contenant des données personnelles.
type PdftoppmRasterizer struct {
	Binary string
}

func NewPdftoppmRasterizer() *PdftoppmRasterizer {
	return &PdftoppmRasterizer{Binary: "pdftoppm"}
}

func (r *PdftoppmRasterizer) Name() string { return "pdftoppm" }

func (r *PdftoppmRasterizer) binary() string {
	if r.Binary != "" {
		return r.Binary
	}
	return "pdftoppm"
}

func (r *PdftoppmRasterizer) Available() error {
	if _, err := exec.LookPath(r.binary()); err != nil {
		return fmt.Errorf("pdf: rastériseur %s indisponible : %w", r.Name(), err)
	}
	return nil
}

func (r *PdftoppmRasterizer) Render(path string, pageNr, dpi int) (image.Image, error) {
	if dpi <= 0 {
		dpi = DefaultDPI
	}
	page := strconv.Itoa(pageNr)

	cmd := exec.CommandContext(context.Background(), r.binary(),
		"-r", strconv.Itoa(dpi), "-png", "-f", page, "-l", page, path)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdf: rendu page %d avec %s : %w : %s",
			pageNr, r.Name(), err, strings.TrimSpace(stderr.String()))
	}

	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("pdf: décodage du rendu page %d : %w", pageNr, err)
	}
	return img, nil
}
