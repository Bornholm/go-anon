#!/usr/bin/env python3
"""
Génère les gazetteers de prénoms et de patronymes depuis les fichiers
OpenData de l'Union Européenne (data/prenom.csv et data/patronymes.csv).

Ces fichiers couvrent l'ensemble des États membres de l'UE et sont donc
adaptés pour toutes les langues supportées (fr, es, en, …).

Usage :
    # Tous les gazetteers en une commande
    python scripts/build_gazetteers_eu.py \
        --prenoms-csv    data/prenom.csv \
        --patronymes-csv data/patronymes.csv \
        --min-count 5

    # Un seul type
    python scripts/build_gazetteers_eu.py \
        --prenoms-csv data/prenom.csv --out-prenoms data/eu_prenoms.txt

Filtrage :
    --min-count N  ignore les entrées dont le compteur est < N (défaut : 2)
                   Permet d'éliminer les hapax typographiques du corpus EU.
"""

import argparse
import csv
import sys


def _write_sorted(path: str, entries: set[str]) -> None:
    with open(path, "w", encoding="utf-8") as f:
        for entry in sorted(entries):
            f.write(entry + "\n")


def build_prenoms(csv_path: str, out_path: str, min_count: int) -> None:
    prenoms: set[str] = set()
    skipped = 0

    with open(csv_path, encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            prenom = row.get("prenom", "").strip()
            count_str = row.get("sum", row.get("count", "0")).strip()
            if not prenom or prenom.startswith("_"):
                continue
            try:
                count = int(count_str)
            except ValueError:
                count = 0
            if count < min_count:
                skipped += 1
                continue
            # Normaliser : première lettre majuscule, reste minuscule
            prenoms.add(prenom.capitalize())

    _write_sorted(out_path, prenoms)
    print(f"  prénoms → {out_path} ({len(prenoms)} entrées, {skipped} ignorées < {min_count})")


def build_patronymes(csv_path: str, out_path: str, min_count: int) -> None:
    patronymes: set[str] = set()
    skipped = 0

    with open(csv_path, encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            pat = row.get("patronyme", "").strip()
            count_str = row.get("count", "0").strip()
            if not pat or len(pat) <= 1:
                continue
            # Exclure les codes numériques (ex: "838930E")
            if any(c.isdigit() for c in pat):
                continue
            try:
                count = int(count_str)
            except ValueError:
                count = 0
            if count < min_count:
                skipped += 1
                continue
            patronymes.add(pat.capitalize())

    _write_sorted(out_path, patronymes)
    print(f"  patronymes → {out_path} ({len(patronymes)} entrées, {skipped} ignorées < {min_count})")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--prenoms-csv",    metavar="FICHIER",
                        help="CSV OpenData EU des prénoms (colonne prenom + sum)")
    parser.add_argument("--patronymes-csv", metavar="FICHIER",
                        help="CSV OpenData EU des patronymes (colonne patronyme + count)")
    parser.add_argument("--out-prenoms",    default="data/eu_prenoms.txt",
                        metavar="FICHIER",
                        help="sortie prénoms (défaut : data/eu_prenoms.txt)")
    parser.add_argument("--out-patronymes", default="data/eu_patronymes.txt",
                        metavar="FICHIER",
                        help="sortie patronymes (défaut : data/eu_patronymes.txt)")
    parser.add_argument("--min-count", type=int, default=2,
                        help="seuil minimum d'occurrences (défaut : 2)")
    args = parser.parse_args()

    if not args.prenoms_csv and not args.patronymes_csv:
        parser.error("au moins un fichier source est requis (--prenoms-csv ou --patronymes-csv)")

    if args.prenoms_csv:
        print(f"Lecture {args.prenoms_csv}…")
        build_prenoms(args.prenoms_csv, args.out_prenoms, args.min_count)

    if args.patronymes_csv:
        print(f"Lecture {args.patronymes_csv}…")
        build_patronymes(args.patronymes_csv, args.out_patronymes, args.min_count)


if __name__ == "__main__":
    main()
