# Plan phasé — couverture maximale des PDF bitmap et des entités cross-segment

Document de travail.

## Posture

**Objectif : la meilleure couverture de conformité possible, quel qu'en soit le
coût en performance.** Cette contrainte n'est pas un détail de calibration —
elle change l'architecture. Trois conséquences structurantes, à garder en tête
en lisant la suite :

1. **On ne choisit plus entre deux méthodes de détection : on les unione.**
   Aucune vue ne peut « dé-détecter » ce qu'une autre a trouvé, donc l'union est
   monotone en rappel. Le débat reflow *vs* fenêtre de recouvrement disparaît.
2. **Refuser n'est pas couvrir.** Un document renvoyé à l'humain n'est pas
   anonymisé. Le fail-closed des phases 0 et 1 reste le filet de sécurité, mais
   il cesse d'être la réponse : les scans doivent être *traités*.
3. **On ne classifie plus les pages — on océrise tout.** Rastériser et océriser
   chaque page, systématiquement, supprime la calibration heuristique et tous
   ses faux négatifs (page composite, formulaire partiellement scanné, tampon,
   signature). Les seuils des phases 0/1 ne servent plus qu'au rapport.

Contrepartie à surveiller, traitée en fin de document : le rappel maximal
sur-anonymise, et un mode strict qui bloque trop finit désactivé.

## Problèmes traités

### P1 — Fail-open silencieux sur les scans

`pkg/pdf/walker.go` : une page sans token texte était ignorée. Un PDF 100 %
scanné produisait `w.segments == nil`, donc :

```
Walk n'itère sur rien
  → ProcessWithReport retourne Segments: 0, Leaks: []
  → report.OK() == true
  → cmd/anon-doc écrit le fichier de sortie et sort en code 0
```

Le document sortait **non anonymisé** et le pipeline le déclarait conforme.

### P2 — Texte invisible sur pixels (PDF « searchable »)

Cas majoritaire en entreprise : scanner ou OCR préalable déposant une couche
texte invisible (`3 Tr`) sur l'image. Le texte extractible est anonymisé — et
lui seul. `pdftotext`, `grep` et le copier-coller ne rendent plus rien, toutes
les vérifications passent au vert, et le nom reste lisible à l'œil comme par
n'importe quel OCR. Plus dangereux que P1, précisément parce que ça ne se voit
pas.

### P3 — Entités cross-segment

Chaque `Walker` découpe selon la structure du format : le PDF à la ligne
(`groupIntoSegments`), le DOCX au paragraphe, le CSV à la cellule.

```
… a été reçu par Jean
Dupont, directeur de …
```

Deux séquences distinctes pour le CRF. Au mieux deux `B-PER` → deux
placeholders pour une même personne. Au pire, chaque moitié sans contexte passe
sous le seuil → **fuite en clair**.

Le problème est plus large que les entités coupées : le PDF donne au modèle des
**fragments de ligne** alors qu'il est entraîné sur des phrases. C'est la
qualité de détection sur *toutes* les entités de la page qui en souffre.

Le DOCX est le bon élève : `walkParagraph` (`pkg/docx/walker.go:54`) concatène
tous les runs d'un paragraphe en un `Segment.Text` unique et réécrit dans le
premier run en vidant les suivants. C'est le modèle de référence.

## Invariants

- **Fail-closed partout.** Toute surface non traitée remonte dans
  `SanitizeReport.Unprocessed` et fait échouer le mode strict.
- **Pas de régression silencieuse sur le PDF natif.** Corpus de non-régression
  (F1 et offsets) avant toute modification du chemin d'extraction.
