# Entraîner un modèle NER depuis les données WikiNER

Reproduire de bout en bout un modèle NER pour une langue supportée (`fr`, `en`,
`es`), depuis les données brutes WikiNER jusqu'au modèle élagué prêt pour l'API.

Les exemples utilisent `<LANG>` comme variable de langue. Remplacez-la par `fr`,
`en` ou `es` selon votre cible.

## Prérequis

- Go 1.21+ installé et `go build` fonctionnel
- ~4 Go de RAM disponible (entraînement corpus complet)
- **Plusieurs heures de CPU par langue** (`-workers 1`, obligatoire) : sur le
  corpus WikiNER complet (~250–280 k phrases), une époque prend ~8–11 min ; un
  entraînement de 20 époques (souvent écourté par l'early-stopping) représente
  ~2 à 3,5 h par langue.

## Étape 1 — Compiler les binaires

```bash
go build -o bin/train         ./cmd/train/
go build -o bin/eval          ./cmd/eval/
go build -o bin/prune         ./cmd/prune/
go build -o bin/brown-cluster ./cmd/brown-cluster/
go build -o bin/demo          ./cmd/demo/
go build -o bin/server        ./cmd/server/
```

Ou en une seule commande :

```bash
make build-tools
```

## Étape 2 — Obtenir les données WikiNER

Les fichiers suivants doivent être présents dans `data/<LANG>/` :

| Fichier                             | Rôle                                   | Format  |
| ----------------------------------- | -------------------------------------- | ------- |
| `wikiner_<LANG>_full.train.wikiner` | Corpus d'entraînement (~250 k phrases) | WikiNER |
| `wikiner_<LANG>_full.dev.wikiner`   | Corpus de validation (~2.5 k phrases)  | WikiNER |
| `wikiner_<LANG>.test.conll`         | Corpus de test (~2.5 k phrases)        | CoNLL   |

### Téléchargement

Les données WikiNER sont hébergées sur le dépôt GitHub du projet FOX (DICE Group) :

```
https://github.com/dice-group/FOX/tree/master/input/Wikiner
```

Chaque langue est répartie sur deux fichiers (`wp2` et `wp3`) :

```bash
wget -O aij-wikiner-<LANG>-wp2.bz2 https://github.com/dice-group/FOX/raw/master/input/Wikiner/aij-wikiner-<LANG>-wp2.bz2
wget -O aij-wikiner-<LANG>-wp3.bz2 https://github.com/dice-group/FOX/raw/master/input/Wikiner/aij-wikiner-<LANG>-wp3.bz2
```

Tailles indicatives des corpus combinés :

| Langue | Phrases (train+dev+test) |
| ------ | ------------------------ |
| `fr`   | ~266 k                   |
| `en`   | ~287 k                   |
| `es`   | ~255 k                   |

### Découper le corpus en train / dev / test

Le script concatène `wp2` et `wp3`, mélange toutes les phrases (graine fixe),
puis découpe en 98 % / 1 % / 1 %. Le corpus de test est converti au format
CoNLL (un token par ligne, tabs).

```bash
mkdir -p data/<LANG>

python scripts/split_wikiner_lang.py \
  --lang    <LANG> \
  --input   aij-wikiner-<LANG>-wp2.bz2 aij-wikiner-<LANG>-wp3.bz2 \
  --out-dir data/<LANG>/
```

Voir [`scripts/split_wikiner_lang.py`](../scripts/split_wikiner_lang.py)

## Étape 3 — Préparer les gazetteers (optionnel mais recommandé)

Les gazetteers fournissent au modèle des listes de référence de prénoms et de
patronymes.

`data/eu_prenoms.txt` et `data/eu_patronymes.txt` ne sont plus dans le dépôt.
Les modèles publiés utilisent ceux que `modelstore` télécharge dans
`~/.cache/go-anon/models` : `firstnames.txt` (48 517 entrées),
`locations.txt` (32 687) et `organizations.txt` (111). Le dernier est trop
maigre pour peser, et c'est une piste ouverte pour améliorer `ORG`.

Cette étape sert à régénérer les fichiers EU depuis la source officielle.

### Source : portail OpenData de l'Union Européenne

Les données proviennent du jeu de données EU des prénoms et patronymes :

```
https://data.europa.eu/data/datasets/5bc35259634f41122d982759?locale=en
```

Ce jeu de données couvre l'ensemble des États membres de l'UE et convient
pour toutes les langues supportées (`fr`, `en`, `es`, …). Téléchargez les
fichiers CSV `prenom.csv` (prénoms) et `patronymes.csv` (noms de famille)
et placez-les dans `data/`.

```bash
python scripts/build_gazetteers_eu.py \
  --prenoms-csv    data/prenom.csv \
  --patronymes-csv data/patronymes.csv \
  --min-count      5
```

Le paramètre `--min-count` filtre les hapax typographiques (défaut : 2 ;
recommandé : 5 pour un gazetteer propre).

Sortie :

| Fichier                  | Contenu              |
| ------------------------ | -------------------- |
| `data/eu_prenoms.txt`    | ~28 k prénoms EU     |
| `data/eu_patronymes.txt` | ~237 k patronymes EU |

Voir [`scripts/build_gazetteers_eu.py`](../scripts/build_gazetteers_eu.py)

> **Note FR** : pour les communes françaises, un gazetteer spécifique
> `data/fr/fr_communes.txt` est également disponible (voir ci-dessous).

### Gazetteer des communes (FR uniquement)

**Source** : fichier des communes françaises sur data.gouv.fr :

```
https://www.data.gouv.fr/datasets/communes-et-villes-de-france-en-csv-excel-json-parquet-et-feather
```

```bash
python scripts/build_gazetteers.py \
  --communes-csv communes-france-2025.csv \
  --out-communes data/fr/fr_communes.txt
```

Voir [`scripts/build_gazetteers.py`](../scripts/build_gazetteers.py)

---

## Étape 4 — Générer les Brown clusters (optionnel mais recommandé)

Les Brown clusters améliorent sensiblement le F1 sur les entités rares.
Les fichiers `data/<LANG>/brown_clusters_<LANG>.txt` sont déjà fournis dans
ce dépôt ; régénérez-les uniquement si vous souhaitez les adapter à un autre
corpus.

```bash
./bin/brown-cluster \
  -input    data/<LANG>/wikiner_<LANG>_full.train.wikiner \
  -format   wikiner \
  -vocab    10000 \
  -clusters 200 \
  -output   data/<LANG>/brown_clusters_<LANG>.txt
```

Paramètres clés :

| Flag         | Valeur recommandée | Effet                            |
| ------------ | ------------------ | -------------------------------- |
| `-vocab`     | 10 000             | Taille du vocabulaire retenu     |
| `-clusters`  | 200                | Nombre de clusters hiérarchiques |
| `-min-count` | 5 (défaut)         | Ignore les mots rares            |

Durée : ~5 min sur un corpus complet.

## Étape 5 — Entraîner le modèle

### 5a. Entraînement de base (sans ressources externes)

```bash
./bin/train \
  -train   data/<LANG>/wikiner_<LANG>_full.train.wikiner \
  -dev     data/<LANG>/wikiner_<LANG>_full.dev.wikiner \
  -lang    <LANG> \
  -format  wikiner \
  -workers 1 \
  -epochs  20 \
  -lr      0.1 \
  -l2      0.01 \
  -prune-threshold 0.001 \
  -output  models/model_<LANG>.crf.gz
```

> **Toujours utiliser `-workers 1`.** L'algorithme Hogwild! (mises à jour
> concurrentes sans verrou) provoque une divergence sur ces corpus : le F1
> stagne autour de 3 % au lieu de converger vers 85 %+.

