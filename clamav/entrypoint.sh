#!/bin/sh
# =====================================================================
#  ClamAV entrypoint
#  - 1er run : freshclam pour télécharger les signatures
#  - Lance freshclam en daemon
#  - Lance clamd en avant-plan (PID 1 = tini)
# =====================================================================
set -eu

DB_DIR="/var/lib/clamav"

# 1er téléchargement si aucune base de signatures (*.cvd ou *.cld)
# Note : freshclam.dat est un fichier d'état, pas une DB — ne pas le compter
if ! ls "${DB_DIR}"/*.cvd "${DB_DIR}"/*.cld >/dev/null 2>&1; then
    echo "[entrypoint] Téléchargement initial des signatures (peut prendre plusieurs minutes)..."
    freshclam --config-file=/etc/clamav/freshclam.conf --foreground=true || {
        echo "[entrypoint][ERROR] freshclam initial échoué. Vérifie l'accès Internet ou le proxy."
        exit 1
    }
fi

# freshclam en arrière-plan pour les MAJ
echo "[entrypoint] Démarrage de freshclam en daemon"
freshclam --config-file=/etc/clamav/freshclam.conf --daemon --checks=24 &

# clamd au premier plan
echo "[entrypoint] Démarrage de clamd"
exec "$@"
