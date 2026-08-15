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
# -lr-decay stabilise la fin d'entraînement. Sans lui le F1 de dev oscille
# jusqu'à la dernière époque et l'arrêt anticipé fige un point arbitraire :
# 88,8 % avec, 86,8 % sans, pour le même corpus.
./bin/train \
  -train data/wikiner_fr_full.train.wikiner \
  -dev   data/wikiner_fr_full.dev.wikiner \
  -lang fr -format wikiner \
  -workers 1 -epochs 20 -lr 0.1 -lr-decay 0.9 -l2 0.01 \
  -clusters data/brown_clusters_fr.txt \
  -prune-threshold 0.001 \
  -output model_fr.crf.gz

# Évaluation F1 (matching strict type + span).
# Passer les MÊMES -gazetteers/-clusters qu'à l'entraînement (sinon F1 sous-évalué ;
# Recognizer.Warnings() alerte sur stderr). -keep-punct et -boundaries pilotent la
# configuration d'inférence (défauts : ponctuation conservée, coupure . ! ? …).
# Vérifier d'abord que le jeu de test ne recoupe pas l'entraînement :
python scripts/check_overlap.py data/prod/train.conll data/wikiner_fr.test.conll

GAZ=~/.cache/go-anon/models
./bin/eval -model model_fr.crf.gz -lang fr \
  -test data/wikiner_fr.test.conll -format conll \
  -clusters data/brown_clusters_fr.txt \
  -gazetteers "firstnames:$GAZ/firstnames.txt,locations:$GAZ/locations.txt,organizations:$GAZ/organizations.txt"

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

# Génération d'un corpus synthétique annoté (documents administratifs)
# Templates dans templates/<lang>/, gazetteers embarqués dans pkg/synth/gazetteer/seed.
./bin/synthcorpus generate -lang fr -count 100 -seed 1 -out data/synth/p0
./bin/synthcorpus validate -corpus data/synth/p0    # invariants BIO, spans, labels
./bin/synthcorpus stats    -corpus data/synth/p0    # diversité, formes, templates
./bin/synthcorpus sample   -template facture-artisan -index 1  # relecture humaine

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
  → découpage ligne/phrase  (pkg/ner)          — chaque ligne puis chaque phrase ; par défaut la ponctuation
                                                 est conservée dans la séquence CRF et le découpage se fait
                                                 aux seules fins de phrase (. ! ? …), aligné sur l'entraînement
  → FeatureExtractor        (pkg/features)     — ~40-60 features/token : morpho, shape, contexte, clusters,
                                                 gazetteers ; chaînes versionnées par FeatureSchema
  → CRF.PredictWithMarginals (pkg/model)       — Viterbi + marginales en une passe d'émissions (BIO)
  → decodeEntitiesWithScores (pkg/ner)         — reconstruction des spans avec scores de confiance
  → EntityFilter[]          (pkg/ner)          — post-filtres chaînables (confiance, longueur, blocklist, regex)
  → Anonymizer.Anonymize    (pkg/anonymizer)   — remplacement en ordre inverse des offsets
