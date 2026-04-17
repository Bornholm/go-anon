package ner

// EntityType représente la catégorie d'une entité nommée.
type EntityType string

const (
	TypePER  EntityType = "PER"
	TypeLOC  EntityType = "LOC"
	TypeORG  EntityType = "ORG"
	TypeMISC EntityType = "MISC"
)

// Entity est une entité nommée détectée dans le texte.
type Entity struct {
	Text       string     // forme de surface
	Type       EntityType // PER / LOC / ORG / MISC
	Start      int        // offset byte début dans le texte original (inclusif)
	End        int        // offset byte fin dans le texte original (exclusif)
	Confidence float64    // score de confiance (0.0–1.0)
}
