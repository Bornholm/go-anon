# Syntaxe des templates

Un template décrit **la forme extraite** d'un document, pas sa mise en page :
colonnes déjà aplaties, remplissages d'espaces explicites. Voir
`docs/observations-corpus-reel.md` § 1 pour le motif.

## En-tête

```
type: facture
lang: fr
source: facture d'établissement public (Ecrion XF)
weight: 1.0
---
```

`source` désigne le document du corpus d'observation dont la structure est reprise.
Traçabilité : savoir de quelle forme réelle dérive un template est ce qui permet
de juger si le corpus couvre le terrain.

## Placeholders

**La casse porte le sens** : un placeholder en majuscules produit une entité
annotée, en minuscules un texte réaliste non annoté (`DATASET.md` § 2.2).

| Placeholder                  | Annoté | Produit                                     |
| ---------------------------- | ------ | ------------------------------------------- |
| `{{PER:slot\|form=…}}`       | `PER`  | nom de personne                             |
| `{{ORG:slot\|form=…}}`       | `ORG`  | raison sociale                              |
| `{{ADDR:slot\|form=…}}`      | `LOC`  | voie, commune, ou adresse complète          |
| `{{date:slot\|format=…}}`    | non    | date                                        |
| `{{amount:slot}}`            | non    | montant                                     |
| `{{ref:slot\|pattern=…}}`    | non    | référence interne                           |
| `{{siret:slot}}` `{{siren:}}`| non    | identifiant à clé valide                    |
| `{{iban:slot}}`              | non    | IBAN à clé valide                           |
| `{{email:slot\|from=…}}`     | non    | email, dérivé d'un slot `PER` ou `ORG`      |
| `{{phone:slot\|form=…}}`     | non    | téléphone                                   |
| `{{decoy:kind}}`             | non    | négatif difficile (§ « Négatifs »)          |
| `{{pad:N-M}}`                | non    | N à M espaces, pour l'entrelacement colonne |

Le **slot** garantit la cohérence intra-document : deux placeholders de même
slot rendent la même valeur. `{{email:x|from=buyer}}` dérive l'adresse du nom
tiré pour le slot `buyer`.

### Formes de `PER`

Relevées sur documents réels, pondérées par le générateur :

| `form=`                | Rendu                                  |
| ---------------------- | -------------------------------------- |
| `civ_prenom_nom`       | `Mme Carmen ROUSSEL`                      |
| `civ_nom_prenom`       | `Monsieur Bertrand Julien`               |
| `prenom_nom`           | `Olivier Vandamme`                        |
| `prenom_nom_caps`      | `CARMEN ROUSSEL MARQUES`                  |
| `nom_prenom_caps`      | `BERTRAND MATHIS`                       |
| `nom_seul`             | `BERTRAND`                                |
| `titre_nom`            | `Dr WEISS Gérard`                  |
| `titre_nom_caps`       | `Dr TALBI KARIM`                     |
| `nom_naissance`        | `Mme BERTRAND Carmen (Née ROUSSEL MARQUES)`  |
| `nom_prenom_etiquette` | `BERTRAND Prénom : Mathis`              |
| `couple`               | `BERTRAND JULIEN Roussel Marques Carmen`    |

