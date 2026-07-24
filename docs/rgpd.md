# go-anon & RGPD — garanties, limites, déploiement

Ce document est destiné à un·e DPO, un·e auditeur·rice ou un·e responsable de
traitement. Il décrit **ce que l'outil garantit, ce qu'il ne garantit pas**, et
comment le déployer conformément au RGPD. Il est volontairement honnête sur les
limites : une garantie surévaluée est un risque de conformité.

## 1. Position juridique : pseudonymisation, pas anonymisation

`go-anon` réalise une **pseudonymisation** au sens de l'**[art. 4(5)](https://www.cnil.fr/fr/reglement-europeen-protection-donnees/chapitre1#Article4) du RGPD**, et
non une anonymisation au sens du **[considérant 26](https://eur-lex.europa.eu/legal-content/FR/TXT/?uri=CELEX:32016R0679)**.

- Le remplacement des entités par des pseudonymes (`⟦PERSON_1_…⟧`, un faux nom,
  une empreinte HMAC) reste **réversible** tant qu'existe une table de
  correspondance (le _mapping_) ou une clé.
- Le **mapping est donc lui-même une donnée personnelle**, l'actif le plus
  sensible du système. Il doit être protégé comme la base des personnes
  concernées.
- **L'anonymisation de facto** (irréversibilité) n'est atteinte que par la
  **destruction du mapping et de la clé** de son compartiment : voir le
  _crypto-shredding_ (§ 5).

> Toute formulation laissant entendre une « anonymisation irréversible par
> défaut » serait trompeuse. Par défaut, la sortie est **pseudonymisée**.

## 2. Modèle de menace

### 2.1 Couvert

| Surface                                     | Mécanisme                                                                                      | Chantier |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------- | -------- |
| Personnes, lieux, organisations             | NER (CRF linéaire) + passes de complétion                                                      | S9       |
| Identifiants structurés                     | Regex : e-mail, IPv4/IPv6, IBAN, SIRET/SIREN, téléphone                                        | —        |
| Secrets                                     | Regex (JWT, clés OpenAI/AWS/GitHub/Slack/Stripe, Bearer), **jamais conservés dans le mapping** | S1       |
| Formes de surface résiduelles               | Passe de vérification finale, mode fail-closed                                                 | S4       |
| Métadonnées & contenus cachés des documents | `Sanitizer` par format (cf. § 4)                                                               | S6       |
| Fuite inter-requêtes (serveur)              | `Recognizer` sans état, partage sûr entre goroutines                                           | S7       |
| Fuite dans logs/erreurs                     | Erreurs porteuses d'offsets/types **jamais de contenu** ; logs métadonnées seuls               | S1, S7   |
| Authenticité des modèles                    | Signature Ed25519 (minisign) du manifeste, clé embarquée                                       | S10      |

### 2.2 Non couvert (limites de conception)

Ces angles morts **ne sont pas des bugs** : ils sont hors de portée d'un système
fondé sur la détection d'entités.

- **Quasi-identifiants combinables** : « un homme de 42 ans, cardiologue à
  Guéret » n'est aucune entité prise isolément, mais ré-identifie une personne
  par recoupement. Aucune détection d'entité ne les couvre.
- **Paraphrases et périphrases** : « le maire de la commune voisine », « l'auteur
  du rapport de mars » désignent une personne sans la nommer.
- **Inférences contextuelles** : informations déductibles du contexte non
  textuellement présentes.
- **Stylométrie** : identification par le style d'écriture.
- **Rappel imparfait du modèle** : un nom que le CRF n'a jamais vu peut être
  manqué. Le preset haut rappel (§ 6) et le corpus de non-régression des fuites
  connues (`pkg/ner/testdata/known_leaks_fr.txt`) réduisent ce risque sans
  l'annuler.

## 3. Garanties mesurées

### 3.1 Mode strict (S4)

Garantie contractuelle documentable :

> **En mode strict** (`WithStrictVerification`), aucune forme de surface d'entité
> détectée ni aucun pattern d'identifiant structuré n'est présent dans la sortie,
> **sinon l'opération échoue sans produire de sortie** (ni fichier partiel, ni
> réponse HTTP). Le rapport de fuite porte des offsets et des types, **jamais le
> texte fuité**.

