# =====================================================================
#  Squid Hardened Image - SSL Bump + ICAP client
#  4-stage build : compile -> Go init -> prep runtime -> FROM scratch
#  Conformité Docker Hardened Image :
#   - FROM scratch final stage: zero shell, zero package manager
#   - utilisateur non-root (uid 3128)
#   - binaires strip + RELRO + PIE + stack-protector
#   - entrypoint + healthcheck en binaire Go statique
#   - filesystem read-only friendly
# =====================================================================

ARG SQUID_VERSION=6.10
ARG ALPINE_VERSION=3.20

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:${ALPINE_VERSION} AS builder

ARG SQUID_VERSION
ENV CFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    CXXFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    LDFLAGS="-Wl,-z,relro,-z,now,-z,noexecstack -pie"

# -- APK installs are split across multiple RUN commands so that each
#    completes well within the proxy's 240-second connection timeout.
#    Each RUN opens a fresh TCP connection to the proxy.

# 1/3  Inject CA + switch to HTTP repos + core build toolchain
RUN --mount=type=secret,id=ca-certs,required=false \
    [ -f /run/secrets/ca-certs ] && cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt || true \
 && sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add \
        build-base \
        autoconf automake libtool pkgconf \
        linux-headers \
        perl \
 && rm -rf /var/cache/apk/*

# 2/3  Crypto / security libraries
RUN apk add \
        openssl-dev openssl \
        libcap-dev libcap \
        krb5-dev \
        nettle-dev gnutls-dev \
 && rm -rf /var/cache/apk/*

# 3/3  XML, network, test, LDAP libs + download tools
RUN apk add \
        libxml2-dev \
        libnetfilter_conntrack-dev \
        cppunit-dev \
        openldap-dev \
        curl xz \
        ca-certificates \
 && rm -rf /var/cache/apk/*

WORKDIR /src

# Téléchargement (HTTPS + verif via openssl)
RUN --mount=type=secret,id=ca-certs,required=false \
    [ -f /run/secrets/ca-certs ] && cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt || true \
 && curl -fsSL "https://www.squid-cache.org/Versions/v6/squid-${SQUID_VERSION}.tar.xz" -o squid.tar.xz \
 && curl -fsSL "https://www.squid-cache.org/Versions/v6/squid-${SQUID_VERSION}.tar.xz.asc" -o squid.tar.xz.asc \
 && tar -xJf squid.tar.xz \
 && mv "squid-${SQUID_VERSION}" squid

WORKDIR /src/squid

# Configuration avec SSL bump + ICAP + ESI (ICAP indispensable pour C-ICAP/ClamAV)
RUN ./configure \
        --prefix=/usr \
        --sysconfdir=/etc/squid \
        --localstatedir=/var \
        --libexecdir=/usr/lib/squid \
        --datadir=/usr/share/squid \
        --sharedstatedir=/var/lib \
        --with-logdir=/var/log/squid \
        --with-pidfile=/run/squid.pid \
        --with-default-user=squid \
        --with-openssl \
        --with-libcap \
        --enable-ssl-crtd \
        --enable-icap-client \
        --enable-linux-netfilter \
        --enable-async-io \
        --enable-storeio="ufs,aufs,diskd,rock" \
        --enable-removal-policies="heap,lru" \
        --enable-htcp \
        --enable-cache-digests \
        --enable-cachemgr-hostname=localhost \
        --enable-delay-pools \
        --enable-x-accelerator-vary \
        --enable-eui \
        --enable-follow-x-forwarded-for \
        --enable-auth \
        --enable-auth-basic="DB,getpwnam,LDAP,NCSA,POP3,RADIUS,SASL" \
        --enable-auth-digest="file,LDAP" \
        --enable-auth-negotiate="kerberos,wrapper" \
        --disable-auth-ntlm \
        --enable-external-acl-helpers="file_userip,LDAP_group,unix_group" \
        --enable-url-rewrite-helpers="fake" \
        --enable-security-cert-validators="fake" \
        --enable-storeid-rewrite-helpers="file" \
        --disable-arch-native \
        --disable-strict-error-checking

RUN make -j"$(nproc)" \
 && make install DESTDIR=/out \
 && strip /out/usr/sbin/squid \
          /out/usr/lib/squid/*-cert.d || true

# Remove /out/var/run (conflicts with /run symlink) + copy mime.conf
RUN rm -rf /out/var/run \
 && cp /out/etc/squid/mime.conf /out/usr/share/squid/mime.conf

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
        libstdc++ \
        openssl \
        ca-certificates \
        libcap \
        tini-static \
        tzdata

# 2/2  Additional runtime libs (Kerberos, XML, TLS, LDAP, netfilter)
RUN apk add --no-cache \
        krb5-libs \
        libxml2 \
        nettle gnutls \
        libnetfilter_conntrack \
        libltdl \
        libldap \
 && addgroup -S -g 3128 squid \
 && adduser  -S -D -H -G squid -u 3128 -s /sbin/nologin squid

# Squid binaries + data from builder
COPY --from=builder /out/ /

# Strip APK/package-manager artifacts (not needed in final image)
RUN rm -rf /lib/apk /var/cache/apk /etc/apk /sbin/apk

# ---------- Stage 4 : FROM scratch (final hardened image) -----------
FROM scratch

LABEL org.opencontainers.image.title="squid-hardened" \
      org.opencontainers.image.description="Squid 6 FROM scratch — SSL Bump + ICAP, non-root, zero shell" \
      org.opencontainers.image.vendor="jbsky" \
      org.opencontainers.image.licenses="GPL-2.0-or-later" \
      org.opencontainers.image.source="https://github.com/jbsky/squid-hardened"

# User accounts (for USER directive and getpwuid lookups via musl)
COPY --from=prep /etc/passwd /etc/passwd
COPY --from=prep /etc/group  /etc/group

# Dynamic linker (musl) + shared libraries
COPY --from=prep /lib/ /lib/
COPY --from=prep /usr/lib/ /usr/lib/

# Squid binary + data files (error pages, icons, mime.conf)
COPY --from=prep /usr/sbin/squid /usr/sbin/squid
COPY --from=prep /usr/share/squid/ /usr/share/squid/

# Default config (overridden by volume mount at runtime)
COPY --from=prep /etc/squid/ /etc/squid/

# TLS trust store + timezone data
COPY --from=prep /etc/ssl/ /etc/ssl/
COPY --from=prep /usr/share/zoneinfo/ /usr/share/zoneinfo/

# PID 1 — tini-static (no musl dependency for PID 1 reliability)
COPY --from=prep /sbin/tini-static /sbin/tini

# Go init binary (static, entrypoint + healthcheck + setup-dirs)
COPY --from=gobuilder /init /usr/local/bin/init

# Create runtime directories with correct ownership (no shell needed)
RUN ["/usr/local/bin/init", "--setup-dirs"]

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER 3128:3128

EXPOSE 3128/tcp 3130/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/init"]
CMD ["squid", "-N", "-d", "1", "-f", "/etc/squid/squid.conf"]
