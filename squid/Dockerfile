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

# Aucune version ici : versions.json (racine du depot) est la seule source.
# La CI passe les build-args par jbsky/hardened-ci/versions, `make build` par
# scripts/versions-build-args.py --docker ; le job lint refuse toute valeur
# par defaut. Pas d'ARG ALPINE_VERSION : les FROM epinglent tag ET digest, et
# --check compare ce tag a .alpine.
ARG SQUID_VERSION

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS builder

ARG SQUID_VERSION
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
RUN test -n "${SQUID_VERSION}" \
    || { echo "build-args requis depuis versions.json : make build, ou docker build \$(scripts/versions-build-args.py --docker) -f <image>/Dockerfile <image>/" >&2; exit 1; }
ENV CFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    CXXFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -fPIE -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    LDFLAGS="-Wl,-z,relro,-z,now,-z,noexecstack -pie"

# -- APK installs are split across multiple RUN commands so that each
#    completes well within the proxy's 240-second connection timeout.
#    Each RUN opens a fresh TCP connection to the proxy.

# 1/3  Inject CA + switch to HTTP repos + core build toolchain
RUN --mount=type=secret,id=ca-certs,required=false \
    if [ -f /run/secrets/ca-certs ]; then cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt; fi \
 && sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add --no-cache \
        build-base \
        autoconf automake libtool pkgconf \
        linux-headers \
        perl

# 2/3  Crypto / security libraries
RUN apk add --no-cache \
        openssl-dev openssl \
        libcap-dev libcap \
        krb5-dev \
        nettle-dev gnutls-dev

# 3/3  XML, network, test, LDAP libs + download tools
# hadolint ignore=DL3059
RUN apk add --no-cache \
        libxml2-dev \
        libnetfilter_conntrack-dev \
        file \
        cppunit-dev \
        openldap-dev \
        curl xz \
        ca-certificates \
        gnupg

WORKDIR /src

# Download from GitHub releases (squid-cache.org no longer hosts v7+ tarballs)
# Tag format: SQUID_7_5 (dots replaced by underscores)
# GPG verification: use pinned signing key from repo (squid-cache.org TLS unreliable)
COPY squid-cache.pgp.asc /tmp/squid-cache.pgp.asc
RUN --mount=type=secret,id=ca-certs,required=false \
    if [ -f /run/secrets/ca-certs ]; then cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt; fi \
 && SQUID_TAG="SQUID_$(echo "${SQUID_VERSION}" | tr '.' '_')" \
 && curl -fsSL "https://github.com/squid-cache/squid/releases/download/${SQUID_TAG}/squid-${SQUID_VERSION}.tar.xz" -o squid.tar.xz \
 && curl -fsSL "https://github.com/squid-cache/squid/releases/download/${SQUID_TAG}/squid-${SQUID_VERSION}.tar.xz.asc" -o squid.tar.xz.asc \
 && GNUPGHOME="$(mktemp -d)" && export GNUPGHOME \
 && gpg --batch --import /tmp/squid-cache.pgp.asc \
 && gpg --batch --verify squid.tar.xz.asc squid.tar.xz \
 && gpgconf --kill gpg-agent \
 && rm -rf "$GNUPGHOME" squid.tar.xz.asc /tmp/squid-cache.pgp.asc \
 && tar -xJf squid.tar.xz \
 && mv "squid-${SQUID_VERSION}" squid

WORKDIR /src/squid

# Configuration avec SSL bump + ICAP (ICAP indispensable pour C-ICAP/ClamAV)
# Squid 7 removed: --enable-async-io, --enable-cachemgr-hostname, ESI support
# DiskIO modules auto-detected; storeio modules explicitly listed
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
        --enable-storeio="ufs,aufs,diskd,rock" \
        --enable-removal-policies="heap,lru" \
        --enable-htcp \
        --enable-cache-digests \
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
 && make install DESTDIR=/out