- **Le schéma de features est gelé.** Rien ici ne doit changer une chaîne de
  feature (règle d'or, `AGENTS.md`).
- **Aucun rapport ne transporte de contenu**, seulement types et offsets.
- La contrainte « aucune dépendance externe » **tombe** pour l'OCR : le moteur
  se choisit sur la qualité, pas sur la friction d'installation.

---

## Phase 0 — Détection des pages raster ✅

**Implémentée.** `pkg/pdf/raster.go`, câblage dans `walker.go` et `sanitize.go`.

Traite P1. Sous la posture actuelle, sa fonction se déplace : elle ne sert plus
à *refuser* les scans (la phase 4′ les traite) mais à **rapporter** ce que le
pipeline a rencontré, et à rester le filet de sécurité si l'OCR est indisponible.

**Heuristique** : image couvrant ≥ 50 % de la page (`rasterCoverageThreshold`)
**et** < 100 caractères non blancs visibles (`rasterMaxTextRunes`), conditions
cumulatives. Abstention en cas de doute (MediaBox absente, ressources
illisibles) : la détection ne doit pas transformer un PDF exotique en refus
systématique.

**Mesure de la couverture** — `imageArea()` interprète le flux de contenu et
suit la matrice courante (`q` / `Q` / `cm`). Une image XObject est dessinée dans
le carré unité : sa surface vaut `|ad − bc|` de la CTM au `Do`. Trois points non
évidents :

- **Récursion dans les XObject Form** (profondeur bornée à 3, ce qui garantit la
  terminaison sur un cycle). Sans elle, les pilotes de scanner encapsulant la
  page dans un formulaire produisaient un faux négatif — c'est-à-dire P1.
- **Images en ligne** (`BI … ID … EI`). Le saut jusqu'à `EI` est indispensable
  à la correction du parcours : les octets binaires suivant `ID` seraient sinon
  lus comme des opérateurs.
- **La mesure majore la couverture réelle** (recouvrements comptés plusieurs
  fois, débordements comptés en entier) : bon sens d'erreur quand l'oubli coûte
  une fuite.

**Signalement** — `SanitizeReport.Unprocessed`, avec plages contiguës collapsées.
`Walker.RasterPages()` expose les numéros.

**Correctif associé** — `cmd/anon-doc` exécute le contrôle des surfaces même
sous `-sanitize=false` dès que `-strict` est posé ; `-sanitize` ne pilote plus
que la purge des métadonnées. La combinaison laissait sinon passer un scan sans
signalement alors que l'utilisateur demandait le fail-closed.

---

## Phase 1 — Mode de rendu texte (`Tr`) et cas hybride ✅

**Implémentée.** Suivi de `Tr` dans `extractTextTokens`, classification dans
`raster.go`, signalement dans `sanitize.go`.

Traite P2. Sous la posture actuelle, la couche invisible cesse d'être un cas
particulier : elle devient **une vue de plus dans l'union** de la phase 3′, et
une source de bbox gratuite pour la phase 5′.

**État graphique** — `textToken.renderMode` porte le mode actif ;
`isInvisible()` couvre les modes 3 et 7. Deux propriétés verrouillées par les
tests parce qu'elles se déduisent mal à la lecture : le mode appartient à l'état
graphique (`q` empile, `Q` restaure), et `BT` ne le réinitialise pas. Seul
`renderMode` est empilé — corriger le reste déplacerait les offsets et donc la
réécriture.

**Classification** — `classifyPage` rend `pageNormal` / `pageRaster` /
`pageHybrid`. Le comptage sépare visible et invisible, ce qui corrige un angle
mort de la phase 0 : un PDF searchable exposait des centaines de caractères
extractibles et passait pour une page de texte ordinaire.

**Complément au critère géométrique** — `modifiedInvisiblePages()` remonte les
pages où du texte entièrement invisible a été remplacé sans qu'elles soient
classées hybrides. Un bloc scanné minoritaire (signature, tampon, figure)
surmonté de sa couche OCR reste sous les 50 % de couverture alors que le
remplacement y produit la même illusion. Le seuil seul ne suffit pas.

**Correctif de robustesse** — `extractTextTokens` saute les images en ligne.
Sans cela, une parenthèse ouvrante dans les pixels déclenchait
`scanLiteralString`, qui consommait le reste du flux : le texte réel de la page
disparaissait **silencieusement** de l'extraction.

---

## Phase 2 — Vérification au niveau document ✅

**Implémentée.** `Processor.verifyDocument` dans `pkg/docprocessor`, via
`WithDocumentVerification()` / `WithStrictDocumentVerification()`.

Rend P3 visible. Devient aussi l'**instrument de mesure** de tout ce qui suit :
la proportion de fuites dont `len(Segments) > 1` dit l'ampleur réelle du
problème sur un corpus donné.

**Ce n'est pas `anonymizer.Verify`** — point de conception central. `Verify`
cherche les formes de surface connues du mapping ; une entité cross-segment n'a
jamais été détectée, elle n'est dans aucun mapping. Il faut donc relancer le
**recognizer**, d'où `anonymizer.Detect`.

**Le séparateur est le cœur du contrôle.** `pkg/ner` découpe d'abord par ligne
(`recognizer.go:289`) : joindre par `\n` reproduirait exactement la
segmentation du walker et ne trouverait jamais rien. La jointure se fait par une
espace. Contrepartie assumée : deux segments voisins sans rapport deviennent
adjacents et peuvent produire un faux positif.

**Exclusion des zones sûres** — `⟦PERSONNE_1_…⟧` se laisse volontiers
reconnaître comme un nom propre. `safeZones` a été généralisée en
`anonymizer.SafeZones(text, replacements)` + `Overlaps`, réutilisée plutôt que
réimplémentée.

**Rapport** — `Report.DocumentLeaks []DocumentLeak`, pris en compte par `OK()`.
`DocumentLeak.Segments` donne les rangs chevauchés : longueur > 1 identifie une
entité cross-segment. Nature `anonymizer.LeakDocumentEntity`.

---

## Phase 3′ — Détection multi-vues avec union — ⏳ quasi complète

**Objectif** : corriger P3, et au-delà, rendre au modèle le contexte de phrase
dont la segmentation le prive.

Sous contrainte de coût il fallait choisir entre reflow et fenêtre de
recouvrement. Sans contrainte, on prend l'**union**, parce qu'ils échouent
différemment : le reflow donne le contexte de phrase mais refuse de joindre
quand le regroupement est ambigu (multi-colonnes, tableaux) ; le recouvrement ne
comprend rien à la structure mais franchit précisément ces frontières-là.

**Fait** : la machinerie d'union, la réconciliation, et trois des quatre vues —
paires, bloc et document (`pkg/docprocessor/views.go`,
`pkg/anonymizer/reconcile.go`, `pkg/pdf/blocks.go`). Activées par
`WithMultiViewDetection()` et `anon-doc -multi-view`.

**Reste** : la vue « couche invisible », et l'unification du pseudonyme d'une
entité scindée (3′d).

### Brique d'injection — `WithAdditionalEntities`

Point de conception : les vues détectent sur une **recomposition**, mais
l'anonymisation opère sur le **segment**. Il fallait donc un moyen de fournir à
`Anonymize` des entités repérées ailleurs. D'où
`anonymizer.WithAdditionalEntities([]ner.Entity)`, unionées avec la détection du
recognizer sur le texte courant.

L'option est défensive par construction : les offsets hors bornes ou inversés
sont écartés, et la forme de surface fournie est **ignorée au profit du texte
courant** (`text[start:end]`). Elle décrit une autre recomposition ; la garder
ferait échouer le contrôle d'égalité de la passe de remplacement, qui vérifie
`text[start:end] == ent.Text` avant chaque substitution. Un appelant ne peut
donc pas corrompre le remplacement en fournissant n'importe quoi.

### 3′a — Les vues

| Vue | Recomposition | Ce qu'elle rattrape | État |
| --- | --- | --- | --- |
| segment | tel quel (comportement actuel) | entités que le contexte long dilue | ✅ implicite |
| paires | `seg[i] + " " + seg[i+1]` | frontières que le reflow a refusé de joindre | ✅ |
| document | tout joint par espace | coupures à distance (saut de page, note) | ✅ |
| bloc | lignes recollées en paragraphe | contexte de phrase — cas dominant | ✅ |
| couche invisible | segments `Tr 3` isolés (phase 1) | texte OCR préexistant | ⏳ |

Chaque vue produit des entités avec leurs offsets **dans la vue** ;
`projectEntity` les redistribue sur les segments couverts, en les scindant aux
frontières et en ramenant les offsets dans le repère de chaque segment.

Les vues « paires » et « document » vivent dans `docprocessor` parce qu'elles ne
supposent rien du format. La vue « bloc » exige que le `Walker` expose sa
structure géométrique : elle appartient à `pkg/pdf`.

**Détection sur le texte source** — les vues se construisent avant toute
réécriture, via une lecture préalable du walker (`collectTexts`). Détecter sur
une sortie partiellement anonymisée reviendrait à chercher des entités dans des
placeholders. Le walker est donc parcouru deux fois quand l'option est active,
ce qu'un test verrouille explicitement.

### 3′b — Réconciliation ✅

`anonymizer.reconcileEntities` unione la détection du recognizer et les entités
fournies, puis résout les chevauchements. **Le span le plus large gagne** — la
traduction directe de la posture : entre deux lectures concurrentes d'une même
zone, sur-anonymiser coûte un mot caviardé à tort, sous-anonymiser coûte une
violation. À largeur égale, l'entité du modèle l'emporte (elle porte le score de
confiance), puis l'ordre des offsets tranche pour rendre le résultat
déterministe.

**Ce n'est pas cosmétique.** La passe de remplacement d'`Anonymize` réécrit de
droite à gauche et vérifie `text[start:end] == ent.Text` avant chaque
substitution : deux entités qui se chevauchent feraient échouer la seconde
**silencieusement**, laissant une portion de la zone en clair. Un test vérifie
donc explicitement que la sortie de la réconciliation est disjointe.

Sans entité fournie, la fonction est un no-op strict — la slice du recognizer
est rendue telle quelle, référence comprise. Le chemin nominal est
rigoureusement inchangé, ce qu'un test verrouille aussi.

La `Session` fait le reste : une même forme de surface reçoit le même
placeholder, quelle que soit la vue qui l'a trouvée.

### 3′c — Reflow (la vue « bloc ») ✅

`pkg/pdf/blocks.go`, exposé via le contrat optionnel `docprocessor.BlockWalker`.
Un walker qui ne l'implémente pas perd simplement cette vue.

**3a du plan initial n'est pas un prérequis** — confirmé à l'implémentation. Les
métriques de fonte (`/Widths`, AFM) restent indispensables au caviardage pixel
(phase 5′), mais le recollage ligne-à-ligne s'en passe.

**Et même les abscisses se sont révélées inutiles.** Je les annonçais
nécessaires pour repérer les changements de colonne ; la mesure purement
verticale suffit, parce qu'une nouvelle colonne **repart toujours vers le haut**
de la page. Un écart négatif entre deux lignes consécutives est donc la
signature du pire cas — la fusion de deux colonnes — et se lit sans jamais
calculer un X. `textToken` reste inchangé.

Deux ruptures ouvrent un bloc :

- **remontée** (`gap < blockMinGap`) : changement de colonne ;
- **interligne rompu** (`gap > blockGapRatio × interligne`) : saut de
  paragraphe, titre, changement de corps.

L'interligne de référence est la **médiane** des écarts de la page, robuste aux
quelques grands sauts qu'une moyenne laisserait dériver. Un bloc ne traverse
jamais une frontière de page, et les blocs d'une seule ligne sont omis (le
segment est de toute façon analysé tel quel).