C'est une garantie sur ce qui est **détecté** : elle ne compense pas un défaut de
rappel (§ 2.2). D'où la recommandation strict **+** haut rappel (§ 6).

### 3.2 Rappel par type et par langue (S9)

Pour la conformité, **le rappel prime sur la précision** : un faux positif =
sur-caviardage bénin ; un faux négatif = donnée personnelle en clair. La métrique
de référence est donc le **F2** (rappel pondéré ×2), publié à côté de la
précision, du rappel et du F1, **par type et par langue** :

```bash
./bin/eval -model model_fr.crf.gz -lang fr -test corpus.conll \
  -clusters data/brown_clusters_fr.txt \
  -gazetteers "firstnames:data/eu_prenoms.txt,lastnames:data/eu_patronymes.txt" \
  -preset high-recall
```

Le rappel **PER** importe davantage que le rappel **MISC** ; `eval` détaille les
deux. Publier ces chiffres dans le README (F1/F2 agrégés) et les mesurer à chaque
release fait partie de la responsabilité de l'[art. 5(2)](https://www.cnil.fr/fr/reglement-europeen-protection-donnees/chapitre2#Article5) (_accountability_).

### 3.3 Propriétés du hachage (S2)

La stratégie `hash` utilise **HMAC-SHA-256 avec clé** (pepper ≥ 32 octets, via
`GOANON_HASH_KEY`), pas un SHA-256 nu. Conséquence structurelle : **il est
impossible de vérifier l'hypothèse « ce hash correspond-il à _Dupont_ ? » sans la
clé**. Un SHA-256 non salé sur un nom propre serait cassable par dictionnaire en
secondes (l'espace des prénoms/noms est minuscule). Sans clé, la stratégie échoue
(fail-closed) sauf `WithInsecureHash` explicite. `WithHashScope` permet de casser
la corrélation inter-dossiers.

## 4. Surfaces documentaires (S6) — inventaire

Le `Sanitizer` purge ou signale les surfaces **hors du texte visible**. En mode
strict, toute surface **détectée mais non neutralisable de façon garantie**
provoque une erreur : **pas de document faussement propre**.

| Format  | Surface                                      | Traitement                                                 |
| ------- | -------------------------------------------- | ---------------------------------------------------------- |
| DOCX    | corps                                        | anonymisé (Walker)                                         |
| DOCX    | `docProps/core.xml`, `app.xml`, `custom.xml` | **purgés**                                                 |
| DOCX    | commentaires (`comments.xml`)                | **supprimés**                                              |
| DOCX    | révisions (`w:ins`/`w:del`)                  | **détectées → strict : erreur** (acceptation non garantie) |
| DOCX    | headers/footers, footnotes, textboxes        | non couverts (extension future)                            |
| ODT     | `meta.xml`                                   | **purgé**                                                  |
| ODT     | annotations (commentaires)                   | **retirées de l'arbre**                                    |
| ODT     | tracked-changes                              | **retirées de l'arbre**                                    |
| PDF     | dictionnaire Info + XMP                      | **purgés**                                                 |
| PDF     | annotations, pièces jointes                  | **détectées → strict : erreur**                            |
| CSV/TSV | —                                            | pas de surface cachée (le fichier _est_ son texte visible) |

Critère de recette DOCX : `unzip -p sortie.docx | grep -c "NomTest"` == 0 sur
l'ensemble des parts OOXML, ou erreur en mode strict.

## 5. Effacement ([art. 17](https://www.cnil.fr/fr/reglement-europeen-protection-donnees/chapitre3#Article17)) et _crypto-shredding_

Le mapping est stocké chiffré (**AES-256-GCM**, `pkg/anonymizer/mappingstore`),
clé 32 octets via `GOANON_MAPPING_KEY` / `GOANON_MAPPING_KEY_FILE`, fichiers en
`0600`, répertoire en `0700`, AAD = identifiant du document (anti-rejeu).

- `Delete` supprime le fichier **et** tente un écrasement _best-effort_.
- **Limite honnête** : sur SSD/COW (btrfs, APFS), l'écrasement physique **n'est
  pas garanti** (wear-leveling, copy-on-write). La vraie garantie d'effacement
  n'est **pas** l'écrasement du fichier mais le **crypto-shredding** : détruire la
  clé d'un compartiment (`keyID`) rend **tous** ses mappings définitivement
  indéchiffrables. C'est l'argument d'effacement le plus solide face à un·e DPO.

Après effacement du mapping/clé, la sortie pseudonymisée devient **de facto
anonyme** (plus aucune clé de correspondance) : c'est là, et seulement là, que
l'irréversibilité du considérant 26 est atteinte.

## 6. Matrice de décision : quel preset ?

| Régime                   | Configuration                                | Usage                                                                                                           |
| ------------------------ | -------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| **Équilibré** (défaut)   | preset `balanced`, sans mode strict          | Analyse, exploration ; le compromis F1. **Pas** la recommandation RGPD.                                         |
| **Haut rappel**          | preset `high-recall`                         | Maximise la détection (F2) ; injecte les prénoms du gazetteer manqués par le CRF, au prix de faux positifs.     |
| **Strict + haut rappel** | `high-recall` **+** `WithStrictVerification` | **Recommandation RGPD.** Détecte au maximum _et_ refuse toute sortie où une entité détectée resterait en clair. |

Le preset haut rappel n'a son plein effet qu'avec le **gazetteer de prénoms**
chargé (`-gazetteers "firstnames:…"`). Sans lui, il dégénère vers l'équilibré.

## 7. Limites honnêtes

- **Pas de zéroïsation mémoire garantie.** En Go, le GC peut être copiant, les
  `string` sont immuables et copiées, des buffers intermédiaires subsistent.
  `Session.Close()` **accélère la collectabilité** mais ne garantit **pas**
  l'effacement physique en RAM. Atténuations d'infrastructure (§ 8).
- **Effacement disque = crypto-shredding, pas écrasement physique** (§ 5).
- **Rappel imparfait** (§ 2.2) : le mode strict garantit le traitement du
  _détecté_, pas l'exhaustivité de la détection.
- **Modèles non signés à ce jour.** Le mécanisme de vérification Ed25519 est
  livré et testé ; tant que le dépôt de ressources ne signe pas ses manifests, la
  vérification émet un **avertissement** et poursuit. Pour données sensibles,
  embarquer les modèles dans l'image et fonctionner **hors-ligne** (§ 8).

## 8. Checklist de déploiement

- [ ] **Mode strict** activé (`-strict` / `WithStrictVerification`).
- [ ] **Preset haut rappel** (`-preset high-recall` / `goanon.HighRecall`) + gazetteer de prénoms.
- [ ] `GOANON_HASH_KEY` et `GOANON_MAPPING_KEY` fournies **par un gestionnaire de
      secrets** (variable d'environnement ou fichier monté), **jamais** en flag
      CLI (visible dans `ps`, l'historique shell, les logs d'orchestrateur).
- [ ] **TTL de mapping** défini (rétention bornée) ; purge planifiée.
- [ ] **Mode hors-ligne** (`-offline`) avec **modèles embarqués dans l'image** ;
      pas de téléchargement au runtime. Sinon, vérification de signature active
      dès que les manifests sont signés (ne pas utiliser `-insecure-skip-verify`).
- [ ] **Swap chiffré ou désactivé**.
- [ ] **Core dumps désactivés** : `GOTRACEBACK=none`, `ulimit -c 0` /
      `LimitCORE=0` (systemd), `securityContext` restrictif (Kubernetes).
- [ ] Logs **métadonnées seulement**, vérifié : aucun corps de requête, aucun
      `ent.Text` (cf. test `TestGuarantee_LogsAndErrorsCarryNoProbe`).
- [ ] Journal d'audit conformité (art. 5(2)) si requis : horodatage, langue,
      stratégie, comptes d'entités par type, SHA-256 du document source.
      **Jamais de forme de surface.**

---

## 9. Références de test

Les garanties ci-dessus sont matérialisées par des tests nommés `TestGuarantee_*`
et des cibles de fuzz :

- `TestGuarantee_*` (secrets hors mapping, round-trip, zéro résiduel en strict,
  zéro PII en clair dans le store, logs sans marqueur, sortie DOCX sans nom
  caché) ;
- `FuzzAnonymizeInvariants`, `FuzzRoundTrip`, `FuzzDeanonymize`, `FuzzVerify` ;
- `TestGuarantee_KnownLeaksRecall` (corpus de fuites connues, non-régression du
  rappel) ;
- `go test -race ./...` (isolation inter-requêtes).
