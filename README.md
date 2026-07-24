<p align="center">
  <img src="./misc/resources/logo.svg" style="height:150px" />
</p>

# `go-anon`

Pipeline de **reconnaissance d'entités nommées (NER) et d'anonymisation** pour le français, l'anglais et l'espagnol.

Le cœur du pipeline NER (modèle CRF, features, tokenisation, anonymisation) est écrit en Go pur, sans dépendance externe. Le traitement des documents bureautiques (DOCX, PDF) et la détection automatique de langue s'appuient sur quelques bibliothèques tierces.

## Fonctionnalités

- Détection d'entités nommées (personnes, lieux, organisations) via un modèle CRF linéaire ;
- Détection par expression régulière d'identifiants structurés (e-mail, IPv4/IPv6, IBAN, SIRET, SIREN, téléphone) et de secrets (JWT, clés OpenAI/AWS/GitHub/Slack/Stripe, Bearer) ;
- Anonymisation configurable : remplacement par tag, caviardage, empreinte HMAC ou pseudonymes cohérents ;
- Traitement de documents bureautiques : DOCX, ODT, CSV/TSV, PDF, avec sanitisation des surfaces cachées (métadonnées, commentaires, révisions) ;
- Détection automatique de la langue (fr/en/es) et téléchargement automatique des modèles pré-entraînés depuis GitHub Releases ;
- Vérification de la sortie et mode fail-closed : aucune sortie produite si une entité ou un identifiant structuré y reste détectable ;
- Mapping de ré-identification chiffré (AES-256-GCM), avec rétention et effacement cryptographique ;
- Serveur HTTP durci : isolation inter-requêtes sans état, plafonds de corps et de concurrence, logs limités aux métadonnées ;
- Presets précision/rappel (`balanced`, `high-recall`) et métriques orientées conformité (rappel par type/langue, **F2**) ;
- Vérification de signature Ed25519 (minisign) du manifeste des modèles.

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

// Restaurer le texte original
restored, _ := goanon.Deanonymize(result.Text, result.Mapping)

// Mode fail-closed : aucune sortie si une entité subsiste dans le texte anonymisé
result, err := anon.Anonymize(texte, goanon.WithStrictVerification())

// Détection des identifiants structurés (e-mail, IP, IBAN…) et des secrets
r, _ = goanon.NewRecognizer(m,
    goanon.WithLanguage("fr"),
    goanon.WithBuiltinRegexPatterns(),
    goanon.WithBuiltinSecretPatterns(), // JWT, sk-, AKIA, ghp_, xox*, sk_live_, Bearer…
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

## Garanties de traitement

La bibliothèque réalise une **pseudonymisation** au sens de l'[art. 4(5)](https://www.cnil.fr/fr/reglement-europeen-protection-donnees/chapitre1#Article4) du RGPD,
pas une anonymisation : le mapping de ré-identification est lui-même une donnée
personnelle, et l'actif le plus sensible du système. Le protéger et le détruire
après usage est ce qui rend la sortie anonyme _de facto_. **Par défaut, la sortie
est réversible** (pseudonymisée), pas anonyme.

> **Ce que l'outil garantit**
>
> - en mode strict : aucune forme de surface d'entité **détectée** ni identifiant
>   structuré ne reste en clair, sinon échec sans sortie ;
> - les secrets (clés, JWT) n'entrent jamais dans le mapping ;
> - la stratégie `hash` n'est pas cassable par dictionnaire sans la clé (HMAC) ;
> - round-trip déterministe et total ;
> - purge des métadonnées/commentaires/révisions des documents ;
> - isolation inter-requêtes du serveur, logs sans contenu.
>
> **Ce que l'outil ne garantit pas**
>
> - l'exhaustivité de la **détection** (un nom manqué par le modèle part en
>   clair, préférer alors le preset haut rappel) ;
> - la couverture des quasi-identifiants combinables, paraphrases, inférences
>   contextuelles, stylométrie ;
> - la zéroïsation mémoire (limite du langage) ni l'écrasement disque physique
>   (l'effacement réel est cryptographique).

Modèle de menace complet, garanties mesurées et checklist de déploiement :
**[`docs/rgpd.md`](docs/rgpd.md)**.

## Documentation

| Document                                             | Contenu                                                                                                             |
| ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| [`docs/anonymisation.md`](docs/anonymisation.md)     | Secrets, placeholders, stratégies de remplacement (dont `hash`), vérification fail-closed, presets précision/rappel |
| [`docs/documents.md`](docs/documents.md)             | Formats bureautiques pris en charge et sanitisation des surfaces cachées                                            |
| [`docs/deploiement.md`](docs/deploiement.md)         | Stockage chiffré du mapping, durcissement du serveur HTTP, authenticité des modèles                                 |
| [`docs/rgpd.md`](docs/rgpd.md)                       | Cadrage juridique, modèle de menace, garanties mesurées, checklist de déploiement                                   |
| [`docs/tutoriel-modele.md`](docs/tutoriel-modele.md) | Entraîner un modèle NER depuis les données WikiNER                                                                  |

## Licence

[GPL-3.0](LICENSE.md)