**Le regroupement peut se permettre d'être agressif** — et c'est une conséquence
directe de l'union. Au pire un bloc mal formé n'apporte rien : il ne peut pas
retirer ce que la vue segment a déjà trouvé. C'est ce qui rend acceptable de
regrouper des lignes de tableau, par exemple.

**Césure** — recollée dans `joinSegments`, pour toutes les vues et pas seulement
les blocs : les paires souffrent du même problème. Le trait d'union est retiré
de la contribution du segment de gauche, ce qui **préserve l'invariant dont
dépend `projectEntity`** : la portion contribuée reste un *préfixe* du texte du
segment, donc les offsets projetés restent des indices valides. Un test le
verrouille explicitement.

La règle est locale, donc imparfaite — distinguer une vraie césure d'un trait
d'union lexical tombé en fin de ligne demanderait un lexique. L'arbitrage suit
la posture : `porte-` + `monnaie` sera recollé à tort en `portemonnaie`, ce qui
ne coûte rien puisque ce n'est pas une entité, alors que refuser de recoller
`Du-` + `pont` manquerait un nom. La condition sur la minuscule suivante protège
le cas qui compte : les noms propres composés (`Saint-` + `Étienne`) gardent leur
trait d'union.

Je décrivais initialement une règle par langue dans `pkg/lang` ; elle n'est pas
nécessaire à ce niveau d'exigence. **Raffinement possible** si le besoin
apparaît : construire les deux variantes de jointure (recollée et non recollée)
comme deux vues distinctes et unioner — ça supprimerait l'ambiguïté au lieu de
l'arbitrer.

