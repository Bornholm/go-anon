# AGENTS.md

This file provides guidance to LLM agents when working with code in this repository.

## Commands

```bash
# Tests
go test ./...                              # suite complète
go test ./pkg/ner/ -run TestRoundTrip      # un test précis
go test ./pkg/model/ -count=1             # sans cache

# Build des binaires (dans bin/)
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

# Démonstration / anonymisation interactive
cat test.txt | ./bin/demo -lang fr -model model_fr_pruned.crf.gz -anonymize
echo "Jean Dupont habite à Paris." | ./bin/demo -lang fr -model model_fr.crf.gz

# Génération de Brown clusters
./bin/brown-cluster -input data/wikiner_fr_full.train.wikiner \
  -format wikiner -vocab 10000 -clusters 200 \
  -output data/brown_clusters_fr.txt
```

## Architecture

Le projet est un pipeline NER (Named Entity Recognition) autonome, sans dépendance externe. Il est utilisé par le plugin anonymiseur de Xolo.

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

| Package          | Rôle                                                                                                        |
| ---------------- | ----------------------------------------------------------------------------------------------------------- |
| `pkg/model`      | CRF linéaire : poids, Viterbi, forward-backward, entraînement SGD/Adam, sérialisation gob+gzip              |
| `pkg/features`   | Extraction de features : morphologie, n-grammes, shape, Brown clusters, word embeddings (GloVe), gazetteers |
| `pkg/ner`        | Orchestration : `Recognizer`, décodage BIO→entités, évaluation F1, post-filtres, corrections BIO            |
| `pkg/anonymizer` | Remplacement/restauration des entités dans le texte original                                                |
| `pkg/corpus`     | Lecture CoNLL et WikiNER, normalisation BIO, conversion BIO↔BIOES                                           |
| `pkg/tokenizer`  | `UnicodeTokenizer` — offsets byte-précis, options FR/EN (apostrophe, trait d'union)                         |
| `pkg/lang`       | Profils linguistiques : stop-words, préfixes honorifiques, features spécifiques FR/EN                       |

### SparseWeights — double représentation

`pkg/model/crf.go` : `SparseWeights` a deux modes, sélectionnés par `W == nil` :

- **Training** (`W map[uint64]float64`) : lecture/écriture O(1), ~40 octets/entrée
- **Inférence** (`Keys []uint64 + Vals []float32` triés) : lecture seule O(log N), ~12 octets/entrée

`Compact()` est appelé automatiquement par `LoadModel()`. `LoadModelMutable()` conserve la map pour pouvoir appeler `Prune()` puis re-sauvegarder.

### Segmentation du texte pour l'inférence

`Recognizer.Recognize` découpe le texte à deux niveaux avant de passer au CRF :

1. Par ligne (`\n`) — empêche les spans de traverser les frontières de paragraphe
2. Par token de ponctuation (`.`, `!`, `?`, `;` par défaut) — configurable via `WithSentenceBoundaries`

Les offsets `Start`/`End` des entités sont toujours des positions **byte** dans le texte original complet.

### Modèles embarqués

`pkg/ner/models/` contient les modèles pré-entraînés embarqués via `//go:embed`. Seul `en.crf.gz` est versionné. `fr.crf.gz` doit être généré avec `cmd/train` et placé dans ce répertoire. Le modèle de production FR est `model_fr_pruned.crf.gz` (threshold=0.001, F1=82.4% sur WikiNER).

### Formats de corpus

- **CoNLL** : une ligne par token (`FORME ... TAG`), phrases séparées par lignes vides. Colonnes configurables.
- **WikiNER** : une phrase par ligne, tokens `FORME|POS|NER` séparés par espaces. `WikiNERReader` applique `normalizeBIO()` qui convertit les `I-X` initiaux en `B-X` (WikiNER utilise un schéma flat).

### Post-filtres (`pkg/ner/filter.go`)

Chaînés via `WithPostFilters(...)` sur le `Recognizer`. Trois filtres built-in :

- `MinConfidenceFilter(threshold)` — supprime les entités sous le seuil de confiance
- `MaxTokensFilter(n)` — supprime les entités dépassant n tokens (spans anormalement longs)
- `BlocklistFilter(type, words...)` — supprime les entités d'un type dont **tous** les tokens sont dans la liste (utile pour éviter que des titres de poste soient étiquetés PER)

### Points de vigilance

- Les offsets sont des **positions byte UTF-8**, pas des positions caractère. Les caractères accentués (`à`, `é`) occupent 2 octets. `strings.Index` est correct ; compter les caractères manuellement est une source classique de bugs (voir `spanEntity()` dans les tests de l'anonymizer).
- `workers > 1` avec peu de phrases entraîne une divergence Hogwild! — utiliser `-workers 1` sur les petits corpus.
- Le schéma BIOES nécessite ~12× plus de données que BIO pour converger ; préférer BIO par défaut.