# Strip every ELF under /out (binaries + helpers). The old code named only
# /out/usr/sbin/squid : any new binary from `make install` slipped through.
#
# The candidates cannot go straight to `strip`: /out/usr/lib/squid holds perl
# and shell helpers (basic_pop3_auth, url_fake_rewrite.sh, helper-mux...) that
# are executable but not ELF, and strip exits 1 on the first one -- that is what
# broke the build of 31/08. They are filtered through `file` first, exactly as
# c-icap and clamav already do. `test -s` is the guard: if `file` ever vanishes
# the list comes out empty and the build fails instead of shipping unstripped
# binaries. xargs, not `find -exec`, because busybox find ignores the exit
# status of what it runs and would swallow a real strip failure.
RUN find /out -type f \( -name '*.a' -o -name '*.la' \) -delete \
 && find /out -type f \( -name '*.so*' -o -perm -111 \) > /tmp/cand.list \
 && : > /tmp/elf.list \
 && while IFS= read -r f; do \
      case "$(file -b "$f")" in *ELF*) echo "$f" >> /tmp/elf.list ;; esac; \
    done < /tmp/cand.list \
 && test -s /tmp/elf.list \
 && xargs -r strip --strip-unneeded < /tmp/elf.list \
 && rm -f /tmp/cand.list /tmp/elf.list

# Remove /out/var/run (conflicts with /run symlink) + copy mime.conf
RUN rm -rf /out/var/run \
 && cp /out/etc/squid/mime.conf /out/usr/share/squid/mime.conf

# ---------- Stage 2 : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.27-alpine@sha256:738d1cf061836894ff6bb8c33881080ac66de8cf0586615012a0c8f592649cfa AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 3 : prep (assemble runtime filesystem) ------------
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS prep

# -- Runtime APK installs split to stay within proxy 240s timeout --

# 1/2  Core runtime libs
RUN sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk upgrade --no-cache \
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

# Collect exactly the shared objects that ship. Copying /lib and /usr/lib whole
# defeats the cleanup done above: it carried libapk.so and, on the images that
# do not delete it, the apk database itself. lddtree lists each binary, its
# transitive dependencies, symlinks with their targets, and the loader for the
# architecture being built, so arm64 needs no special case.
#
# lddtree prints each binary it is handed, so this list holds the roots as well
# as their dependencies -- and every one of those roots is copied again, on its
# own COPY line, in the final stage. Layers are not deduplicated, so the squid binary was
# going out twice: 7,8 Mo of this image. The roots keep their individual COPY
# and are filtered out of the tar input here; what this archive carries is the
# dependencies and the loader.
#
# Only /usr/sbin/squid is filtered. The helpers under /usr/lib/squid are
# closure roots too, but the final stage never copies them separately -- they
# reach the image through this archive alone, and filtering them would delete
# security_file_certgen, which SSL-bump needs.
#
# The completeness check runs on the UNFILTERED list, above: a filter must
# never be able to hide a missing dependency.
RUN --mount=type=cache,target=/var/cache/apk \
    apk add --no-cache lddtree \
 && mkdir -p /rootfs \
 && test -n "$(find /usr/lib/squid -type f -perm -111 -print -quit)" \
 && { lddtree -l /usr/sbin/squid; \
      find /usr/lib/squid -type f -perm -111 -exec lddtree -l {} +; } \
      > /tmp/closure.list 2> /tmp/closure.err \
 && if grep -q 'Not found' /tmp/closure.list /tmp/closure.err; then \
      echo "closure incomplete -- a dependency is missing from this stage:" >&2; \
      grep 'Not found' /tmp/closure.list /tmp/closure.err >&2; \
      exit 1; \
    fi \
 && sort -u /tmp/closure.list -o /tmp/closure.list \
 && grep -v -E '^/usr/sbin/squid$' /tmp/closure.list > /tmp/closure.deps \
 && tar -cf /tmp/closure.tar -T /tmp/closure.deps \
 && tar -xf /tmp/closure.tar -C /rootfs \
 && rm -f /tmp/closure.list /tmp/closure.deps /tmp/closure.err /tmp/closure.tar