### 3′d — Unification du pseudonyme d'une entité scindée

**Limite connue de l'implémentation actuelle.** Une entité à cheval est scindée
à la frontière : chaque moitié est anonymisée dans son segment, donc reçoit un
placeholder **distinct**. `⟦PERSONNE_1_x⟧` / `⟦PERSONNE_2_x⟧` là où un lecteur
ne voit qu'une seule personne.

La fuite est fermée — c'est l'objectif de couverture — mais la qualité de
pseudonymisation en souffre : la ré-identification donne deux entrées pour une
personne, et la corrélation entre les deux mentions est perdue.

Unifier demanderait que l'anonymiseur accepte une **forme de surface canonique**
distincte du span remplacé : « remplace `Jean` ici par le placeholder de
`Jean Dupont` ». C'est un raffinement de qualité, pas de couverture, d'où son
report.

Alternative à évaluer en même temps : porter tout le remplacement dans le
segment de gauche et vider la portion de droite. Plus propre à la lecture, mais
ça déplace le texte entre segments — le `Walker` doit l'accepter, ce qui n'est
aujourd'hui vrai que du DOCX.

Côté PDF, ces deux variantes supposent le même travail : la table `repls`
(`walker.go:164`) est construite par page mais alimentée segment par segment, en
supposant que les segments **partitionnent** les tokens. Dès qu'une entité
traverse deux segments, l'hypothèse tombe et `repls` doit arbitrer les
collisions au lieu d'écraser.

### 3′e — À vérifier pendant la mise en œuvre

`makeSegment` concatène les tokens d'une ligne **sans séparateur**. Selon la
façon dont les PDF émettent les espaces (dans les chaînes, ou par positionnement
`Td` entre deux `Tj`), des mots peuvent déjà être collés *à l'intérieur* d'une
ligne. Si c'est le cas, c'est un problème plus proche et moins cher que le
cross-segment, à traiter d'abord.

**Tests** : corpus de non-régression PDF natif avant toute modification ;
fixtures nom coupé en fin de ligne, 2 colonnes, tableau, saut de page, mot
césuré ; vérification que chaque vue prise seule manque au moins un cas que
l'union rattrape.

