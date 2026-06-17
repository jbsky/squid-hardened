# Secrets GitHub à configurer

Aller dans **Settings → Secrets and variables → Actions** du repo et créer :

| Secret | Valeur |
|---|---|
| `DOCKER_USERNAME` | Ton Docker Hub username (même que pour `docker login`) |
| `DOCKER_TOKEN`    | Personal Access Token Docker Hub (Settings → Security → PAT) |

Ces deux secrets servent **uniquement** à `docker login dhi.io` pour que BuildKit
puisse pull le frontend `dhi.io/build:2-alpine3.23` au moment du build.

Le push des images vers `ghcr.io` utilise le `GITHUB_TOKEN` automatique de
GitHub Actions (aucun secret supplémentaire).

## Vérifier l'accès dhi.io en local

```bash
docker login dhi.io
# Username: ton-docker-id
# Password: ton-docker-token

# Test : pull le frontend directement
docker pull dhi.io/build:2-alpine3.23
```

## Build local avec DHI

```bash
# Avec les YAML DHI (nécessite docker login dhi.io)
docker buildx build clamav/ -f clamav/clamav.yaml \
  --sbom=generator=dhi.io/scout-sbom-indexer:1 \
  --provenance=1 \
  --tag test-clamav:local --load

# Via docker compose + override DHI
docker compose -f docker-compose.yml -f docker-compose.dhi.yml \
  --profile explicit up --build -d
```

## Build local sans DHI (Dockerfiles classiques)

```bash
# Pas besoin de docker login dhi.io
make build
make up-explicit
```
