// Commande prune — élaguer les poids d'un modèle CRF existant pour réduire
// son empreinte disque et mémoire, sans nécessiter de réentraînement.
//
// Usage :
//
//	prune -model model.crf.gz -threshold 0.001 -output model_pruned.crf.gz
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/bornholm/go-anon/pkg/model"
)

func main() {
	modelPath := flag.String("model", "", "chemin du modèle source .crf.gz (obligatoire)")
	outputPath := flag.String("output", "", "chemin du modèle de sortie .crf.gz (obligatoire)")
	threshold := flag.Float64("threshold", 0.001, "seuil d'élagage : supprime les poids |w| < threshold")

	flag.Parse()

	if *modelPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -model et -output sont obligatoires")
		flag.Usage()
		os.Exit(1)
	}

	// Chargement
	log.Printf("chargement : %s", *modelPath)
	infoIn, err := os.Stat(*modelPath)
	if err != nil {
		log.Fatalf("stat modèle source : %v", err)
	}
	f, err := os.Open(*modelPath)
	if err != nil {
		log.Fatalf("ouverture modèle source : %v", err)
	}
	crf, err := model.LoadModelMutable(f)
	f.Close()
	if err != nil {
		log.Fatalf("chargement modèle : %v", err)
	}

	total := crf.Weights.Len()
	log.Printf("poids chargés : %d entrées (fichier : %.1f Mo)", total, float64(infoIn.Size())/1e6)

	// Élagage
	removed := crf.Weights.Prune(*threshold)
	remaining := crf.Weights.Len()
	pct := 100.0 * float64(removed) / float64(total)
	log.Printf("élagage (seuil=%.4f) : %d supprimées / %d (%.1f%%), %d restantes",
		*threshold, removed, total, pct, remaining)

	// Sauvegarde
	out, err := os.Create(*outputPath)
	if err != nil {
		log.Fatalf("création fichier de sortie : %v", err)
	}
	defer out.Close()

	if err := crf.Save(out); err != nil {
		log.Fatalf("sauvegarde modèle : %v", err)
	}

	infoOut, _ := os.Stat(*outputPath)
	log.Printf("modèle sauvegardé : %s (%.1f Mo → %.1f Mo, gain : %.0f%%)",
		*outputPath,
		float64(infoIn.Size())/1e6,
		float64(infoOut.Size())/1e6,
		100.0*(1.0-float64(infoOut.Size())/float64(infoIn.Size())),
	)
}
