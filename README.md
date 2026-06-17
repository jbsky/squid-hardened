# Stack Squid + C-ICAP + ClamAV hardenee pour VyOS

[![Build](https://github.com/jbsky/squid-hardened/actions/workflows/build-push.yml/badge.svg)](https://github.com/jbsky/squid-hardened/actions/workflows/build-push.yml)
[![Docker Hub squid](https://img.shields.io/docker/v/jbsky/squid-hardened?sort=semver&label=squid)](https://hub.docker.com/r/jbsky/squid-hardened)
[![Docker Hub c-icap](https://img.shields.io/docker/v/jbsky/c-icap-hardened?sort=semver&label=c-icap)](https://hub.docker.com/r/jbsky/c-icap-hardened)
[![Docker Hub clamav](https://img.shields.io/docker/v/jbsky/clamav-hardened?sort=semver&label=clamav)](https://hub.docker.com/r/jbsky/clamav-hardened)
[![Hardening](https://img.shields.io/badge/hardening-platine-blueviolet)](https://github.com/jbsky/squid-hardened#security--verification)

Stack proxy filtrant antiviral, conteneurisee en **Docker Hardened Image** (FROM scratch, Go static init, tini PID 1, non-root, RELRO/PIE/SSP/FORTIFY, zero shell), prete a remplacer un proxy Squid existant.

Deux modes opérationnels :

| Mode | Réseau | Port | Caractéristiques |
|---|---|---|---|
| **Transparent** | LAN 1 | 3128 (intercept) | DNAT depuis VyOS, HTTP scanné, HTTPS pass-through |
| **Explicite + SSL Bump** | LAN 2 | 3128 / 3129 (bump) | WPAD via DHCP option 252, HTTPS déchiffré et scanné |

## Arborescence

```
stack-squid/
├── docker-compose.yml            # orchestrateur avec profils
├── docker-compose.dhi.yml        # variante registry privee
├── Makefile                      # raccourcis dev
├── versions.json                 # versions trackees
├── squid/                        # image Squid
│   ├── Dockerfile                # Multi-stage (Alpine → FROM scratch)
│   ├── go.mod + init.go          # Go static init binary
│   ├── squid-explicit.conf       # config mode explicite + SSL Bump
│   ├── squid-transparent.conf    # config mode intercept
│   ├── squid-vyos.conf           # config VyOS production
│   ├── squid.yaml                # Squid YAML config format
│   ├── squid-cache.pgp.asc      # cle GPG embarquee
│   ├── conf.d/nobump_domains.acl # domaines sans SSL Bump
│   └── nobump_domains.acl        # ACL legacy
├── clamav/                       # image ClamAV
│   ├── Dockerfile
│   ├── go.mod + init.go          # Go static init binary
│   ├── clamd.conf
│   ├── freshclam.conf
│   └── clamav.yaml
├── c-icap/                       # image C-ICAP + squidclamav
│   ├── Dockerfile
│   ├── go.mod + init.go          # Go static init binary
│   ├── c-icap.conf
│   ├── c-icap.yaml
│   └── squidclamav.conf
├── vyos/                         # configs VyOS
│   ├── vyos-transparent.config   # DNAT simple
│   ├── vyos-explicit.config      # WPAD + SSL Bump
│   ├── vyos-pbr-intercept.config # PBR + in-container REDIRECT
│   ├── squid-redirect.service    # systemd unit (nft REDIRECT injection)
│   └── wpad-example.dat
├── scripts/
│   ├── generate-ca.sh            # cree la CA SSL Bump
│   ├── check-versions.sh         # detection versions upstream
│   ├── deploy.sh                 # build / up / down / scan / sbom
│   └── test-eicar.sh             # validation chaine ICAP
├── docs/                         # documentation detaillee
│   ├── vyos-transparent-proxy.md # guide interception PBR (EN)
│   ├── vyos-transparent-proxy.fr.md # idem (FR)
│   ├── architecture.md
│   ├── hardening.md
│   ├── vyos-howto.md
│   ├── ca-deployment.md
│   └── ci-secrets.md
└── .github/workflows/
    ├── build-push.yml            # Build + sign + scan + release
    ├── version-check.yml         # Detection versions upstream
    └── security-audit.yml        # Weekly Trivy + Grype + cosign
```

## Pré-requis

- Hôte Docker (séparé de VyOS recommandé) : Linux + Docker ≥ 24, Compose v2
- VyOS 1.4 (Sagitta) ou 1.5 (Circinus) avec firewall nftables
- `openssl`, `curl`, `bash` sur l'hôte Docker
- Outils audit recommandés : `trivy`, `syft`, `dive`
- Accès Internet pour pull Alpine + sources Squid/c-icap + signatures ClamAV

## Mise en route rapide

```bash
cd squid-stack
cp .env.example .env

# 1) Génération de la CA SSL Bump (mode explicite uniquement)
./scripts/generate-ca.sh

# 2) Build des images hardenées
./scripts/deploy.sh build

# 3) Démarrage selon le mode
./scripts/deploy.sh up explicit     # HTTPS + SSL Bump
# ou
./scripts/deploy.sh up transparent  # HTTP transparent
# ou
./scripts/deploy.sh up all          # les deux en parallèle (ports différents)

# 4) Vérification
./scripts/test-eicar.sh 127.0.0.1:3128

# 5) Audit sécurité
./scripts/deploy.sh scan
./scripts/deploy.sh sbom
```

## Application des configs VyOS

Les fichiers `vyos/*.config` sont en syntaxe `set` directement collable en mode `configure` :

```vyos
configure
load /config/scripts/squid-stack/vyos-explicit.config
# vérifier les variables (IP/interfaces) avant commit !
commit
save
exit
```

Adapter impérativement les IP et interfaces (`eth0/1/2`, sous-réseaux) à votre topologie réelle.

## Bascule depuis le Squid existant

1. Déployer la nouvelle stack sur un **autre IP** que l'ancien (192.168.99.10 par défaut)
2. Tester depuis un client de test (curl + EICAR)
3. Mettre à jour la règle DNAT VyOS (mode transparent) ou le WPAD (mode explicite) pour basculer
4. Conserver l'ancien Squid en standby pendant 24-48h
5. Décommissionner après validation des logs

## SSL Bump – Points critiques

- **La CA générée doit être installée sur tous les clients** du LAN explicite (GPO Windows, profil iOS/Android MDM, store système Linux, NSS pour Firefox)
- Sites bancaires/santé : ajoutés à `nobump_domains.acl` (splice = pass-through)
- Applications avec **certificate pinning** (Chrome, banques mobiles, MAJ système) doivent être whitelistées
- **Conformité légale** : informer les utilisateurs (charte informatique, RGPD, code du travail)

## Sécurité Docker Hardened Image – ce qui est appliqué

| Mesure | Squid | C-ICAP | ClamAV |
|---|---|---|---|
| Multi-stage build | ✅ | ✅ | n/a (alpine direct) |
| Non-root user | uid 3128 | uid 4100 | uid 4000 |
| `cap_drop: ALL` | ✅ | ✅ | ✅ |
| `no-new-privileges` | ✅ | ✅ | ✅ |
| Strip + RELRO + PIE | ✅ | ✅ | (alpine pkg) |
| Tini PID 1 | ✅ | ✅ | ✅ |
| Read-only friendly | ✅ (sauf cache) | ✅ | ✅ |
| `tmpfs` /tmp,/run | ✅ | ✅ | ✅ |
| Healthcheck | ✅ | ✅ | ✅ |
| OCI labels | ✅ | ✅ | ✅ |
| SBOM (via syft) | ✅ | ✅ | ✅ |

## Logs

Tous les logs sont en `/var/log/{squid,c-icap,clamav}` dans les conteneurs, persistés via des volumes nommés.

Format Squid étendu (mode explicite) inclut :
- SNI TLS (`%ssl::>sni`)
- Mode bump utilisé (`%ssl::bump_mode`)

Pour SIEM/ELK : monter un sidecar `filebeat` ou exporter en stdout (commentaires dans la conf).

## Test EICAR

Le fichier de test antivirus officiel EICAR (`https://secure.eicar.org/eicar.com.txt`) doit être bloqué.

Voir `scripts/test-eicar.sh`.

## Dépannage

| Symptôme | Cause probable | Action |
|---|---|---|
| `SECURITY ALERT: Host header forgery` | Conntrack manquant en intercept | `modprobe nf_conntrack`, vérifier que VyOS conntracke bien le DNAT |
| Erreur SSL côté client | CA non installée | Pousser `certs/bump.crt` sur le client |
| `clamd timeout` | DB pas téléchargée | `docker logs clamav` – le 1er freshclam prend 5-15 min |
| Sites cassent en bump | Certificate pinning | Ajouter à `nobump_domains.acl` + reload Squid |
| EICAR passe quand même | ICAP désactivé / clamd KO | Vérifier `icap_service_failure_limit` et logs c-icap |

## Documentation détaillée

Voir `docs/` pour :
- `vyos-transparent-proxy.md` – interception HTTPS transparente avec PBR + conteneurs Podman ([EN](docs/vyos-transparent-proxy.md) / [FR](docs/vyos-transparent-proxy.fr.md))
- `architecture.md` – schémas et flux
- `hardening.md` – détail des mesures de durcissement
- `vyos-howto.md` – guide pas-à-pas VyOS (mode DNAT simple)
- `ca-deployment.md` – pousser la CA sur Windows/macOS/iOS/Android/Linux

## Security & Verification

All three images are signed with [cosign](https://github.com/sigstore/cosign) using keyless OIDC (Sigstore).

### Verify image signatures

```bash
# From ghcr.io (signatures stored natively)
for img in squid-hardened c-icap-hardened clamav-hardened; do
  cosign verify \
    --certificate-identity-regexp '^https://github.com/jbsky/stack-squid/' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    ghcr.io/jbsky/$img:latest
done

# From Docker Hub (signatures stored in ghcr.io)
for img in squid-hardened c-icap-hardened clamav-hardened; do
  COSIGN_REPOSITORY=ghcr.io/jbsky/$img \
    cosign verify \
    --certificate-identity-regexp '^https://github.com/jbsky/stack-squid/' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    docker.io/jbsky/$img:latest
done
```

### Hardening tier "Platine" guarantees

| Property | Description |
|----------|-------------|
| FROM scratch | No base image, no shell, no package manager |
| Go static init | Binary entrypoint + healthcheck (no script) |
| tini PID 1 | Proper signal forwarding and zombie reaping |
| Non-root | Runs as unprivileged UID |
| Compiler hardening | RELRO, PIE, SSP, FORTIFY_SOURCE, stack-clash, NX |
| Cosign signed | OIDC keyless signature via Sigstore transparency log |
| SBOM | Software Bill of Materials embedded in manifest |
| SLSA provenance | Build provenance attestation (level 2) |
