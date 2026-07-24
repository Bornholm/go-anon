package modelstore

import "testing"

// TestEmbeddedPublicKeyValid garantit que la clé publique compilée dans le
// binaire est parsable. Un mauvais copier-coller de embeddedPublicKey rendrait
// DefaultTrustedKey() nil (vérification silencieusement désactivée) : ce test
// l'attrape en CI. Il tolère une clé vide (phase de transition sans signature).
func TestEmbeddedPublicKeyValid(t *testing.T) {
	if embeddedPublicKey == "" {
		t.Skip("aucune clé embarquée (transition) : vérification par clé explicite uniquement")
	}

	pk := DefaultTrustedKey()
	if pk == nil {
		t.Fatal("embeddedPublicKey est non vide mais DefaultTrustedKey() retourne nil : clé invalide")
	}
	if _, err := ParsePublicKey(embeddedPublicKey); err != nil {
		t.Fatalf("embeddedPublicKey invalide : %v", err)
	}
}