### 5b. Entraînement complet avec Brown clusters et gazetteers (recommandé)

C'est la configuration qui produit les modèles de production. `$GAZ` désigne le
cache de `modelstore`, où les gazetteers publiés sont téléchargés :

```bash
GAZ=~/.cache/go-anon/models

./bin/train \
  -train      data/<LANG>/wikiner_<LANG>_full.train.wikiner \
  -dev        data/<LANG>/wikiner_<LANG>_full.dev.wikiner \
  -lang       <LANG> \
  -format     wikiner \
  -workers    1 \
  -epochs     20 \
  -lr         0.1 \
  -lr-decay   0.9 \
  -l2         0.01 \
  -clusters   data/<LANG>/brown_clusters_<LANG>.txt \
  -gazetteers "firstnames:$GAZ/firstnames.txt,locations:$GAZ/locations.txt" \
  -prune-threshold 0.001 \
  -output     models/model_<LANG>_pruned.crf.gz
```

**Variante FR.** Ajoutez le gazetteer des communes :

```bash
  -gazetteers "firstnames:$GAZ/firstnames.txt,locations:data/fr/fr_communes.txt"
```

### 5c. Entraînement avec word embeddings GloVe (expérimental)

```bash
./bin/train \
  -train      data/<LANG>/wikiner_<LANG>_full.train.wikiner \
  -dev        data/<LANG>/wikiner_<LANG>_full.dev.wikiner \
  -lang       <LANG> \
  -format     wikiner \
  -workers    1 \
  -epochs     20 \
  -lr         0.05 \
  -l2         0.01 \
  -clusters   data/<LANG>/brown_clusters_<LANG>.txt \
  -embeddings data/glove.6B.100d.txt \
  -gazetteers "firstnames:$GAZ/firstnames.txt,locations:$GAZ/locations.txt" \
  -prune-threshold 0.001 \
  -output     models/model_<LANG>_glove.crf.gz
```

