# Documents bureautiques: formats et sanitisation

`anon-doc` traite plusieurs formats de documents avec la même mécanique. Un
`Walker` par format parcourt et réécrit le texte visible ; un `Sanitizer` purge
les surfaces cachées (métadonnées, commentaires, révisions).

## Formats pris en charge

| Format  | Package    | Traitement du corps                                              |
| ------- | ---------- | ---------------------------------------------------------------- |
| DOCX    | `pkg/docx` | itération sur les paragraphes, réécriture des runs               |
| ODT     | `pkg/odt`  | parsing XML en mémoire, réécriture in-place, resérialisation ZIP |
| CSV/TSV | `pkg/csv`  | détection auto du séparateur, anonymisation cellule par cellule  |
| PDF     | `pkg/pdf`  | lecture seule (pdfcpu), extraction texte avec offsets, redact    |

```bash
# La langue est détectée automatiquement (-lang auto par défaut)
anon-doc -model auto -input rapport.docx -output rapport_anon.docx
# Format tabulaire, stratégie de caviardage
anon-doc -model auto -lang fr -strategy redact -input data.csv -output data_anon.csv
```

## Compromis précision/rappel

`anon-doc` utilise par défaut le preset **`high-recall`** : pour la conformité,
un faux négatif (donnée personnelle en clair) est sans commune mesure avec un
faux positif (un mot caviardé à tort). `-preset balanced` revient au compromis
F1, plus adapté à l'analyse qu'à la conformité.

Ce réglage suppose un gazetteer de prénoms — son levier de rappel injecte les
prénoms que le modèle a manqués. Sans lui, `anon-doc` avertit et le preset
n'apporte rien :

```bash
anon-doc -model auto -gazetteers auto -input rapport.pdf -output rapport_anon.pdf
```

**Surveiller la précision.** La sortie récapitule les occurrences anonymisées
par type :

```
Entités remplacées : 37 (52 occurrence(s) LOC:9 ORG:4 PER:39)
```

C'est le retour le plus direct sur les effets de bord d'une configuration réglée
pour le rappel : un type qui s'emballe s'y voit avant de rendre les documents
inexploitables. En cas de dérive, `-preset balanced` ou des filtres de confiance
resserrent la détection.

## Sanitisation des surfaces cachées

Un document « anonymisé » ne se limite pas à son texte visible. Un DOCX porte
l'auteur et le dernier modificateur dans ses propriétés, du texte supprimé dans
ses révisions, des commentaires ; un PDF un dictionnaire Info et des métadonnées
XMP ; un ODT un `meta.xml`. `anon-doc` purge ces surfaces avant d'écrire (option
`-sanitize`, activée par défaut) :

```bash
anon-doc -model auto -strict -input rapport.docx -output rapport_anon.docx
```

| Format | Surface traitée                                                    |
| ------ | ------------------------------------------------------------------ |
| DOCX   | `docProps` purgés, commentaires supprimés, révisions **détectées** |
| ODT    | `meta.xml` purgé, annotations et modifications suivies retirées    |
| PDF    | dictionnaire Info et métadonnées XMP purgés, annotations signalées |
| CSV    | aucune surface cachée (le fichier est son texte visible)           |

Certaines surfaces ne se neutralisent pas avec garantie : les révisions DOCX,
les annotations et pièces jointes PDF. En mode `-strict`, leur présence refuse
le document au lieu de produire une sortie faussement propre. L'opérateur doit
alors accepter les révisions ou retirer les pièces jointes en amont. Hors
strict, elles sont signalées sur stderr (comptes seulement).

En mode `-strict`, le contrôle des surfaces est exécuté même sous
`-sanitize=false` ; `-sanitize` ne pilote alors que la purge des métadonnées.

## Entités coupées par la segmentation

Chaque `Walker` découpe le document selon sa structure : le PDF à la ligne, le
DOCX au paragraphe, le CSV à la cellule. Une entité qui tombe sur une de ces
coupures — « Jean » en fin de ligne, « Dupont » au début de la suivante — n'est
une entité pour personne : le modèle ne voit que deux fragments sans contexte.

