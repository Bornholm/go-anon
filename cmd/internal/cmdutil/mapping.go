package cmdutil

import (
	"errors"
	"fmt"
	"time"

	"github.com/bornholm/go-anon/pkg/anonymizer/mappingstore"
)

// OpenMappingStore ouvre le store chiffré des tables de ré-identification.
//
// La clé vient de GOANON_MAPPING_KEY (ou du fichier pointé par
// GOANON_MAPPING_KEY_FILE), jamais d'un flag. Sans clé, la fonction échoue :
// écrire un mapping en clair revient à exporter la base des personnes
// concernées, et cela ne doit jamais être le comportement par défaut.
func OpenMappingStore(dir string, ttl time.Duration) (*mappingstore.FileStore, error) {
	key, err := mappingstore.KeyFromEnv()
	if err != nil {
		if errors.Is(err, mappingstore.ErrKeyNotSet) {
			return nil, fmt.Errorf(
				"le store de mappings requiert une clé de chiffrement : définir %s "+
					"(32 octets, hexadécimal ou base64) ou %s",
				mappingstore.KeyEnvVar, mappingstore.KeyFileEnvVar)
		}
		return nil, fmt.Errorf("chargement de la clé de mapping : %w", err)
	}

	var opts []mappingstore.FileStoreOption
	if ttl > 0 {
		opts = append(opts, mappingstore.WithTTL(ttl))
	}
	return mappingstore.NewFileStore(dir, key, opts...)
}
