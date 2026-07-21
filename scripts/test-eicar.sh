#!/usr/bin/env bash
# =====================================================================
#  Test de bon fonctionnement de la chaine Squid -> C-ICAP -> ClamAV
#  via telechargement du fichier EICAR (faux virus officiel)
#
#  Usage:
#    ./test-eicar.sh                  # teste via docker network (defaut)
#    ./test-eicar.sh 192.168.x.x:3128  # teste via proxy externe (prod)
# =====================================================================
set -euo pipefail

PROXY="${1:-}"
# The compose project name (and therefore the network name's prefix)
# defaults to the checkout directory's basename, which varies: "stack-squid"
# locally, "squid-hardened" on a fresh GitHub Actions checkout (matches the
# repo name). The suffix is always "_proxy_net" regardless of project name,
# so find it dynamically instead of hardcoding one directory name.
NETWORK="$(docker network ls --format '{{.Name}}' | grep '_proxy_net$' | head -1)"
if [[ -z "$NETWORK" ]]; then
    echo "[test] ERREUR: aucun réseau *_proxy_net trouvé (stack pas démarrée ?)" >&2
    exit 1
fi
# EICAR test string, base64-encoded to avoid shell/printf interpretation issues
EICAR_B64="WDVPIVAlQEFQWzRcUFpYNTQoUF4pN0NDKTd9JEVJQ0FSLVNUQU5EQVJELUFOVElWSVJVUy1URVNULUZJTEUhJEgrSCo="

# Docker daemon injects http_proxy into all containers; must clear for local tests.
NOPROXY=(-e http_proxy= -e https_proxy= -e HTTP_PROXY= -e HTTPS_PROXY= -e no_proxy= -e NO_PROXY=)

# -----------------------------------------------------------------------
# Mode 1: test interne via docker network (pas besoin d'internet)
# -----------------------------------------------------------------------
if [[ -z "$PROXY" ]]; then
    echo "[test] Mode docker-network (explicit profile)"
    echo "[test] Demarrage d'un serveur EICAR temporaire..."

    # Start EICAR HTTP server on the proxy network
    docker run --rm -d --name eicar-test-server --network "$NETWORK" \
        "${NOPROXY[@]}" \
        python:3.12-alpine sh -c \
        "echo '${EICAR_B64}' | base64 -d > /tmp/eicar.txt && python3 -m http.server 8080 --directory /tmp" \
        >/dev/null 2>&1
    trap 'docker rm -f eicar-test-server >/dev/null 2>&1 || true' EXIT

    # Get the container IP (avoids Docker DNS timing issues with squid)
    EICAR_IP=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' eicar-test-server)
    echo "[test] Serveur EICAR sur ${EICAR_IP}:8080"

    # Wait for server ready (direct TCP check via IP)
    echo "[test] Attente du serveur EICAR..."
    for _i in $(seq 1 20); do
        if docker run --rm --network "$NETWORK" "${NOPROXY[@]}" \
            curlimages/curl:latest \
            -sf -o /dev/null "http://${EICAR_IP}:8080/eicar.txt" 2>/dev/null; then
            break
        fi
        sleep 1
    done

    echo "[test] Telechargement via squid-explicit -> c-icap -> clamav..."
    # Get squid IP to avoid any DNS issues with the test itself
    SQUID_IP=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' squid-explicit)

    result=$(docker run --rm --network "$NETWORK" "${NOPROXY[@]}" \
        curlimages/curl:latest \
        -s -D- -o /dev/null -x "http://${SQUID_IP}:3128" \
        "http://${EICAR_IP}:8080/eicar.txt" 2>&1)

    echo "[test] Reponse:"
    echo "$result" | head -15

    if echo "$result" | grep -qi "Eicar-Test-Signature FOUND\|virus.*FOUND\|X-Virus-ID:\|X-Infection-Found:"; then
        echo ""
        echo "[test] PASS - Antivirus a bloque EICAR (chaine ICAP fonctionnelle)"
        exit 0
    fi
    if echo "$result" | grep -qi "EICAR-STANDARD-ANTIVIRUS-TEST-FILE"; then
        echo ""
        echo "[test] FAIL - EICAR a traverse le proxy (ICAP/ClamAV NE FONCTIONNE PAS)"
        exit 2
    fi
    echo ""
    echo "[test] AMBIGUOUS - verifier les logs squid + c-icap"
    exit 1
fi

# -----------------------------------------------------------------------
# Mode 2: test via proxy externe (production / VyOS)
# -----------------------------------------------------------------------
echo "[test] Test EICAR via proxy ${PROXY}"
EICAR_URL="http://www.eicar.org/download/eicar.com.txt"
echo "[test] Telechargement de ${EICAR_URL}..."

http_code=$(curl -s -o /tmp/eicar.out -w "%{http_code}" \
    --noproxy '*' --connect-timeout 10 --max-time 30 \
    -x "http://${PROXY}" "${EICAR_URL}" || true)
echo "[test] Code HTTP : ${http_code}"
echo "[test] Apercu reponse :"
head -c 200 /tmp/eicar.out 2>/dev/null; echo

if grep -q -i -E "(virus|infected|clamav|eicar.*found|x-infection-found)" /tmp/eicar.out 2>/dev/null; then
    echo "[test] PASS - Antivirus a bloque EICAR (chaine ICAP fonctionnelle)"
    exit 0
fi
if [[ "$http_code" == "307" || "$http_code" == "403" ]]; then
    echo "[test] PASS - Proxy a bloque la requete (HTTP ${http_code})"
    exit 0
fi
if grep -qi "EICAR-STANDARD-ANTIVIRUS-TEST-FILE" /tmp/eicar.out 2>/dev/null; then
    echo "[test] FAIL - EICAR a traverse le proxy (ICAP/ClamAV NE FONCTIONNE PAS)"
    exit 2
fi
echo "[test] AMBIGUOUS - verifier les logs squid + c-icap"
exit 1
