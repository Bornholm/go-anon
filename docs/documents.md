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

L'inventaire complet des surfaces par format (headers/footers, footnotes,
tracked-changes…) et les critères de recette figurent dans
[`rgpd.md` § 4](rgpd.md).
