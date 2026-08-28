# Secrets GitHub à configurer

Aller dans **Settings → Secrets and variables → Actions** du dépôt et créer :

| Secret | Valeur | À quoi il sert |
|---|---|---|
| `DOCKERHUB_USERNAME` | Ton Docker Hub username | Push du miroir Docker Hub et purge des vieux tags |
| `DOCKERHUB_TOKEN`    | Personal Access Token Docker Hub (Settings → Security → PAT) | idem |

Les deux sont **facultatifs** : sans eux, la CI publie sur `ghcr.io` seulement et
saute les étapes Docker Hub, sans échouer.

Le push vers `ghcr.io` et la purge GHCR utilisent le `GITHUB_TOKEN` automatique
de GitHub Actions — aucun secret à créer pour ça.

## Build local

```bash
make build
make up-explicit
```

Derrière un proxy qui fait du SSL bump, injecter la CA au build via un secret
BuildKit (les Dockerfiles la lisent si elle est présente, et s'en passent sinon) :

```bash
DOCKER_BUILDKIT=1 docker build \
  --secret id=ca-certs,src=/usr/local/share/ca-certificates/bump.crt \
  -t jbsky/clamav-hardened:local -f clamav/Dockerfile clamav/
```

## Docker Hardened Images — supprimé

Le dépôt a porté un second chemin de build, **DHI** (définitions YAML déclaratives
`squid.yaml` / `c-icap.yaml` / `clamav.yaml`, buildées par le frontend BuildKit
`dhi.io/build`). Il est supprimé, pour trois raisons :

- **DHI est un produit payant**, hors de portée ici ;
- il **n'a jamais tourné** : le job `check-dhi` testait la présence du secret
  `DOCKER_USERNAME`, absent, donc tous les jobs DHI étaient `skipped` à chaque run ;
- ses définitions **installaient des paquets apk** là où les Dockerfiles compilent
  depuis les sources. Deux définitions de la même image, publiées sous le même tag,
  qui divergeaient en silence : les YAML annonçaient encore squid 7.5 et c-icap 0.6.4
  deux mois après le passage à 7.6 et 0.6.5.

Les Dockerfiles multi-stage sont désormais la seule source de vérité.
