# Docker Hardened Image – Détail du durcissement

Ce document détaille les choix de sécurité appliqués aux images de cette stack, alignés sur les bonnes pratiques **CIS Docker Benchmark**, **NIST SP 800-190** et le label informel "Docker Hardened Image" (multi-stage minimal, non-root, attaque-surface réduite).

## 1. Construction (build-time)

### 1.1 Multi-stage (3 stages)
Chaque image est construite en trois stages :
1. **builder** — chaîne de compilation complète, compilation from source
2. **gobuilder** — `golang:1.22-alpine`, compile l'entrypoint Go statique
3. **runtime** — Alpine minimal, copie uniquement les artefacts (`COPY --from=builder`, `COPY --from=gobuilder`)

Ni le builder ni le gobuilder ne sont publiés.

### 1.2 Flags de durcissement compilateur
Squid et c-icap sont compilés avec :
```
CFLAGS  : -O2 -fstack-protector-strong -fstack-clash-protection -fPIE
          -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security
LDFLAGS : -Wl,-z,relro,-z,now,-z,noexecstack -pie
```
Effet : RELRO complet, PIE/ASLR, NX, protection contre stack-clash, vérification des format strings.

Vérification :
```bash
docker run --rm --entrypoint /usr/sbin/squid localhost/squid-hardened:latest -v
# ou avec checksec.sh sur le binaire extrait
```

### 1.3 Strip
Tous les ELF du stage final sont passés à `strip` pour retirer les symboles de debug → réduction taille + complique reverse engineering.

### 1.4 Versions épinglées
Versions explicites via `ARG` : `SQUID_VERSION`, `CICAP_VERSION`, `ALPINE_VERSION`. Pas de `:latest`. Build reproductible (à condition de pinner aussi les paquets apk – TODO si exigé).

## 2. Surface d'attaque (runtime image)

### 2.1 Base minimale
Alpine 3.20 → ~7 Mo de base, glibc-free (musl).

### 2.2 Package manager supprimé
`apk` (binaire, base de données et configuration) est supprimé du stage runtime sur les 3 images (y compris ClamAV où il provient de la base Alpine). Un attaquant ne peut pas installer de paquets supplémentaires.

### 2.3 Shell-free (Tier 2 — zero shell, zero busybox)
Les 3 images sont **entièrement dépourvues de shell**. `/bin/sh`, `/bin/busybox` et tous les symlinks busybox sont supprimés du stage runtime. Un attaquant ne peut pas obtenir de shell interactif, même avec un RCE.

Technique : le dernier `RUN` du Dockerfile fait supprimer busybox par lui-même (le processus shell en mémoire termine avant que le binaire soit effacé du filesystem). Les entrypoints et healthchecks sont des **binaires Go statiques** (`CGO_ENABLED=0`) compilés dans un stage `gobuilder` séparé.

Vérification :
```bash
docker exec <container> /bin/sh -c "echo test"
# OCI runtime exec failed: stat /bin/sh: no such file or directory
```

### 2.4 Pas d'outils offensifs
Pas de `curl` (CLI), `wget`, `bash`, `python`, `gcc`, `make`, `apk` dans le runtime. Le module squidclamav lie `libcurl.so` dynamiquement mais le binaire `curl` n'est pas installé.

### 2.5 Healthchecks intégrés
Healthchecks via le binaire Go `/usr/local/bin/init --healthcheck` — protocoles natifs sans dépendance shell :

| Image | Healthcheck |
|-------|------------|
| squid | HTTP GET `cache_object://localhost/info` sur :3128 → attend `200` |
| c-icap | ICAP `OPTIONS icap://localhost/squidclamav` sur :1344 → attend `200` |
| clamav | TCP `PING\n` sur :3310 → attend `PONG` |

### 2.6 Labels OCI
`org.opencontainers.image.*` pour traçabilité et signature (cosign).

## 3. Identité

### 3.1 Utilisateur non-root
- Squid : `uid=3128 gid=3128`
- C-ICAP : `uid=4100 gid=4100`
- ClamAV : `uid=4000 gid=4000`

Vérifiable :
```bash
docker run --rm localhost/squid-hardened:latest id
# uid=3128(squid) gid=3128(squid)
```

### 3.2 Pas de shell login
Tous les comptes système ont `/sbin/nologin` comme shell.

## 4. Capabilities & permissions

### 4.1 `cap_drop: ALL`
Toutes les capabilities Linux sont droppées par défaut.