```

Points de configuration importants (cf. `pkg/ner` et `goanon.go`) :

- `WithPunctuationTokens(bool)` — inclusion de la ponctuation dans la séquence
  CRF (défaut : activé). `WithSentenceBoundaries(...)` — délimiteurs de phrase
  (défaut : `. ! ? …`). Ces deux défauts valent **+1,7 pt de F1** sur WikiNER fr
  par rapport à l'ancien comportement (ponctuation retirée, découpage aux virgules).
- `WithConfidenceScores(bool)` — calcul des marginales de confiance (défaut :
  activé) ; le désactiver saute le forward-backward pour le débit maximal.
- `Recognizer.Warnings()` — écarts détectés entre la `FeatureConfig` du modèle
  et la configuration d'inférence (gazetteers/clusters manquants, langue
  différente). Affiché par `eval`, `demo` et `anon-doc`.
- **`Recognizer` sans état** — `Recognize` ne mute aucun champ du Recognizer
  (l'ancien `lastText` a été supprimé ; les post-filtres reçoivent le texte en
  argument via la signature `EntityFilter func(text string, entities []Entity)`).
  Un même Recognizer est donc partageable entre goroutines sans course ni
  contamination inter-requêtes (cf. `TestRecognizer_ConcurrentNoContamination`).
- **Hygiène des `Session`** — `Session.Close()` libère les tables (PII
  collectable) et interdit tout usage ultérieur (`ErrSessionClosed`) ;
  `SetMaxEntities` borne la croissance du mapping (`ErrSessionFull`).
- **Presets précision/rappel** (`pkg/ner/preset.go`) — `Balanced` (défaut,
  compromis F1) et `HighRecall` (conformité : ajoute `FirstNameDetectionPass`
  qui injecte les prénoms du gazetteer manqués par le CRF). `eval -preset`
  mesure chaque preset ; `Metrics` expose désormais **F2** (rappel pondéré ×2).
  Recommandation RGPD : `HighRecall` + `WithStrictVerification` (cf.
  `docs/rgpd.md`).

### Packages

| Package            | Rôle                                                                                                         |
| ------------------ | ------------------------------------------------------------------------------------------------------------ |
| `pkg/model`        | CRF linéaire : poids, Viterbi, forward-backward, entraînement SGD/Adam, sérialisation gob+gzip (formats v1/v2/v3, cf. ci-dessous) |
| `pkg/features`     | Extraction de features : morphologie, n-grammes, shape, Brown clusters, word embeddings (GloVe), gazetteers ; schéma versionné (`FeatureSchema`) |
| `pkg/ner`          | Orchestration : `Recognizer`, décodage BIO→entités, évaluation F1, post-filtres, corrections BIO             |
| `pkg/anonymizer`   | Remplacement des entités : stratégies (tag/redact/hash), `Session` cross-segments, passes de post-traitement, vérification fail-closed (`verify.go`) |
| `pkg/anonymizer/mappingstore` | Store chiffré (AES-256-GCM) des tables de ré-identification : rétention, purge, effacement cryptographique |
| `pkg/docprocessor` | Interface `Walker` + `Processor` — orchestration de l'anonymisation de documents format-agnostique ; interface `Sanitizer` + `Sanitize()` : purge des surfaces cachées (métadonnées, commentaires, révisions), fail-closed en mode strict |
| `pkg/docx`         | `Walker` DOCX : itération sur les paragraphes, réécriture des runs ; `Sanitizer` (docProps purgés, commentaires supprimés, révisions détectées) |
| `pkg/odt`          | `Walker` ODT : parsing XML en mémoire, réécriture in-place, resérialisation ZIP ; `Sanitizer` (meta.xml purgé, annotations et tracked-changes retirées) |
| `pkg/csv`          | `Walker` CSV/TSV : détection auto du séparateur, anonymisation cellule par cellule ; `Sanitizer` no-op (pas de surface cachée) |
| `pkg/pdf`          | `Walker` PDF (lecture seule via pdfcpu) : extraction texte avec offsets, redact dans le flux de contenu ; `Sanitizer` (Info + XMP purgés, annotations/pièces jointes signalées) |
| `pkg/corpus`       | Lecture CoNLL et WikiNER, normalisation BIO, conversion BIO↔BIOES                                            |
| `pkg/tokenizer`    | `UnicodeTokenizer` — offsets byte-précis, options FR/ES (apostrophe) et EN (trait d'union)                   |
| `pkg/lang`         | Profils linguistiques : stop-words, préfixes honorifiques, features spécifiques FR/EN/ES                     |
| `pkg/langdetect`   | Interface `Detector` de détection automatique de langue ; implémentation `WhatlangDetector` (whatlanggo)      |
| `pkg/synth`        | Générateur de corpus synthétique : `gazetteer` (TSV pondéré), `template` (parser dédié + AST), `render` (segments annotés, offsets dérivés), `value` (générateurs PER/ORG/ADDR et valeurs de contexte), `generate` (parcours d'AST, bruit, projection BIO, manifest) |
| `pkg/checksum`     | Clés de contrôle des identifiants : Luhn (SIREN/SIRET, exception La Poste), mod-97-10 (IBAN), clé NIR. Branché sur `ner.RegexPattern.Validate` |
| `pkg/modelstore`   | Téléchargement automatique, cache, vérification SHA-256 **et signature Ed25519/minisign du manifeste** (authenticité de la source, clé embarquée dans `keys.go`), découverte des modèles depuis GitHub Releases ; code de langue borné `^[a-z]{2}$` (anti-traversée) |

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

## Format des modèles et schéma de features

**Formats de sérialisation** (`pkg/model/serialize.go`, champ `Version`) :

- **v1** : poids dans une `map[uint64]float64` (héritage).
- **v2** : `WeightKeys []uint64` triés + `WeightVals []float32` — une entrée par
  paire (feature, label). Format des modèles publiés jusqu'à mi-2026.
- **v3** : poids **groupés par feature** (`BaseKeys` + blocs contigus de L poids).
  Une seule recherche binaire par feature à l'inférence au lieu de L. Émis par
  l'entraînement (le `Trainer` collecte les bases de features). Chargement
  rétrocompatible v1/v2/v3 ; le cycle `prune` préserve le format d'origine.
- **v4** : **format de flux** (`pkg/model/serialize_stream.go`) — magic
  `GOANONv4`, en-tête gob (métadonnées + longueurs des tableaux), puis les poids
  en binaire brut little-endian. Orthogonal à la représentation des poids : un
  modèle plat (v2) comme groupé (v3) peut être écrit en v4, `SourceFormat`
  conservant l'original. Écrit par `SaveStream`, détecté automatiquement par
  `LoadModel`.

Gains v3 : ~×4,6 de latence d'inférence cumulée (chemin chaud) et modèles
~30–50 % plus petits, à qualité égale.

Gains v4 — le **pic mémoire de chargement**. Les formats gob imposent deux
surcoûts inévitables côté décodeur : le message entier est bufferisé avant
décodage (`internal/saferio`), et chaque slice est reconstruite par doublements
(`encoding/gob.growSlice`) alors que sa taille finale est connue dès l'en-tête.
En v4 les longueurs précèdent les données : chaque tableau est alloué une fois
à sa taille exacte, puis rempli par blocs via un tampon de 256 kio.

Mesuré sur le modèle `fr` (165 Mio de poids utiles) :

| | v3 (gob) | v4 (flux) |
|---|---|---|
| pic RSS au chargement | 819 Mio | **163 Mio** |
| total alloué | 1418 Mio | **159 Mio** |
| durée de chargement | 1,56 s | **0,79 s** |
| taille du fichier | 104,4 Mio | **93,3 Mio** |

Inférence strictement identique (entités, offsets et scores de confiance
inchangés) : les poids sont des `float32` transportés bit-à-bit.

**Compatibilité** : la lecture est rétrocompatible (v1/v2/v3/v4 détectés
automatiquement), mais un modèle v4 n'est **pas** lisible par une version de
go-anon antérieure à l'introduction du format. Republier des modèles en v4
impose donc de faire monter les consommateurs d'abord. `cmd/convert` réécrit un
modèle existant en v4, sans réentraînement ni perte.

**Schéma de features** (`FeatureConfig.FeatureSchema`, propagé à l'inférence par
`ner.New`) :

- **Schéma 0** (historique, gelé) : comportement des modèles v1/v2 existants,
  conservé **bit-à-bit** pour ne rien casser. Contient deux bugs assumés
  (`word.len` faux pour les mots ≥ 10 caractères via `itos`, feature `gazseq`
  posée uniquement sur le premier token d'un span gazetteer multi-mots).
- **Schéma 1** : `word.len` correct (`strconv.Itoa`) et `gazseq.<nom>.B`/`.I`
  sur chaque token d'un span multi-mots. Utilisé par tout nouvel entraînement
  (`features.LatestSchema`).

**Règle d'or** : les chaînes de features sont hachées dans les modèles. Toute
modification de l'extracteur qui change une chaîne doit être gardée par un
nouveau `FeatureSchema` (sinon dégradation silencieuse des modèles existants).
`ner.New` refuse un modèle dont le schéma dépasse `features.LatestSchema`.

## Performances de référence (WikiNER, matching strict)

Les jeux d'évaluation livrés avec le dépôt recoupent l'entraînement. La cause
est dans le corpus : WikiNER contient 41 % de phrases dupliquées en anglais et
44 % en espagnol, qu'un découpage aléatoire répartit des deux côtés.

| Jeu historique | Phrases partagées avec l'entraînement |
| --- | ---: |
| `data/wikiner_fr.dev.conll` | 97,9 % |
| `data/fr/wikiner_fr.test.conll` | 91,8 % |
| `data/es/wikiner_es.test.conll` | 87,2 % |
| `data/en/wikiner_en.test.conll` | 81,6 % |
| `data/wikiner_fr.test.conll` | 0,2 %, propre par accident de calendrier |

`scripts/split_wikiner_clean.py` déduplique puis découpe. Les jeux qu'il produit
ne partagent aucune phrase avec l'entraînement, et ce sont eux qui donnent les
chiffres ci-dessous.

| Langue | F1 | Ancien F1 publié | Jeu | Format |
| ------ | ---- | ---- | --- | ------ |
| fr | 88,6 % | 93,6 % | `data/wikiner_fr.test.conll` | v4 |
| en | 83,0 % | 90,7 % | `data/prod/en_clean_test.conll` | v4 |
| es | 88,6 % | 95,6 % | `data/prod/es_clean_test.conll` | v4 |

Les anciens chiffres n'étaient pas faux par erreur de calcul, ils mesuraient
autre chose. Le même modèle `es` obtient 88,6 % sur le jeu propre et 95,9 % sur
l'ancien, le même modèle `en` 83,0 % contre 89,8 %. L'écart tient entre 6 et
7 points selon la langue.

Le modèle `fr` intègre en plus le corpus synthétique de `DATASET.md`, qui lui
vaut 80,2 % sur documents administratifs contre 11,4 % sans. `en` et `es` n'en
bénéficient pas encore.

Vérifier tout nouveau jeu avant de publier un chiffre :

```bash
python scripts/check_overlap.py data/prod/en_clean_train.conll data/prod/en_clean_test.conll
```

Évaluer **toujours** avec les mêmes `-gazetteers` et `-clusters` qu'à
l'entraînement ; `Recognizer.Warnings()` signale les écarts. Voir
`docs/tutoriel-modele.md` pour la reproduction complète.
