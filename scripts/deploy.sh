#!/usr/bin/env bash
# =====================================================================
#  Script de déploiement / cycle de vie
#  Usage:
#    ./deploy.sh build         # build des images hardenées
#    ./deploy.sh up transparent
#    ./deploy.sh up explicit
#    ./deploy.sh up all        # les deux profils
#    ./deploy.sh down
#    ./deploy.sh logs
#    ./deploy.sh scan          # scan trivy/grype des images
# =====================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT}"

cmd="${1:-help}"
profile="${2:-}"

case "${cmd}" in
  build)
    echo "[deploy] Build des images Docker hardenées..."
    DOCKER_BUILDKIT=1 docker compose build --pull
    ;;

  up)
    [[ -z "${profile}" ]] && { echo "Profil requis: transparent|explicit|all"; exit 1; }
    if [[ "${profile}" == "explicit" || "${profile}" == "all" ]]; then
        if [[ ! -f "${ROOT}/certs/bump.pem" ]]; then
            echo "[deploy] Aucune CA détectée → génération automatique"
            "${ROOT}/scripts/generate-ca.sh"
        fi
    fi
    echo "[deploy] Démarrage profil: ${profile}"
    docker compose --profile "${profile}" up -d
    docker compose ps
    ;;

  down)
    docker compose --profile all down
    ;;

  logs)
    docker compose --profile all logs -f --tail=200
    ;;

  scan)
    echo "[deploy] Scan vulnérabilités (nécessite trivy installé)"
    for img in localhost/squid-hardened:latest \
               localhost/c-icap-hardened:latest \
               localhost/clamav-hardened:latest; do
      echo "→ ${img}"
      trivy image --severity HIGH,CRITICAL --no-progress "${img}" || true
    done
    ;;

  sbom)
    echo "[deploy] Génération SBOM (syft requis)"
    mkdir -p sbom/
    for img in squid-hardened c-icap-hardened clamav-hardened; do
        syft "localhost/${img}:latest" -o spdx-json > "sbom/${img}.spdx.json"
        echo "→ sbom/${img}.spdx.json"
    done
    ;;

  help|*)
    sed -n '3,15p' "$0"
    ;;
esac
