#!/usr/bin/env bash
# =====================================================================
#  Génère la CA d'entreprise pour SSL Bump
#  Produit :
#    certs/bump.pem        → clé privée + cert (utilisé par Squid)
#    certs/bump.crt        → cert public (à distribuer sur les clients)
#    certs/dhparam.pem     → paramètres DH
#  ⚠️  La clé privée doit rester strictement confidentielle.
# =====================================================================
set -euo pipefail

# certs/ is gitignored (private key material), so it doesn't exist on a
# fresh checkout -- create it before resolving its absolute path with cd.
mkdir -p "$(dirname "$0")/../certs"
CERT_DIR="$(cd "$(dirname "$0")/../certs" && pwd)"
CN="${SSL_BUMP_CA_CN:-Internal Squid Bump CA}"
ORG="${SSL_BUMP_CA_ORG:-MonOrganisation}"
DAYS="${SSL_BUMP_CA_DAYS:-3650}"

# 0755, not 0700: this directory gets bind-mounted read-only into the squid
# container, which reads bump.pem/dhparam.pem as an arbitrary non-root UID
# (3128) that can't be predicted here -- 0700 blocks even directory traversal
# for that UID (confirmed: "Failed to open dhparam.pem: Permission denied"
# alongside the same for bump.pem), regardless of the individual files' own
# modes. The private key gets its real protection from bump.key being 0600
# below, not from directory-level lockout.
chmod 0755 "${CERT_DIR}"

if [[ -f "${CERT_DIR}/bump.pem" ]]; then
    echo "[generate-ca] ${CERT_DIR}/bump.pem existe déjà – refusé pour éviter écrasement."
    echo "[generate-ca] Supprime-le manuellement si tu veux régénérer."
    exit 1
fi

echo "[generate-ca] Génération de la clé privée + certificat (RSA 4096, ${DAYS} jours)"
openssl req -new -x509 -nodes -newkey rsa:4096 \
    -keyout "${CERT_DIR}/bump.key" \
    -out    "${CERT_DIR}/bump.crt" \
    -days   "${DAYS}" \
    -subj   "/C=FR/O=${ORG}/CN=${CN}" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign,digitalSignature" \
    -addext "subjectKeyIdentifier=hash"

# Squid attend clé + cert dans un seul fichier. bump.pem est bind-mounté
# en lecture seule dans le container squid, qui tourne sous un UID non-root
# arbitraire (3128) forcément différent de l'UID qui exécute ce script --
# 0600 le rendrait illisible par squid (confirmé : "FATAL: No valid signing
# certificate", squid crash-loop immédiat). La protection réelle est le
# repertoire certs/ en 0700 (seul l'UID qui l'a généré peut y accéder sur
# l'hôte) ; une fois bind-monté, seul le mode du fichier compte.
cat "${CERT_DIR}/bump.key" "${CERT_DIR}/bump.crt" > "${CERT_DIR}/bump.pem"
chmod 0600 "${CERT_DIR}/bump.key"
chmod 0644 "${CERT_DIR}/bump.pem"
chmod 0644 "${CERT_DIR}/bump.crt"

echo "[generate-ca] Génération des paramètres DH 2048 (peut prendre quelques minutes)"
openssl dhparam -out "${CERT_DIR}/dhparam.pem" 2048
chmod 0644 "${CERT_DIR}/dhparam.pem"

echo ""
echo "[generate-ca] ✅ CA générée :"
echo "  - Clé+cert pour Squid : ${CERT_DIR}/bump.pem    (NE PAS DISTRIBUER)"
echo "  - Cert public clients  : ${CERT_DIR}/bump.crt   (à pousser via GPO/MDM)"
echo "  - DH params            : ${CERT_DIR}/dhparam.pem"
echo ""
echo "Empreintes :"
openssl x509 -in "${CERT_DIR}/bump.crt" -noout -fingerprint -sha256
openssl x509 -in "${CERT_DIR}/bump.crt" -noout -subject -issuer -dates
