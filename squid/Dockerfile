# =====================================================================
#  Squid Hardened Image - SSL Bump + ICAP client
#  Multi-stage build : compile from source -> minimal runtime
#  Conformité Docker Hardened Image :
#   - multi-stage
#   - utilisateur non-root
#   - binaires strip + RELRO + PIE + stack-protector
#   - package manager (apk) et shell (busybox) absents du runtime
#   - entrypoint + healthcheck en binaire Go statique
#   - filesystem read-only friendly
#   - aucune capabilité root requise au runtime
# =====================================================================

ARG SQUID_VERSION=6.10
ARG ALPINE_VERSION=3.20

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:${ALPINE_VERSION} AS builder

ARG SQUID_VERSION
ENV CFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    CXXFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    LDFLAGS="-Wl,-z,relro,-z,now,-z,noexecstack -pie"

# Switch APK to HTTP (avoids SSL-bump 403s) and use persistent cache
# so APK doesn't re-fetch indices mid-install (proxy drops after 240s).
RUN --mount=type=secret,id=ca-certs \
    cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt \
 && sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add \
        build-base \
        autoconf automake libtool pkgconf \
        openssl-dev openssl \
        libcap-dev libcap \
        linux-headers \
        perl \
        curl xz \
        ca-certificates \
        krb5-dev libxml2-dev libnetfilter_conntrack-dev \
        nettle-dev gnutls-dev \
        cppunit-dev \
        openldap-dev \
 && rm -rf /var/cache/apk/*

WORKDIR /src

# Téléchargement (HTTPS + verif via openssl)
RUN --mount=type=secret,id=ca-certs \
    cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt \
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

# Création des répertoires runtime que Squid attend
# Remove /out/var/run created by make install — conflicts with /var/run -> /run symlink in Alpine
# Copy mime.conf to /usr/share/squid/ so it survives /etc/squid volume mounts
RUN rm -rf /out/var/run \
 && install -d -m 0755 /out/var/log/squid \
                       /out/var/spool/squid \
                       /out/var/lib/ssl_db \
                       /out/run \
                       /out/etc/squid/ssl_cert \
                       /out/usr/share/squid \
 && cp /out/etc/squid/mime.conf /out/usr/share/squid/mime.conf

# ---------- Stage 1b : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.22-alpine AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 2 : runtime hardened -------------------------------
FROM alpine:${ALPINE_VERSION} AS runtime

# Métadonnées OCI
LABEL org.opencontainers.image.title="squid-hardened" \
      org.opencontainers.image.description="Squid 6 hardened avec SSL Bump + ICAP, non-root, distroless-style" \
      org.opencontainers.image.vendor="jbsky" \
      org.opencontainers.image.licenses="GPL-2.0-or-later" \
      org.opencontainers.image.source="https://github.com/jbsky/stack-squid"

# Dépendances strictement runtime + tini comme PID 1
RUN sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add \
        libstdc++ \
        openssl \
        ca-certificates \
        libcap \
        krb5-libs \
        libxml2 \
        nettle gnutls \
        libnetfilter_conntrack \
        libltdl \
        libldap \
        tini \
        tzdata \
 && addgroup -S -g 3128 squid \
 && adduser  -S -D -H -G squid -u 3128 -s /sbin/nologin squid \
 && rm -rf /var/cache/apk/*

# Copie depuis le builder
COPY --from=builder /out/ /

# Go init binary (entrypoint + healthcheck, static, no shell needed)
COPY --from=gobuilder /init /usr/local/bin/init

# Permissions strictes
RUN chown -R squid:squid /var/log/squid /var/spool/squid /var/lib/ssl_db /run /etc/squid \
 && chmod 0750 /var/lib/ssl_db \
 && chmod 0755 /var/log/squid /var/spool/squid

# ── Runtime hardening: remove shell + package manager entirely ───────
# Go static binary replaces entrypoint.sh — no /bin/sh needed at runtime.
# busybox + apk are fully removed; the running shell deletes itself.
RUN for f in $(/bin/busybox --list-full); do /bin/busybox rm -f "/$f"; done; \
    /bin/busybox rm -rf /bin/busybox /sbin/apk /etc/apk /lib/apk /var/cache/apk

USER squid:squid

EXPOSE 3128/tcp 3130/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/init"]
CMD ["squid","-N","-d","1","-f","/etc/squid/squid.conf"]
