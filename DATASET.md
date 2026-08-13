# Plan d'implémentation — Corpus synthétique pour documents administratifs

**Version** : 2.0
**Date** : 2026-08-04
**Cible** : corpus annoté BIO complétant WikiNER, pour améliorer le CRF de
go-anon sur les documents administratifs et commerciaux (FR d'abord, EN/ES
ensuite)

---

## 1. Objectifs et périmètre

### 1.1 Constat de départ

Les modèles publiés (fr 93,6 / en 90,7 / es 95,6 F1) sont entraînés sur
WikiNER : de la prose encyclopédique. Les documents que traite `anon-doc` —
factures, devis, bulletins de paie, emails — ont une structure, une densité
d'entités et une ponctuation qui n'ont rien à voir. C'est l'écart de domaine
que ce corpus vise à combler.

### 1.2 Objectif fonctionnel

Produire un corpus synthétique annoté au format CoNLL BIO, **dans le jeu de
labels existant** (`PER` / `LOC` / `ORG`), destiné à être **mélangé à WikiNER**
lors de l'entraînement, pas à le remplacer.

### 1.3 Contraintes structurantes

| Contrainte                           | Implication technique                                             |
| ------------------------------------ | ----------------------------------------------------------------- |
| Annotation exacte par construction   | Les offsets sont connus à l'insertion ; aucune post-annotation    |
| Continuité avec les modèles publiés  | Jeu de labels inchangé, tokeniseur inchangé, schéma de features 1 |
| Reproductibilité                     | Corpus régénérable à l'identique depuis une seed                  |
| Distribution réaliste                | Tirage pondéré depuis gazetteers open data                        |
| Séparation stricte train / test réel | Le jeu de test authentique n'entre jamais dans le générateur      |

### 1.4 Hors périmètre

- Entraînement et évaluation du CRF (consommateur du corpus, pas producteur)
- Rendu PDF ou image des documents (le corpus est textuel)
- **Bruit OCR proprement dit** — renvoyé à `OCR.md` ; le bruit d'espacement des
  PDF natifs, lui, entre dans le périmètre (§ 8)
- **Entités à signature régulière** (`IBAN`, `SIRET`, `NIR`, `EMAIL`, `PHONE`) —
  traitées en production par regex + clé de contrôle, voir § 2.2

---

## 2. Typologie d'entités

### 2.1 Ce que le CRF doit apprendre

Le générateur produit **trois labels**, ceux du modèle actuel
(`pkg/ner/entity.go`) :

| Label | Contenu en contexte documentaire                     | Densité indicative / doc |
| ----- | ---------------------------------------------------- | ------------------------ |
| `PER` | Personne physique (nom, prénom, civilité)            | 1 – 3                    |
| `ORG` | Raison sociale, service, établissement               | 1 – 4                    |
| `LOC` | Adresse postale, commune, toponyme                   | 1 – 3                    |

`MISC` n'est pas produit : les entités qui l'occuperaient dans WikiNER
(nationalités, événements) n'apparaissent pas dans ces documents, et en
fabriquer artificiellement dégraderait ce que le modèle a déjà appris.

**Décision — mapping plutôt que nouveau jeu de labels.** `PERSON→PER`,
`ORGANISATION→ORG`, `ADDRESS→LOC`. Motifs : le corpus reste mélangeable à
WikiNER (on part de 93,6 F1 au lieu de zéro), les modèles publiés restent
compatibles, et `anon-doc` ne régresse pas sur le texte courant. Un jeu de
labels dédié imposerait un modèle « documents » distinct par langue, donc une
matrice de publication doublée, sans bénéfice démontré.

### 2.2 Ce que le CRF ne doit *pas* apprendre

`IBAN`, `SIRET`, `SIREN`, `NIR`, `EMAIL`, `PHONE`, `IPV4/6` sont des langages
réguliers, dont plusieurs portent une clé de contrôle. `pkg/ner/regex_builtins.go`
les couvre déjà en post-filtre. Une regex validée par checksum a une précision
et un rappel de 100 % là où un CRF plafonne autour de 98 % — les faire passer
par le modèle est une régression déguisée, et gonflerait le corpus de spans que
le modèle apprendrait moins bien que le code existant.

