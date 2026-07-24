// Commande mappings — administration du store de tables de ré-identification.
//
// Un mapping est une donnée personnelle : le conserver au-delà du nécessaire est
// une non-conformité, et le détruire est l'acte qui rend la sortie pseudonymisée
// anonyme de facto. Cette commande rend ces deux gestes accessibles.
//
// Usage :
//
//	export GOANON_MAPPING_KEY="$(openssl rand -hex 32)"
//	mappings -store ./mappings list
//	mappings -store ./mappings purge
//	mappings -store ./mappings delete dossier-42
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/bornholm/go-anon/cmd/internal/cmdutil"
	"github.com/bornholm/go-anon/pkg/anonymizer/mappingstore"
)

func main() {
	dir := flag.String("store", "mappings", "répertoire du store de mappings chiffrés")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	store, err := cmdutil.OpenMappingStore(*dir, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "erreur : %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	switch args[0] {
	case "list":
		err = runList(ctx, store)
	case "purge":
		err = runPurge(ctx, store)
	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "erreur : delete attend un identifiant")
			os.Exit(1)
		}
		err = runDelete(ctx, store, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "erreur : sous-commande inconnue %q\n", args[0])
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "erreur : %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage : mappings [-store RÉPERTOIRE] SOUS-COMMANDE

Sous-commandes :
  list             inventaire des mappings (métadonnées seulement, aucun déchiffrement)
  purge            suppression des mappings dont la rétention est dépassée
  delete ID...     suppression immédiate des mappings désignés

La clé de déchiffrement est lue depuis %s ou %s.

Options :
`, mappingstore.KeyEnvVar, mappingstore.KeyFileEnvVar)
	flag.PrintDefaults()
}

func runList(ctx context.Context, store *mappingstore.FileStore) error {
	entries, err := store.List(ctx)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("Aucun mapping.")
		return nil
	}

	now := time.Now().UTC()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IDENTIFIANT\tCRÉÉ LE\tEXPIRE LE\tÉTAT\tCLÉ")
	for _, e := range entries {
		expires, state := "—", "valide"
		if !e.ExpiresAt.IsZero() {
			expires = e.ExpiresAt.Format(time.RFC3339)
			if e.Expired(now) {
				state = "expiré"
			}
		} else {
			state = "sans rétention"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%08x\n",
			e.ID, e.CreatedAt.Format(time.RFC3339), expires, state, e.KeyID)
	}
	return w.Flush()
}

func runPurge(ctx context.Context, store *mappingstore.FileStore) error {
	n, err := store.PurgeExpired(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%d mapping(s) purgé(s).\n", n)
	return nil
}

func runDelete(ctx context.Context, store *mappingstore.FileStore, ids []string) error {
	for _, id := range ids {
		if err := store.Delete(ctx, id); err != nil {
			return err
		}
		fmt.Printf("Mapping supprimé : %s\n", id)
	}
	fmt.Fprintln(os.Stderr, "note : la suppression du fichier ne garantit pas l'effacement "+
		"physique des blocs (SSD, systèmes copy-on-write) ; l'effacement opposable "+
		"est la destruction de la clé du compartiment")
	return nil
}
