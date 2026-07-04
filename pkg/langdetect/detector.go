// Package langdetect fournit une abstraction de détection automatique de la
// langue d'un texte, indépendante de l'implémentation sous-jacente.
package langdetect

// Result porte le résultat d'une détection de langue.
type Result struct {
	Lang       string  // code ISO 639-1 ("fr", "en", "es"), "" si indéterminé
	Confidence float64 // score de confiance, dépend de l'implémentation
	Reliable   bool    // true si la détection est jugée suffisamment fiable
}

// Detector détecte la langue d'un texte.
type Detector interface {
	Detect(text string) (Result, error)
}
