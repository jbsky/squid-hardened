#!/bin/sh
# =====================================================================
#  Squid entrypoint – hardened
#  - Initialise la DB des certificats forgés (security_file_certgen)
#  - Crée les répertoires de cache si nécessaire
#  - Démarre Squid en avant-plan (PID 1 = tini)
# =====================================================================
set -eu

SSL_DB="/var/lib/ssl_db/db"
CACHE_DIR="/var/spool/squid"
CONF="${SQUID_CONF:-/etc/squid/squid.conf}"

# 0) Ensure mime.conf exists (hidden when /etc/squid is volume-mounted)
if [ ! -f /etc/squid/mime.conf ] && [ -f /usr/share/squid/mime.conf ]; then
    echo "[entrypoint] mime.conf absent, lien depuis /usr/share/squid/"
    ln -sf /usr/share/squid/mime.conf /etc/squid/mime.conf
fi

# 1) Init SSL Bump — seulement si la conf l'utilise
# Note: security_file_certgen -c CREATES the target dir (must not exist).
# /var/lib/ssl_db is a volume mount point (already exists), so we use
# a subdirectory /var/lib/ssl_db/db that certgen can create from scratch.
if grep -q "sslcrtd_program\|ssl_bump" "${CONF}" 2>/dev/null; then
    # Init DB certificats forgés
    if [ ! -f "${SSL_DB}/index.txt" ]; then
        echo "[entrypoint] Initialisation de la DB SSL forgée dans ${SSL_DB}"
        rm -rf "${SSL_DB}"
        /usr/lib/squid/security_file_certgen -c -s "${SSL_DB}" -M 20MB
    fi
    # Warn si CA absente
    if [ ! -f /etc/squid/ssl_cert/bump.pem ]; then
        echo "[entrypoint][WARN] SSL Bump activé mais /etc/squid/ssl_cert/bump.pem absent."
        echo "[entrypoint][WARN] Génère ta CA avec scripts/generate-ca.sh puis monte-la en read-only."
    fi
fi

# 2) Init du cache si vide (mode rock/aufs)
if [ ! -d "${CACHE_DIR}/00" ] && grep -qE '^cache_dir' "${CONF}"; then
    echo "[entrypoint] Initialisation du cache Squid"
    squid -N -z -f "${CONF}" || true
fi

# 4) Vérification syntaxique de la conf avant lancement
echo "[entrypoint] Parse-check de la configuration..."
squid -k parse -f "${CONF}"

# 5) Lancement
echo "[entrypoint] Démarrage de Squid"
exec "$@"
