# AGENTS.md

This file provides guidance to LLM agents when working with code in this repository.

## Commands

```bash
# Tests
go test ./...                              # suite complète
go test ./pkg/ner/ -run TestRoundTrip      # un test précis
go test ./pkg/model/ -count=1             # sans cache

# Build des binaires principaux (server + anon-doc)
make build

# Build des outils d'entraînement (dans bin/)
make build-tools

# Ou individuellement :
go build -o bin/server    ./cmd/server/
go build -o bin/anon-doc  ./cmd/anon-doc/
go build -o bin/train        ./cmd/train/
go build -o bin/eval         ./cmd/eval/
go build -o bin/demo         ./cmd/demo/
go build -o bin/prune        ./cmd/prune/
go build -o bin/brown-cluster ./cmd/brown-cluster/

# Entraînement (exemple FR, corpus complet)
./bin/train \
  -train data/wikiner_fr_full.train.wikiner \
  -dev   data/wikiner_fr_full.dev.wikiner \
  -lang fr -format wikiner \
  -workers 1 -epochs 20 -lr 0.1 -l2 0.01 \
  -clusters data/brown_clusters_fr.txt \
  -prune-threshold 0.001 \
  -output model_fr.crf.gz

# Évaluation F1 (matching strict type + span)
./bin/eval -model model_fr.crf.gz -lang fr \
  -test data/wikiner_fr.test.conll -format conll

# Réduction d'un modèle existant sans ré-entraînement
./bin/prune -model model_fr_full_bio.crf.gz \
  -threshold 0.001 -output model_fr_pruned.crf.gz

# Démonstration / anonymisation interactive (texte brut)
cat test.txt | ./bin/demo -lang fr -model model_fr_pruned.crf.gz -anonymize
echo "Jean Dupont habite à Paris." | ./bin/demo -lang fr -model model_fr.crf.gz

# Anonymisation de documents bureautiques (téléchargement automatique)
# La langue est détectée automatiquement par défaut (-lang auto) ; on peut la
# forcer avec -lang fr|en|es. La détection échantillonne le texte du document.
./bin/anon-doc -model auto \
  -input rapport.docx -output rapport_anon.docx
./bin/anon-doc -model auto -lang fr \
  -input rapport.docx -output rapport_anon.docx
./bin/anon-doc -model auto:fr \
  -input doc.docx -output out.docx -save-mapping mapping.json
# Stratégies disponibles : "tag" (défaut), "redact", "hash"
./bin/anon-doc -model auto -lang fr -strategy redact \
  -input data.csv -output data_anon.csv
# Cache personnalisé, mode offline
./bin/anon-doc -model auto -lang fr \
  -models-cache ~/.cache/go-anon-models -offline \
  -input doc.docx -output out.docx

# Serveur avec téléchargement automatique
./bin/server -models auto
./bin/server -models "fr:auto,en:/path/to/en.crf.gz" -port 8080

# Génération de Brown clusters
./bin/brown-cluster -input data/wikiner_fr_full.train.wikiner \
  -format wikiner -vocab 10000 -clusters 200 \
  -output data/brown_clusters_fr.txt
```

## Architecture

Le projet est un pipeline NER (Named Entity Recognition) autonome, sans dépendance externe.

### Pipeline d'inférence

```
texte brut
  → UnicodeTokenizer        (pkg/tokenizer)   — segmentation rune-par-rune, offsets byte-accurate
  → découpage ligne/phrase  (pkg/ner)          — chaque ligne puis chaque phrase (délimiteurs configurables)
  → FeatureExtractor        (pkg/features)     — ~40-60 features/token : morpho, shape, contexte, clusters, gazetteers
  → CRF.Predict / Viterbi   (pkg/model)        — décodage optimal en BIO
  → decodeEntitiesWithScores (pkg/ner)         — reconstruction des spans avec scores de confiance
  → EntityFilter[]          (pkg/ner)          — post-filtres chaînables (confiance, longueur, blocklist)
  → Anonymizer.Anonymize    (pkg/anonymizer)   — remplacement en ordre inverse des offsets
```

### Packages

| Package            | Rôle                                                                                                         |
| ------------------ | ------------------------------------------------------------------------------------------------------------ |
| `pkg/model`        | CRF linéaire : poids, Viterbi, forward-backward, entraînement SGD/Adam, sérialisation gob+gzip               |
| `pkg/features`     | Extraction de features : morphologie, n-grammes, shape, Brown clusters, word embeddings (GloVe), gazetteers  |
| `pkg/ner`          | Orchestration : `Recognizer`, décodage BIO→entités, évaluation F1, post-filtres, corrections BIO             |
| `pkg/anonymizer`   | Remplacement des entités : stratégies (tag/redact/hash), `Session` cross-segments, passes de post-traitement |
| `pkg/docprocessor` | Interface `Walker` + `Processor` — orchestration de l'anonymisation de documents format-agnostique           |
| `pkg/docx`         | `Walker` DOCX : itération sur les paragraphes, réécriture des runs                                           |
| `pkg/odt`          | `Walker` ODT : parsing XML en mémoire, réécriture in-place, resérialisation ZIP                              |
| `pkg/csv`          | `Walker` CSV/TSV : détection auto du séparateur, anonymisation cellule par cellule                           |
| `pkg/pdf`          | `Walker` PDF (lecture seule via pdfcpu) : extraction texte avec offsets, redact dans le flux de contenu      |
| `pkg/corpus`       | Lecture CoNLL et WikiNER, normalisation BIO, conversion BIO↔BIOES                                            |
| `pkg/tokenizer`    | `UnicodeTokenizer` — offsets byte-précis, options FR/EN (apostrophe, trait d'union)                          |
| `pkg/lang`         | Profils linguistiques : stop-words, préfixes honorifiques, features spécifiques FR/EN                        |
| `pkg/langdetect`   | Interface `Detector` de détection automatique de langue ; implémentation `WhatlangDetector` (whatlanggo)      |
| `pkg/modelstore`   | Téléchargement automatique, cache, vérification SHA-256 et découverte des modèles depuis GitHub Releases      |

## Modèles pré-entraînés

Les modèles sont distribués via GitHub Releases sur le dépôt dédié
[`go-anon-resources`](https://github.com/bornholm/go-anon-resources).

- **Langues disponibles** : français (fr), anglais (en), espagnol (es)
- **Manifest** : `https://bornholm.github.io/go-anon-resources/manifest.json`
- **Cache local** : `os.UserCacheDir()/go-anon/models`

Utilisation avec téléchargement automatique :

```bash
# CLI — téléchargement automatique pour la langue spécifiée
./bin/anon-doc -model auto -lang fr -input doc.docx -output out.docx

# Serveur — charge toutes les langues disponibles
./bin/server -models auto
```

Voir `pkg/modelstore` pour l'API et le fonctionnement.
