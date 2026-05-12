#!/usr/bin/env python3
"""
Génère les fichiers gazetteers fr_prenoms.txt et fr_communes.txt.

Sources prénoms (deux options, couverture croissante) :
  Option A — fichier national INSEE (~37k prénoms) :
    https://www.insee.fr/fr/statistiques/7633685
    Archive ZIP contenant nat<annee>_csv.txt (séparateur ;)
    Colonnes : sexe;preusuel;annais;nombre

  Option B — fichier régional data.gouv.fr (~48k prénoms, recommandé) :
    https://www.data.gouv.fr/datasets/fichier-des-prenoms-depuis-1900
    Fichier Parquet, colonne "prenom"
    Dépendance : pip install pyarrow

  La différence vient de l'agrégation sous "_PRENOMS_RARES" dans le fichier
  national : les prénoms rares à l'échelle nationale mais présents dans
  plusieurs régions apparaissent individuellement dans le fichier régional.

Source communes :
    https://www.data.gouv.fr/datasets/communes-et-villes-de-france-en-csv-excel-json-parquet-et-feather
    Fichier CSV (séparateur ,), colonne "nom_standard"

Usage :
    # Avec le ZIP INSEE (aucune dépendance externe)
    python scripts/build_gazetteers.py \
        --prenoms-zip  nat2022_csv.zip \
        --communes-csv communes-france-2025.csv

    # Avec le Parquet data.gouv.fr (couverture maximale, nécessite pyarrow)
    python scripts/build_gazetteers.py \
        --prenoms-parquet prenoms-2024.parquet \
        --communes-csv    communes-france-2025.csv
"""

import argparse
import csv
import sys
import zipfile


def build_prenoms_zip(zip_path: str, out_path: str) -> None:
    """Lit le fichier national INSEE (archive ZIP, CSV séparateur ;)."""
    prenoms: set[str] = set()

    with zipfile.ZipFile(zip_path) as zf:
        csv_names = [n for n in zf.namelist() if n.endswith(".txt") or n.endswith(".csv")]
        if not csv_names:
            print(f"Erreur : aucun fichier .txt/.csv trouvé dans {zip_path}", file=sys.stderr)
            sys.exit(1)

        with zf.open(csv_names[0]) as f:
            reader = csv.DictReader(
                (line.decode("utf-8") for line in f),
                delimiter=";",
            )
            for row in reader:
                prenom = row.get("preusuel", "").strip()
                if prenom and not prenom.startswith("_"):
                    prenoms.add(prenom.lower())

    _write_sorted(out_path, prenoms)
    print(f"  prénoms → {out_path} ({len(prenoms)} entrées)")


def build_prenoms_parquet(parquet_path: str, out_path: str) -> None:
    """Lit le fichier régional data.gouv.fr (Parquet, nécessite pyarrow)."""
    try:
        import pyarrow.parquet as pq
    except ImportError:
        print(
            "Erreur : pyarrow est requis pour lire le Parquet.\n"
            "Installez-le avec : pip install pyarrow\n"
            "Ou utilisez --prenoms-zip avec le fichier INSEE.",
            file=sys.stderr,
        )
        sys.exit(1)

    table = pq.read_table(parquet_path, columns=["prenom"])
    prenoms = {
        row["prenom"].as_py().lower()
        for row in table.to_pylist()
        if row["prenom"] and not row["prenom"].as_py().startswith("_")
    }
    prenoms.discard("")

    _write_sorted(out_path, prenoms)
    print(f"  prénoms → {out_path} ({len(prenoms)} entrées)")


def build_communes(csv_path: str, out_path: str) -> None:
    communes: set[str] = set()

    with open(csv_path, encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            name = row.get("nom_standard", "").strip().lower()
            if name:
                communes.add(name)

    _write_sorted(out_path, communes)
    print(f"  communes → {out_path} ({len(communes)} entrées)")


def _write_sorted(path: str, entries: set[str]) -> None:
    with open(path, "w", encoding="utf-8") as f:
        for entry in sorted(entries):
            f.write(entry + "\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    prenoms_group = parser.add_mutually_exclusive_group()
    prenoms_group.add_argument("--prenoms-zip",     metavar="FICHIER",
                               help="archive ZIP INSEE (nat<annee>_csv.zip) — ~37k prénoms")
    prenoms_group.add_argument("--prenoms-parquet", metavar="FICHIER",
                               help="Parquet data.gouv.fr — ~48k prénoms (nécessite pyarrow)")
    parser.add_argument("--communes-csv", metavar="FICHIER",
                        help="CSV des communes françaises (data.gouv.fr)")
    parser.add_argument("--out-prenoms",  default="data/fr_prenoms.txt", metavar="FICHIER",
                        help="sortie prénoms (défaut : data/fr_prenoms.txt)")
    parser.add_argument("--out-communes", default="data/fr_communes.txt", metavar="FICHIER",
                        help="sortie communes (défaut : data/fr_communes.txt)")
    args = parser.parse_args()

    if not args.prenoms_zip and not args.prenoms_parquet and not args.communes_csv:
        parser.error("au moins un argument source est requis")

    if args.prenoms_zip:
        print(f"Lecture {args.prenoms_zip} (format ZIP/CSV INSEE)…")
        build_prenoms_zip(args.prenoms_zip, args.out_prenoms)

    if args.prenoms_parquet:
        print(f"Lecture {args.prenoms_parquet} (format Parquet data.gouv.fr)…")
        build_prenoms_parquet(args.prenoms_parquet, args.out_prenoms)

    if args.communes_csv:
        print(f"Lecture {args.communes_csv}…")
        build_communes(args.communes_csv, args.out_communes)


if __name__ == "__main__":
    main()