La détection par segment est structurellement aveugle à ce cas. Deux mécanismes
s'y attaquent, tous deux activés par défaut :

- **`-multi-view`** détecte sur plusieurs recompositions du document (paires de
  segments consécutifs, document entier) et unione les entités trouvées.
  L'entité coupée redevient visible et est anonymisée. Coût : environ trois
  passes de détection supplémentaires.
- **`-verify-document`** recompose la **sortie** et y relance une détection,
  pour signaler ce qui aurait échappé au premier.

Le premier répare, le second contrôle :

```bash
# Signale les entités redevenues détectables après recomposition
anon-doc -model auto -input rapport.pdf -output rapport_anon.pdf
# Les refuse au lieu de produire le document
anon-doc -model auto -strict -input rapport.pdf -output rapport_anon.pdf
```

Le rapport distingue les entités **à cheval sur plusieurs segments** : ce sont
celles qu'aucun contrôle par segment ne pouvait voir.

Limite connue : une entité coupée est scindée à la frontière, et chaque moitié
reçoit un pseudonyme distinct. Le nom n'apparaît plus en clair, mais la table de
ré-identification porte deux entrées pour une seule personne.

## Contenu bitmap : OCR et caviardage pixel

Le pipeline texte ne voit que ce qu'une couche texte décrit. Une page scannée,
un encart bitmap, un tampon ou une signature lui sont invisibles — et sans
traitement dédié, le document ressortirait en paraissant traité.

`anon-doc` océrise donc les PDF et **efface réellement les pixels** :

```bash
anon-doc -model auto -input scan.pdf -output scan_anon.pdf
```

| Étape | Ce qui se passe |
| --- | --- |
| **OCR systématique** | Chaque page est rendue puis océrisée, y compris celles qui portent déjà du texte. Aucun tri en amont : c'est ce qui évite de manquer un encart scanné dans un rapport natif. |
| **Caviardage pixel** | Les pages concernées sont aplaties : rendues, noircies aux emplacements des entités, réinjectées comme unique contenu. Les pixels d'origine **cessent d'exister** — peindre un rectangle noir par-dessus ne les effacerait pas. |
| **Vérification visuelle** | Le document produit est relu, rendu et océrisé, pour confirmer qu'il n'en reste rien de lisible. |

Options : `-ocr auto\|on\|off`, `-ocr-lang`, `-ocr-dpi` (300 par défaut),
`-ocr-min-confidence`. En `auto`, l'absence de `tesseract` ou `pdftoppm` est un
avertissement ; en `on`, une erreur.

**Contreparties du caviardage.** Une page aplatie perd sa couche texte : elle
n'est plus sélectionnable ni recherchable, son poids augmente, et sa définition
est plafonnée par le DPI de rendu. Seules les pages effectivement concernées
sont aplaties.

**En mode `-strict`**, une donnée restée lisible dans le document produit le
fait **détruire** : le contrôle porte sur le fichier écrit, le fail-closed ne
peut donc pas prendre une autre forme.

### PDF « searchable » : le cas trompeur

Un scan surmonté d'une couche texte invisible (sortie de scanner ou OCR
préalable) est le cas le plus dangereux : le texte extractible est anonymisé, et
lui seul. `pdftotext`, `grep` et le copier-coller ne rendent plus rien, toutes
les vérifications textuelles passent au vert, mais **les pixels restent lisibles
à l'œil**. Ces pages sont détectées et traitées comme les scans.

### Limites

L'OCR n'a pas un rappel parfait : sur un scan dégradé, penché ou de faible
contraste, une donnée peut échapper à la reconnaissance et donc au caviardage.
Les pages scannées restent signalées à ce titre.

L'inventaire complet des surfaces par format (headers/footers, footnotes,
tracked-changes…) et les critères de recette figurent dans
[`rgpd.md` § 4](rgpd.md).