# Every helper in /usr/lib/squid is a closure root. Keeping only the ones the
# configuration names is not safe: several are compiled-in defaults that no
# directive mentions -- unlinkd is spawned by the UFS store and squid dies with
# "FATAL: unlinkd_program ... No such file or directory" without it, and
# log_file_daemon and diskd are in the same category. Trimming this set is
# possible but has to be done against squid's built-in defaults, not against
# what the config file happens to reference.
#
# OpenSSL providers are dlopen'd so no closure sees them; the 1.x engines
# (engines-3/) are deprecated and unused.
RUN mkdir -p /rootfs/usr/lib \
 && cp -a /usr/lib/ossl-modules /rootfs/usr/lib/

# Strip APK/package-manager artifacts (not needed in final image)
# prep garde sa base apk : c'est elle que lisent le scan CVE et la SBOM
# attachee a l'image publiee. Avant le 2026-10-03 ce rm etait dans prep, et
# Trivy y voyait « alpine, 0 vulnerabilite » (OpenSSL 3.5.7 passe inapercu).
# L'image finale tire ses fichiers de prep-clean, sans aucun artefact apk.
FROM prep AS prep-clean
RUN rm -rf /lib/apk /lib/libapk* /var/cache/apk /etc/apk /sbin/apk

# ---------- Stage 4 : FROM scratch (final hardened image) -----------
FROM scratch

LABEL org.opencontainers.image.title="squid-hardened" \
      org.opencontainers.image.description="Squid 7 FROM scratch — SSL Bump + ICAP, non-root, zero shell" \
      org.opencontainers.image.vendor="jbsky" \
      org.opencontainers.image.licenses="GPL-2.0-or-later" \
      org.opencontainers.image.source="https://github.com/jbsky/squid-hardened" \
      security.hardening.tier="platine" \
      security.hardening.features="from-scratch,go-init,tini-pid1,zero-shell,non-root,compiler-hardening,cosign-signed,sbom,slsa-provenance"

# User accounts (for USER directive and getpwuid lookups via musl)
COPY --link --from=prep-clean /etc/passwd /etc/passwd
COPY --link --from=prep-clean /etc/group  /etc/group

# Dynamic linker (musl) + shared libraries
COPY --link --from=prep-clean /rootfs/ /

# Squid binary + data files (error pages, icons, mime.conf)
COPY --link --from=prep-clean /usr/sbin/squid /usr/sbin/squid
COPY --link --from=prep-clean /usr/share/squid/ /usr/share/squid/

# Default config (overridden by volume mount at runtime)
COPY --link --from=prep-clean /etc/squid/ /etc/squid/

# TLS trust store + timezone data
COPY --link --from=prep-clean /etc/ssl/ /etc/ssl/
COPY --link --from=prep-clean /usr/share/zoneinfo/ /usr/share/zoneinfo/

# PID 1 — tini-static (no musl dependency for PID 1 reliability)
COPY --link --from=prep-clean /sbin/tini-static /sbin/tini

# Go init binary (static, entrypoint + healthcheck + setup-dirs)
COPY --link --from=gobuilder /init /usr/local/bin/init

# Create runtime directories with correct ownership (no shell needed)
RUN ["/usr/local/bin/init", "--setup-dirs"]

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER 3128:3128

EXPOSE 3128/tcp 3130/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/init"]
# Pas de "-d <level>" : les configs livrees ecrivent cache_log sur
# stdio:/dev/stderr, et "-d" duplique en plus chaque message sur stderr
# (verifie : chaque ligne apparait deux fois). Sans "-d", c'est le module
# de log qui possede stderr -- une seule copie, et un debug_options releve
# pour un diagnostic sort aussi sur la console (il serait tronque au
# niveau de "-d" sinon).
CMD ["squid", "-N", "-f", "/etc/squid/squid.conf"]