`couple` produit **deux spans adjacents sans séparateur**. Cas réel (facture d'énergie) sans
indice de frontière : la politique de découpage est fixée par le générateur, pas
par le décodage BIO.

### Formes de `ORG`

| `form=`               | Rendu                                             |
| --------------------- | ------------------------------------------------- |
| (défaut)              | `Caisse Régionale d'Allocations Familiales de la Drôme` |
| `court`               | `CRAF` — sigle de la dénomination                 |
| `caps`                | `CLINIQUE PRIVEE ANESTHESIE`                        |
| `caps_tirets`         | `BIOANALYS−BOURGOGNE−FRANCHE−COMTE`                |
| `juridique_patronyme` | `SARLU Cheminées Fabien Coudray`              |
| `juridique_capital`   | `Telcom Mobile – SAS au capital de 412 500 000 Euros` |

`juridique_patronyme` produit une raison sociale **contenant un nom de
personne**, forme dominante chez les artisans (facture d'artisan) et principal piège à faux
positif `PER`. Le span annoté couvre la raison sociale entière, patronyme
compris.

`caps_tirets` utilise le signe moins U+2212, tel que restitué par l'extraction
du compte-rendu de laboratoire, et non le trait d'union U+002D.

#### `famille=` — secteur de l'organisation

`{{ORG:lab|famille=sante}}` contraint la dénomination à un secteur :
`sante`, `social`, `administration`, `commerce`, `technique`, `assurance`.
Sans cet argument, le secteur est tiré au hasard — acceptable pour un artisan,
pas pour un laboratoire d'analyses, qui se retrouverait à signer un « Syndicat
de Construction du Var ».

Une famille non vide écarte aussi la raison sociale bâtie sur un patronyme :
pour garder cette forme, ne pas mettre `famille=` du tout.

Une valeur inconnue est une **erreur de parsing**. Sans quoi une faute de
frappe retomberait silencieusement sur l'ensemble des familles, et le document
mêlerait les secteurs sans que rien ne le signale.

### Formes de `ADDR`

| `form=`             | Rendu                                        |
| ------------------- | -------------------------------------------- |
| `street`            | `2 Bis petite rue du Lavoir`            |
| `street_caps`       | `1 GRANDE RUE`                               |
| `citycode`          | `21400 Montigny le roi`                        |
| `citycode_caps`     | `21400 CHATILLON`                             |
| `citycode_dash`     | `21400 − CHATILLON`                           |
| `citycedex`         | `92120 Montrouge Cedex`                         |
| `citycedex_caps`    | `75371 PARIS CEDEX 08`                       |
| `city`              | `Dijon`                                      |
| `city_caps`         | `PARIS`                                      |
| `inline`            | `16 rue des Petits Carreaux 75002 Paris`    |
| `inline_dash_caps`  | `22 AVENUE DES ACACIAS − 21000 DIJON`   |
| `freeform`          | `EN FACE DE LA CABINE TELEPHO`               |

Toutes les formes d'un même slot sont cohérentes entre elles : même voie, même
commune, même code postal. `street` et `citycode` forment deux spans distincts,
comme dans un bloc adresse réel.

## Sections optionnelles

```
[?contact]Contact : {{PER:seller_contact}} — {{email:c|from=seller_contact}}[/]
```

Tirée ou non selon une probabilité de génération. Le nom permet de corréler
plusieurs sections (même nom = même décision).

## Blocs répétables

```
@block lignes
{{decoy:designation}}{{pad:4-8}}{{amount:pu}}{{pad:2-6}}{{amount:total}}
@end

{{LINES:3-8}}
```

`{{LINES:3-8}}` insère le bloc `lignes` entre 3 et 8 fois.

## Négatifs

`{{decoy:kind}}` insère un contre-exemple sans annotation
(`DATASET.md` § 7) :

| `kind=`       | Produit                                                     |
| ------------- | ----------------------------------------------------------- |
| `voie_nom`    | nom de voie bâti sur prénom + patronyme (`avenue Gaston Meunier`) |
| `orgperson`   | raison sociale contenant un nom (`SARLU Cheminées Fabien Coudray`) — annotée `ORG`, jamais `PER` |
| `id_secteur`  | identifiant sectoriel (`FINESS ET 602453193`, `N° client : 6 018 574 976`) |
| `ref_longue`  | référence à gabarit de SIRET ou d'IBAN, clé invalide         |
| `titre_caps`  | titre de section en capitales                                |
| `mention`     | mention légale générique, dense en nombres                   |
| `designation` | libellé de prestation commerciale                            |
| `analyse`     | libellé d'analyse médicale (`Hémogramme`, `Ferritine`)       |
| `code_court`  | code sectoriel compact : APE/NAF, BIC, accréditation         |
| `domaine`     | nom de domaine dérivé d'une activité                         |
| `age`         | âge entre parenthèses (`14 mois 21 jours`, `47 ans`)         |

`orgperson` est le seul `decoy` annoté : il produit un span `ORG` là où le
modèle est tenté de voir un `PER`.

## Formats des valeurs non annotées

### `{{date:slot|format=…}}`

| `format=`    | Rendu                          | Relevé sur |
| ------------ | ------------------------------ | ---------- |
| `slash`      | `10/12/2025`                   | établissement public, artisan, laboratoire |
| `dash`       | `20-06-2025`                   | télécom            |
| `dm_dash`    | `19-06`                        | télécom            |
| `long`       | `19 juin 2025`                 | télécom            |
| `slash_time` | `07/02/2026 10:02`             | laboratoire        |
| `dash_time`  | `19-05-2025 22:36:06`          | laboratoire        |
| `long_time`  | `Samedi 07 Février 2026 à 18:34` | laboratoire        |

Les dates d'un même document sont tirées dans un ordre plausible (échéance après
émission), sans que ce soit un invariant vérifié : `DATASET.md` § 6.3.

### `{{ref:slot|pattern=…}}`

| `pattern=`  | Rendu              | Relevé sur |
| ----------- | ------------------ | ---------- |
| `FA`        | `FA00417305`       | artisan            |
| `DE`        | `DE00051842`       | artisan            |
| `FD`        | `FD00039614`       | artisan            |
| `FPX`       | `FPX2024B415208`   | établissement public |
| `CS`        | `CS4185039772`     | laboratoire        |
| `numeric10` | `5074318629`       | télécom            |

Ces gabarits sont repris tels quels des documents réels parce qu'ils constituent
le vivier de faux positifs : `CS4185039772` est capturé par la regex IBAN,
`FPX2024B415208` mêle lettres et millésime.

### `{{amount:slot|form=…}}`

Défaut : montant monétaire localisé (`2 142.86`, `4 745,17 €`, `19.99 e` —
noter le `e` produit par l'extraction LaTeX de la facture télécom à la place de `€`).
`form=medical` produit une valeur d'analyse avec unité (`4,64 T/L`).

### `{{phone:slot|form=…}}`

`dotted` (`01.99.00.30.31`), `spaced` (`09 70 00 33 33`), `compact`
(`0639980170`).

### `{{email:slot|from=…|cut=eol}}`

`from=` dérive l'adresse du slot `PER` ou `ORG` désigné, ce qui reproduit le
lien réel entre identité et adresse (`JULIEN.BERTRAND.ALT@EXAMPLE.COM`).

`cut=eol` coupe l'adresse par un saut de ligne au milieu du domaine, comme
observé sur la facture d'établissement public :

```
mél : enic-naric@france-education-
international.fr
```

Cas réel qui met en défaut `reEmail`. À produire pour que le corpus le contienne,
et à traiter séparément côté détection.

## Bruit d'espacement

```
{{noise:intraword}}…{{/noise}}
```

Marque une zone où l'espacement intra-mot du crénage PDF peut être appliqué :

```
V is ite te c hniq ue e ffe c tué e le 31/ 01/ 2025
```

Ce bruit est présent sur des PDF **natifs**, sans OCR (facture d'artisan). Il est appliqué
segment par segment, après rendu, avec recalcul mécanique des offsets. Le taux
est piloté par le champ `noise:` de l'en-tête du template et par la
configuration de génération.

## Remplissage de colonnes

`{{pad:N-M}}` insère entre N et M espaces. C'est ce qui reproduit l'entrelacement
des colonnes propre au texte extrait (`docs/observations-corpus-reel.md` § 1).
Les bornes sont larges à dessein : une largeur fixe apprendrait au modèle une
position absolue qui n'existe pas.

## Portée des slots

Un slot nomme une **identité**, pas une occurrence : toutes les occurrences de
`{{PER:buyer}}` d'un document désignent la même personne, sous des formes de
surface qui peuvent différer.

Corollaire à connaître dans les blocs répétables : un `{{amount:pu}}` **slotté**
rendrait la même valeur sur chaque ligne de facture. Les valeurs qui doivent
varier d'une ligne à l'autre s'écrivent sans slot — `{{amount}}`.