**Bénéficie à tous les formats** : CSV (nom étalé sur deux cellules), DOCX
(frontière de paragraphe).

---

## Phase 4′ — OCR systématique ✅

**Objectif** : que le pipeline voie ce qu'un lecteur voit, sur toutes les pages.

**Fait** : `pkg/ocr` (interface, moteur tesseract, reconstruction des lignes),
`pkg/pdf/rasterize.go`, `pkg/pdf/ocr.go`, et le signalement des entités
irrécupérables via `docprocessor.ReadOnlyTextWalker`.

**Ce que ça donne** : le contenu bitmap est lu, et ses entités sont soit
caviardées (PDF, via la phase 5′), soit signalées page par page avec refus en
mode strict (autres formats).

### 4′a — Interface ✅

`ocr.Engine` : une image, une langue, des mots positionnés. La granularité
**mot avec boîte** est le minimum utile — une ligne entière ne permet pas de
caviarder une entité en milieu de ligne.

`ocr.Lines()` reconstitue les lignes en regroupant par identifiant et en
**réordonnant horizontalement** : un moteur peut rendre les mots dans un autre
ordre, et un mot mal placé décalerait toutes les correspondances offset → boîte.
`Line.Spans` puis `Line.BoxesFor(start, end)` font le pont entre le monde du
texte, où les entités ont des offsets, et celui des pixels, où il faudra les
effacer. C'est la brique dont dépend directement la phase 5′.

### 4′b — Moteur ✅

`ocr.TesseractExec` : PNG sur l'entrée standard, TSV en sortie. Pas de cgo, donc
compilation croisée préservée ; le coût est un fork par page, que la posture
accepte.

**Le parseur TSV n'utilise délibérément pas `encoding/csv`.** Tesseract
n'échappe pas le champ texte : un mot commençant par un guillemet fait démarrer
au lecteur CSV une chaîne quotée qui engloutit les lignes suivantes — y compris
avec `LazyQuotes`. Un seul caractère mal reconnu sur un scan suffirait donc à
faire disparaître la fin d'une page. Un simple découpage sur tabulation est à la
fois plus simple et plus sûr. Le test qui a révélé ce comportement est conservé.

Une confiance négative (champ non mesuré) est ramenée à 0 plutôt que propagée,
sans quoi toute comparaison au seuil serait faussée.

### 4′c — Rastérisation ✅

`pdf.PdftoppmRasterizer` — le PNG transite par l'entrée/sortie standard, **rien
n'est écrit sur disque** : pas d'image en clair d'un document sensible laissée
derrière soi.

Défaut à 300 dpi (`pdf.DefaultDPI`) : en deçà, les moteurs perdent les corps de
8–9 points des mentions légales et bas de page, précisément là où se logent les
données personnelles.

### 4′d — Océrisation systématique ✅

`Walker.RunOCR` traite **toutes** les pages, y compris celles qui portent déjà
du texte. Trier en amont supposerait de savoir reconnaître ce qui mérite un
OCR — c'est-à-dire refaire l'heuristique de la phase 0 avec ses faux négatifs
(page composite, formulaire partiellement scanné, encart au milieu d'un rapport
natif).

Les erreurs de rendu ou de reconnaissance sont **remontées, pas avalées** : une
page qu'on n'a pas su lire est une page dont on ignore le contenu.

### 4′e — Signalement ✅

`docprocessor.ReadOnlyTextWalker` expose du texte « lisible mais non
réécrivable ». Le `Processor` le scanne **systématiquement**, sans dépendre
d'aucune option : le coût d'une détection est négligeable devant celui de l'OCR
qui l'a produite, et un document dont on sait qu'il contient un nom illisible
par le pipeline ne doit jamais être déclaré conforme.

`Report.RegionLeaks` porte la nature `anonymizer.LeakUnwritableRegion`, distincte
des autres : ce n'est pas un remplacement qui a échoué, c'est du contenu que le
pipeline **n'a aucun moyen de retirer**.

### 4′f — CLI ✅

`-ocr auto|on|off`, `-ocr-lang`, `-ocr-dpi`, `-ocr-min-confidence`. En `auto`,
l'absence d'outil est un avertissement et les pages bitmap restent signalées par
la sanitisation ; en `on`, c'est une erreur — l'utilisateur a demandé la
couverture, la lui donner à moitié sans le dire serait pire que de refuser.

### Reste à faire

- **Union de plusieurs moteurs** : à mesurer avant de payer la complexité.
- **Second passage dans la langue détectée** sur les scans purs (circularité
  décrite en 4′d du plan initial) : `-ocr-lang` permet déjà de forcer.
