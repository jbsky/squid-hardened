# =====================================================================
#  C-ICAP + squidclamav Hardened Image
#  4-stage build : compile c-icap + squidclamav -> Go init -> prep -> FROM scratch
# =====================================================================

ARG ALPINE_VERSION=3.20
ARG CICAP_VERSION=0.5.13
ARG SQUIDCLAMAV_VERSION=7.3

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:${ALPINE_VERSION} AS builder

ARG CICAP_VERSION
ARG SQUIDCLAMAV_VERSION

ENV CFLAGS="-O2 -fstack-protector-strong -fPIE -D_FORTIFY_SOURCE=2" \
    LDFLAGS="-Wl,-z,relro,-z,now,-z,noexecstack -pie"

# -- APK installs split to stay within proxy 240s timeout --

# 1/2  Inject CA + switch to HTTP repos + core build toolchain + download tools
RUN --mount=type=secret,id=ca-certs \
    cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt \
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
RUN --mount=type=secret,id=ca-certs \
    cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt \
 && curl -fsSL "https://sourceforge.net/projects/c-icap/files/c-icap/0.5.x/c_icap-${CICAP_VERSION}.tar.gz/download" -o c-icap.tar.gz \
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
RUN --mount=type=secret,id=ca-certs \
    cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt \
 && curl -fsSL "https://github.com/darold/squidclamav/archive/refs/tags/v${SQUIDCLAMAV_VERSION}.tar.gz" -o sc.tar.gz \
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
FROM golang:1.26-alpine AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 3 : prep (assemble runtime filesystem) ------------
FROM alpine:${ALPINE_VERSION} AS prep

# -- Runtime APK installs split to stay within proxy 240s timeout --

# 1/2  Core runtime libs
RUN sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add --no-cache \
        openssl \
        ca-certificates \
        tini-static \
        tzdata

# 2/2  ClamAV + compression + regex libs
RUN apk add --no-cache \
        clamav-libs \
        libcurl \
        zlib bzip2 \
        pcre pcre2 \
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
RUN rm -rf /lib/apk /var/cache/apk /etc/apk /sbin/apk

# ---------- Stage 4 : FROM scratch (final hardened image) -----------
FROM scratch

LABEL org.opencontainers.image.title="c-icap-hardened" \
      org.opencontainers.image.description="c-icap + squidclamav FROM scratch, non-root, zero shell" \
      org.opencontainers.image.licenses="LGPL-2.1-or-later"

# User accounts
COPY --from=prep /etc/passwd /etc/passwd
COPY --from=prep /etc/group  /etc/group

# Dynamic linker (musl) + shared libraries
COPY --from=prep /lib/ /lib/
COPY --from=prep /usr/lib/ /usr/lib/

# c-icap binary + modules
COPY --from=prep /usr/bin/c-icap /usr/bin/c-icap
COPY --from=prep /usr/share/c_icap/ /usr/share/c_icap/

# Config (with correct ownership from prep stage)
COPY --from=prep /etc/c-icap/ /etc/c-icap/

# TLS trust store + timezone data
COPY --from=prep /etc/ssl/ /etc/ssl/
COPY --from=prep /usr/share/zoneinfo/ /usr/share/zoneinfo/

# PID 1 — tini-static
COPY --from=prep /sbin/tini-static /sbin/tini

# Go init binary (static, entrypoint + healthcheck + setup-dirs)
COPY --from=gobuilder /init /usr/local/bin/init

# Create runtime directories with correct ownership (no shell needed)
RUN ["/usr/local/bin/init", "--setup-dirs"]

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER 4100:4100

EXPOSE 1344/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/init"]
CMD ["c-icap", "-N", "-D", "-f", "/etc/c-icap/c-icap.conf"]
