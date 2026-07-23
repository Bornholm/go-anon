package ner

// EntityType représente la catégorie d'une entité nommée.
type EntityType string

const (
	TypePER  EntityType = "PER"
	TypeLOC  EntityType = "LOC"
	TypeORG  EntityType = "ORG"
	TypeMISC EntityType = "MISC"

	TypeAPIKey EntityType = "API_KEY"
	TypeJWT    EntityType = "JWT"
	TypeSecret EntityType = "SECRET"
)

// IsSecretType retourne true pour les types dont la valeur ne doit jamais être
// conservée dans un mapping, hachée de façon stable, ni ré-identifiable.
//
// Ces types (jetons d'API, JWT, mots de passe) ne sont pas des données
// personnelles au sens du RGPD mais des credentials : les conserver dans une
// table de ré-identification transformerait celle-ci en coffre à secrets, et un
// pseudonyme stable permettrait de corréler les usages d'un même secret.
// L'anonymiseur les caviarde donc systématiquement, quelle que soit la stratégie.
func IsSecretType(t EntityType) bool {
	switch t {
	case TypeAPIKey, TypeJWT, TypeSecret:
		return true
	}
	return false
}

// Entity est une entité nommée détectée dans le texte.
type Entity struct {
	Text       string     // forme de surface
	Type       EntityType // PER / LOC / ORG / MISC
	Start      int        // offset byte début dans le texte original (inclusif)
	End        int        // offset byte fin dans le texte original (exclusif)
	Confidence float64    // score de confiance (0.0–1.0)
}
