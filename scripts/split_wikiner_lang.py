#!/usr/bin/env python3
"""
Découpe les fichiers aij-wikiner-<lang>-wp*.bz2 en train / dev / test pour go-anon.

Usage :
    python scripts/split_wikiner_lang.py \
        --lang    es \
        --input   aij-wikiner-es-wp2.bz2 aij-wikiner-es-wp3.bz2 \
        --out-dir data/es/

Sortie :
    data/es/wikiner_es_full.train.wikiner   (~98 % des phrases, format WikiNER)
    data/es/wikiner_es_full.dev.wikiner     (~1 %  des phrases, format WikiNER)
    data/es/wikiner_es.test.conll           (~1 %  des phrases, format CoNLL)
"""

import argparse
import bz2
import random

TRAIN_RATIO = 0.98
DEV_RATIO   = 0.01
SEED        = 42


def read_wikiner(path: str) -> list[str]:
    opener = bz2.open if path.endswith(".bz2") else open
    with opener(path, "rt", encoding="utf-8") as f:
        return [l.rstrip("\n") for l in f if l.strip()]


def wikiner_to_conll(line: str) -> list[tuple[str, str, str]]:
    """Convertit une ligne WikiNER en liste de (mot, pos, tag) en BIO."""
    tokens = []
    prev_entity = None
    for token in line.split():
        parts = token.split("|")
        if len(parts) != 3:
            continue
        word, pos, tag = parts
        if tag.startswith("I-"):
            entity = tag[2:]
            tag = ("B-" if entity != prev_entity else "I-") + entity
            prev_entity = entity
        else:
            prev_entity = None
        tokens.append((word, pos, tag))
    return tokens


def write_wikiner(path: str, lines: list[str]) -> None:
    with open(path, "w", encoding="utf-8") as f:
        for line in lines:
            f.write(line + "\n")


def write_conll(path: str, sentences: list[list[tuple[str, str, str]]]) -> None:
    with open(path, "w", encoding="utf-8") as f:
        for sent in sentences:
            for word, pos, tag in sent:
                f.write(f"{word}\t{pos}\t{tag}\n")
            f.write("\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--lang",    required=True,
                        help="code de langue ISO 639-1 (ex: es, en)")
    parser.add_argument("--input",   required=True, nargs="+",
                        help="fichiers source (ex: aij-wikiner-es-wp2.bz2)")
    parser.add_argument("--out-dir", default=None,
                        help="répertoire de sortie (défaut : data/<lang>/)")
    parser.add_argument("--seed",    type=int, default=SEED,
                        help=f"graine aléatoire (défaut : {SEED})")
    args = parser.parse_args()

    lang = args.lang
    out  = (args.out_dir or f"data/{lang}").rstrip("/")
    rng  = random.Random(args.seed)

    lines: list[str] = []
    for path in args.input:
        print(f"Lecture {path}…")
        chunk = read_wikiner(path)
        print(f"  {len(chunk)} phrases")
        lines.extend(chunk)
    print(f"Total : {len(lines)} phrases")

    rng.shuffle(lines)
    n_train = int(len(lines) * TRAIN_RATIO)
    n_dev   = int(len(lines) * DEV_RATIO)
    train_lines = lines[:n_train]
    dev_lines   = lines[n_train:n_train + n_dev]
    test_lines  = lines[n_train + n_dev:]

    train_path = f"{out}/wikiner_{lang}_full.train.wikiner"
    dev_path   = f"{out}/wikiner_{lang}_full.dev.wikiner"
    test_path  = f"{out}/wikiner_{lang}.test.conll"

    write_wikiner(train_path, train_lines)
    write_wikiner(dev_path,   dev_lines)
    print(f"  train → {train_path} ({len(train_lines)} phrases)")
    print(f"  dev   → {dev_path}   ({len(dev_lines)} phrases)")

    test_sents = [wikiner_to_conll(l) for l in test_lines]
    write_conll(test_path, test_sents)
    print(f"  test  → {test_path} ({len(test_sents)} phrases, format CoNLL)")


if __name__ == "__main__":
    main()
