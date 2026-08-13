# Plan — jeu de test réel annoté

**Objet** : produire le jeu d'évaluation qui manque à go-anon, sur documents
authentiques, annoté à la main.

**Pourquoi maintenant** : la fin du lot 2 de `DATASET.md` a montré que le F1
synthétique est saturé (98,8 %) et **diverge** de la performance réelle — le
modèle le mieux noté produit le plus de faux positifs sur un document réel.
Aucune métrique existante ne distingue plus une amélioration d'un
sur-ajustement au générateur. Tant que ce jeu n'existe pas, le travail sur le
corpus avance à l'aveugle.

---

## 0. Trois décisions préalables

Elles conditionnent tout le reste et ne sont pas techniques.

### 0.1 D'où viennent les documents

Trois provenances, par ordre de confort juridique décroissant :

| Provenance | Confort | Représentativité |
| --- | --- | --- |
| Documents dont vous êtes destinataire ou titulaire | bon | bonne |
| Documents publics (annonces légales, BOAMP, marchés) | excellent | faible — peu de PII de particuliers |
| Documents de tiers (clients, employeur) | dépend d'un accord écrit | excellente |

Le cas réaliste est le premier. Vos propres factures, relevés, courriers
administratifs contiennent vos données — que vous pouvez traiter — **et celles
de tiers** : signataires, agents, contacts, prescripteurs. Ce sont ces tiers qui
demandent une base légale, pas vous.

Pour de la R&D sur un outil de protection des données, l'intérêt légitime
(art. 6.1.f) se soutient : la finalité est la minimisation elle-même, le
traitement est interne, non diffusé, et la volumétrie est faible. Ce
raisonnement doit être **écrit avant la collecte**, pas reconstruit après. Il
tient d'autant mieux que les mesures du § 8 sont en place.

### 0.2 Qui annote

Si c'est vous seul, l'accord inter-annotateur ne peut pas être mesuré de façon
indépendante. Le § 6 propose un substitut, mais il est plus faible : sachez-le
avant de commencer plutôt que de le découvrir au moment de publier un chiffre.

### 0.3 Durée de conservation

Fixez-la maintenant, avec la date de suppression. Un jeu de test réel est utile
tant que le modèle évolue ; passé ce cap, il est du risque pur.

---

## 1. Dimensionnement

Une métrique de rappel se mesure sur les entités, pas sur les documents. Pour un
rappel réel autour de 90 %, la demi-largeur de l'intervalle de confiance à 95 %
donne :

| Précision voulue | Entités **par type** |
| --- | ---: |
| ± 5 points | 138 |
| ± 3 points | 384 |
| ± 2 points | 864 |

En dessous de 100 entités par type, un écart de 5 points n'est pas
interprétable — c'est exactement le piège dans lequel les mesures du lot 2 sont
tombées.

Sur les six documents d'observation, la densité est de 8 à 15 entités
annotables par document. La cible est donc :

- **palier 1 — 30 documents** : valide le guide et l'outillage, ne produit pas
  encore de mesure publiable
- **palier 2 — 100 documents** (~1 000 entités, ~330 par type) : première
  mesure exploitable, ± 3 points
- **palier 3 — 150 documents** : mesure de référence, marge pour le
  scellement du § 7

## 2. Composition — stratifier, et brider les émetteurs

Un corpus de cent factures du même fournisseur mesure un gabarit, pas un
domaine. Deux règles :

- **quota par émetteur** : aucun émetteur au-delà de 15 % du corpus
- **stratification explicite** sur les axes qui ont montré un effet :

| Axe | Modalités à couvrir |
| --- | --- |
| Type de document | facture, devis, courrier administratif, compte-rendu, attestation, bulletin |
| Producteur PDF | au moins 6 chaînes distinctes (LaTeX, Ecrion, Stimulsoft, PDFium, iText, Word) |
| Extraction | texte natif propre / natif à crénage disloqué / scan sans couche texte |
| Densité | documents pauvres en entités inclus — ils mesurent la précision |

