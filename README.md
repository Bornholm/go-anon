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
// result.Text → "[PERSON_1] habite à [LOCATION_1]."

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

## Licence

[GPL-3.0](LICENSE.md)
