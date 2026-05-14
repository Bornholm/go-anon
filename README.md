<p align="center">
  <img src="./misc/resources/logo.svg" style="height:150px" />
</p>


# go-anon

Pipeline de **reconnaissance d'entités nommées (NER) et d'anonymisation** pour le français et l'anglais. Zéro dépendance externe — bibliothèque standard Go uniquement.

## Installation

```bash
go get github.com/bornholm/go-anon
```

## Utilisation rapide

```go
import (
    "os"
    goanon "github.com/bornholm/go-anon"
)

// 1. Charger le modèle
f, err := os.Open("model_en.crf.gz")
if err != nil {
    log.Fatal(err)
}
defer f.Close()

m, err := goanon.LoadModel(f)
if err != nil {
    log.Fatal(err)
}

// 2. Construire le recognizer
r, err := goanon.NewRecognizer(m, goanon.WithLanguage("en"))
if err != nil {
    log.Fatal(err)
}

// 3. Détecter les entités
entities, err := r.Recognize("Marie Curie was born in Warsaw.")
// → [{Text:"Marie Curie", Type:"PER", Confidence:0.97}, {Text:"Warsaw", Type:"LOC", ...}]

// 4. Anonymiser
anon := goanon.NewAnonymizer(r, goanon.Config{Strategy: goanon.TagReplace})
result, err := anon.Anonymize("Marie Curie was born in Warsaw.")
// result.Text  → "[PERSON_1] was born in [LOCATION_1]."
// result.Mapping → {"[PERSON_1]": "Marie Curie", "[LOCATION_1]": "Warsaw"}

// 5. Dé-anonymiser
original, err := anon.Deanonymize(result.Text, result.Mapping)
```

## Modèles

Les modèles ne sont **pas inclus** dans la librairie. Ils doivent être fournis explicitement via `LoadModel`. Utiliser `cmd/train` pour entraîner un modèle :

```bash
# Entraîner un modèle français
./bin/train \
  -train data/wikiner_fr_full.train.wikiner \
  -dev   data/wikiner_fr_full.dev.wikiner \
  -lang fr -format wikiner \
  -workers 1 -epochs 20 -lr 0.1 -l2 0.01 \
  -clusters data/brown_clusters_fr.txt \
  -prune-threshold 0.001 \
  -output model_fr.crf.gz
```

| Langue   | Code `WithLanguage` |
| -------- | ------------------- |
| Français | `"fr"`              |
| Anglais  | `"en"`              |

## Stratégies d'anonymisation

| Constante    | Exemple        | Description                        |
| ------------ | -------------- | ---------------------------------- |
| `TagReplace` | `[PERSON_1]`   | Remplacement par tag typé (défaut) |
| `Redact`     | `████`         | Caviardage caractère par caractère |
| `Hash`       | `[PER_a1b2c3]` | Empreinte SHA-256 (6 hex)          |
| `Consistent` | `[PERSON_1]`   | Même entité → même placeholder     |

## Filtres post-NER

```go
r, _ := goanon.NewRecognizer(
    goanon.WithLanguage("fr"),
    goanon.WithPostFilters(
        goanon.MinConfidenceFilter(0.7),   // supprimer < 70 % de confiance
        goanon.MaxTokensFilter(5),          // supprimer les spans > 5 tokens
        goanon.BlocklistFilter(goanon.TypePER, "Monsieur", "Madame"),
    ),
)
```

## Gazetteers et Brown clusters

```go
import "os"

f, _ := os.Open("data/villes.txt")
gaz, _ := goanon.LoadGazetteer("cities", f)

fc, _ := os.Open("data/brown_clusters_fr.txt")
clusters, _ := goanon.LoadBrownClusters(fc)

r, _ := goanon.NewRecognizer(
    goanon.WithLanguage("fr"),
    goanon.WithGazetteers(map[string]*goanon.Gazetteer{"cities": gaz}),
    goanon.WithBrownClusters(clusters),
)
```

## Outils en ligne de commande

```bash
go build -o bin/train        ./cmd/train/
go build -o bin/eval         ./cmd/eval/
go build -o bin/demo         ./cmd/demo/
go build -o bin/prune        ./cmd/prune/
go build -o bin/brown-cluster ./cmd/brown-cluster/
```

| Outil           | Description                                              |
| --------------- | -------------------------------------------------------- |
| `train`         | Entraîner un modèle CRF sur un corpus CoNLL ou WikiNER   |
| `eval`          | Évaluer le F1 (strict type + span) sur un corpus de test |
| `demo`          | Démo interactive / anonymisation depuis stdin            |
| `prune`         | Réduire un modèle existant sans ré-entraînement          |
| `brown-cluster` | Générer des Brown clusters depuis un corpus              |

```bash
# Démo rapide (-model est obligatoire)
echo "Jean Dupont habite à Paris." | ./bin/demo -model model_fr.crf.gz -lang fr -anonymize

# Évaluation
./bin/eval -model model_fr.crf.gz -lang fr \
  -test data/wikiner_fr.test.conll -format conll
```

## Architecture

```
texte brut
  → UnicodeTokenizer     (pkg/tokenizer)  — segmentation rune-par-rune, offsets byte-accurate
  → FeatureExtractor     (pkg/features)   — ~40-60 features/token : morpho, shape, contexte, clusters
  → CRF Viterbi          (pkg/model)      — décodage optimal en BIO
  → decodeEntities       (pkg/ner)        — reconstruction des spans avec scores de confiance
  → EntityFilter[]       (pkg/ner)        — post-filtres chaînables
  → Anonymizer           (pkg/anonymizer) — remplacement en ordre inverse des offsets
```

### Packages

| Package          | Rôle                                                       |
| ---------------- | ---------------------------------------------------------- |
| `pkg/model`      | CRF linéaire : poids, Viterbi, forward-backward, SGD/Adam  |
| `pkg/features`   | Features : morphologie, shape, Brown clusters, gazetteers  |
| `pkg/ner`        | Orchestration NER, décodage BIO→entités, filtres           |
| `pkg/anonymizer` | Remplacement et restauration des entités                   |
| `pkg/corpus`     | Lecture CoNLL et WikiNER, conversion BIO/BIOES             |
| `pkg/tokenizer`  | `UnicodeTokenizer` — offsets byte-précis, FR/EN            |
| `pkg/lang`       | Profils linguistiques : stop-words, préfixes, abréviations |

## Points de vigilance

- Les offsets `Start`/`End` sont des **positions byte UTF-8**, pas des indices de caractères. Les caractères accentués (`é`, `à`) occupent 2 octets.
- `workers > 1` avec peu de phrases cause une divergence Hogwild! — utiliser `-workers 1` sur les petits corpus.
- Le modèle FR de production recommandé est entraîné avec `prune-threshold 0.001` (F1 ≈ 82,4 % sur WikiNER).

## Licence

Voir [LICENSE](LICENSE.md).
