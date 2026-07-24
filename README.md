<p align="center">
  <img src="./misc/resources/logo.svg" style="height:150px" />
</p>

# `go-anon`

Pipeline de **reconnaissance d'entités nommées (NER) et d'anonymisation** pour le français, l'anglais et l'espagnol.

Le cœur du pipeline NER (modèle CRF, features, tokenisation, anonymisation) est écrit en Go pur, sans dépendance externe. Le traitement des documents bureautiques (DOCX, PDF) et la détection automatique de langue s'appuient sur quelques bibliothèques tierces.

## Fonctionnalités

- Détection d'entités nommées (personnes, lieux, organisations) via un modèle CRF linéaire ;
- Détection par expression régulière d'identifiants structurés : e-mail, IPv4/IPv6, IBAN, SIRET, SIREN, numéro de téléphone ;
- Détection de jetons d'authentification et clés d'API : JWT, OpenAI, AWS, GitHub, Slack, Stripe, Bearer ;
- Anonymisation configurable : remplacement par tag, caviardage, empreinte SHA-256 ou pseudonymes cohérents ;
- Traitement de documents bureautiques : DOCX, ODT, CSV/TSV, PDF ;
- Support du français, de l'anglais et de l'espagnol avec profils linguistiques dédiés ;
- Détection automatique de la langue (fr/en/es) pour éviter de la spécifier en amont ;
- Téléchargement automatique des modèles pré-entraînés depuis GitHub Releases ;
- Vérification de la sortie et mode fail-closed : aucune sortie produite si une
  entité connue ou un identifiant structuré y reste détectable ;
- Mapping de ré-identification chiffré (AES-256-GCM), avec rétention et
  effacement cryptographique ;
- Sanitisation des surfaces cachées des documents (métadonnées d'auteur,
  commentaires, révisions) — fail-closed en mode strict ;
- Serveur HTTP durci : isolation inter-requêtes sans état, plafonds de corps et
  de concurrence, délais, logs limités aux métadonnées ;
- Post-filtres chaînables : seuil de confiance, longueur de span, liste noire ;
- Enrichissement optionnel via gazetteers et Brown clusters ;
- Configuration d'inférence auto-propagée depuis le modèle (schéma de features,
  fenêtre de contexte) et `Recognizer.Warnings()` qui signale toute ressource
  manquante (gazetteer/clusters) par rapport à l'entraînement ;
- Serveur HTTP intégré via `cmd/server`.

## Utilisation

```bash
go get github.com/bornholm/go-anon
```

```go
import goanon "github.com/bornholm/go-anon"

// Charger un modèle
f, _ := os.Open("model_fr.crf.gz")
m, _ := goanon.LoadModel(f)

// Reconnaître des entités
r, _ := goanon.NewRecognizer(m, goanon.WithLanguage("fr"))
entities, _ := r.Recognize("Jean Dupont habite à Paris.")
// → [{Text:"Jean Dupont", Type:"PER"}, {Text:"Paris", Type:"LOC"}]

// Anonymiser
anon := goanon.NewAnonymizer(r, goanon.Config{Strategy: goanon.TagReplace})
result, _ := anon.Anonymize("Jean Dupont habite à Paris.")
// result.Text → "⟦PERSON_1_a3f9c2⟧ habite à ⟦LOCATION_1_a3f9c2⟧."
// Le suffixe est un nonce de session : voir « Placeholders » ci-dessous.

// Restaurer le texte original
restored, _ := goanon.Deanonymize(result.Text, result.Mapping)

// Mode fail-closed : aucune sortie si une entité subsiste dans le texte anonymisé
result, err := anon.Anonymize(texte, goanon.WithStrictVerification())
// err != nil → rien n'est produit ; voir « Vérification » ci-dessous

// Activer la détection des identifiants structurés courants (e-mail, IP, IBAN…)
r, _ = goanon.NewRecognizer(m,
    goanon.WithLanguage("fr"),
    goanon.WithBuiltinRegexPatterns(),
)

// Activer la détection des jetons d'authentification et clés d'API
r, _ = goanon.NewRecognizer(m,
    goanon.WithLanguage("fr"),
    goanon.WithBuiltinSecretPatterns(), // JWT, OpenAI sk-, AWS AKIA, GitHub ghp_, Slack xox*, Stripe sk_live_, Bearer…
)

// Détecter automatiquement la langue avant de choisir le modèle
det := goanon.NewWhatlangDetector(goanon.SupportedLanguages()...)
res, _ := det.Detect("Jean Dupont habite à Paris.")
// res.Lang → "fr" (si res.Reliable)
```

