# Déploiement: stockage du mapping, serveur et authenticité des modèles

Une fois la détection réglée, il reste des questions d'exploitation : où sont
stockées les tables de ré-identification, comment le serveur encaisse une charge
hostile, et à qui faire confiance pour les modèles. Ce document répond aux trois.

La checklist de déploiement RGPD (secrets, swap, core dumps, audit) est dans
[`rgpd.md` § 8](rgpd.md).

## Stockage du mapping

Le mapping est la table de ré-identification. Le sauvegarder en clair revient à
exporter la base des personnes concernées. `pkg/anonymizer/mappingstore` le
chiffre en AES-256-GCM, avec l'identifiant du mapping en données authentifiées.
Un fichier ne peut donc pas être rejoué sous un autre identifiant.

```bash
export GOANON_MAPPING_KEY="$(openssl rand -hex 32)"   # ou GOANON_MAPPING_KEY_FILE
anon-doc -model auto -input rapport.docx -output rapport_anon.docx \
  -save-mapping dossier-42 -mapping-store ./mappings -mapping-ttl 720h

mappings -store ./mappings list     # inventaire, sans déchiffrement
mappings -store ./mappings purge    # suppression des mappings expirés
mappings -store ./mappings delete dossier-42
```

Le répertoire est en `0700`, les fichiers en `0600`, l'écriture est atomique.

Attention, c'est un changement incompatible : `-save-mapping` désigne désormais
un identifiant dans un store chiffré, plus un chemin de fichier JSON. Sans clé,
la commande échoue. L'ancien comportement reste accessible via
`-save-mapping-insecure fichier.json`, avec avertissement.

À propos de l'effacement ([art. 17](https://www.cnil.fr/fr/reglement-europeen-protection-donnees/chapitre3#Article17)) : supprimer un fichier ne garantit pas la
disparition physique des blocs. Sur SSD (wear leveling) comme sur les systèmes
copy-on-write (btrfs, ZFS, APFS), les données d'origine peuvent survivre. La
seule garantie est cryptographique. Détruire la clé d'un compartiment rend
illisibles tous ses mappings, où qu'en soient les octets. C'est le seul
effacement démontrable, et l'argument à opposer à un DPO.

## Durcissement du serveur

`cmd/server` isole les requêtes et borne ses ressources. Le `Recognizer` est
sans état (aucune contamination inter-tenants, vérifié sous `-race`), le corps
des requêtes est plafonné (`-max-body`), les anonymisations concurrentes sont
limitées par un sémaphore (`-max-concurrent`), et des délais (`ReadTimeout`,
`WriteTimeout`…) coupent les connexions lentes. Les logs ne portent que des
métadonnées (méthode, statut, comptes d'entités par type), jamais de corps ni de
forme de surface. Les réponses d'erreur sont génériques, corrélées aux logs par
un identifiant `X-Request-Id`.

```bash
./bin/server -models auto
./bin/server -models "fr:auto,en:/path/to/en.crf.gz" -port 8080
```

## Authenticité des modèles

Le SHA-256 du manifeste protège l'intégrité du transfert, pas l'authenticité de
la source : qui contrôle le dépôt de releases fournit un modèle malveillant _et_
son hash. `modelstore` vérifie donc une signature Ed25519 (format minisign) du
manifeste avec une clé publique embarquée, avant de faire confiance aux hashs.
La chaîne va de la signature au manifeste, du manifeste au hash, du hash au
modèle. Le code de langue est borné à `^[a-z]{2}$` pour interdire toute
traversée de chemin via le manifeste. `-insecure-skip-verify` désactive la
vérification pour un manifeste custom non signé, avec avertissement. Pour des
données sensibles, embarquer les modèles dans l'image et fonctionner hors-ligne
(cf. [`rgpd.md`](rgpd.md)).

Les modèles sont distribués via GitHub Releases sur le dépôt dédié
[`go-anon-resources`](https://github.com/bornholm/go-anon-resources) (langues :
fr, en, es ; manifest à
`https://bornholm.github.io/go-anon-resources/manifest.json` ; cache local dans
`os.UserCacheDir()/go-anon/models`).
