# =====================================================================
#  C-ICAP + squidclamav Hardened Image
#  4-stage build : compile c-icap + squidclamav -> Go init -> prep -> FROM scratch
# =====================================================================

# ALPINE_VERSION kept for check-versions.sh/versions.json reference only --
# the FROM lines below pin tag+digest together as a literal so a version
# bump requires deliberately re-resolving the digest, not a silent drift
# if this ARG changes without the pin being updated to match.
ARG ALPINE_VERSION=3.21
ARG CICAP_VERSION=0.6.5
ARG CICAP_SHA256=82e457b3f234d56f537c70ec76a1d51ce8f6a55522592f2df07fc2557856f184
ARG SQUIDCLAMAV_VERSION=7.5
ARG SQUIDCLAMAV_SHA256=4d076b692e45cb78b384b5c4a1c63f755d565bdc01c732cc5ddbb8b4f046ceae

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d AS builder

ARG CICAP_VERSION
ARG CICAP_SHA256
ARG SQUIDCLAMAV_VERSION
ARG SQUIDCLAMAV_SHA256

ENV CFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    CXXFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    LDFLAGS="-Wl,-z,relro,-z,now,-z,noexecstack -pie"

# -- APK installs split to stay within proxy 240s timeout --

# 1/2  Inject CA + switch to HTTP repos + core build toolchain + download tools
RUN --mount=type=secret,id=ca-certs,required=false \
    [ -f /run/secrets/ca-certs ] && cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt || true \
 && sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add \
        build-base autoconf automake libtool pkgconf \
        curl xz \
        ca-certificates \
 && rm -rf /var/cache/apk/*

# 2/2  Development libraries
RUN apk add \
        clamav-dev \
        curl-dev \
        zlib-dev bzip2-dev \
        pcre-dev pcre2-dev \
        openssl-dev \
 && rm -rf /var/cache/apk/*

WORKDIR /src

# Téléchargement c-icap depuis SourceForge (miroir HTTPS)
RUN --mount=type=secret,id=ca-certs,required=false \
    [ -f /run/secrets/ca-certs ] && cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt || true \
 && curl -fsSL "https://sourceforge.net/projects/c-icap/files/c-icap/0.6.x/c_icap-${CICAP_VERSION}.tar.gz/download" -o c-icap.tar.gz \
 && printf '%s  c-icap.tar.gz\n' "${CICAP_SHA256}" > c-icap.tar.gz.sha256 \
 && sha256sum -c c-icap.tar.gz.sha256 \
 && rm c-icap.tar.gz.sha256 \
 && tar -xzf c-icap.tar.gz \
 && mv "c_icap-${CICAP_VERSION}" c-icap

WORKDIR /src/c-icap
RUN ./configure \
        --prefix=/usr \
        --sysconfdir=/etc/c-icap \
        --localstatedir=/var \
        --enable-large-files \
        --with-openssl \
 && make -j"$(nproc)" \
 && make install DESTDIR=/out

# squidclamav
WORKDIR /src
RUN --mount=type=secret,id=ca-certs,required=false \
    [ -f /run/secrets/ca-certs ] && cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt || true \
 && curl -fsSL "https://github.com/darold/squidclamav/archive/refs/tags/v${SQUIDCLAMAV_VERSION}.tar.gz" -o sc.tar.gz \
 && printf '%s  sc.tar.gz\n' "${SQUIDCLAMAV_SHA256}" > sc.tar.gz.sha256 \
 && sha256sum -c sc.tar.gz.sha256 \
 && rm sc.tar.gz.sha256 \
 && tar -xzf sc.tar.gz \
 && mv "squidclamav-${SQUIDCLAMAV_VERSION}" squidclamav

WORKDIR /src/squidclamav
# squidclamav doit voir c-icap installé pour son configure
RUN make -C /src/c-icap install \
 && ./configure --prefix=/usr --sysconfdir=/etc/c-icap --with-c-icap=/usr \
 && make -j"$(nproc)" \
 && make install DESTDIR=/out

# Strip
RUN find /out -type f \( -name '*.so*' -o -perm -111 \) -exec sh -c 'file "$1" 2>/dev/null | grep -q "ELF" && strip "$1" 2>/dev/null || true' _ {} \;

# Remove /out/var/run created by make install — conflicts with /var/run -> /run symlink
RUN rm -rf /out/var/run

# ---------- Stage 2 : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.26-alpine@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 3 : prep (assemble runtime filesystem) ------------
FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d AS prep

# -- Runtime APK installs split to stay within proxy 240s timeout --

# 1/2  Core runtime libs
RUN sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add --no-cache \
        openssl \
        ca-certificates \
        tini-static \
        tzdata

# 2/2  ClamAV + compression + regex libs + libatomic (required by c-icap 0.6.x)
RUN apk add --no-cache \
        clamav-libs \
        libcurl \
        zlib bzip2 \
        pcre pcre2 \
        libatomic \
 && addgroup -S -g 4100 cicap \
 && adduser  -S -D -H -G cicap -u 4100 -s /sbin/nologin cicap

# c-icap + squidclamav binaries from builder
COPY --from=builder /out/ /

# Config files (set ownership + permissions while we have a shell)
COPY c-icap.conf       /etc/c-icap/c-icap.conf
COPY squidclamav.conf  /etc/c-icap/squidclamav.conf
RUN chown root:cicap /etc/c-icap/*.conf \
 && chmod 0640       /etc/c-icap/*.conf

# Strip APK/package-manager artifacts
RUN rm -rf /lib/apk /lib/libapk* /var/cache/apk /etc/apk /sbin/apk

# ---------- Stage 4 : FROM scratch (final hardened image) -----------
FROM scratch

LABEL org.opencontainers.image.title="c-icap-hardened" \
      org.opencontainers.image.description="c-icap + squidclamav FROM scratch, non-root, zero shell" \
      org.opencontainers.image.vendor="jbsky" \
      org.opencontainers.image.licenses="LGPL-2.1-or-later" \
      org.opencontainers.image.source="https://github.com/jbsky/squid-hardened" \
      security.hardening.tier="platine" \
      security.hardening.features="from-scratch,go-init,tini-pid1,zero-shell,non-root,compiler-hardening,cosign-signed,sbom,slsa-provenance"

# User accounts
COPY --link --from=prep /etc/passwd /etc/passwd
COPY --link --from=prep /etc/group  /etc/group

# Dynamic linker (musl) + shared libraries
COPY --link --from=prep /lib/ /lib/
COPY --link --from=prep /usr/lib/ /usr/lib/

# c-icap binary + modules
COPY --link --from=prep /usr/bin/c-icap /usr/bin/c-icap
COPY --link --from=prep /usr/share/c_icap/ /usr/share/c_icap/

# Config (with correct ownership from prep stage)
COPY --link --from=prep /etc/c-icap/ /etc/c-icap/

# TLS trust store + timezone data
COPY --link --from=prep /etc/ssl/ /etc/ssl/
COPY --link --from=prep /usr/share/zoneinfo/ /usr/share/zoneinfo/

# PID 1 — tini-static
COPY --link --from=prep /sbin/tini-static /sbin/tini

# Go init binary (static, entrypoint + healthcheck + setup-dirs)
COPY --link --from=gobuilder /init /usr/local/bin/init

# Create runtime directories with correct ownership (no shell needed)
RUN ["/usr/local/bin/init", "--setup-dirs"]

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER 4100:4100

EXPOSE 1344/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/init"]
CMD ["c-icap", "-N", "-D", "-f", "/etc/c-icap/c-icap.conf"]