En ligne de commande, la langue est détectée automatiquement par défaut :

```bash
# La langue est détectée à partir du contenu du document (-lang auto par défaut)
anon-doc -model auto -input rapport.docx -output rapport_anon.docx
# ou forcée explicitement
anon-doc -model auto -lang fr -input rapport.docx -output rapport_anon.docx
```

Voir [`docs/tutoriel-modele.md`](./docs/tutoriel-modele.md) pour entraîner un modèle français.

## Garanties de traitement

La bibliothèque réalise une **pseudonymisation** au sens de l'art. 4(5) du RGPD,
pas une anonymisation : le mapping de ré-identification est lui-même une donnée
personnelle, et l'actif le plus sensible du système. Le protéger et le détruire
après usage est ce qui rend la sortie anonyme *de facto*.

### Secrets

Les types `API_KEY`, `JWT` et `SECRET` (jetons GitHub/Stripe/AWS, Bearer,
variables `*_PASSWORD`…) court-circuitent toute stratégie : ils sont remplacés
par un marqueur fixe `⟦API_KEY_REDACTED⟧`, n'entrent ni dans `Mapping` ni dans
`OriginalToPlaceholder`, et ne sont pas restaurables. Leur forme de surface
n'apparaît pas non plus dans `Result.Entities` (sauf `WithExposeSecrets(true)`,
réservé au débogage local). Deux occurrences d'un même secret reçoivent un
marqueur identique et indistinct : aucune corrélation possible.

### Placeholders