Le dernier point est le plus souvent oublié. Un corpus composé uniquement de
documents riches en PII surestime le rappel et n'expose jamais les faux
positifs.

## 3. Sur quoi annoter — le texte extrait, pas le PDF

Annotez la sortie de `pkg/pdf`, pas le document visuel. Trois raisons : c'est
exactement ce que le modèle voit, les offsets sont ceux du pipeline, et
`bin/eval` consomme le CoNLL résultant sans code nouveau.

**Le piège** : le texte extrait dépend de la version de `pkg/pdf`. Un
changement du walker désaligne silencieusement toutes les annotations. Chaque
document annoté doit donc porter, dans un manifest :

- le SHA-256 du PDF source
- le SHA-256 du texte extrait
- le commit de go-anon ayant produit l'extraction

Une vérification au chargement compare le hash du texte : s'il a bougé, le jeu
refuse de s'évaluer plutôt que de produire un F1 faux. C'est le même principe
que le `FeatureSchema` — rendre l'incohérence bruyante.

## 4. Guide d'annotation — à écrire avant le premier document

C'est le livrable qui détermine la qualité du jeu. Il doit trancher, avec un
exemple pour chaque règle, les cas déjà identifiés sur le corpus d'observation :

1. **Granularité de l'adresse.** Un span unique couvrant voie + code postal +
   commune, ou deux spans distincts ? `DATASET.md` § 18 annonce « span unique »
   mais le générateur produit deux spans (`street` et `citycode` sont des
   placeholders séparés). **Cette contradiction doit être tranchée ici** : elle
   change mécaniquement le F1 en matching strict, et l'incohérence actuelle
   fausse déjà la comparaison entre corpus synthétique et réel.
2. **Raison sociale contenant un patronyme** — span `ORG` couvrant l'ensemble,
   jamais un `PER` imbriqué. Déjà la règle du générateur, à confirmer.
3. **Civilités et titres** — `Mme`, `Dr`, `Maître` inclus ou exclus du span.
4. **Deux personnes juxtaposées sans séparateur** — politique de coupure.
5. **Nom de naissance entre parenthèses** — un span ou deux.
6. **Entités disloquées par le crénage** (`21510 BEAU LIEU`) — annoter le span
   tel qu'il apparaît, bornes incluses.