- **Rendu des pages composites** : `pdftoppm` rend la page entière, ce qui
  suffit ; un moteur de rendu embarqué reste à évaluer pour supprimer la
  dépendance externe.
- Le signalement des pages raster (phase 0) reste posé même sur une page
  caviardée : le rappel de l'OCR n'étant pas garanti, l'avertissement demeure
  honnête. À rediscuter si la fatigue d'alerte se manifeste.

---

## Phase 5′ — Caviardage pixel ✅

**Objectif** : que l'anonymisation d'une image soit vraie.

**Implémentée.** `pkg/pdf/redact.go`, branchée via
`docprocessor.RedactingWalker`.

**Point non négociable** : peindre un rectangle noir dans le flux de contenu
n'efface rien. L'image d'origine reste dans le fichier et se ré-extrait en une
commande — c'est le mode de fuite qui a valu des incidents publics à plusieurs
administrations.

### Aplatissement plutôt que retouche d'image

La page concernée est **rendue, noircie, puis réinjectée comme unique contenu**.
Les objets d'origine — images, flux, polices — ne sont plus référencés.

Ce choix a été retenu contre la retouche des XObject image décrite dans le plan
initial, pour trois raisons :

- **Aucune gymnastique de coordonnées.** Les boîtes de l'OCR sont déjà exprimées
  dans l'espace pixel du rendu, qui *est* l'espace de l'image aplatie.
  Correspondance 1:1, alors que retoucher un XObject imposerait de remonter la
  CTM de son placement.
- **Uniformité.** Fonctionne pour les images incorporées, les images en ligne,
  le vectoriel, le texte — tout ce qui se rend.
- **Certitude.** Les pixels d'origine ne sont pas modifiés, ils **cessent
  d'exister** dans le document produit.

Contreparties assumées, et c'est pourquoi seules les pages concernées sont
aplaties : la page perd sa couche texte (plus sélectionnable ni recherchable),
son poids augmente, et sa définition est plafonnée par le DPI de rendu.

### Détails qui décident du résultat

- **Compression Flate, pas JPEG.** Le caviardage doit être exact : un
  ré-encodage avec perte laisserait des artefacts autour des zones noircies, et
  rien ne garantit qu'un contraste résiduel n'y redevienne lisible après
  traitement.
- **Marge de 15 % de la hauteur** autour de chaque boîte. Les moteurs OCR
  épousent les glyphes au plus près ; sans marge, jambages et accents survivent.
- **Écriture en deux temps.** Le rendu doit porter sur le document *déjà
  anonymisé* : aplatir une page rendue depuis l'original y réinjecterait le
  texte en clair que le pipeline venait de remplacer. D'où le fichier
  intermédiaire, supprimé en sortie, relu dans un contexte neuf — réécrire deux
  fois le même contexte pdfcpu n'est pas un usage prévu.
- **Le rastériseur et le DPI sont mémorisés par `RunOCR`** : le caviardage doit
  rendre les pages exactement comme l'OCR les a vues, sinon les boîtes ne
  désignent plus les mêmes pixels.
- `ocrPage.regionText()` est la **source unique** de la correspondance
  offset ↔ ligne, partagée entre l'exposition du texte et le caviardage. La
  dupliquer les ferait diverger au premier changement de séparateur.

### Séparation des rôles

`docprocessor.RedactingWalker` étend `ReadOnlyTextWalker` d'un
`MarkRedaction(region, start, end)`. Le processeur détecte et désigne par
offsets ; le walker seul sait traduire ces offsets dans la géométrie de son
format. Un walker qui expose du texte sans implémenter l'interface se contente
du signalement de la phase 4′ — c'est le cas de tous les formats non PDF.

Conséquence : une entité marquée n'est plus signalée comme fuite, puisqu'elle
ne sera pas dans le document produit.

### Découverte : la vérification par ré-OCR a besoin d'un mode épars

Le test bout en bout a révélé un piège qui vaut pour toute la phase 6′.

Un large aplat noir fait classer par l'analyse de mise en page de tesseract la
zone entière — **et le texte voisin avec elle** — comme non textuelle. En mode
automatique (`PSMAuto`), la relecture d'une page caviardée ne rend alors *rien
du tout*. Une vérification ainsi configurée conclurait « aucune donnée
lisible » sur une page où il en reste : **un contrôle qui passe toujours**,
c'est-à-dire le pire défaut possible pour une vérification.

D'où `ocr.PSMSparse` et `ocr.NewTesseractExecSparse()`, à utiliser pour relire
un document déjà caviardé. Le test le vérifie dans les deux sens : « Dupont »
a disparu, « Jean » est toujours lu — sans quoi il ne prouverait pas la portée
du caviardage.

---

## Phase 6′ — Vérification par ré-OCR ✅

**Objectif** : la garantie de bout en bout.

