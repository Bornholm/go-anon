# Observations sur documents réels — fondations des templates

**Source** : six documents administratifs français authentiques, conservés hors
dépôt.
**Méthode** : extraction `pdftotext -layout`, qui approche ce que produit le
`Walker` de `pkg/pdf`.

| Type                               | Producteur          | Texte |
| ---------------------------------- | ------------------- | ----- |
| scan                               | Acrobat Distiller   | aucun |
| facture télécom (3 p.)             | LaTeX               | oui   |
| facture établissement public       | Ecrion XF           | oui   |
| facture artisan (2 p.)             | Stimulsoft Reports  | oui   |
| facture énergie (4 p.)             | PDFium              | oui   |
| compte-rendu de laboratoire (5 p.) | pdftk / iText       | oui   |

Ils sont désignés dans la suite par leur type ; les fichiers eux-mêmes restent
hors dépôt.

> **Toutes les valeurs citées dans ce document sont synthétiques.** Les
> documents d'origine contiennent des données personnelles réelles ; ils
> servent à observer des **formes**, et seules ces formes sont reproduites ici.
> Chaque nom, adresse, identifiant, téléphone et email a été remplacé par une
> valeur inventée de structure identique. Aucune valeur d'origine n'entre dans
> ce fichier, ni dans les gazetteers, ni dans les templates.

---

## 1. Les templates doivent modéliser le texte *extrait*, pas le document visuel

C'est l'observation qui conditionne toutes les autres. Le CRF ne voit jamais la
mise en page : il voit la séquence de tokens que produit l'extracteur. Or une
mise en page à deux colonnes produit un entrelacement que rien ne signale.

Facture d'établissement public, bloc émetteur à gauche et destinataire à droite :

```
Facture n° FPX2024B415208                                                 21400 Châtillon
```

Une référence de facture et un code postal deviennent adjacents dans la
séquence. La facture d'énergie pousse le phénomène plus loin : des lignes de plus de
200 caractères où le bloc « nous contacter » est intercalé dans le récapitulatif
de montants, et où le nom du titulaire du contrat apparaît à 150 colonnes de
distance de son adresse.

**Conséquence pour le générateur** : les templates sont écrits dans la forme
*extraite*, colonnes déjà aplaties, avec des remplissages d'espaces variables.
Un corpus de documents linéairement bien formés n'apprendrait rien de ce que le
pipeline rencontre réellement.

---

## 2. Formes de `PER` observées

Toutes proviennent de six documents seulement, ce qui donne une idée de la
variété à couvrir.

| Forme                                   | Occurrence                                    |
| --------------------------------------- | --------------------------------------------- |
| civilité + Prénom + NOM                 | `Mme Carmen ROUSSEL`                          |
| civilité + NOM + Prénom                 | `Monsieur Bertrand Julien`                    |
| capitales intégrales                    | `CARMEN ROUSSEL MARQUES`, `M. BERTRAND JULIEN` |
| double patronyme (hispanique)           | `ROUSSEL MARQUES`                             |
| nom de naissance entre parenthèses      | `Mme BERTRAND Carmen (Née ROUSSEL MARQUES)`   |
| titre professionnel                     | `Dr TALBI KARIM`, `Dr WEISS Gérard (Biologiste)` |
| deux personnes concaténées sans ponctuation | `BERTRAND JULIEN Roussel Marques Carmen`  |
| étiquette collée au nom                 | `Enf(G) BERTRAND Prénom : Mathis`             |
| fonction + nom sur la même ligne        | `Ordonnateur : M. Olivier VANDAMME`, `Le directeur général par interim Olivier VANDAMME` |

Deux cas méritent d'être traités explicitement par le générateur :

