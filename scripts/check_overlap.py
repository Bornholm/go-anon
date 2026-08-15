#!/usr/bin/env python3
"""Mesure le recouvrement entre un corpus d'entraînement et un jeu d'évaluation.

Un jeu de test qui partage ses phrases avec l'entraînement note le modèle sur
ce qu'il a appris par cœur. Le dépôt en contenait trois sur quatre, et l'écart
atteint 6,3 points de F1 pour un même modèle selon le jeu choisi.

    python scripts/check_overlap.py data/prod/train.conll data/wikiner_fr.test.conll

Le code de sortie vaut 1 au-delà du seuil, pour un usage en pré-commit ou en CI.
"""

import argparse
import sys


def sentences(path):
    """Lit un fichier CoNLL et retourne ses phrases, mots joints par espace."""
    out, cur = [], []
    with open(path, encoding="utf-8") as f:
        for line in f:
            if line.strip() == "":
                if cur:
                    out.append(" ".join(cur))
                    cur = []
            else:
                cur.append(line.split("\t")[0].split(" ")[0])
    if cur:
        out.append(" ".join(cur))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("train", help="corpus d'entraînement, format CoNLL")
    ap.add_argument("eval", nargs="+", help="jeux à vérifier")
    ap.add_argument(
        "--max-overlap",
        type=float,
        default=1.0,
        help="pourcentage toléré avant échec (défaut : 1,0)",
    )
    ap.add_argument("--show", type=int, default=0, help="afficher N phrases partagées")
    args = ap.parse_args()

    train = set(sentences(args.train))
    print(f"entraînement : {len(train)} phrases distinctes\n")

    failed = False
    for path in args.eval:
        sents = sentences(path)
        if not sents:
            print(f"{path} : vide")
            continue
        shared = [s for s in sents if s in train]
        pct = 100 * len(shared) / len(sents)
        verdict = "OK" if pct <= args.max_overlap else "CONTAMINÉ"
        print(f"{path}\n  {len(shared)}/{len(sents)} phrases partagées = {pct:.1f} %  [{verdict}]")
        if pct > args.max_overlap:
            failed = True
            for s in shared[: args.show]:
                print(f"    {s[:100]}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
