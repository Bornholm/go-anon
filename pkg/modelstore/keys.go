package modelstore

// embeddedPublicKey est la clé publique minisign de confiance pour les releases
// officielles de go-anon-resources, compilée dans le binaire.
//
// La vérification de signature du manifest est donc active par défaut : le
// manifest servi par GitHub Pages doit être accompagné d'une signature valide
// (manifest.json.minisig) produite avec la clé secrète correspondante, sinon le
// chargement échoue (fail-closed). Cf. minisign.go et le guide de publication
// docs/releasing.md du dépôt de ressources.
//
// Format : la ligne base64 d'un fichier .pub minisign (42 octets décodés :
// "Ed" + keyID + clé Ed25519).
const embeddedPublicKey = "RWQwSMlJlchJMBtcvWwXYt4r5WSFEb0KO0Ticpy4QCvfDTvC7yJaC7DC"

// DefaultTrustedKey retourne la clé publique embarquée de confiance, ou nil si
// aucune n'est encore compilée. Une clé nil signifie « pas de vérification de
// signature possible sans clé fournie explicitement (WithTrustedKey) ».
func DefaultTrustedKey() *PublicKey {
	if embeddedPublicKey == "" {
		return nil
	}
	pk, err := ParsePublicKey(embeddedPublicKey)
	if err != nil {
		// Une clé embarquée invalide est un bug de build, pas une condition
		// d'exécution : on préfère nil (avertissement au runtime) à un panic.
		return nil
	}
	return pk
}
