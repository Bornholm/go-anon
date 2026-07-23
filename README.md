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

## Licence

[GPL-3.0](LICENSE.md)