> Les embeddings GloVe `glove.6B.100d.txt` (~330 Mo) augmentent la mémoire
> nécessaire à ~6 Go. Le gain sur WikiNER est marginal ; ils sont plus utiles
> sur des domaines hors-distribution.

### Options d'entraînement complètes

| Flag               | Défaut         | Description                                         |
| ------------------ | -------------- | --------------------------------------------------- |
| `-train`           | —              | Corpus d'entraînement (obligatoire)                 |
| `-dev`             | —              | Corpus de validation pour early stopping            |
| `-lang`            | `en`           | Profil linguistique : `fr`, `en` ou `es`            |
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

## Étape 6 — Évaluer le modèle

```bash
./bin/eval \
  -model      models/model_<LANG>_pruned.crf.gz \
  -lang       <LANG> \
  -test       data/<LANG>/wikiner_<LANG>.test.conll \
  -format     conll \
  -clusters   data/<LANG>/brown_clusters_<LANG>.txt \
  -gazetteers "firstnames:$GAZ/firstnames.txt,locations:$GAZ/locations.txt"
```

> **Important** : passer à l'évaluation exactement les mêmes `-gazetteers` et
> `-clusters` qu'à l'entraînement, sinon les features gazetteers/clusters
> manquent et le F1 chute de plusieurs points. `Recognizer.Warnings()` (affiché
> sur stderr par `eval`, `demo` et `anon-doc`) signale ces écarts.

Flags d'évaluation liés à la configuration d'inférence :