### 4.2 `cap_add` minimal
- Squid explicit : `NET_BIND_SERVICE` seulement (ports >1024, donc en réalité inutile)
- Squid transparent : `NET_BIND_SERVICE` + `NET_ADMIN` + `NET_RAW` (nécessaires pour `intercept` et `SO_ORIGINAL_DST`)
- C-ICAP, ClamAV : aucune

### 4.3 `no-new-privileges: true`
Empêche tout binaire setuid de fonctionner (défense en profondeur).

## 5. Système de fichiers

### 5.1 `tmpfs` pour /tmp et /run
Évite la persistance d'artefacts de scan + perf (mémoire).

### 5.2 Volumes nommés
Données persistantes (cache Squid, base ClamAV, logs) dans des volumes Docker dédiés.

### 5.3 Read-only friendly
Squid en mode `read_only: true` n'est pas possible à cause du cache rock-store. Compromis : tmpfs + volumes pour les seuls répertoires en écriture. Tout `/etc` est en lecture seule (`*.conf:ro`).

## 6. Réseau

### 6.1 Bridge dédié `br-proxy`
Subnet 172.28.0.0/24 isolé.

### 6.2 Pas d'`expose` vers l'extérieur sauf nécessaire
ClamAV et C-ICAP n'ont **aucun** port publié – joignables uniquement sur le bridge interne.

### 6.3 Healthchecks TCP raw
Pas d'écoute additionnelle nécessaire.

## 7. Init / signaux

### 7.1 Tini comme PID 1
Reaping correct des zombies, propagation des signaux SIGTERM/SIGINT, pas de bug "PID 1 trap".

### 7.2 Forme exec
`ENTRYPOINT ["/sbin/tini","--","..."]` (pas de shell wrapping inutile).

## 8. Logs

### 8.1 Driver json-file limité
50 Mo × 5 fichiers par conteneur → pas de remplissage disque.

### 8.2 stdout/stderr possible
Confs ont des lignes commentées pour bascule vers stdout (utile en k8s, Loki, Splunk).

## 9. Vérifications recommandées

### 9.1 Trivy (CVE)
```bash
trivy image --severity HIGH,CRITICAL localhost/squid-hardened:latest
```
Objectif : 0 HIGH/CRITICAL non-corrigées.

### 9.2 Grype
```bash
grype localhost/squid-hardened:latest
```

### 9.3 Dive (efficacité layers)
```bash
dive localhost/squid-hardened:latest
```
Objectif : score > 90%.

### 9.4 SBOM (syft)
```bash
syft localhost/squid-hardened:latest -o spdx-json > sbom.spdx.json
```

### 9.5 Signature (cosign – optionnel)
```bash
cosign generate-key-pair
cosign sign --key cosign.key registry.local/squid-hardened:latest
```

## 10. Entrypoints Go (Tier 2)

Les 3 entrypoints sont des binaires Go statiques (`/usr/local/bin/init`) compilés sans CGO dans un stage `gobuilder` (`golang:1.22-alpine`). Chaque binaire gère à la fois l'initialisation du service et le healthcheck Docker (flag `--healthcheck`).

### Architecture

```
tini (PID 1)
  └── /usr/local/bin/init [CMD...]
        ├── Initialisation (SSL DB, cache, freshclam, wait clamd...)
        └── syscall.Exec(CMD) → remplace le processus par le service
```

### Fonctions par service

| Service | Init | Healthcheck |
|---------|------|-------------|
| squid | mime.conf symlink, SSL DB init, cache init, parse-check, exec squid | HTTP 200 sur :3128 |
| c-icap | TCP wait clamd (120s timeout), exec c-icap | ICAP OPTIONS 200 sur :1344 |
| clamav | freshclam initial + daemon background, exec clamd | PING/PONG TCP :3310 |

Sources : `squid/init.go`, `c-icap/init.go`, `clamav/init.go` (stdlib uniquement, pas de dépendances externes).

## 11. Améliorations futures

- [x] ~~Entrypoints Go~~ → **fait** (Tier Platine, voir section 10)
- [x] ~~FROM scratch~~ → **fait** (zero shell, zero pkg manager)
- [x] ~~Signature cosign~~ → **fait** (OIDC keyless via GitHub Actions)
- [ ] Profil **seccomp** custom (le défaut est désactivé dans le compose pour debug)
- [ ] Profil **AppArmor** / SELinux dédié
- [ ] Pinner les paquets apk par hash dans les stages builder (`apk add foo=1.2.3-r0`)
- [ ] Build reproductible (`SOURCE_DATE_EPOCH`)
- [ ] Runtime immutabilité (`--read-only` rootfs + tmpfs)
- [ ] Déploiement par digest (pin `@sha256:` au lieu de `:latest`)
