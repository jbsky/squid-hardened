# =====================================================================
#  C-ICAP + squidclamav Hardened Image
#  4-stage build : compile c-icap + squidclamav -> Go init -> prep -> FROM scratch
# =====================================================================

# ALPINE_VERSION kept for check-versions.sh/versions.json reference only --
# the FROM lines below pin tag+digest together as a literal so a version
# bump requires deliberately re-resolving the digest, not a silent drift
# if this ARG changes without the pin being updated to match.
ARG ALPINE_VERSION=3.24
ARG CICAP_VERSION=0.6.5
ARG CICAP_SHA256=82e457b3f234d56f537c70ec76a1d51ce8f6a55522592f2df07fc2557856f184
ARG SQUIDCLAMAV_VERSION=7.5
ARG SQUIDCLAMAV_SHA256=4d076b692e45cb78b384b5c4a1c63f755d565bdc01c732cc5ddbb8b4f046ceae

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS builder

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
 && apk add --no-cache \
        build-base autoconf automake libtool pkgconf \
        curl xz file \
        ca-certificates

# 2/2  Development libraries
RUN apk add --no-cache \
        clamav-dev \
        curl-dev \
        zlib-dev bzip2-dev \
        pcre-dev pcre2-dev \
        openssl-dev

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

# Strip, then remove /out/var/run created by make install — conflicts with
# /var/run -> /run symlink
# strip, without `|| true`: that suffix turned a strip that fails -- a truncated
# binary, a read-only file, a missing strip -- into a silent success, and the
# image shipped with its symbols. The candidates are filtered into a list, then
# stripped with xargs, so a failure ends the build instead of being swallowed by
# find, which ignores the exit status of -exec.
RUN find /out -type f \( -name '*.so*' -o -perm -111 \) > /tmp/cand.list \
 && : > /tmp/elf.list \
 && while IFS= read -r f; do \
      case "$(file -b "$f")" in *ELF*) echo "$f" >> /tmp/elf.list ;; esac; \
    done < /tmp/cand.list \
 && test -s /tmp/elf.list \
 && xargs -r strip --strip-unneeded < /tmp/elf.list \
 && rm -f /tmp/cand.list /tmp/elf.list \
 && rm -rf /out/var/run

# ---------- Stage 2 : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.27-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 3 : prep (assemble runtime filesystem) ------------
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS prep

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

# Collect exactly the shared objects that ship. Copying /lib and /usr/lib whole
# defeats the cleanup done above: it carried libapk.so and, on the images that
# do not delete it, the apk database itself. lddtree lists each binary, its
# transitive dependencies, symlinks with their targets, and the loader for the
# architecture being built, so arm64 needs no special case.
#
# lddtree prints each binary it is handed, so this list holds the roots as well
# as their dependencies -- and every one of those roots is copied again, on its
# own COPY line, in the final stage. Layers are not deduplicated, so the c-icap binary was
# going out twice: 0,21 Mo of this image. The roots keep their individual COPY
# and are filtered out of the tar input here; what this archive carries is the
# dependencies and the loader.
#
# Only /usr/bin/c-icap is filtered: the modules under /usr/lib/c_icap are roots
# as well, but nothing copies them separately -- they ship through this archive.
#
# The completeness check runs on the UNFILTERED list, above: a filter must
# never be able to hide a missing dependency.
RUN --mount=type=cache,target=/var/cache/apk \
    apk add --no-cache lddtree \
 && mkdir -p /rootfs \
 && find /usr/lib/c_icap -name '*.la' -delete \
 && test -n "$(find /usr/lib/c_icap -name '*.so' -print -quit)" \
 && { lddtree -l /usr/bin/c-icap; \
      find /usr/lib/c_icap -name '*.so' -exec lddtree -l {} +; } \
      > /tmp/closure.list 2> /tmp/closure.err \
 && if grep -q 'Not found' /tmp/closure.list /tmp/closure.err; then \
      echo "closure incomplete -- a dependency is missing from this stage:" >&2; \
      grep 'Not found' /tmp/closure.list /tmp/closure.err >&2; \
      exit 1; \
    fi \
 && sort -u /tmp/closure.list -o /tmp/closure.list \
 && grep -v -E '^/usr/bin/c-icap$' /tmp/closure.list > /tmp/closure.deps \
 && tar -cf /tmp/closure.tar -T /tmp/closure.deps \
 && tar -xf /tmp/closure.tar -C /rootfs \
 && rm -f /tmp/closure.list /tmp/closure.deps /tmp/closure.err /tmp/closure.tar

# The c-icap modules -- squidclamav.so above all -- are dlopen'd, so they are
# closure roots, enumerated with find and never a glob: busybox sh hands an
# unmatched glob through literally. The build refuses to continue if that find
# comes back empty. The .la libtool archives are link-time metadata, never read
# at runtime. OpenSSL providers are dlopen'd too; the 1.x engines are unused.
RUN mkdir -p /rootfs/usr/lib \
 && cp -a /usr/lib/ossl-modules /rootfs/usr/lib/

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
COPY --link --from=prep /rootfs/ /

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
