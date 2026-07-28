# Anonymisation: secrets, placeholders, stratégies et vérification

L'anonymiseur fait plus que substituer les entités détectées. Il écarte les
secrets du mapping pour qu'ils ne soient jamais restaurables, tire un nonce par
session pour rendre ses placeholders imprévisibles, et relit sa propre sortie
avant de la livrer. Voici comment ces mécanismes fonctionnent et comment les
régler.

Le cadrage juridique (pseudonymisation, modèle de menace, checklist RGPD) est
traité dans [`rgpd.md`](rgpd.md).

## Secrets

Les types `API_KEY`, `JWT` et `SECRET` (jetons GitHub/Stripe/AWS, Bearer,
variables `*_PASSWORD`…) court-circuitent toute stratégie. Ils sont remplacés
par un marqueur fixe `⟦API_KEY_REDACTED⟧`, n'entrent ni dans `Mapping` ni dans
`OriginalToPlaceholder`, et ne sont pas restaurables. Leur forme de surface
n'apparaît pas non plus dans `Result.Entities`, sauf `WithExposeSecrets(true)`,
réservé au débogage local. Deux occurrences d'un même secret reçoivent un
marqueur identique et indistinct, sans corrélation possible.

## Placeholders

Format : `⟦TYPE_N_nonce⟧`. Les délimiteurs U+27E6/U+27E7 sont quasi inexistants
en texte naturel, et le nonce (3 octets, tiré par session) rend le format
imprédictible. Un placeholder ne peut donc pas être pré-injecté dans le texte
source. Si le texte source en contient un malgré tout, `Anonymize` retourne
`ErrPlaceholderCollision` au lieu de corrompre silencieusement le round-trip
(`WithEscapeCollisions()` échappe plutôt que d'échouer).

`Deanonymize` remplace en un scan unique et déterministe, et retourne
`ErrIncompleteMapping` si un placeholder subsiste en sortie.

L'ancien format `[TYPE_N]` reste accessible via `WithLegacyPlaceholders()`.
Attention, c'est un changement incompatible : les consommateurs qui parsent
`[PERSON_1]` doivent migrer ou activer cette option.

## Stratégie `redact` (caviardage)

L'entité est remplacée par un bloc de `█` dont la longueur est **tirée
indépendamment du texte remplacé**, entre `DefaultRedactMinRunes` (4) et
`DefaultRedactMaxRunes` (8) caractères, via `crypto/rand`. Un bloc dont la
longueur reproduisait celle de l'entité — le comportement historique — était un
canal auxiliaire : « ██████ habite à ████ » divulgue la taille de chaque forme
de surface, ce qui suffit souvent à ré-identifier par croisement avec un
annuaire ou une liste de noms.

```go
cfg := anonymizer.Config{
    Strategy:       anonymizer.Redact,
    RedactMinRunes: 6, // Min == Max ⇒ longueur constante
    RedactMaxRunes: 6,
}
```

Bornes incohérentes (min < 1, max < min, max > `MaxRedactRunes` = 64) →
`ErrInvalidRedactRange`, plutôt qu'un caviardage silencieusement inefficace.

Le tirage a lieu **par occurrence** : deux mentions de la même personne ne se
laissent pas relier par une longueur de bloc commune.

Conséquence directe : `redact` est **irréversible**, comme les secrets. Ni
`Result.Mapping`, ni `Result.OriginalToPlaceholder`, ni la `Session` ne
conservent d'entrée pour les entités caviardées — plusieurs entités distinctes
produisent le même bloc, un mapping à clés collisionnelles ferait restaurer du
texte faux par `Deanonymize`. Le cache de cohérence (`ConsistentMap`) est
également court-circuité : il n'a rien à stabiliser et retiendrait des formes de
surface en mémoire. Seuls les types confiés à un `CustomReplacer` gardent leur
entrée de mapping, leur placeholder étant sous le contrôle de l'appelant.

Le vidage intervient **après** les passes de post-traitement et la vérification,
qui s'appuient sur le mapping pour traquer les occurrences résiduelles.

En CLI, `-strategy redact` combiné à `-save-mapping` ou `-save-mapping-insecure`
est refusé au démarrage. Le nombre d'entités traitées reste disponible via
`docprocessor.Report.TotalEntities()`.

## Stratégie `hash`

Le pseudonyme est dérivé par HMAC-SHA-256 avec une clé secrète (pepper). Un
SHA-256 non salé sur des noms propres se casse par dictionnaire en quelques
secondes. La clé se fournit par variable d'environnement, jamais par flag CLI,
car les arguments de processus sont visibles dans `ps` et les logs
d'orchestrateur :

```bash
export GOANON_HASH_KEY="$(openssl rand -hex 32)"   # 32 octets minimum
anon-doc -model auto -strategy hash -input doc.docx -output out.docx
```

Sans clé, la stratégie échoue au lieu de se dégrader silencieusement.
`-insecure-hash` rétablit l'ancien SHA-256 nu pour les démonstrations, avec
avertissement.

`-hash-scope` (ou `goanon.WithHashScope`) entre dans le HMAC. Par défaut, une
même clé produit le même pseudonyme partout : la corrélation entre documents
devient possible, parfois voulue pour l'analyse, parfois interdite. Un scope par
dossier ou par client casse ce chaînage.

## Vérification de la sortie

La sortie anonymisée est recontrôlée avant d'être rendue : formes de surface du
mapping encore présentes, identifiants structurés (e-mail, IBAN, jetons…)
re-détectables, corruption d'encodage, placeholder déjà présent dans la source.
Le contrôle raisonne par zones sûres. Les spans écrits par l'anonymiseur sont
exclus du scan, si bien qu'un pseudonyme ou un digest n'est jamais compté comme
une fuite.

```go
// Observation : rapport attaché au résultat, rien n'est bloqué.
res, _ := anon.Anonymize(texte, goanon.WithVerification())
res.Verification.OK()      // false s'il reste quelque chose
res.Verification.Leaks     // offsets et types, jamais le texte fuité

// Fail-closed : une seule fuite suffit à refuser de produire une sortie.
res, err := anon.Anonymize(texte, goanon.WithStrictVerification())
```

En ligne de commande, `-strict` sur `anon-doc`, `demo` et `server` :

```bash
anon-doc -model auto -strict -input rapport.docx -output rapport_anon.docx
```

En mode strict, `anon-doc` n'écrit aucun fichier de sortie si un segment fuit,
et le serveur répond `422` sans corps anonymisé. Sans `-strict`, le rapport part
sur stderr (comptes par nature de fuite, jamais de contenu).

Le jeu de motifs re-passé sur la sortie est indépendant de la configuration du
`Recognizer`. Si le pipeline n'a pas activé `WithBuiltinRegexPatterns()`, les
e-mails restés en clair _seront_ signalés, ils sont bien là. Pour restreindre la
vérification en connaissance de cause : `goanon.WithVerifyPatterns(...)`.

## Rappel et presets (orientation conformité)

Pour la conformité, le rappel prime sur la précision. Un faux positif est un
sur-caviardage bénin ; un faux négatif est une donnée personnelle laissée en
clair. `eval` publie donc précision et rappel séparément, par type et par
langue, et le F2 (rappel pondéré ×2) comme métrique de référence :

```bash
eval -model model_fr.crf.gz -lang fr -test corpus.conll \
  -gazetteers "firstnames:data/eu_prenoms.txt" -preset high-recall
```

Deux presets sont fournis (`goanon.Balanced` / `goanon.HighRecall`, ou
`-preset`). `balanced` est le compromis F1 par défaut ; `high-recall` injecte en
PER les prénoms du gazetteer manqués par le modèle. La recommandation RGPD est
`high-recall` plus mode strict. Le corpus
`pkg/ner/testdata/known_leaks_fr.txt` verrouille en CI la non-régression du
rappel sur des fuites historiques.