Ces entités **apparaissent** dans les documents générés (un document sans IBAN
ni SIRET n'est pas une facture), mais **ne sont pas annotées** dans la sortie
CoNLL. Elles jouent deux rôles : contexte lexical réaliste pour les features, et
matière première des négatifs difficiles (§ 7).

### 2.3 `DATE`, `AMOUNT`, `REFERENCE` : hors périmètre par défaut

Question tranchée en amont, parce qu'elle représenterait ~60 % de la densité
d'entités du corpus : ces catégories ne sont pas des données à caractère
personnel au sens du RGPD, et masquer tous les montants d'une facture produit un
document inexploitable. Elles ne sont ni annotées ni générées comme entités.

Réouverture possible si un besoin métier explicite apparaît (par exemple une
date de naissance, qui est du PII — mais c'est un cas de `DATE` très particulier,
mieux traité par une regex contextuelle que par une classe entière).

---

## 3. Architecture

### 3.1 Emplacement

**Décision — dans ce dépôt, pas dans un projet séparé.** Le générateur doit
consommer `pkg/tokenizer`, `pkg/corpus` et à terme `pkg/checksum`. Un dépôt
distinct recréerait exactement la duplication de tokenisation que le plan
initial cherchait à éviter.

```
pkg/checksum/            # Luhn, mod-97-10, clé NIR — package de PRODUCTION (§ 4)
pkg/synth/
├── gazetteer/           # chargement et tirage pondéré
├── template/            # parsing, AST, composition
├── value/               # générateurs de valeurs (noms, adresses, identifiants…)
├── render/              # assemblage en segments annotés
└── corpus/              # projection BIO, écriture, splits, manifest
cmd/synthcorpus/         # generate | validate | stats (sous-commandes)
templates/               # templates par langue — VERSIONNÉS (ce sont des sources)
└── fr/*.tmpl
data/synth/gazetteers/   # fichiers sources normalisés (data/ est gitignoré)
```

**Décision — les templates ne vont pas dans `data/`.** `data/` est exclu du dépôt
(`.gitignore`) parce qu'il accueille des corpus et des modèles téléchargés. Les
templates, eux, sont du code source : ils doivent être diffables, relus en revue
et empreintés dans le manifest (§ 11.2). D'où `templates/` à la racine.

### 3.2 Le renderer produit des segments, pas une chaîne

C'est la décision architecturale centrale, et elle supprime un risque plutôt
qu'elle ne le mitige.

```go
// Segment est un fragment de document. Label vide = texte non annoté.
type Segment struct {
    Text  string
    Label ner.EntityType // "" pour le texte libre
}

type Document struct {
    Segments []Segment
    Meta     Metadata
}
```

Le texte final et les spans sont dérivés de la liste de segments par une
concaténation qui calcule les offsets au passage. Conséquence : **il est
impossible de produire un span désaligné**. Toute transformation ultérieure
(variation typographique, altération de casse, et le bruit si on l'ajoute un
jour) s'applique segment par segment ; les offsets sont recalculés
mécaniquement, jamais maintenus à la main.

Le plan v1 identifiait le désalignement d'offsets comme « le bug le plus
probable de tout le projet » et y répondait par des tests property-based. Rendre
le bug inexprimable coûte moins cher que le tester.

---

## 4. Lot 0 — `pkg/checksum` en production

**Ce lot ne dépend d'aucun corpus et rapporte immédiatement.** Il est décrit ici
parce que le générateur en dépendra ensuite, mais sa valeur est autonome.

### 4.1 Problème actuel

```go
// pkg/ner/regex_builtins.go:116
reSIREN = regexp.MustCompile(`\b\d{9}\b`)
```

Toute séquence de neuf chiffres est détectée comme SIREN et anonymisée. Sur les
documents visés — pleins de références, numéros de commande, codes article — le
taux de faux positifs est élevé. Même problème, moins aigu, pour `reSIRET`
(14 chiffres) et `reIBAN` (structure sans vérification de clé).

### 4.2 Livrable

- `pkg/checksum` : Luhn (SIREN/SIRET), mod-97-10 (IBAN), clé NIR
  (`97 − n mod 97`, avec gestion des départements corses `2A`/`2B`).
  Table-driven sur vecteurs connus, y compris les cas particuliers (La Poste,
  dont le SIREN 356000000 ne satisfait pas Luhn).
- Champ `Validate func(string) bool` sur `ner.RegexPattern`, appliqué après le
  match et avant l'insertion de l'entité dans `RegexEntityFilter`
  (`pkg/ner/regex.go`). Nil = comportement actuel, donc rétrocompatible.
- Branchement sur `BuiltinRegexPatterns` pour SIREN, SIRET, IBAN.

### 4.3 Mesure

Taux de faux positifs SIREN/SIRET/IBAN sur un échantillon de documents réels,
avant et après. Ne nécessite **aucune annotation** : on relit les spans produits
et on compte ceux qui n'en sont pas.

---

## 5. Sources de données (gazetteers)

### 5.1 Inventaire

| Source                    | Contenu                                  | Usage           | Licence                |
| ------------------------- | ---------------------------------------- | --------------- | ---------------------- |
| Fichier des prénoms INSEE | Prénoms + fréquence par année            | `PER` (pondéré) | Licence Ouverte        |
| Noms de famille INSEE     | Patronymes + fréquence                   | `PER` (pondéré) | Licence Ouverte        |
| Base Adresse Nationale    | Voies, codes postaux, communes           | `LOC`           | Licence Ouverte / ODbL |
| SIRENE                    | Raisons sociales, formes juridiques, NAF | `ORG`           | Licence Ouverte        |
| Équivalents ES / EN       | Prénoms, patronymes, toponymes           | localisation    | à qualifier            |

Le dépôt contient déjà `data/fr_prenoms.txt`, `data/eu_patronymes.txt`,
`data/communes-france-2025.csv` et `data/fr_communes.txt` : point de départ pour
le lot 1, à compléter par les fréquences INSEE pour la pondération.

**Point d'attention** : la couverture ES et EN est le maillon faible. À traiter
au lot 4, sans bloquer le français.

### 5.2 Format interne

```
# data/synth/gazetteers/fr/prenoms.tsv
# value <TAB> weight <TAB> metadata(json, optionnel)
Marie	312445	{"gender":"F"}
Jean	298112	{"gender":"M"}
```

TSV pondéré : lisible, diffable, streamable, trivial à produire depuis les
sources brutes. Le coût de parsing est négligeable au regard du volume.

### 5.3 Traitement de la distribution

Le tirage brut selon les fréquences INSEE surreprésente une poignée de valeurs
et noie la longue traîne. Deux corrections :

- **Troncature de queue** : seuil de fréquence minimal configurable, pour écarter
  le bruit orthographique des fichiers sources.
- **Aplatissement partiel** : exposant `α ∈ [0,1]` sur les poids (`w' = w^α`).
  `α = 1` donne la distribution réelle, `α = 0` l'uniforme. Départ : `0.6`.

**Justification** : le modèle doit apprendre des priors lexicaux réalistes sans
ne jamais voir de valeurs rares. Les features de `pkg/features` sont sensibles à
cette distribution (gazetteers, clusters Brown), l'exposant rend le compromis
explicite et réglable.

---

## 6. Templates

### 6.1 Variété par composition, pas par volume

**Décision — 20 à 40 templates composables par type de document, pas 200 à 500
templates plats.** Un template plat rédigé par LLM demande une relecture
humaine ; 500 par type et par langue, c'est ~6 000 relectures, ce qui ne se
fera pas. Et ce n'est pas le bon levier : un template avec sections optionnelles,
blocs répétables et ordre permutable couvre plus d'espace structurel qu'une
famille de variantes figées, tout en restant relisable.

Le LLM garde un rôle, en amont et hors ligne : produire de la **variété
structurelle** (nouvelles dispositions, formulations de champ, en-têtes) à
valider humainement. Il ne produit ni valeurs d'entités ni annotations — il est
mauvais aux deux.

### 6.2 Format

```
Facture n° {{REF:invoice}}
Date : {{DATE:issue}}

Émetteur :
{{ORG:seller}}
{{ADDR:seller_addr}}
SIRET : {{SIRET:seller_siret}}

[?contact]Contact : {{PER:seller_contact}} — {{EMAIL:seller_contact}}[/]

Client :
{{PER:buyer}}
{{ADDR:buyer_addr}}

{{LINES:3-8}}

Total HT : {{AMOUNT}}
TVA 20 % : {{AMOUNT}}
Total TTC : {{AMOUNT}}

Règlement par virement : {{IBAN:seller_iban}}
```

Les placeholders sans label annotable (`REF`, `DATE`, `AMOUNT`, `SIRET`,
`IBAN`, `EMAIL`) produisent du texte réaliste **non annoté** (§ 2.2). Seuls
`PER`, `ORG` et `ADDR` génèrent des spans.

| Mécanisme                     | Rôle                                                |
| ----------------------------- | --------------------------------------------------- |
| Nommage des slots (`:seller`) | Cohérence intra-document : même valeur si même nom  |
| Sections optionnelles `[?x]`  | Variabilité structurelle sans multiplier les fichiers |
| Blocs répétables `{{LINES:}}` | Lignes de facture, lignes de cotisation             |
| Permutation de blocs          | Ordre des sections variable, déclaré par template   |

**Décision** : parser dédié, pas `text/template`. Le rendu de la bibliothèque
standard ne restitue pas les offsets des valeurs insérées, qui sont précisément
ce dont on a besoin. Le parser produit un AST, le renderer une liste de segments
(§ 3.2). Plus court à écrire que le contournement.

### 6.3 Cohérence : de surface seulement

**Décision — pas de moteur de contraintes arithmétiques ni chronologiques.**

Un CRF linéaire à features locales (`pkg/features`) ne peut pas apprendre
« TTC = HT × 1,2 » ni un ordre de dates inter-lignes : il ne verra jamais la
différence entre un document juste et un document faux. Le mécanisme
`|derived=` / `|after=` du plan v1, et les contrôles `validate` correspondants,
coûtent cher pour un gain nul sur le modèle.

Ce qui reste, parce que ça change réellement les features observées :

- Cohérence nom de personne ↔ adresse email dérivée
- Cohérence adresse ↔ code postal ↔ commune (tirage lié dans la BAN)
- Cohérence organisation ↔ forme juridique
- Plages de valeurs plausibles (montants, dates dans une fenêtre crédible)

Les montants et dates restent monotones à l'œil (une échéance après une date
d'émission) parce que c'est trivial à obtenir par ordre de tirage, mais ce n'est
pas un invariant vérifié ni bloquant.

---

## 7. Négatifs difficiles — **dès le lot 1**

Sans contre-exemples, le CRF apprend des heuristiques trop grossières et produit
des faux positifs en production. C'est ce qui décidera si le corpus sert à
quelque chose, donc ça ne peut pas attendre un troisième lot.

| Piège visé                    | Négatif à injecter                                    |
| ----------------------------- | ----------------------------------------------------- |
| « majuscule = entité »        | Titres de section, mentions légales en capitales      |
| « prénom = `PER` »            | Noms de rue (`rue Victor Hugo`), noms de produit      |
| « toponyme = `LOC` »          | Marques et raisons sociales dérivées de toponymes     |
| « suite de mots capitalisés » | Intitulés de colonnes, libellés de champ              |
| « 9/14 chiffres = SIREN »     | Références commande, codes-barres, numéros de contrat |
| « IBAN-like = IBAN »          | Références bancaires internes, codes produit          |

Les trois dernières lignes valident le lot 0 : elles doivent être rejetées par
la clé de contrôle, pas par le CRF.

Taux d'injection configurable par catégorie, départ suggéré 10–20 % des
documents, à calibrer sur la précision mesurée sur documents réels (§ 10).

---

## 8. Bruit

### 8.1 Bruit OCR — renvoyé à `OCR.md`

**Décision — pas de simulation d'OCR dans ce projet.** Une matrice de confusion
devinée (`0↔O`, `rn→m`) ne ressemble pas au bruit du moteur réellement utilisé :
elle entraînerait le modèle sur des erreurs que la chaîne ne produit pas, et pas
sur celles qu'elle produit. La seule calibration propre passe par le rendu en
image et le vrai moteur OCR — ce qui appartient au plan `OCR.md`.

### 8.2 Bruit d'espacement des PDF natifs — dans le périmètre

L'examen du corpus d'observation a invalidé la position initiale, qui écartait tout module
de bruit. La facture d'artisan, un PDF **natif** produit par Stimulsoft Reports, donne à
l'extraction :

```
V is ite te c hniq ue e ffe c tué e le 31/ 01/ 2025, p o ur une
ins ta lla tio n à l' a d re s s e 20 rue d e la B e urlo g è re 21510
A ig na y le d uc .
```

Le crénage du générateur de PDF produit un espacement intra-mot que l'extraction
restitue littéralement. Aucun OCR n'est intervenu ; le pipeline subit déjà ce
bruit aujourd'hui, et il détruit toutes les features morphologiques de
`pkg/features` — préfixes, suffixes, forme, appartenance gazetteer. Un modèle
qui ne l'a jamais vu ne détecte rien sur ces passages, et une adresse complète y
passe inaperçue.

Contrairement au bruit OCR, celui-ci est **observable et mesurable directement**
sur les documents dont on dispose. Il entre donc au lot 2, appliqué segment par
segment (§ 3.2, où le recalcul d'offsets est gratuit), avec un taux calibré sur
le corpus d'observation et non deviné.

Voir `docs/observations-corpus-reel.md` § 5.

---

## 9. Tokenisation et projection BIO

### 9.1 Prérequis déjà levé

Le plan v1 posait comme prérequis l'extraction d'un tokeniseur partagé. C'est
fait : `pkg/tokenizer` (`UnicodeTokenizer`, offsets byte-précis, options FR/ES et
EN) est déjà consommé par `pkg/ner`, `pkg/features` et `pkg/anonymizer`. Le
générateur l'importe, point.

Il doit être configuré **exactement** comme à l'inférence : ponctuation
conservée dans la séquence, découpage aux seules fins de phrase (`. ! ? …`),
conformément aux défauts de `pkg/ner`. Le manifest (§ 11.2) enregistre cette
configuration.

### 9.2 Projection

Les spans issus de la concaténation des segments sont projetés en labels BIO sur
les tokens. Cas traités explicitement :

- Span ne coïncidant pas avec une frontière de token → politique fixée et
  documentée (extension au token englobant, avec compteur d'occurrences dans
  `stats` : un taux non nul signale un générateur de valeur mal conçu)
- Spans chevauchants → impossibles par construction (§ 3.2), assertion défensive
- Segment annoté vide → rejet du document et erreur de génération

### 9.3 Format de sortie

CoNLL BIO, compatible `pkg/corpus` et `cmd/train` sans adaptation. Une phrase
par bloc, ligne vide entre blocs, colonnes token + label.

**Décision** : émettre en parallèle un JSONL de métadonnées par document
(template source, seed, langue, type, inventaire des entités, sections tirées).
Indispensable à l'analyse d'erreur : savoir *quel template* génère les erreurs
du modèle est ce qui permet d'améliorer le corpus de façon ciblée plutôt qu'au
jugé.

---

## 10. Mesure

### 10.1 Deux indicateurs, deux coûts très différents

C'est le point qui débloque le projet. La **précision** se mesure sans
annotation : on passe 50 documents réels dans le modèle, on relit les spans
produits, on compte les faux positifs. Quelques heures de travail, répétable à
chaque itération. Seul le **rappel** exige un jeu annoté.

Conséquence sur l'ordonnancement : les lots 0, 1 et 2 sont pilotables
immédiatement par la précision. Le jeu annoté ne devient bloquant qu'au lot 3.

### 10.2 Jeu de test réel

100 à 200 documents authentiques annotés manuellement, jamais mélangés au
synthétique. Mesurer sur du synthétique évalue la capacité du modèle à
reproduire ses propres templates — l'écart entre F1 synthétique et F1 réel est
le seul indicateur qui pilote les décisions.

**Ce jeu est le vrai coût du projet, et il porte un risque qui lui est propre** :
ce sont des documents contenant du PII réel. Les collecter, les stocker et les
annoter demande une base légale, une durée de conservation et un contrôle
d'accès explicites — dans un projet dont c'est précisément le sujet. À arbitrer
nommément (qui produit ce jeu, sous quel régime, où il est stocké) avant le
lot 3, pas au moment où il bloque.

### 10.3 Non-régression sur WikiNER

À chaque entraînement mixte, mesurer aussi le F1 sur `wikiner_fr.test.conll`.
Le corpus synthétique ne doit pas dégrader le comportement sur le texte courant :
`anon-doc` traite aussi des documents en prose. Une baisse de plus de 0,5 point
sur WikiNER invalide le dosage du mélange.

### 10.4 Dosage du mélange

Paramètre à mesurer, pas à décréter. Le corpus synthétique est structurellement
répétitif ; noyer WikiNER sous 100 000 documents de facture spécialiserait le
modèle sur les templates. Balayer plusieurs ratios (10 %, 25 %, 50 % de
synthétique) et retenir celui qui maximise le F1 documents sans régression
WikiNER.

Premier point mesuré : **5,8 %** de tokens synthétiques (P1) donnent +0,9 point
sur WikiNER et font passer le F1 documents de 7 % à 96 % (§ 16, lot 1). Le
dosage n'est donc pas encore contraint par le haut — le balayage vers 10 % et
25 % reste à faire.

---

## 11. Reproductibilité

### 11.1 Modèle de seed

Seed hiérarchique dérivée : `seed_document = hash(seed_globale, type, langue, index)`.

Propriété obtenue : régénérer le document *n* ne nécessite pas de rejouer les
*n−1* précédents, et ajouter des documents en fin de corpus ne modifie pas les
existants.

**Décision** : ne jamais utiliser le générateur global de `math/rand`. Chaque
document reçoit son `*rand.Rand`, ce qui garantit l'indépendance et rend la
génération parallélisable sans état partagé.

### 11.2 Manifest de corpus

Fichier accompagnant chaque corpus : seed globale, version du générateur,
empreintes des gazetteers, empreintes des templates, configuration de
tokenisation, configuration complète, horodatage, comptages.

**Justification** : sans cela, un écart de performance entre deux runs
d'entraînement est inexploitable — impossible de distinguer un changement de
modèle d'un changement de corpus.

---

## 12. Volumétrie

### 12.1 Paliers

| Palier | Volume / type / langue | Objectif                                    |
| ------ | ---------------------- | ------------------------------------------- |
| P0     | 100                    | Validation du pipeline, inspection manuelle |
| P1     | 500                    | Première courbe d'apprentissage             |
| P2     | 3 000                  | Plateau attendu sur entités fréquentes      |
| P3     | 8 000 – 10 000         | Plateau sur entités rares                   |

### 12.2 Critère d'arrêt

Ne pas passer au palier suivant sans mesure. Entraîner sur 25 %, 50 %, 100 % du
palier courant et observer le F1 réel. Un gain inférieur à 0,5 point entre 50 %
et 100 % indique la saturation : générer davantage est sans effet.

**Point clé** : si la courbe plafonne à un F1 insuffisant, le problème est la
**variété structurelle des templates**, pas le volume. Le levier est de
retourner produire des templates et des sections optionnelles, pas d'augmenter
le compteur.

---

## 13. Validation du corpus

Sous-commande `validate`, exécutée en fin de génération et en CI.

| Contrôle                                          | Sévérité      |
| ------------------------------------------------- | ------------- |
| Séquences BIO bien formées (pas de `I-` orphelin) | erreur        |
| Spans chevauchants                                | erreur        |
| Labels hors du jeu `PER`/`LOC`/`ORG`              | erreur        |
| Réversibilité tokens → texte                      | erreur        |
| Span vide ou dégénéré                             | erreur        |
| Clés de contrôle valides sur les identifiants générés valides | erreur |
| Taux de spans non alignés sur une frontière de token | avertissement |
| Distribution des labels dans les bornes attendues | avertissement |
| Taux de duplication de valeurs d'entités          | avertissement |
| Couverture des templates                          | avertissement |

Les contrôles arithmétiques et chronologiques du plan v1 sont retirés (§ 6.3) :
ils bloquaient sur des propriétés que le modèle ne perçoit pas.

Un corpus qui échoue à un contrôle « erreur » n'est pas publié.

---

## 14. Statistiques

Sous-commande `stats` :

- Comptage d'occurrences par label, global et par type / langue
- Longueur moyenne des spans par label
- Diversité lexicale par label (valeurs distinctes, entropie)
- Taux de duplication exacte de documents
- Distribution des templates et des sections optionnelles
- Taux d'injection effectif des négatifs, par catégorie

**Usage** : détecter qu'une catégorie est sous-représentée avant d'avoir gaspillé
un cycle d'entraînement complet.

---

## 15. Tests

| Cible                     | Type de test                  | Justification                                |
| ------------------------- | ----------------------------- | -------------------------------------------- |
| `pkg/checksum`            | Table-driven, vecteurs connus | Correction algorithmique non négociable      |
| Concaténation de segments | Table-driven + fuzz           | Cœur de la garantie d'alignement             |
| Projection span → BIO     | Table-driven, cas limites     | Frontières de token, spans adjacents         |
| Reproductibilité          | Golden files                  | Même seed → même sortie, byte à byte         |
| Parser de templates       | Table-driven + fuzz           | Robustesse aux templates malformés           |
| Cohérence de surface      | Unitaires par règle           | Email dérivé, CP ↔ commune                   |

L'invariant central du plan v1 — « le texte aux offsets du span est exactement
la valeur insérée » — n'a plus besoin d'être testé en property-based : il est
garanti par le type `Segment` (§ 3.2). Un test unitaire sur la concaténation
suffit.

---

## 16. Lots de livraison

### Lot 0 — `pkg/checksum` en production ✅ livré

- `pkg/checksum` (Luhn + exception La Poste, mod-97-10 IBAN avec table de
  longueurs par pays, clé NIR avec substitution corse) et ses tests
- Champ `Validate` sur `ner.RegexPattern`, appliqué après sélection du span et
  avant marquage de la zone — un match rejeté ne masque pas les patterns suivants
- Branchement sur `BuiltinRegexPatterns` pour IBAN, SIRET, SIREN
- `ner.SIRENContextualPattern` : variante exigeant un marqueur textuel

**Mesure sur les six documents du corpus d'observation** :

| Type    | Avant | Après | Faux positifs éliminés                     |
| ------- | ----- | ----- | ------------------------------------------ |
| `IBAN`  | 11    | 2     | 9 — numéros de TVA intracom, réf. de dossier |
| `SIRET` | 4     | 4     | 0 — aucun faux positif dans l'échantillon  |
| `SIREN` | 3     | 2     | 1 avec le pattern contextuel (numéro FINESS) |

Aucun identifiant authentique n'est perdu. Le pattern SIREN contextuel n'est
**pas** activé par défaut : il échange du rappel contre de la précision, arbitrage
qui relève de la posture de conformité.

### Lot 1 — Socle français, facture ✅ livré (hors relecture humaine)

- `pkg/synth/gazetteer` — TSV pondéré, aplatissement `α`, socle embarqué
  (`go:embed`) surchargeable par un répertoire
- `pkg/synth/template` — parser dédié, AST, sections optionnelles, blocs
  répétables, zones de bruit ; fuzzé
- `pkg/synth/render` — segments annotés, offsets dérivés de la concaténation
- `pkg/synth/value` — `PER` (11 formes), `ORG` (6 formes), `ADDR` (12 formes),
  dates, montants, références, téléphones, emails, identifiants à clé valide
- `pkg/synth/generate` — parcours d'AST, bruit d'espacement, projection BIO via
  `pkg/tokenizer`, écriture CoNLL + JSONL + manifest
- `cmd/synthcorpus` — `generate`, `validate`, `stats`, `sample`
- **Négatifs difficiles** (§ 7) : 9 catégories
- 4 templates FR décalqués du corpus d'observation (facture publique, artisan, télécom,
  compte-rendu de laboratoire)

**Palier P0 mesuré** (100 documents, seed 1) : 6 270 phrases, 41 963 tokens,
1 602 entités (`PER` 422, `ORG` 437, `LOC` 743). `validate` : 0 erreur,
0 avertissement. 100 % des identifiants du corpus détectés par les patterns de
production ont une clé valide.

**Palier P1** (500 documents, seed 42) : 31 533 phrases, 209 518 tokens, soit
**5,8 %** des tokens du corpus mixte. P0 n'en représentait que 1,2 % — trop
dilué pour peser sur l'apprentissage.

**Entraînement mixte mesuré** (`data/mix/run.sh`, 2026-08-06). Deux bras,
hyperparamètres et corpus de dev identiques, seul le corpus d'entraînement
diffère : `wikiner.train.conll` (131 410 phrases) contre `mixed.train.conll`
(162 943 = WikiNER + P1). 12 époques, `early-stop 4`, `lr 0.1`, `l2 0.01`,
clusters Brown FR, sans gazetteer.

Deux passes : la première avec la génération `ORG` d'origine, la seconde après
le correctif décrit plus bas. Le bras témoin, qui ne dépend pas du corpus
synthétique, n'a été entraîné qu'une fois.

| Modèle            | F1 WikiNER | F1 synthétique |
| ----------------- | ---------- | -------------- |
| témoin            | 87,2 %     | 7,4 %          |
| mixte, passe 1    | 87,6 %     | 93,2 %         |
| mixte, passe 2    | 88,1 %     | 96,3 %         |
| mixte, passe 3    | **88,3 %** | **94,8 %**     |
| delta passe 3     | **+1,1**   | +87,4          |

Les passes 1 et 2 sont mesurées sur le jeu de test d'alors ; la passe 3, qui
introduit le croisement des formes et les libellés d'analyses composés, est
mesurée sur un jeu régénéré. Seules les colonnes d'une même passe se comparent.
Sur le jeu de la passe 3, le modèle de la passe 2 obtient 94,7 % — le
croisement des formes ne rapporte donc **rien en domaine connu** (94,7 → 94,8),
ce qui est attendu : il redistribue des formes que le corpus produisait déjà,
il n'en crée pas. Son apport est ailleurs, sur les documents non vus, et c'est
la mesure LOO ci-dessous qui l'établit. Par type il n'est pas neutre pour
autant : `ORG` 90,1 → 94,4 et `PER` 96,8 → 97,4, `LOC` 96,3 → 93,2.

Non-régression validée : **+1,1 point** sur WikiNER, très au-dessus du seuil de
−0,5 fixé au § 10.3. Le mélange à 5,8 % ne coûte rien au texte courant, il lui
rapporte. Par type, WikiNER : `MISC` +2,8, `LOC` +0,9, `PER` +0,3, `ORG` +0,5.

Le gain `MISC` mérite d'être noté : c'est le type que le témoin sur-prédit
massivement hors domaine (ci-dessous). Le corpus synthétique, qui ne contient
aucun `MISC`, apprend au modèle **quand ne pas** en poser — et cette retenue
profite aussi à WikiNER.

**Le F1 synthétique reste un indicateur plancher**, pas une mesure de
généralisation : 6,2 % des phrases porteuses d'entités du test apparaissent
verbatim dans l'entraînement, les deux corpus étant tirés des mêmes gazetteers.
Ce résidu n'est plus réductible sans les séparer, ce qui serait discutable — le
modèle doit justement généraliser sur un vocabulaire partagé. Il vaut « le
modèle a-t-il appris quelque chose de ce domaine », et rien de plus.

**Le chiffre réellement instructif est celui du témoin** : 7,2 % de F1, avec
**1 055 entités `MISC` prédites pour 0 attendue**. Un modèle WikiNER seul ne se
contente pas de manquer les entités d'un document administratif, il en invente
massivement sur les en-têtes, les numéros et les libellés — exactement le
comportement qui rend l'anonymisation inexploitable en production. Le corpus
synthétique corrige d'abord cela.

Les valeurs absolues sont sous la référence publiée de 93,6 % (12 époques au
lieu de 20, gazetteers de la recette non chargés). Les deux bras en pâtissent à
l'identique ; seul le delta est interprétable. Aucun de ces modèles n'est un
candidat à la publication.

Le F1 de dev est bruité d'une époque à l'autre (jusqu'à 9 points d'écart entre
deux époques consécutives à `lr=0.1`). Les deux bras terminent au même niveau
de dev (0,8892 pour le témoin, 0,8903 pour le mixte), ce qui écarte l'hypothèse
d'un delta dû à une trajectoire SGD plus chanceuse d'un côté.

**Correctif ORG appliqué** (2026-08-06). La dénomination n'est plus tirée dans
une liste plate de 16 entrées mais **composée** :
`tête + qualificatif + domaine + ancrage territorial`, sur quatre gazetteers
(`org_types`, `org_qualificatifs`, `org_domaines`, `org_territoires`). Trois
contraintes rendent le résultat lisible :

- le qualificatif s'accorde en genre avec la tête (métadonnée `g`) ;
- le domaine appartient à la **famille** de la tête (métadonnée `f` :
  `sante`, `social`, `administration`, `commerce`, `technique`, `assurance`),
  pour qu'un compte-rendu d'analyses ne soit pas signé d'un « Syndicat de
  Construction du Var » ;
- l'ancrage territorial devient obligatoire dès qu'un des deux compléments
  manque, ces formes courtes ne comptant qu'une centaine de combinaisons par
  famille.

Les templates imposent la famille par l'argument `{{ORG:slot|famille=sante}}`,
validé au parsing — une famille inconnue est une erreur, pas un repli
silencieux. La liste plate subsiste, portée à 150 entrées et minoritaire dans
le tirage : elle porte les formes que la composition ne produit pas (marques,
sigles lexicalisés, noms propres), sans quoi le corpus serait uniformément
régulier. Le nom court est désormais le **sigle** (`CHR`, `CAF`), forme ORG
fréquente dans les documents réels et jusqu'ici absente du corpus.

| Palier P1 | avant | après |
| --- | --- | --- |
| `ORG` distincts / occurrences | 582 / 2 185 | **989** / 2 185 |
| entropie `ORG` | 8,01 | **9,62** |
| phrases de test vues en entraînement | 9,8 % | **6,2 %** |

Le résidu de 6,2 % n'est plus de la mémorisation de gabarit : ce sont des
lignes « code postal + commune » et « PRÉNOM NOM », un recouvrement de
vocabulaire irréductible entre deux corpus tirés des mêmes gazetteers.

**Reste à faire pour clore le lot** : la relecture humaine des documents. Les 20
à 30 templates visés sont à 4 — suffisant pour valider le pipeline, insuffisant
pour la variété (lot 2).

#### Ce que le corpus généralise réellement

Le F1 de 96,3 % ne dit rien tant qu'on ignore s'il mesure une compétence de
domaine ou la mémorisation de quatre gabarits. Le protocole
(`data/mix/loo/run.sh`) retire le compte-rendu de laboratoire de
l'entraînement et l'utilise seul comme jeu de test.

| Modèle | corpus d'entraînement | F1 sur le laboratoire |
| --- | --- | --- |
| témoin | WikiNER | 5,4 % |
| mixte | WikiNER + les 4 templates | 98,5 % |
| mixte-LOO | WikiNER + les 3 autres | **25,5 %** |

L'effondrement est massif, et le détail par type l'explique — il suit
exactement la **couverture des formes de surface**, pas la parenté des
documents :

| Type | formes du labo présentes ailleurs | F1 LOO | F1 témoin |
| --- | --- | --- | --- |
| `LOC` | 5 sur 7 | 44,4 % (rappel 72,5 %) | 8,3 % |
| `PER` | 0 sur 6 | 18,3 % | 13,0 % |
| `ORG` | 0 sur 2 | 1,1 % | 0,2 % |

`LOC` transfère parce que `street`, `citycode`, `citycode_caps` et `city` sont
communes aux quatre templates. `PER` ne transfère presque pas : le laboratoire
est le seul à employer `titre_nom`, `nom_naissance`, `nom_prenom_etiquette` et
`nom_seul`. `ORG` ne transfère pas du tout : `caps_tirets` n'existe nulle part
ailleurs.

**Conséquence directe sur le lot 2.** Ce que le CRF apprend est un répertoire de
formes, pas un registre administratif abstrait. Ajouter des templates n'apporte
que les formes qu'ils introduisent, et un template de plus qui réemploie les
formes existantes n'apporte rien. La priorité n'est donc pas « 4 → 30
templates » mais **la couverture de l'espace des formes** : croiser les formes
existantes entre types de documents, et n'ajouter des templates que pour les
formes qu'ils sont seuls à porter.

**Limite du protocole, et sa levée.** Retirer un template retire à la fois un
gabarit de document et un jeu de formes. Les deux variables ont été séparées en
réinjectant les quinze formes du laboratoire dans les trois autres templates
(sections optionnelles : fiche client étiquetée, maître d'ouvrage titré,
titulaire de contrat avec nom de naissance, raison sociale en capitales-tirets,
code postal à tiret…), puis en refaisant le LOO à l'identique — même jeu de
test, mêmes hyperparamètres.

| | LOO | LOO à formes croisées | mixte complet |
| --- | ---: | ---: | ---: |
| précision | 19,6 % | 35,2 % | 98,4 % |
| **rappel** | 36,7 % | **79,9 %** | 98,6 % |
| F1 | 25,5 % | 48,9 % | 98,5 % |
| F2 | 31,2 % | 63,7 % | 98,6 % |
| `PER` F1 | 18,3 % | 61,6 % | 99,2 % |
| `LOC` rappel | 72,5 % | **99,6 %** | 96,2 % |
| `ORG` F1 | 1,1 % | 31,5 % | 100 % |

**Le rappel tient à la forme, la précision tient à la structure.** Le seul
croisement des formes — sans ajouter un seul type de document — fait passer le
rappel de 36,7 à 79,9 % sur un document jamais vu, et le `LOC` à 99,6 %. Une
entité dont la forme de surface a été vue ailleurs est retrouvée, où qu'elle
soit.

La précision, elle, plafonne à 35,2 % : le modèle sur-génère massivement (1 540
`LOC` prédites pour 480 réelles). N'ayant jamais vu l'agencement d'un
compte-rendu médical — ses libellés de champs, ses tableaux de résultats, ses
mentions réglementaires — il y applique ses attentes de facture et étiquette
trop. C'est là, et seulement là, que le nombre de gabarits compte : un template
enseigne où les entités **ne sont pas**.

`ORG` remonte moins que `PER` (31,5 contre 61,6), ce qui était prévisible : la
famille `sante` n'existe dans aucun des trois autres templates, le vocabulaire
médical des organisations reste donc inédit même après croisement des formes.

Contrôle de non-régression : 87,3 % sur WikiNER (témoin 87,2 %, mixte 88,1 %).

**Conséquence sur le lot 2.** Les deux leviers ne servent pas la même métrique,
et l'ordre s'en déduit. Pour la conformité RGPD, où un faux négatif est une
fuite et où le F2 est la référence, le croisement des formes double la
performance à coût quasi nul — c'est le levier prioritaire. La multiplication
des templates sert la précision, donc l'exploitabilité du document anonymisé,
et vient ensuite.

Contrôle de non-régression : le LOO reste à 86,9 % sur WikiNER (témoin 87,2 %,
mixte 88,1 %), écart dans le bruit SGD constaté entre époques.

#### Précision sur documents réels

Les deux modèles passés sur cinq PDF natifs du corpus d'observation, OCR
désactivé. Sans annotation de référence, seule la précision est lisible ; le
rappel ne l'est pas.

| Type | témoin | mixte | mixte, passe 3 |
| --- | ---: | ---: | ---: |
| `PER` | 37 | 23 | 20 |
| `LOC` | 55 | 69 | **80** |
| `ORG` | 55 | 20 | 15 |
| `MISC` | 206 | 53 | **32** |

Les 206 `MISC` du témoin sont presque intégralement du bruit : totaux, en-têtes
de colonnes, unités de mesure, fragments de mentions légales. Trois gains du
mixte sont attribuables à des choix de conception :

- **les adresses** — sur la facture d'établissement public, les deux modèles
  n'ont aucune `LOC` en commun ; le mixte sort les quatre adresses réelles ;
- **le bruit de crénage** — sur la facture d'énergie, le mixte reconnaît les
  codes postaux et l'adresse en champ libre disloqués par l'espacement
  intra-mot (§ 5 des observations), que le témoin ignore entièrement ;
- **la raison sociale portant un patronyme** — le témoin la coupe en `ORG` +
  `PER`, le mixte produit un span `ORG` unique. C'est le négatif `orgperson`
  qui opère.

Deux défauts résiduels, tous deux à traiter :

- des `LOC` d'un seul caractère sur le document le plus bruité — post-filtre de
  longueur minimale, pas un problème de corpus ;
- des libellés d'analyse médicale classés `PER` (`Ratio TCK`, `Volume
  Plaquettaire Moyen`). Le `decoy:analyse` ne produisait que des libellés d'un
  mot ; il lui manquait les libellés composés à capitales initiales, dont la
  signature de surface est celle d'un prénom-nom.

L'élargissement du `decoy:analyse` à trente libellés composés **corrige la
moitié du défaut** : `Ratio TCK`, `Conclusion Allongement`, `Stago Néoplastine`
et `Date` ne sont plus étiquetés `PER`, mais `Céphaline Kaolin` et `Volume
Plaquettaire Moyen` résistent bien qu'ils figurent désormais mot pour mot dans
le gazetteer. Le négatif ne suffit donc pas seul : sur le document réel ces
libellés apparaissent isolés en tête de colonne, un contexte que le bloc
`@block analyses` — libellé suivi d'une valeur et d'un code — ne reproduit pas.
C'est le contexte gauche-droite qu'il faut varier, pas la liste.

### Lot 2 — Variété structurelle

Priorité réordonnée par la mesure de généralisation du lot 1 : le croisement des
formes porte le rappel, la variété des gabarits porte la précision.

- ✅ **Croisement des formes entre templates** — livré, rappel 36,7 → 79,9 %
- ✅ Régénération du corpus et réentraînement du mixte — passe 3, 88,3 % WikiNER
- ✅ Élargissement du `decoy:analyse` — moitié du défaut corrigée
- ✅ Post-filtre de longueur minimale (`ner.MinRunesFilter`, opt-in)
- **Variation du contexte des libellés d'analyse** : le négatif seul ne suffit
  pas quand le contexte réel diffère de celui du bloc générateur
- Sections optionnelles, blocs répétables, permutation de blocs
- Cohérence de surface complète (§ 6.3)
- Module de bruit d'espacement intra-mot, calibré sur le corpus d'observation (§ 8.2)
- Variété de templates par apport LLM validé humainement, **pour les formes
  qu'ils sont seuls à porter**
- Paliers P1 puis P2, balayage du dosage de mélange (§ 10.4)

**Critère de sortie** : première courbe d'apprentissage, précision réelle en
hausse, WikiNER stable.

### Lot 3 — Jeu de test réel et extension typologique

Le jeu annoté (§ 10.2) devient bloquant ici, avec son cadre juridique arbitré.

- Jeu de test réel : 100 – 200 documents annotés
- Devis (proche de la facture, forte réutilisation)
- **Compte-rendu médical** — type ajouté après examen du corpus d'observation : le
  plus dense en données sensibles de tout l'échantillon (patient mineur, date de
  naissance, représentant légal, médecin prescripteur, établissement, données de
  santé), et porteur d'une structure absente des factures — un pied de page
  répétant l'identité du patient à chaque page, qui exerce directement la
  `Session` cross-segments de `pkg/anonymizer`
- Bulletin de paie (densité élevée, structure tabulaire)
- Email professionnel (structure libre)

**Critère de sortie** : F1 réel mesuré par type de document.

### Lot 4 — Extension linguistique

- Qualification et intégration des gazetteers ES et EN
- Localisation des formats (dates, montants, téléphones, adresses)
- Templates ES et EN
- Paliers P2 puis P3 sur les trois langues

---

## 17. Risques

| Risque                                                | Impact                                      | Mitigation                                              |
| ----------------------------------------------------- | ------------------------------------------- | ------------------------------------------------------- |
| Jeu de test réel jamais produit (coût, cadre juridique) | Rappel non mesurable, lots 3–4 non pilotables | Précision mesurable sans annotation (§ 10.1) ; arbitrage nommé avant lot 3 |
| Sur-spécialisation sur les templates                  | Régression sur texte courant                | Corpus mixte, non-régression WikiNER en garde-fou (§ 10.3) |
| Variété de templates insuffisante                     | Plateau de F1 bas, non résolu par le volume | Mesurer tôt (P1), diagnostiquer avant de scaler         |
| Gazetteers ES / EN de qualité insuffisante            | Performance dégradée hors français          | Lot dédié, qualification explicite avant intégration    |
| Distribution de gazetteers trop plate ou trop piquée  | Priors lexicaux mal appris                  | Exposant d'aplatissement explicite et réglable          |
| Dérive de configuration de tokenisation               | Décalage systématique corpus / production   | `pkg/tokenizer` partagé, configuration dans le manifest |

Le désalignement d'offsets — risque n°1 du plan v1 — ne figure plus au tableau :
il est éliminé par construction (§ 3.2).

---

## 18. Décisions consolidées

| Sujet                    | Décision                                          | Motif                                                          |
| ------------------------ | ------------------------------------------------- | -------------------------------------------------------------- |
| Jeu de labels            | `PER`/`LOC`/`ORG` existants, corpus mixte WikiNER | Continuité des modèles publiés, pas de régression sur la prose |
| Identifiants à clé       | Regex + `pkg/checksum` en production, hors CRF    | 100 % de précision par le code, ~98 % par le modèle            |
| `DATE`/`AMOUNT`/`REF`    | Générés, non annotés                              | Pas du PII ; masquer produit un document inexploitable         |
| Emplacement              | Dans ce dépôt (`pkg/synth`, `cmd/synthcorpus`)    | Réutilise `pkg/tokenizer` et `pkg/corpus` sans duplication     |
| Renderer                 | Liste de segments, offsets dérivés                | Rend le désalignement d'offsets inexprimable                   |
| Cohérence sémantique     | De surface uniquement                             | Un CRF local ne perçoit pas l'arithmétique inter-lignes        |
| Bruit OCR                | Hors périmètre, renvoyé à `OCR.md`                | Une matrice devinée entraîne sur des erreurs qui n'existent pas |
| Bruit d'espacement PDF   | Dans le périmètre, calibré sur le corpus d'observation         | Présent sur des PDF natifs, il détruit les features morphologiques |
| Templates                | 20–40 composables par type, forme **extraite**    | La composition couvre plus d'espace que le volume ; le CRF ne voit que le texte extrait, colonnes aplaties |
| Négatifs difficiles      | Lot 1, pas lot 3                                  | Ils déterminent si le corpus a une valeur                      |
| Rôle du LLM              | Variété structurelle en amont, hors ligne         | Mauvais sur les valeurs et l'annotation, bon sur la structure  |
| Aléatoire                | Instance par document, seed dérivée               | Reproductibilité + parallélisation sans état partagé           |
| Granularité `LOC`        | Span unique pour l'adresse                        | Aligné sur l'usage d'anonymisation                             |
| Jeu de test              | Réel exclusivement                                | Le F1 synthétique ne mesure rien d'utile                       |
| Métadonnées              | JSONL par document + manifest                     | Analyse d'erreur ciblée, comparabilité des runs                |

---

## 19. Prérequis avant démarrage

1. **Aucun pour le lot 0** — il se livre immédiatement.
2. **Lot 1** : gazetteers FR pondérés préparés (fréquences INSEE jointes aux
   listes existantes de `data/`).
3. **Lot 3** : jeu de test réel arbitré — qui le produit, sous quelle base
   légale, où il est stocké, avec quelle durée de conservation.
4. **Qualification juridique des gazetteers** : compatibilité des licences avec
   l'usage envisagé, en particulier la redistribution éventuelle du corpus
   généré.

La chaîne d'entraînement CRF est déjà opérationnelle (`cmd/train`, `cmd/eval`,
CoNLL BIO, F1 par label) : ce prérequis du plan v1 est levé.