**Implémentée.** `Processor.VerifyOutput` (`pkg/docprocessor/visual.go`) via
`docprocessor.VisualVerifier`, avec `Walker.VisualText` côté PDF.

Tous les autres contrôles inspectent des **chaînes** : ils vérifient que le
texte manipulé par le pipeline ne porte plus de donnée personnelle. Celui-ci
relit le fichier écrit **tel qu'un lecteur le voit** — rendu puis océrisé — et
parle donc le même langage que le risque réel : *ce qu'un humain peut lire dans
le document produit ne contient aucune donnée personnelle.*

### Deux contrôles complémentaires

- **Formes de surface connues de la session.** Leur réapparition prouve qu'un
  remplacement ou un caviardage a manqué sa cible. Signal le plus fort, car il
  ne dépend d'aucune redétection. Implémenté en présentant le texte relu à
  `anonymizer.Verify` comme s'il s'agissait d'une sortie — elle sait déjà
  chercher les formes connues et les identifiants structurés hors des zones de
  remplacement, il n'y avait rien à réécrire.
- **Détection fraîche**, qui attrape ce que le pipeline n'avait jamais vu : une
  donnée qu'un premier OCR avait mal lue et que la relecture déchiffre. Nature
  `anonymizer.LeakVisualResidue`.

Les deux excluent les zones écrites par l'anonymiseur (`SafeZones`) : relire une
sortie revient à relire ses propres placeholders.

### Le fail-closed prend ici la forme d'une destruction

Le contrôle porte sur le fichier **produit** : il ne peut donc pas précéder
l'écriture. En mode strict, `anon-doc` **supprime le document** et échoue. Un
fichier dont on sait qu'il expose une donnée personnelle ne doit pas subsister
sur le disque.

### Le piège du mode de segmentation

Rappel de la phase 5′, parce qu'il conditionne toute la validité de cette
phase : un large aplat de caviardage fait classer par l'analyse de mise en page
la zone entière — **et le texte voisin avec elle** — comme non textuelle. En
`PSMAuto`, la relecture d'une page caviardée ne rend alors *rien du tout*, et le
contrôle conclut « aucune donnée lisible » sur une page où il en reste : **une
vérification qui passe toujours**.

`OCROptions.VerifyEngine` porte donc un moteur distinct de celui de la
détection, documenté comme devant être en mode épars (`ocr.PSMSparse`).
`anon-doc` le câble sur `ocr.NewTesseractExecSparse()`.

### Tests

Le test décisif ne vérifie pas qu'un document propre passe — ce serait
indiscernable d'un contrôle cassé — mais qu'une **fuite est effectivement
détectée** : un caviardage volontairement porté sur la mauvaise ligne, simulant
le mode d'échec numéro un du caviardage pixel (boîtes mal alignées, décalage
d'origine, axe Y inversé). La relecture doit voir la donnée manquée *et*
confirmer que la zone effectivement caviardée, elle, a disparu.

Côté `docprocessor` : forme de surface survivante, entité jamais vue par le
pipeline, non-signalement des placeholders, silence sur document propre, et
absence d'effet sur un format qui ne sait pas se relire.

---

## Réglages transverses — ⏳ partiels

- **`HighRecall` par défaut** ✅ — `anon-doc -preset` vaut `high-recall`, pas le
  compromis F1. Pour la conformité, un faux négatif (donnée personnelle en
  clair) est sans commune mesure avec un faux positif (sur-caviardage bénin).
  C'est la recommandation de `docs/rgpd.md`, appliquée d'office : un défaut
  qu'il faut penser à activer ne protège personne.

  Avertissement explicite si le gazetteer `firstnames` manque : le levier de
  rappel de ce preset est `FirstNameDetectionPass`, qui n'a alors rien à
  injecter. Le preset dégénérerait **silencieusement** vers `Balanced`, laissant
  croire à une couverture qu'on n'a pas.

  `cmd/server` n'est délibérément pas touché : ses passes sont choisies par le
  client, requête par requête, et changer ce défaut modifierait la sémantique
  de l'API pour les appelants existants.

- **Décompte par type** ✅ — `Report.Entities` et `Report.RedactedZones`,
  affichés par `anon-doc`. C'est le seul retour **immédiat** sur la précision :
  une configuration réglée pour le rappel s'emballe silencieusement, et un type
  qui explose (des LOC partout, des PER sur des noms communs) se repère là avant
  de rendre les documents inexploitables.

- **Seuils de confiance abaissés** — reste à faire, et demande la calibration
  ci-dessous : descendre à l'aveugle ne se distingue pas d'un réglage au hasard.

- **Pilotage sur F2** — `eval` expose déjà la métrique et `-preset` mesure
  chaque preset. Ce qui manque n'est pas l'outillage mais un corpus annoté
  représentatif.

## Ce que la posture coûte

