#!/bin/sh
# =====================================================================
#  c-icap entrypoint – vérifie l'accès à clamd avant de démarrer
# =====================================================================
set -eu

CLAMD_HOST="${CLAMD_HOST:-clamav}"
CLAMD_PORT="${CLAMD_PORT:-3310}"

echo "[entrypoint] Attente de clamd (${CLAMD_HOST}:${CLAMD_PORT})..."
i=0
while ! nc -z "${CLAMD_HOST}" "${CLAMD_PORT}" 2>/dev/null; do
    i=$((i+1))
    if [ $i -gt 120 ]; then
        echo "[entrypoint][ERROR] clamd injoignable après 120s"
        exit 1
    fi
    sleep 1
done
echo "[entrypoint] clamd OK, démarrage c-icap"

exec "$@"