| Flag          | Défaut    | Effet                                                                                            |
| ------------- | --------- | ------------------------------------------------------------------------------------------------ |
| `-keep-punct` | `true`    | Conserve la ponctuation dans la séquence CRF (aligné train)                                      |
| `-boundaries` | `. ! ? …` | Délimiteurs de phrase ; `-keep-punct=false` + anciens délimiteurs reproduit l'inférence pré-2026 |
| `-clusters`   | —         | Fichier Brown clusters (doit correspondre à l'entraînement)                                      |
| `-gazetteers` | —         | Gazetteers `nom:fichier.txt,...` (idem)                                                          |

Performances de référence sur WikiNER, matching strict, jeux dédupliqués
(`scripts/split_wikiner_clean.py`), mesures 2026-08 :

| Langue | F1 global | PER   | LOC   | ORG   | MISC  | Schéma / format |
| ------ | --------- | ----- | ----- | ----- | ----- | --------------- |
| `fr`   | 88.6%     | 93.9% | 88.8% | 83.1% | 80.8% | schéma 1 / v4   |
| `en`   | 83.0%     | 89.7% | 82.9% | 80.2% | 75.5% | schéma 1 / v4   |
| `es`   | 88.6%     | 93.2% | 89.5% | 83.2% | 77.8% | schéma 1 / v4   |

Les mesures antérieures à août 2026 portaient sur des jeux qui recoupaient
l'entraînement de 81 % à 98 % selon la langue. WikiNER contient 41 % de phrases
dupliquées en anglais et 44 % en espagnol : tout découpage aléatoire les
répartit des deux côtés. Dédupliquer avant de découper ramène le recouvrement à
zéro et coûte entre 6 et 7 points de F1 affiché.

Le modèle `fr` inclut le corpus synthétique produit par `cmd/synthcorpus`.
Il obtient 80,2 %
sur documents administratifs, contre 11,4 % pour le même entraînement sans ce
corpus. `en` et `es` n'en disposent pas.

> Les nouveaux entraînements produisent des modèles au **schéma de features 1**
> (deux bugs corrigés : `word.len`, gazetteers multi-mots) et au **format v3**,
> qui groupe les poids par feature. `cmd/convert` les réécrit en **v4** avant
> publication : le pic mémoire au chargement passe de 819 à 163 Mio sur le
> modèle français, et le fichier perd 40 %. Le schéma et le format sont
> enregistrés dans le modèle et propagés à l'inférence, sans option à passer.
>
> **Décroissance du pas.** `-lr-decay 0.9` remplace la patience élargie qui
> servait à contourner le bruit du F1 de dev. À pas constant la courbe oscille
> jusqu'à la dernière époque et l'arrêt anticipé fige un point arbitraire :
> 88,8 % avec décroissance, 86,8 % sans, pour le même corpus français.

## Étape 7 — Réduire la taille du modèle (optionnel)

Si `-prune-threshold` n'a pas été passé à l'entraînement, appliquez-le après :

```bash
./bin/prune \
  -model     models/model_<LANG>.crf.gz \
  -threshold 0.001 \
  -output    models/model_<LANG>_pruned.crf.gz
```

Un seuil de `0.001` supprime typiquement 70–80 % des poids (features très peu
discriminantes) avec une perte de F1 négligeable (< 0,3 %).

## Étape 8 — Tester manuellement

```bash
# Français
echo "Jean Dupont travaille chez Airbus à Toulouse." \
  | ./bin/demo -lang fr -model models/model_fr_pruned.crf.gz -anonymize

# Espagnol
echo "Pedro Almodóvar nació en Calzada de Calatrava y trabaja para la Academia de Cine." \
  | ./bin/demo -lang es -model models/model_es_pruned.crf.gz -anonymize

# Anglais
echo "Barack Obama was born in Hawaii and studied at Harvard University." \
  | ./bin/demo -lang en -model models/model_en_pruned.crf.gz -anonymize
```

## Étape 9 — Intégrer dans l'API embarquée

```bash
./bin/server \
  -models     "fr:models/model_fr_pruned.crf.gz,en:models/model_en_pruned.crf.gz,es:models/model_es_pruned.crf.gz" \
  -gazetteers "firstnames:$GAZ/firstnames.txt,locations:$GAZ/locations.txt" \
  -port 8080
```

Pour l'embarquer dans le binaire via `//go:embed`, placer les fichiers dans
`pkg/ner/models/<LANG>.crf.gz` (non versionnés dans ce dépôt en raison de leur
taille).

## Récapitulatif des fichiers

```
data/
├── eu_prenoms.txt                          # Gazetteer prénoms EU (à régénérer, § 3)
├── eu_patronymes.txt                       # Gazetteer patronymes EU (à régénérer, § 3)
├── fr/
│   ├── wikiner_fr_full.train.wikiner       # corpus train FR (~250k phrases)
│   ├── wikiner_fr_full.dev.wikiner         # corpus dev FR
│   ├── wikiner_fr.test.conll               # corpus test FR
│   ├── brown_clusters_fr.txt               # Brown clusters FR (fourni)
│   ├── fr_prenoms.txt                      # Gazetteer prénoms FR (fourni)
│   └── fr_communes.txt                     # Gazetteer communes FR (fourni)
├── en/
│   ├── wikiner_en_full.train.wikiner       # corpus train EN (~281k phrases)
│   ├── wikiner_en_full.dev.wikiner         # corpus dev EN
│   ├── wikiner_en.test.conll               # corpus test EN
│   └── brown_clusters_en.txt               # Brown clusters EN (fourni)
└── es/
    ├── wikiner_es_full.train.wikiner       # corpus train ES (~250k phrases)
    ├── wikiner_es_full.dev.wikiner         # corpus dev ES
    ├── wikiner_es.test.conll               # corpus test ES
    └── brown_clusters_es.txt               # Brown clusters ES (fourni)

models/
├── model_fr_pruned.crf.gz                  # modèle FR (à générer)
├── model_en_pruned.crf.gz                  # modèle EN (à générer)
└── model_es_pruned.crf.gz                  # modèle ES (à générer)
```

## Dépannage

**Divergence Hogwild! avec `-workers > 1`** : réduire à `-workers 1` ou
augmenter `-batch-size 4`.

**Out of memory avec GloVe** : les embeddings chargent ~330 Mo en RAM. Avec
le corpus complet, prévoir 6 Go.

**F1 inférieur à 80 %** : vérifier que `-clusters` et `-gazetteers` sont bien
passés. Sans Brown clusters, le F1 descend de ~2–3 points.

**Modèle trop volumineux** : augmenter `-prune-threshold` à `0.005` (légère
perte de F1, taille divisée par 4 supplémentaire).

**Langue non reconnue à l'évaluation** : recompiler `bin/eval` après tout ajout
de profil linguistique (`make build-tools`).