7. **Occurrences répétées** (pied de page d'identité patient) — toutes
   annotées ; c'est ce que mesure la `Session` cross-segments.
8. **`MISC`** — le générateur n'en produit pas, le modèle en prédit. Décider
   s'il est annoté ou traité comme hors périmètre, et le dire explicitement :
   c'est la différence entre mesurer 1 055 faux positifs et n'en mesurer aucun.

## 5. Deux métriques, pas une

Le F1 NER strict n'est pas la métrique d'anonymisation. Une donnée personnelle
masquée sous le mauvais type reste masquée : la fuite n'a pas eu lieu.

- **F1 strict par type** — pour piloter le modèle, comparable à WikiNER
- **couverture PII** — insensible au type : proportion des caractères
  personnels réellement couverts par un span, quel qu'il soit. C'est la
  métrique de conformité, et c'est elle qu'on publie dans `docs/rgpd.md`

Les deux se calculent sur les mêmes annotations ; seule l'agrégation change.
Prévoyez la seconde dans `pkg/ner/metrics.go` dès le palier 1 — l'ajouter après
coup obligerait à rejouer les mesures.

## 6. Pré-annotation et biais d'ancrage

Annoter mille entités à froid est long ; pré-annoter puis corriger est trois à
cinq fois plus rapide. Mais **pré-annoter avec le modèle qu'on évalue biaise le
jeu en sa faveur** : l'annotateur valide les erreurs plausibles au lieu de les
voir.

Protocole :

- pré-annoter avec les **regex + gazetteers seuls**, jamais avec le CRF évalué
- annoter **20 documents entièrement à froid**, tirés au hasard, et comparer le
  taux de correction à celui des documents pré-annotés. Un écart marqué mesure
  le biais et doit être publié avec les résultats
- en cas de doute sur un span, trancher **sans** consulter la prédiction du
  modèle

## 7. Gel, et hygiène d'usage

Un jeu de test qu'on consulte à chaque itération cesse d'être un jeu de test :
l'expérimentateur s'y surajuste, même sans entraîner dessus.

Découpez en deux dès la constitution :

- **test de travail (⅔)** — consultable librement, sert à l'analyse d'erreur
- **test scellé (⅓)** — ouvert uniquement aux jalons de publication de modèle,
  jamais pour arbitrer une itération. Chaque ouverture est datée dans le
  manifest

Si les deux divergent significativement, le travail s'est surajusté au premier,
et c'est le second qui dit la vérité.

**Le jeu réel n'entre jamais dans l'entraînement**, ni comme corpus de dev :
`DATASET.md` § 1.3 en fait une contrainte structurante. Le dev reste WikiNER.

## 8. Stockage et sécurité

- hors dépôt (`data/` est déjà gitignoré), sur volume chiffré
- aucune copie vers un service tiers — **y compris un LLM**, y compris pour
  « aider à annoter »
- les PDF sources et le texte extrait ont le même niveau de sensibilité que les
  annotations
- suppression à l'échéance du § 0.3, vérifiée
- une entrée au registre des traitements : finalité, base légale, catégories de
  données, durée, mesures techniques

## 9. Ce qui est publiable

Publiable : les métriques, le guide d'annotation, la distribution statistique
du corpus (nombre de documents par strate, densité par type), les hashes.

Jamais publiable : les documents, le texte extrait, les annotations. Un corpus
« anonymisé » ne peut pas servir de jeu de test — il aurait été traité par
l'outil qu'il doit évaluer.

## 10. Outillage à écrire

Minimal, et largement dérivé de l'existant :

```
cmd/testset/
  extract      PDF → texte + manifest (hashes, commit)
  preannotate  texte → CoNLL pré-rempli par regex + gazetteers
  validate     invariants BIO, spans, hashes, dérive d'extraction
  stats        distribution par strate, densité, couverture des axes
```

`validate` et `stats` reprennent la logique de `cmd/synthcorpus`. L'évaluation
elle-même ne demande rien : `bin/eval -test <corpus.conll> -format conll`
fonctionne tel quel.

## 11. Séquence

| Étape | Sortie | Bloque |
| --- | --- | --- |
| 1. Décisions § 0, écrites | note de cadrage | tout le reste |
| 2. Guide d'annotation, § 4 | `docs/guide-annotation.md` | la collecte |
| 3. Outillage § 10 | `cmd/testset` | l'annotation |
| 4. Palier 1 — 30 documents | jeu pilote | — |
| 5. Contrôle de cohérence § 6 | taux d'accord, biais mesuré | la suite |
| 6. Révision du guide | v2 | la suite |
| 7. Paliers 2 et 3 | jeu de référence | la clôture du lot 2 |

L'étape 5 est celle qu'on saute et qu'on regrette. Si la seconde passe sur les
mêmes 20 documents diverge de plus de 5 % de la première, le guide est ambigu :
corrigez-le et **réannotez le palier 1** avant d'en produire cent de plus.

## 12. Effort

À 8 minutes par document pré-annoté et 25 minutes à froid :

| Palier | Documents | Heures d'annotation |
| --- | ---: | ---: |
| 1 | 30 | ~5 |
| 2 | +70 | ~10 |
| 3 | +50 | ~7 |

Environ trois jours de travail effectif pour le jeu complet, hors outillage et
hors collecte. C'est le prix du seul instrument de mesure fiable dont le projet
puisse disposer.
