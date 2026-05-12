# Entraîner le modèle FR à partir du jeu de données WikiNER

Ce tutoriel couvre la reproduction complète d'un modèle français
(`model_fr.crf.gz`, F1 ≈ 82,4 % sur WikiNER) depuis les données brutes.

## Prérequis

- Go 1.21+ installé et `go build` fonctionnel
- ~4 Go de RAM disponible (entraînement corpus complet)
- ~15 min de temps CPU (workers = nombre de cœurs)

## Étape 1 — Compiler les binaires

```bash
go build -o bin/train         ./cmd/train/
go build -o bin/eval          ./cmd/eval/
go build -o bin/prune         ./cmd/prune/
go build -o bin/brown-cluster ./cmd/brown-cluster/
go build -o bin/demo          ./cmd/demo/
```

## Étape 2 — Obtenir les données WikiNER

Les fichiers suivants doivent être présents dans `data/` :

| Fichier                         | Rôle                                   | Format  |
| ------------------------------- | -------------------------------------- | ------- |
| `wikiner_fr_full.train.wikiner` | Corpus d'entraînement (~261 k phrases) | WikiNER |
| `wikiner_fr_full.dev.wikiner`   | Corpus de validation (~2.4 k phrases)  | WikiNER |
| `wikiner_fr.test.conll`         | Corpus de test (~2.7 k phrases)        | CoNLL   |

### Téléchargement

Les données WikiNER sont hébergées sur le dépôt GitHub du projet FOX (DICE Group) :

```
https://github.com/dice-group/FOX/tree/master/input/Wikiner
```

Le corpus français est réparti sur **deux fichiers** (`wp2` et `wp3`) qu'il faut
combiner pour obtenir les ~266 k phrases nécessaires :

```bash
wget -O aij-wikiner-fr-wp2.bz2 https://github.com/dice-group/FOX/raw/master/input/Wikiner/aij-wikiner-fr-wp2.bz2
wget -O aij-wikiner-fr-wp3.bz2 https://github.com/dice-group/FOX/raw/master/input/Wikiner/aij-wikiner-fr-wp3.bz2
```

### Découper le corpus en train / dev / test

Le script concatène `wp2` et `wp3`, mélange toutes les phrases (graine fixe),
puis découpe en 98 % / 1 % / 1 %. Le corpus de test est converti au format
CoNLL (un token par ligne, tabs).

```bash
python scripts/split_wikiner.py \
  --input   aij-wikiner-fr-wp2.bz2 aij-wikiner-fr-wp3.bz2 \
  --out-dir data/
```

Voir [`scripts/split_wikiner.py`](../scripts/split_wikiner.py)

## Étape 3 — Préparer les gazetteers (optionnel mais recommandé)

Les gazetteers fournissent au modèle une liste de référence de prénoms et de
communes françaises. Les fichiers `data/fr_prenoms.txt` et
`data/fr_communes.txt` sont déjà présents dans ce dépôt ; cette étape est
nécessaire uniquement si vous souhaitez les régénérer depuis les sources
officielles.

### Gazetteers des prénoms

Télécharger l'archive ZIP depuis :
```
https://www.insee.fr/fr/statistiques/7633685
```

```bash
python scripts/build_gazetteers.py \
  --prenoms-zip nat2022_csv.zip \
  --out-prenoms data/fr_prenoms.txt
```

### Gazetteer des communes

**Source** : fichier des communes françaises sur data.gouv.fr :

```
https://www.data.gouv.fr/datasets/communes-et-villes-de-france-en-csv-excel-json-parquet-et-feather
```

```bash
python scripts/build_gazetteers.py \
  --communes-csv  communes-france-2025.csv \
  --out-communes  data/fr_communes.txt
```

### Générer les deux en une commande

```bash
python scripts/build_gazetteers.py \
  --prenoms-zip  nat2022_csv.zip \
  --communes-csv communes-france-2025.csv
```

Voir [`scripts/build_gazetteers.py`](../scripts/build_gazetteers.py)

---

## Étape 4 — Générer les Brown clusters (optionnel mais recommandé)

