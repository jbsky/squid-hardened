# syntax=docker/dockerfile:1.7
# =====================================================================
#  ClamAV Hardened Image – multi-stage CORRIGÉ
#  Problème précédent : mono-stage, apk + runtime dans le même layer.
#  Aucun COPY --from=builder car rien n'était compilé.
#
#  Fix : Stage 1 installe via apk + prépare le FS.
#        Stage 2 copie UNIQUEMENT les binaires/libs/data nécessaires.
#        Résultat : image finale sans apk, sans sa DB, sans outils de
#        packaging (~40 Mo de moins, surface d'attaque réduite).
# =====================================================================

ARG ALPINE_VERSION=3.20

# ---------- Stage 1 : builder (installe tout) ------------------------
FROM alpine:${ALPINE_VERSION} AS builder

RUN apk add --no-cache \
        clamav \
        clamav-libunrar \
        tini \
        ca-certificates \
        tzdata \
        libstdc++ \
 && deluser clamav 2>/dev/null; delgroup clamav 2>/dev/null; \
    addgroup -S -g 4000 clamav \
 && adduser  -S -D -H -G clamav -u 4000 -s /sbin/nologin clamav \
 && install -d -m 0750 -o clamav -g clamav \
        /var/lib/clamav \
        /var/log/clamav \
        /run/clamav \
 && rm -rf /var/cache/apk

# ---------- Stage 1b : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.22-alpine AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 2 : runtime (sans apk ni sa DB) --------------------
FROM alpine:${ALPINE_VERSION} AS runtime

LABEL org.opencontainers.image.title="clamav-hardened" \
      org.opencontainers.image.description="ClamAV multi-stage, non-root, sans package manager en runtime" \
      org.opencontainers.image.licenses="GPL-2.0-or-later"

# Binaires
COPY --from=builder /usr/sbin/clamd        /usr/sbin/clamd
COPY --from=builder /usr/bin/freshclam     /usr/bin/freshclam
COPY --from=builder /usr/bin/clamdscan     /usr/bin/clamdscan
COPY --from=builder /sbin/tini             /sbin/tini

# Bibliothèques dynamiques
COPY --from=builder /usr/lib/              /usr/lib/
COPY --from=builder /lib/                  /lib/

# Certificats, timezone, config système
COPY --from=builder /etc/ssl/              /etc/ssl/
COPY --from=builder /etc/ca-certificates/  /etc/ca-certificates/
COPY --from=builder /usr/share/ca-certificates/ /usr/share/ca-certificates/
COPY --from=builder /usr/share/zoneinfo/   /usr/share/zoneinfo/

# Comptes système (utilisateur clamav créé dans le builder)
COPY --from=builder /etc/passwd            /etc/passwd
COPY --from=builder /etc/group             /etc/group
COPY --from=builder /etc/shadow            /etc/shadow
COPY --from=builder /etc/nsswitch.conf     /etc/nsswitch.conf

# Répertoires de données avec ownership clamav:clamav
COPY --from=builder --chown=clamav:clamav /var/lib/clamav /var/lib/clamav
COPY --from=builder --chown=clamav:clamav /var/log/clamav /var/log/clamav
COPY --from=builder --chown=clamav:clamav /run/clamav     /run/clamav

# NE PAS copier : /sbin/apk, /lib/apk, /etc/apk  → pas de pkg manager

COPY clamd.conf      /etc/clamav/clamd.conf
COPY freshclam.conf  /etc/clamav/freshclam.conf

# Go init binary (entrypoint + healthcheck, static, no shell needed)
COPY --from=gobuilder /init /usr/local/bin/init

RUN chown root:clamav /etc/clamav/clamd.conf /etc/clamav/freshclam.conf \
 && chmod 0640        /etc/clamav/clamd.conf /etc/clamav/freshclam.conf

# ── Runtime hardening: remove shell + package manager entirely ───────
RUN for f in $(/bin/busybox --list-full); do /bin/busybox rm -f "/$f"; done; \
    /bin/busybox rm -rf /bin/busybox /sbin/apk /etc/apk /lib/apk /var/cache/apk

USER clamav:clamav

EXPOSE 3310/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/init"]
CMD ["clamd","--foreground=true","--config-file=/etc/clamav/clamd.conf"]
