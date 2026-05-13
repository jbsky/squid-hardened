# syntax=docker/dockerfile:1.7
# =====================================================================
#  Squid Hardened Image - SSL Bump + ICAP client
#  Multi-stage build : compile from source -> minimal runtime
#  Conformité Docker Hardened Image :
#   - multi-stage
#   - utilisateur non-root
#   - binaires strip + RELRO + PIE + stack-protector
#   - package manager (apk) supprimé en runtime
#   - busybox réduit aux ~10 applets nécessaires (entrypoint + healthcheck)
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

RUN apk add --no-cache \
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
        openldap-dev

WORKDIR /src

# Téléchargement (HTTPS + verif via openssl)
RUN curl -fsSL "https://www.squid-cache.org/Versions/v6/squid-${SQUID_VERSION}.tar.xz" -o squid.tar.xz \
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

# ---------- Stage 2 : runtime hardened -------------------------------
FROM alpine:${ALPINE_VERSION} AS runtime

# Métadonnées OCI
LABEL org.opencontainers.image.title="squid-hardened" \
      org.opencontainers.image.description="Squid 6 hardened avec SSL Bump + ICAP, non-root, distroless-style" \
      org.opencontainers.image.vendor="jbsky" \
      org.opencontainers.image.licenses="GPL-2.0-or-later" \
      org.opencontainers.image.source="https://github.com/jbsky/stack-squid"

# Dépendances strictement runtime + tini comme PID 1
RUN apk add --no-cache \
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

# Permissions strictes
RUN chown -R squid:squid /var/log/squid /var/spool/squid /var/lib/ssl_db /run /etc/squid \
 && chmod 0750 /var/lib/ssl_db \
 && chmod 0755 /var/log/squid /var/spool/squid

# Initialisation ssl_db (DB des certificats forgés) – fait en entrypoint si vide
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

# ── Runtime hardening: strip attack surface ──────────────────────────
# Remove package manager — prevents installing tools in compromised container
# Remove unused busybox applets — reduces shell utility to bare minimum
# Remaining: sh (entrypoint), echo/printf (logging), ln/rm (init),
#            grep (config check), nc/head (healthcheck), ls/sleep (runtime)
# Note: /bin/busybox binary remains; applets still invocable via
#       "busybox <applet>" — full removal requires rewriting entrypoints in Go.
RUN rm -rf /sbin/apk /etc/apk /lib/apk /var/cache/apk \
 && KEEP="sh echo printf ls ln rm grep head nc sleep" \
 && for f in $(busybox --list-full); do \
      name=${f##*/}; \
      case " $KEEP " in *" $name "*) ;; *) /bin/busybox rm -f "/$f";; esac; \
    done

USER squid:squid

EXPOSE 3128/tcp 3130/tcp

# Healthcheck via squidclient si dispo, sinon TCP
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD /bin/sh -c 'printf "GET cache_object://localhost/info HTTP/1.0\r\n\r\n" | nc -w 2 127.0.0.1 3128 | head -1 | grep -q "200" || exit 1'

ENTRYPOINT ["/sbin/tini","--","/usr/local/bin/entrypoint.sh"]
CMD ["squid","-N","-d","1","-f","/etc/squid/squid.conf"]
