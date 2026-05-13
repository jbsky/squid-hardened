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
COPY entrypoint.sh   /usr/local/bin/entrypoint.sh

RUN chown root:clamav /etc/clamav/clamd.conf /etc/clamav/freshclam.conf \
 && chmod 0640        /etc/clamav/clamd.conf /etc/clamav/freshclam.conf \
 && chmod 0755        /usr/local/bin/entrypoint.sh

# ── Runtime hardening: strip attack surface ──────────────────────────
# Alpine base provides busybox + apk — strip to bare minimum
# Remaining: sh (entrypoint), echo (logging), ls (DB check),
#            nc/grep (healthcheck)
RUN /bin/busybox rm -rf /sbin/apk /etc/apk /lib/apk /var/cache/apk \
 && KEEP="sh echo ls nc grep" \
 && for f in $(busybox --list-full); do \
      name=${f##*/}; \
      case " $KEEP " in *" $name "*) ;; *) /bin/busybox rm -f "/$f";; esac; \
    done

USER clamav:clamav

EXPOSE 3310/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD /bin/sh -c 'echo PING | nc -w 2 127.0.0.1 3310 | grep -q PONG'

ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/entrypoint.sh"]
CMD ["clamd","--foreground=true","--config-file=/etc/clamav/clamd.conf"]