Les Brown clusters améliorent sensiblement le F1 sur les entités rares.
Le fichier `data/brown_clusters_fr.txt` est déjà fourni dans ce dépôt ;
régénérez-le uniquement si vous souhaitez l'adapter à un autre corpus.

```bash
./bin/brown-cluster \
  -input   data/wikiner_fr_full.train.wikiner \
  -format  wikiner \
  -vocab   10000 \
  -clusters 200 \
  -output  data/brown_clusters_fr.txt
```

Paramètres clés :

| Flag         | Valeur recommandée | Effet                            |
| ------------ | ------------------ | -------------------------------- |
| `-vocab`     | 10 000             | Taille du vocabulaire retenu     |
| `-clusters`  | 200                | Nombre de clusters hiérarchiques |
| `-min-count` | 5 (défaut)         | Ignore les mots rares            |

Durée : ~5 min sur le corpus complet FR.

## Étape 4 — Entraîner le modèle

### 4a. Entraînement de base (sans ressources externes)

```bash
./bin/train \
  -train  data/wikiner_fr_full.train.wikiner \
  -dev    data/wikiner_fr_full.dev.wikiner \
  -lang   fr \
  -format wikiner \
  -workers 1 \
  -epochs 20 \
  -lr     0.1 \
  -l2     0.01 \
  -prune-threshold 0.001 \
  -output model_fr.crf.gz
```

> **Toujours utiliser `-workers 1`.** L'algorithme Hogwild! (mises à jour
> concurrentes sans verrou) provoque une divergence sur ce corpus : le F1
> stagne autour de 3 % au lieu de converger vers 85 %+.

### 4b. Entraînement complet avec Brown clusters et gazetteers (recommandé)

C'est la configuration qui produit le modèle de production (F1 ≈ 82,4 %) :

```bash
./bin/train \
  -train      data/wikiner_fr_full.train.wikiner \
  -dev        data/wikiner_fr_full.dev.wikiner \
  -lang       fr \
  -format     wikiner \
  -workers    1 \
  -epochs     20 \
  -lr         0.1 \
  -l2         0.01 \
  -clusters   data/brown_clusters_fr.txt \
  -gazetteers "firstnames:data/fr_prenoms.txt,locations:data/fr_communes.txt" \
  -prune-threshold 0.001 \
  -output     model_fr_pruned.crf.gz
```

### 4c. Entraînement avec word embeddings GloVe (expérimental)

```bash
./bin/train \
  -train      data/wikiner_fr_full.train.wikiner \
  -dev        data/wikiner_fr_full.dev.wikiner \
  -lang       fr \
  -format     wikiner \
  -workers    1 \
  -epochs     20 \
  -lr         0.05 \
  -l2         0.01 \
  -clusters   data/brown_clusters_fr.txt \
  -embeddings data/glove.6B.100d.txt \
  -gazetteers "firstnames:data/fr_prenoms.txt,locations:data/fr_communes.txt" \
  -prune-threshold 0.001 \
  -output     model_fr_glove.crf.gz
```

> Les embeddings GloVe `glove.6B.100d.txt` (~330 Mo) augmentent la mémoire
> nécessaire à ~6 Go. Le gain sur WikiNER est marginal ; ils sont plus utiles
> sur des domaines hors-distribution.

### Options d'entraînement complètes

| Flag               | Défaut         | Description                                         |
| ------------------ | -------------- | --------------------------------------------------- |
| `-train`           | —              | Corpus d'entraînement (obligatoire)                 |
| `-dev`             | —              | Corpus de validation pour early stopping            |
| `-lang`            | `en`           | Profil linguistique : `fr` ou `en`                  |
| `-format`          | `conll`        | Format : `conll` ou `wikiner`                       |
| `-epochs`          | 20             | Nombre d'époques SGD                                |
| `-lr`              | 0.1            | Learning rate initial                               |
| `-lr-decay`        | 0.0            | Décroissance multiplicative par époque (ex: `0.95`) |
| `-l2`              | 0.01           | Régularisation L2                                   |
| `-workers`         | nb cœurs       | Parallélisme Hogwild!                               |
| `-early-stop`      | 5              | Arrêt si pas d'amélioration après N epochs          |
| `-batch-size`      | 1              | Mini-batch SGD (1 = SGD pur, >1 = plus stable)      |
| `-dropout`         | 0.0            | Dropout sur les features (ex: `0.1`)                |
| `-window`          | 3              | Demi-fenêtre de contexte                            |
| `-clusters`        | —              | Fichier Brown clusters                              |
| `-gazetteers`      | —              | `"nom:fichier.txt,..."`                             |
| `-embeddings`      | —              | Fichier GloVe                                       |
| `-optimizer`       | `sgd`          | `sgd`, `momentum` ou `adam`                         |
| `-prune-threshold` | 0.0            | Élagage inline avant sauvegarde                     |
| `-output`          | `model.crf.gz` | Chemin du modèle de sortie                          |

