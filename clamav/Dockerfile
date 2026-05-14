# =====================================================================
#  ClamAV Hardened Image – FROM scratch
#  3-stage build : apk install + prep -> Go init -> FROM scratch
#  No compilation needed (apk packages), so builder IS the prep stage.
# =====================================================================

ARG ALPINE_VERSION=3.20

# ---------- Stage 1 : builder (install + prepare filesystem) ---------
FROM alpine:${ALPINE_VERSION} AS builder

# -- APK installs split to stay within proxy 240s timeout --

# 1/2  Switch to HTTP repos + core packages
RUN sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add --no-cache \
        ca-certificates \
        tini-static \
        tzdata \
        libstdc++

# 2/2  ClamAV packages (pulls in significant deps)
RUN apk add --no-cache \
        clamav \
        clamav-libunrar \
 && deluser clamav 2>/dev/null; delgroup clamav 2>/dev/null; \
    addgroup -S -g 4000 clamav \
 && adduser  -S -D -H -G clamav -u 4000 -s /sbin/nologin clamav

# Config files (set ownership + permissions while we have a shell)
COPY clamd.conf      /etc/clamav/clamd.conf
COPY freshclam.conf  /etc/clamav/freshclam.conf
RUN chown root:clamav /etc/clamav/clamd.conf /etc/clamav/freshclam.conf \
 && chmod 0640        /etc/clamav/clamd.conf /etc/clamav/freshclam.conf

# Strip APK/package-manager artifacts (won't be copied to scratch)
RUN rm -rf /lib/apk /var/cache/apk /etc/apk /sbin/apk

# ---------- Stage 2 : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.22-alpine AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 3 : FROM scratch (final hardened image) -----------
FROM scratch

LABEL org.opencontainers.image.title="clamav-hardened" \
      org.opencontainers.image.description="ClamAV FROM scratch, non-root, zero shell" \
      org.opencontainers.image.licenses="GPL-2.0-or-later"

# User accounts
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group  /etc/group

# ClamAV binaries
COPY --from=builder /usr/sbin/clamd    /usr/sbin/clamd
COPY --from=builder /usr/bin/freshclam /usr/bin/freshclam
COPY --from=builder /usr/bin/clamdscan /usr/bin/clamdscan

# Dynamic linker (musl) + shared libraries
COPY --from=builder /usr/lib/ /usr/lib/
COPY --from=builder /lib/     /lib/

# Config (with correct ownership from builder stage)
COPY --from=builder /etc/clamav/ /etc/clamav/

# TLS trust store + timezone data
COPY --from=builder /etc/ssl/ /etc/ssl/
COPY --from=builder /usr/share/zoneinfo/ /usr/share/zoneinfo/

# PID 1 — tini-static
COPY --from=builder /sbin/tini-static /sbin/tini

# Go init binary (static, entrypoint + healthcheck + setup-dirs)
COPY --from=gobuilder /init /usr/local/bin/init

# Create runtime directories with correct ownership (no shell needed)
RUN ["/usr/local/bin/init", "--setup-dirs"]

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER 4000:4000

EXPOSE 3310/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/init"]
CMD ["clamd", "--foreground=true", "--config-file=/etc/clamav/clamd.conf"]
