package cmdutil

import (
	"errors"
	"fmt"

	goanon "github.com/bornholm/go-anon"
)

// HashOptions construit les options d'anonymisation liées à la stratégie Hash.
//
// La clé est lue depuis GOANON_HASH_KEY et jamais depuis un flag : les arguments
// de processus sont visibles dans `ps`, l'historique du shell et les logs
// d'orchestrateur. En son absence, la fonction échoue — sauf si insecure est
// vrai, ce qui rétablit explicitement le SHA-256 non salé historique.
//
// scope compartimente les pseudonymes : deux scopes distincts produisent des
// pseudonymes différents pour la même personne, ce qui casse la corrélation
// inter-dossiers.
func HashOptions(strategy goanon.Strategy, scope string, insecure bool) ([]goanon.AnonymizeOption, error) {
	if strategy != goanon.Hash {
		return nil, nil
	}

	opts := []goanon.AnonymizeOption{}
	if scope != "" {
		opts = append(opts, goanon.WithHashScope(scope))
	}

	key, err := goanon.HashKeyFromEnv()
	if err != nil {
		if insecure {
			return append(opts, goanon.WithInsecureHash()), nil
		}
		if errors.Is(err, goanon.ErrHashKeyNotSet) {
			return nil, fmt.Errorf(
				"la stratégie \"hash\" requiert une clé HMAC : définir %s (32 octets minimum, "+
					"hexadécimal ou base64), ou passer -insecure-hash pour un usage hors production",
				goanon.HashKeyEnvVar)
		}
		return nil, fmt.Errorf("chargement de %s : %w", goanon.HashKeyEnvVar, err)
	}

	return append(opts, goanon.WithHashKey(key)), nil
}
