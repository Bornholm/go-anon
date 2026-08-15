#!/usr/bin/env python3
"""Découpe un corpus WikiNER en train/dev/test disjoints.

Les découpages livrés avec le dépôt se recouvrent massivement : le test `en`
partage 81,6 % de ses phrases avec l'entraînement, le test `es` 87,2 %, et les
dev sont dans le même état. Un modèle évalué dessus se note sur ce qu'il a
appris par cœur, et l'écart atteint plusieurs points de F1.

Ce script repart du corpus complet, déduplique, puis découpe. Les trois jeux
sont disjoints par construction, pas par chance.

    python scripts/split_wikiner_clean.py \\
        --input data/en/wikiner_en_full.train.wikiner \\
        --outdir data/prod --lang en

La normalisation BIO reproduit `normalizeBIO` de pkg/corpus/wikiner.go :
WikiNER encode en IOB1, où un span qui suit un `O` commence par `I-`. Le reste
du pipeline attend de l'IOB2.
"""

import argparse
import hashlib
import os
import random


def read_wikiner(path):
    """Lit un fichier WikiNER et retourne des phrases [(mot, tag), ...]."""
    sents = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            toks = []
            for tok in line.split(" "):
                parts = tok.rsplit("|", 2)
                if len(parts) != 3 or not parts[0]:
                    continue
                toks.append((parts[0], parts[2]))
            if toks:
                sents.append(toks)
    return sents


def normalize_bio(sent):
    """IOB1 vers IOB2, à l'identique de pkg/corpus/wikiner.go."""
    out = []
    for i, (word, tag) in enumerate(sent):
        if tag == "O" or "-" not in tag:
            out.append((word, "O" if tag == "O" else tag))
            continue
        typ = tag.split("-", 1)[1]
        prev = sent[i - 1][1] if i > 0 else "O"
        prev_typ = prev.split("-", 1)[1] if "-" in prev else None
        if i == 0 or prev == "O" or prev_typ != typ:
            out.append((word, "B-" + typ))
        else:
            out.append((word, "I-" + typ))
    return out


def write_conll(path, sents):
    with open(path, "w", encoding="utf-8") as f:
        for sent in sents:
            for word, tag in sent:
                f.write(f"{word}\t{tag}\n")
            f.write("\n")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--input", required=True, help="corpus WikiNER complet")
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--lang", required=True)
    ap.add_argument("--dev-ratio", type=float, default=0.05)
    ap.add_argument("--test-ratio", type=float, default=0.05)
    ap.add_argument("--seed", type=int, default=42)
    args = ap.parse_args()

    sents = read_wikiner(args.input)
    print(f"{len(sents)} phrases lues")

    # Déduplication sur le texte : deux phrases identiques réparties de part et
    # d'autre du découpage suffisent à contaminer le test.
    seen, uniq = set(), []
    for s in sents:
        key = " ".join(w for w, _ in s)
        if key not in seen:
            seen.add(key)
            uniq.append(normalize_bio(s))
    print(f"{len(uniq)} phrases distinctes ({len(sents) - len(uniq)} doublons retirés)")

    random.Random(args.seed).shuffle(uniq)
    n_test = int(len(uniq) * args.test_ratio)
    n_dev = int(len(uniq) * args.dev_ratio)
    test, dev, train = uniq[:n_test], uniq[n_test:n_test + n_dev], uniq[n_test + n_dev:]

    os.makedirs(args.outdir, exist_ok=True)
    for name, part in (("train", train), ("dev", dev), ("test", test)):
        path = os.path.join(args.outdir, f"{args.lang}_clean_{name}.conll")
        write_conll(path, part)
        digest = hashlib.sha256(open(path, "rb").read()).hexdigest()
        print(f"{path:44} {len(part):7} phrases  sha256 {digest[:16]}…")


if __name__ == "__main__":
    main()