## Étape 5 — Évaluer le modèle

```bash
./bin/eval \
  -model  model_fr_pruned.crf.gz \
  -lang   fr \
  -test   data/wikiner_fr.test.conll \
  -format conll
```

Sortie attendue pour le modèle de production :

```
Évaluation sur data/wikiner_fr.test.conll (7 xxx phrases)

Global :
  Precision : 83.x%
  Recall    : 81.x%
  F1        : 82.4%

Par type :
  PER    P=88.x%  R=87.x%  F1=87.x%
  LOC    P=84.x%  R=82.x%  F1=83.x%
  ORG    P=76.x%  R=72.x%  F1=74.x%
  MISC   P=71.x%  R=69.x%  F1=70.x%
```

## Étape 6 — Réduire la taille du modèle (optionnel)

Si `-prune-threshold` n'a pas été passé à l'entraînement, appliquez-le après :

```bash
./bin/prune \
  -model     model_fr.crf.gz \
  -threshold 0.001 \
  -output    model_fr_pruned.crf.gz
```

Un seuil de `0.001` supprime typiquement 70-80 % des poids (features très peu
discriminantes) avec une perte de F1 négligeable (< 0,3 %).

## Étape 7 — Tester manuellement

```bash
echo "Jean Dupont travaille chez Airbus à Toulouse." \
  | ./bin/demo -lang fr -model model_fr_pruned.crf.gz  -anonymize
```

Vous devriez obtenir:

```bash
[PERSON_1] travaille chez [ORGANIZATION_1] à [LOCATION_1].

Mapping (3 substitutions) :
  [LOCATION_1]              → "Toulouse"
  [ORGANIZATION_1]          → "Airbus"
  [PERSON_1]                → "Jean Dupont"
```

## Étape 8 — Intégrer dans l'API embarquée

Copier le modèle produit pour qu'il soit utilisé par le serveur HTTP :

```bash
# Démarrer le serveur avec le nouveau modèle
./bin/server \
  -models "fr:model_fr_pruned.crf.gz" \
  -gazetteers "firstnames:data/fr_prenoms.txt" \
  -port 8080
```

Pour l'embarquer dans le binaire via `//go:embed`, placer le fichier dans
`pkg/ner/models/fr.crf.gz` (non versionné dans ce dépôt en raison de sa
taille).

## Récapitulatif des fichiers

```
data/
├── wikiner_fr_full.train.wikiner   # corpus train (~290k phrases)
├── wikiner_fr_full.dev.wikiner     # corpus dev (~2,5k phrases)
├── wikiner_fr.test.conll           # corpus test (~7k phrases)
├── brown_clusters_fr.txt           # Brown clusters (fourni)
├── fr_prenoms.txt                  # Gazetteer prénoms FR (fourni)
└── fr_communes.txt                 # Gazetteer communes FR (fourni)

model_fr_pruned.crf.gz              # modèle produit (à générer)
```

## Dépannage

**Divergence Hogwild! avec `-workers > 1`** : réduire à `-workers 1` ou
augmenter `-batch-size 4`.

**Out of memory avec GloVe** : les embeddings chargent ~330 Mo en RAM. Avec
le corpus complet, prévoir 6 Go.

**F1 inférieur à 80 %** : vérifier que `-clusters` et `-gazetteers` sont bien
passés. Sans Brown clusters, le F1 descend de ~2-3 points.

**Modèle trop volumineux** : augmenter `-prune-threshold` à `0.005` (F1
≈ 81,5 %, taille divisée par 4 supplémentaire).