Format : `⟦TYPE_N_nonce⟧`. Les délimiteurs U+27E6/U+27E7 sont quasi inexistants
en texte naturel, et le nonce (3 octets, tiré par session) rend le format
imprédictible — un placeholder ne peut pas être pré-injecté dans le texte source.
Si le texte source contient malgré tout un placeholder, `Anonymize` retourne
`ErrPlaceholderCollision` plutôt que de corrompre silencieusement le round-trip
(`WithEscapeCollisions()` échappe au lieu d'échouer).

`Deanonymize` remplace en un scan unique et déterministe, et retourne
`ErrIncompleteMapping` si un placeholder subsiste en sortie.

L'ancien format `[TYPE_N]` reste accessible via `WithLegacyPlaceholders()`.
**Changement incompatible** : les consommateurs qui parsent `[PERSON_1]` doivent
migrer ou activer cette option.

### Stratégie `hash`

Le pseudonyme est dérivé par **HMAC-SHA-256** avec une clé secrète (pepper) :
un SHA-256 non salé sur des noms propres est cassable par dictionnaire en
quelques secondes. La clé se fournit par variable d'environnement, jamais par
flag CLI (les arguments de processus sont visibles dans `ps` et les logs
d'orchestrateur) :

```bash
export GOANON_HASH_KEY="$(openssl rand -hex 32)"   # 32 octets minimum
anon-doc -model auto -strategy hash -input doc.docx -output out.docx
```

Sans clé, la stratégie **échoue** au lieu de se dégrader silencieusement.
`-insecure-hash` rétablit l'ancien SHA-256 nu pour les démonstrations, avec
avertissement.

`-hash-scope` (ou `goanon.WithHashScope`) entre dans le HMAC : par défaut une
même clé produit le même pseudonyme partout — corrélation possible entre
documents, parfois voulue pour l'analyse, parfois interdite. Un scope par
dossier ou par client casse cette linkability.

### Vérification de la sortie

La sortie anonymisée est recontrôlée avant d'être rendue : formes de surface du
mapping encore présentes, identifiants structurés (e-mail, IBAN, jetons…)
re-détectables, corruption d'encodage, placeholder déjà présent dans la source.
Le contrôle raisonne par **zones sûres** — les spans écrits par l'anonymiseur
sont exclus du scan, ce qui évite qu'un pseudonyme ou un digest soit compté
comme une fuite.

```go
// Observation : rapport attaché au résultat, rien n'est bloqué.
res, _ := anon.Anonymize(texte, goanon.WithVerification())
res.Verification.OK()      // false s'il reste quelque chose
res.Verification.Leaks     // offsets et types — jamais le texte fuité

// Fail-closed : une seule fuite suffit à refuser de produire une sortie.
res, err := anon.Anonymize(texte, goanon.WithStrictVerification())
```

En ligne de commande, `-strict` sur `anon-doc`, `demo` et `server` :

```bash
anon-doc -model auto -strict -input rapport.docx -output rapport_anon.docx
```

En mode strict, `anon-doc` n'écrit **aucun** fichier de sortie si un segment
fuit, et le serveur répond `422` sans corps anonymisé. Sans `-strict`, le
rapport part sur stderr (comptes par nature de fuite, jamais de contenu).

Le jeu de motifs re-passé sur la sortie est indépendant de la configuration du
`Recognizer` : si le pipeline n'a pas activé `WithBuiltinRegexPatterns()`, les
e-mails restés en clair *seront* signalés — ils sont bien là. Pour restreindre
la vérification en connaissance de cause : `goanon.WithVerifyPatterns(...)`.

### Stockage du mapping

Le mapping est la table de ré-identification : le sauvegarder en clair revient à
exporter la base des personnes concernées. `pkg/anonymizer/mappingstore` le
chiffre en **AES-256-GCM**, avec l'identifiant du mapping en données
authentifiées — un fichier ne peut pas être rejoué sous un autre identifiant.

```bash
export GOANON_MAPPING_KEY="$(openssl rand -hex 32)"   # ou GOANON_MAPPING_KEY_FILE
anon-doc -model auto -input rapport.docx -output rapport_anon.docx \
  -save-mapping dossier-42 -mapping-store ./mappings -mapping-ttl 720h

mappings -store ./mappings list     # inventaire, sans déchiffrement
mappings -store ./mappings purge    # suppression des mappings expirés
mappings -store ./mappings delete dossier-42
```

Le répertoire est en `0700`, les fichiers en `0600`, l'écriture est atomique.

**Changement incompatible** : `-save-mapping` désigne désormais un identifiant
dans un store chiffré, et non plus un chemin de fichier JSON. Sans clé, la
commande échoue. L'ancien comportement reste accessible via
`-save-mapping-insecure fichier.json`, avec avertissement.

**Sur l'effacement (art. 17)** : la suppression d'un fichier ne garantit pas la
disparition physique des blocs — sur SSD (wear leveling) comme sur les systèmes
copy-on-write (btrfs, ZFS, APFS), les données d'origine peuvent survivre. La
garantie réelle est cryptographique : **détruire la clé d'un compartiment rend
illisibles tous ses mappings**, où qu'en soient les octets. C'est le seul
effacement démontrable, et l'argument à opposer à un DPO.

### Sanitisation des documents

Un document « anonymisé » ne se limite pas à son texte visible. Un DOCX porte
l'auteur et le dernier modificateur dans ses propriétés, du texte supprimé dans
ses révisions, des commentaires ; un PDF un dictionnaire Info et des métadonnées
XMP ; un ODT un `meta.xml`. `anon-doc` purge ces surfaces avant d'écrire (option
`-sanitize`, activée par défaut) :

```bash
anon-doc -model auto -strict -input rapport.docx -output rapport_anon.docx
```

| Format | Surface traitée                                                        |
| ------ | --------------------------------------------------------------------- |
| DOCX   | `docProps` purgés, commentaires supprimés, révisions **détectées**    |
| ODT    | `meta.xml` purgé, annotations et modifications suivies retirées       |
| PDF    | dictionnaire Info et métadonnées XMP purgés, annotations signalées    |
| CSV    | aucune surface cachée (le fichier est son texte visible)              |

Certaines surfaces ne peuvent pas être neutralisées avec garantie (les révisions
DOCX, les annotations et pièces jointes PDF). En mode `-strict`, leur présence
**refuse le document** plutôt que de produire une sortie faussement propre — à
charge de l'opérateur d'accepter les révisions ou de retirer les pièces jointes
en amont. Hors strict, elles sont signalées sur stderr (comptes seulement).

### Durcissement du serveur

`cmd/server` isole les requêtes et borne ses ressources : le `Recognizer` est
sans état (aucune contamination inter-tenants, vérifié sous `-race`), le corps
des requêtes est plafonné (`-max-body`), les anonymisations concurrentes sont
limitées par un sémaphore (`-max-concurrent`), et des délais (`ReadTimeout`,
`WriteTimeout`…) coupent les connexions lentes. Les logs ne portent que des
métadonnées (méthode, statut, comptes d'entités par type) — jamais de corps ni
de forme de surface ; les réponses d'erreur sont génériques, corrélées aux logs
par un identifiant `X-Request-Id`.

## Licence

[GPL-3.0](LICENSE.md)