Pas de la performance — c'est accepté — mais deux choses à regarder en face.

**L'utilité du document.** Le rappel maximal sur-anonymise. Un document où un
mot sur vingt est caviardé à tort devient difficilement exploitable, et c'est le
genre de dégradation qui pousse les opérateurs à contourner l'outil. Mesurer la
précision en parallèle du rappel, même sans optimiser dessus.

**La fatigue d'alerte.** Un mode strict qui refuse un document sur trois finit
désactivé — et on se retrouve alors moins couvert qu'avec des seuils
raisonnables. C'est pourquoi basculer les scans de « refuser » à « océriser et
caviarder » compte autant : ça transforme un refus en traitement.

## Récapitulatif

| Phase | Traite | Deps | Statut |
| --- | --- | --- | --- |
| 0 — détection raster | P1 | — | ✅ |
| 1 — `Tr` / hybride | P2 | — | ✅ |
| 2 — vérif document | P3 (visibilité) | — | ✅ |
| 3′ — détection multi-vues | P3 | — | ⏳ paires + bloc + document ✅, reste 3′d |
| 4′ — OCR systématique | couverture | 3′ | ✅ |
| 5′ — caviardage pixel | couverture | 4′ | ✅ |
| 6′ — vérif par ré-OCR | garantie | 4′, 5′ | ✅ |

**Toutes les phases sont livrées.** Le contenu bitmap est lu, détecté,
réellement effacé, et le résultat est vérifié par relecture visuelle.

Restent, par ordre de valeur :

1. **Les réglages transverses** ci-dessous — `HighRecall` en défaut, seuils
   abaissés, pilotage sur F2. Peu de code, effet immédiat sur le rappel.
2. **La calibration sur corpus réel** : seuils de la phase 0, DPI, seuil de
   confiance OCR, et surtout la **mesure de la précision**, que rien n'a encore
   contrôlée.
3. **L'unification du pseudonyme d'une entité scindée** (3′d) — qualité de
   pseudonymisation, pas couverture.
4. **La vue « couche invisible »** dans l'union multi-vues.

## Jeu de test

**Fait** : un test d'intégration bout en bout sur le **PDF « searchable »**
(`pkg/pdf/integration_test.go`), c'est-à-dire sur le cas le plus dangereux —
celui où toutes les vérifications textuelles passent au vert pendant que les
pixels restent lisibles.

Le fixture est fabriqué et non versionné : une page de texte est rendue par le
rastériseur, puis réassemblée en PDF avec l'image en pleine page **plus** une
couche texte invisible qui en reprend le contenu. C'est exactement ce que
produit un scanner à OCR intégré, sans jamais manipuler de PII réelle.

Le test enchaîne les quatre phases concernées et vérifie à chaque étape :
classement en page hybride (1), océrisation (4′), caviardage des pixels et
anonymisation de la couche texte (5′), vérification visuelle (6′). Il conclut
par un contrôle direct, indépendant du chemin de vérification : ni l'extraction
de texte ni la relecture ne doivent rendre le nom.

**Le fixture porte un mot témoin** qui n'est pas une donnée personnelle et doit
survivre. Sans lui, le test passerait à vide sur une page rendue blanche — « le
nom n'est plus lisible » serait vrai parce que plus rien ne l'est. C'est le
piège récurrent de ce chantier : une vérification satisfaite par la destruction
de ce qu'elle observe.

**Reste à constituer**, documents synthétiques sans PII réelle :

- PDF natif texte (non-régression, référence F1/F2)
- PDF mixte page à page, et page composite (texte + encart scanné)
- PDF 2 colonnes, tableau, nom coupé sur saut de page, mot césuré (3′)
- Scan de mauvaise qualité, penché, faible contraste (4′/6′)
- Document à forte densité de faux positifs potentiels (mesure de précision)

## Décisions ouvertes

La posture en a tranché plusieurs (moteur OCR, refuser *vs* traiter, reflow *vs*
recouvrement). Restent :

1. **Résolution de rastérisation** — 300 dpi comme plancher, à valider sur les
   scans dégradés du jeu de test.
2. **Union de plusieurs moteurs OCR** — gain de rappel réel à mesurer avant de
   payer la complexité.
3. **Politique de réconciliation entre types** en 3′b : garder les deux
   candidats ou trancher. À décider sur données.
4. **Seuil de confiance plancher** — jusqu'où descendre avant que la précision
   ne rende les documents inexploitables.

## Impacts documentaires

- `AGENTS.md` : tableau des packages (`pkg/ocr`), section formats.
- `docs/documents.md` : capacités et limites par format — la section « PDF
  scannés : limite connue » disparaît à la phase 4′.
- `docs/rgpd.md` : le caviardage raster et sa vérification changent les
  garanties annoncées ; à réviser **avec** la phase 5′, pas après.