- **Deux personnes juxtaposées sans séparateur** (`BERTRAND JULIEN Roussel
  Marques Carmen`, facture d'énergie). Il n'existe aucun indice de frontière ; c'est un cas
  où le découpage en deux spans est arbitraire. Politique à fixer et à
  documenter, pas à laisser au hasard du décodage BIO.
- **Ordre NOM-Prénom en capitales**, majoritaire dans les documents
  administratifs et rare dans WikiNER. C'est précisément l'écart de domaine que
  le corpus doit combler.

---

## 3. Formes de `ORG` observées

```
Bourgogne Formation internationale
SARLU Cheminees Fabien Coudray
BIOANALYS−BOURGOGNE−FRANCHE−COMTE
Telcom Mobile – SAS au capital de 412 500 000 Euros
ENR-SA
Trésorerie Générale de la Nièvre
CLINIQUE PRIVEE ANESTHESIE
MABTP
```

Trois difficultés distinctes :

- **`SARLU Cheminees Fabien Coudray`** — une raison sociale qui *contient* un
  nom de personne, précédée d'une forme juridique. Le cas d'école du faux positif
  `PER` dans un `ORG`. Le générateur doit produire des raisons sociales dérivées
  de patronymes, ce qui est extrêmement courant chez les artisans.
- **`Bourgogne Formation internationale`** — trois mots communs, capitalisation
  irrégulière. Aucun indice morphologique.
- **`BIOANALYS−BOURGOGNE−FRANCHE−COMTE`** — capitales, tirets, et un toponyme
  régional en composant. Noter le tiret U+2212 (signe moins) produit par
  l'extraction, pas un trait d'union U+002D.

---

## 4. Formes de `LOC` observées

```
2 Bis petite rue du Lavoir
1, avenue Gaston Meunier
92120 Montrouge Cedex
16 rue des Petits Carreaux 75002 Paris
22-30 avenue de Villiers 75017 Paris Cedex 17
21400 − CHATILLON
EN FACE DE LA CABINE TELEPHO
```

- **`avenue Gaston Meunier`** : nom de voie construit sur un prénom + patronyme.
  Négatif indispensable — sans lui, le modèle apprend « prénom + nom capitalisés
  = `PER` » et se trompe sur toutes les adresses françaises.
- **`EN FACE DE LA CABINE TELEPHO`** (facture d'énergie, lieu de consommation) : adresse non
  normalisée, en capitales, tronquée par la largeur du champ. Ce n'est pas une
  aberration, c'est la forme normale des champs libres de systèmes de gestion.
- **Adresse noyée dans un texte libre** (facture d'artisan) : « une installation à l'adresse
  20 rue de la Fontaine 21400 Montigny le roi », au milieu d'une description de
  prestation, dans une cellule de tableau. Cas de rappel difficile : l'adresse
  n'est pas dans un bloc adresse.

---

## 5. Un bruit d'extraction existe sur les PDF natifs

Facture d'artisan, ligne 30 du texte extrait :

```
V is ite te c hniq ue e ffe c tué e le 31/ 01/ 2025, p o ur une
ins ta lla tio n à l' a d re s s e 20 rue d e la Fo nta ine 21400
Mo ntig ny le ro i.
```

Le crénage appliqué par le générateur de PDF produit un espacement intra-mot que
l'extraction restitue littéralement. **Aucun OCR n'est intervenu** : c'est un PDF
natif, et le pipeline le subit déjà aujourd'hui.

**Cette observation corrige la décision § 8 de `DATASET.md`.** Le plan écartait
tout module de bruit au motif qu'une matrice de confusion OCR devinée est
inutile. C'est toujours vrai pour l'OCR. Mais le bruit d'espacement intra-mot est
d'une autre nature : il est présent dans les documents natifs, il est
observable et mesurable directement sur le corpus d'origine, et il détruit toutes
les features morphologiques de `pkg/features` (préfixes, suffixes, forme,
appartenance gazetteer). Un modèle qui ne l'a jamais vu ne détectera rien sur ces
passages.

Le générateur doit donc produire ce bruit — appliqué segment par segment, où le
recalcul d'offsets est gratuit (`DATASET.md` § 3.2).

---

## 6. Identifiants et faux positifs

Relevé complet des captures des patterns intégrés sur les six documents, avant
et après validation par clé de contrôle :

| Type    | Matches bruts | Retenus après clé | Faux positifs éliminés          |
| ------- | ------------- | ----------------- | -------------------------------- |
| `IBAN`  | 11            | 2                 | 9 (numéros de TVA, réf. dossier) |
| `SIRET` | 4             | 4                 | 0                                |
| `SIREN` | 3             | 3                 | 0 — voir ci-dessous              |

Les faux positifs `IBAN` sont des **numéros de TVA intracommunautaire** de la
forme `FR12321456782`, dont la structure `FR` + chiffres est indistinguable d'un
IBAN par la seule forme, et une **référence de dossier de laboratoire** de la
forme `CS4185039772`.

**Le SIREN résiste à la clé.** Un numéro FINESS d'établissement de santé, de la
forme `FINESS ET 602453193`, satisfait Luhn par coïncidence — une chance sur
dix. Seul le contexte le distingue, d'où `ner.SIRENContextualPattern`.

Autres gabarits d'identifiants observés, tous à écarter, tous utilisables comme
négatifs :

```
N° client : 6 018 574 976          Identifiant : 59842885
N° 32 416 977 142                  N° de compte : 4 04 4 041 104 933
N° 12 251 230 072 112 (PDL)        Accréditation n°8−2132
FINESS ET 602453193                Qualibois n° QB/ 56542
FA00417305, DE00051842, FD00039614, FPX2024B415208, CS4185039772, CL00312
```

---

## 7. Téléphones et emails

Formes de téléphone : `01.99.00.30.31`, `0639980170`, `06.39.98.80.18`,
`09 70 00 33 33`. Numéros courts à ne pas confondre : `3244`, `3404`.

Un email est **coupé par un saut de ligne** (facture d'établissement public) :

```
Tél accueil : 01.99.00.30.31 - mél : contact@bourgogne-formation-
internationale.fr
```

`reEmail` en capture `contact@bourgogne-formation-`, un domaine tronqué. Le
générateur doit produire ce cas, et c'est aussi un défaut de détection à traiter
séparément.

`JULIEN.BERTRAND.ALT@EXAMPLE.COM` : email en capitales, dérivé du nom du
titulaire — donc porteur d'identité même après troncature du domaine.

---

## 8. Un type de document manquant à la typologie

Le **compte-rendu de laboratoire d'analyses médicales** n'était pas
prévu au plan, et c'est de loin le document le plus dense en données sensibles :
patient (mineur), date de naissance, sexe, adresse, téléphone, représentant
légal, médecin prescripteur, établissement prescripteur, laboratoire, biologiste
signataire, et des résultats d'examens qui sont des données de santé.

Il présente aussi une structure absente des factures : **pied de page répété sur
chaque page avec l'identité du patient** (`Enf(G) BERTRAND Mathis Né(e) BERTRAND
04/03/2023`), qui multiplie les occurrences de la même entité dans un même
document — exactement ce que la `Session` cross-segments de `pkg/anonymizer` doit
traiter de façon cohérente.

À ajouter au lot 3 de `DATASET.md`, en remplacement ou en complément du bulletin
de paie.

---

## 9. Conséquences pour le générateur

| Observation                            | Exigence sur le générateur                                       |
| -------------------------------------- | ---------------------------------------------------------------- |
| Entrelacement des colonnes (§ 1)       | Templates écrits en forme extraite, remplissage d'espaces variable |
| Ordre NOM-Prénom, capitales (§ 2)      | Générateur `PER` à formes multiples, pondérées                   |
| Deux personnes juxtaposées (§ 2)       | Politique de découpage explicite et documentée                   |
| Raison sociale dérivée d'un nom (§ 3)  | Générateur `ORG` combinant forme juridique + patronyme           |
| Voie portant un nom de personne (§ 4)  | Négatif systématique, issu des noms de voie de la BAN            |
| Adresse en texte libre (§ 4)           | Insertion d'adresses hors bloc adresse                           |
| Bruit d'espacement intra-mot (§ 5)     | Module de bruit, calibré sur le corpus d'origine, par segment    |
| Identifiants sectoriels (§ 6)          | Négatifs à gabarit d'identifiant, non annotés                    |
| Email coupé en fin de ligne (§ 7)      | Césure de ligne au milieu d'un email                             |
| Pied de page répété (§ 8)              | Répétition de l'identité en pied de page                         |
