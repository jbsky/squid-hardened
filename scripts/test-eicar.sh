#!/usr/bin/env bash
# =====================================================================
#  Test de bon fonctionnement de la chaîne Squid → C-ICAP → ClamAV
#  via téléchargement du fichier EICAR (faux virus officiel)
# =====================================================================
set -euo pipefail

PROXY="${1:-127.0.0.1:3128}"
EICAR_URL="https://secure.eicar.org/eicar.com.txt"

echo "[test] Test EICAR via proxy ${PROXY}"
echo "[test] Téléchargement de ${EICAR_URL}..."

http_code=$(curl -s -o /tmp/eicar.out -w "%{http_code}" -x "http://${PROXY}" -k "${EICAR_URL}" || true)
echo "[test] Code HTTP : ${http_code}"
echo "[test] Aperçu réponse :"
head -c 200 /tmp/eicar.out; echo

if grep -q -E "(virus|VIRUS|infected|CLAMAV)" /tmp/eicar.out 2>/dev/null; then
    echo "[test] ✅ Antivirus a bloqué EICAR – chaîne ICAP fonctionnelle"
    exit 0
fi
if grep -qi "EICAR-STANDARD-ANTIVIRUS-TEST-FILE" /tmp/eicar.out; then
    echo "[test] ❌ EICAR a traversé le proxy – ICAP/ClamAV NE FONCTIONNE PAS"
    exit 2
fi
echo "[test] ⚠️ Résultat ambigu – vérifier les logs squid + c-icap"
exit 1
