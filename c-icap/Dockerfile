# =====================================================================
#  C-ICAP + squidclamav Hardened Image
#  Multi-stage : compile c-icap + module squidclamav -> minimal runtime
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

RUN --mount=type=secret,id=ca-certs,target=/etc/ssl/certs/ca-certificates.crt \
    apk add --no-cache \
        build-base autoconf automake libtool pkgconf \
        clamav-dev \
        curl-dev \
        zlib-dev bzip2-dev \
        pcre-dev pcre2-dev \
        openssl-dev \
        curl xz \
        ca-certificates

WORKDIR /src

# Téléchargement c-icap depuis SourceForge (miroir HTTPS)
RUN --mount=type=secret,id=ca-certs,target=/etc/ssl/certs/ca-certificates.crt \
    curl -fsSL "https://sourceforge.net/projects/c-icap/files/c-icap/0.5.x/c_icap-${CICAP_VERSION}.tar.gz/download" -o c-icap.tar.gz \
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
RUN --mount=type=secret,id=ca-certs,target=/etc/ssl/certs/ca-certificates.crt \
    curl -fsSL "https://github.com/darold/squidclamav/archive/refs/tags/v${SQUIDCLAMAV_VERSION}.tar.gz" -o sc.tar.gz \
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

# Remove /out/var/run created by make install — conflicts with /var/run -> /run symlink in Alpine
RUN rm -rf /out/var/run \
 && install -d -m 0755 /out/var/log/c-icap /out/run/c-icap

# ---------- Stage 1b : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.22-alpine AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 2 : runtime ----------------------------------------
FROM alpine:${ALPINE_VERSION} AS runtime

LABEL org.opencontainers.image.title="c-icap-hardened" \
      org.opencontainers.image.description="c-icap server + squidclamav, non-root, hardened" \
      org.opencontainers.image.licenses="LGPL-2.1-or-later"

RUN --mount=type=secret,id=ca-certs,target=/etc/ssl/certs/ca-certificates.crt \
    apk add --no-cache \
        clamav-libs \
        libcurl \
        zlib bzip2 \
        pcre pcre2 \
        openssl \
        ca-certificates \
        tini \
        tzdata \
 && addgroup -S -g 4100 cicap \
 && adduser  -S -D -H -G cicap -u 4100 -s /sbin/nologin cicap \
 && rm -rf /var/cache/apk/*

COPY --from=builder /out/ /

COPY c-icap.conf       /etc/c-icap/c-icap.conf
COPY squidclamav.conf  /etc/c-icap/squidclamav.conf

# Go init binary (entrypoint + healthcheck, static, no shell needed)
COPY --from=gobuilder /init /usr/local/bin/init

RUN chown -R cicap:cicap /var/log/c-icap /run/c-icap \
 && chown root:cicap     /etc/c-icap/*.conf \
 && chmod 0640           /etc/c-icap/*.conf

# ── Runtime hardening: remove shell + package manager entirely ───────
RUN for f in $(/bin/busybox --list-full); do /bin/busybox rm -f "/$f"; done; \
    /bin/busybox rm -rf /bin/busybox /sbin/apk /etc/apk /lib/apk /var/cache/apk /usr/bin/bz*

USER cicap:cicap

EXPOSE 1344/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/init"]
CMD ["c-icap","-N","-D","-f","/etc/c-icap/c-icap.conf"]
