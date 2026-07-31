// Commande convert — réécrire un modèle CRF existant au format v4 (flux),
// sans réentraînement et sans altérer les poids.
//
// Le format v4 réduit fortement le pic mémoire de chargement : les formats
// gob (v1/v2/v3) bufferisent le modèle entier puis reconstruisent les tableaux
// par doublements, là où le v4 alloue chaque tableau à sa taille finale et le
// remplit en flux. Mesuré sur le modèle fr : ~819 Mio de pic RSS en v3 contre
// ~163 Mio en v4, pour une inférence identique et un fichier ~11 % plus petit.
//
// Usage :
//
//	convert -model model.crf.gz -output model_v4.crf.gz
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

	flag.Parse()

	if *modelPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -model et -output sont obligatoires")
		flag.Usage()
		os.Exit(1)
	}

	infoIn, err := os.Stat(*modelPath)
	if err != nil {
		log.Fatalf("stat modèle source : %v", err)
	}

	log.Printf("chargement : %s", *modelPath)

	f, err := os.Open(*modelPath)
	if err != nil {
		log.Fatalf("ouverture modèle source : %v", err)
	}

	// Chargement en lecture seule : les poids sont conservés dans leur
	// représentation compacte, aucune conversion n'a lieu.
	crf, err := model.LoadModel(f)
	f.Close()
	if err != nil {
		log.Fatalf("chargement modèle : %v", err)
	}

	log.Printf("poids chargés : %d entrées (fichier : %.1f Mo)",
		crf.Weights.Len(), float64(infoIn.Size())/1e6)

	out, err := os.Create(*outputPath)
	if err != nil {
		log.Fatalf("création fichier de sortie : %v", err)
	}
	defer out.Close()

	if err := crf.SaveStream(out); err != nil {
		log.Fatalf("sauvegarde modèle : %v", err)
	}

	infoOut, err := os.Stat(*outputPath)
	if err != nil {
		log.Fatalf("stat modèle de sortie : %v", err)
	}

	log.Printf("modèle converti au format v4 : %s (%.1f Mo → %.1f Mo)",
		*outputPath, float64(infoIn.Size())/1e6, float64(infoOut.Size())/1e6)
}
