# Politique de sécurité

## Signalement d'une vulnérabilité

Merci de **ne pas** ouvrir d'issue publique pour une vulnérabilité de sécurité.

Signalez-la de façon privée via l'onglet **Security → Report a vulnerability**
du dépôt GitHub (GitHub Private Vulnerability Reporting). À défaut, contactez les
mainteneurs directement.

Merci d'inclure, dans la mesure du possible :

- une description du problème et de son impact ;
- les versions concernées (`go-anon` et, le cas échéant, les modèles) ;
- un scénario de reproduction minimal — **sans données personnelles réelles** ;
- toute atténuation connue.

## Périmètre

Sont particulièrement dans le périmètre :

- **fuite de formes de surface** : une entité détectée qui reparaît en clair dans
  la sortie (le mode strict, `WithStrictVerification`, doit l'empêcher) ;
- **fuite via le mapping** : un secret (clé d'API, JWT) qui entre dans une table
  de ré-identification (cf. `ner.IsSecretType`) ;
- **fuite hors du texte visible** : PII dans les métadonnées, commentaires ou
  révisions d'un document (cf. `docprocessor.Sanitizer`) ;
- **fuite dans les logs ou les erreurs** : contenu utilisateur (`ent.Text`, corps
  de requête) apparaissant dans un message d'erreur ou un journal ;
- **corruption du round-trip** ou ré-identification erronée
  (`Anonymize`/`Deanonymize`) ;
- **chaîne d'approvisionnement des modèles** : contournement de la vérification
  de signature/hachage du manifeste.

## Hors périmètre

Les limites documentées dans [`docs/rgpd.md`](docs/rgpd.md) ne constituent pas
des vulnérabilités : quasi-identifiants combinables, paraphrases, inférences
contextuelles, stylométrie, et l'absence de zéroïsation mémoire garantie (limite
structurelle du langage Go, cf. `docs/rgpd.md` § « Limites honnêtes »).

## Bonnes pratiques attendues des déploiements

La sécurité du produit dépend de sa configuration. Voir la checklist de
déploiement dans [`docs/rgpd.md`](docs/rgpd.md) : mode strict, preset haut
rappel, clés (`GOANON_HASH_KEY`, `GOANON_MAPPING_KEY`) via un gestionnaire de
secrets, mode hors-ligne, TTL de mapping, swap chiffré et core dumps désactivés.
