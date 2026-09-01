# =====================================================================
#  ClamAV Hardened Image – FROM scratch
#  4-stage build : compile -> Go init -> prep runtime -> FROM scratch
#  Conformité Docker Hardened Image :
#   - FROM scratch final stage: zero shell, zero package manager
#   - utilisateur non-root (uid 4000)
#   - binaires strip + RELRO + PIE + stack-protector
#   - entrypoint + healthcheck en binaire Go statique
#   - filesystem read-only friendly
# =====================================================================

ARG CLAMAV_VERSION=1.5.4
# ALPINE_VERSION kept for check-versions.sh/versions.json reference only --
# the FROM lines below pin tag+digest together as a literal so a version
# bump requires deliberately re-resolving the digest, not a silent drift
# if this ARG changes without the pin being updated to match.
ARG ALPINE_VERSION=3.24

# ---------- Stage 1 : builder ----------------------------------------
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS builder

ARG CLAMAV_VERSION
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# -pie is NOT put in LDFLAGS: CMake seeds CMAKE_SHARED_LINKER_FLAGS from the
# same variable, and -pie on a -shared link is an error. Position-independent
# code is requested through CMAKE_POSITION_INDEPENDENT_CODE (which emits -fPIE
# for executables and -fPIC for libraries), and -pie is added to the executable
# linker flags only. Verified after the build with `readelf -h` (all ELF DYN).
ENV CFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security" \
    CXXFLAGS="-O2 -fstack-protector-strong -fstack-clash-protection -D_FORTIFY_SOURCE=2 -Wformat -Werror=format-security"

# -- APK installs are split across multiple RUN commands so that each
#    completes well within the proxy's 240-second connection timeout.
#    Each RUN opens a fresh TCP connection to the proxy.

# 1/3  Inject CA + switch to HTTP repos + core build toolchain
RUN --mount=type=secret,id=ca-certs,required=false \
    if [ -f /run/secrets/ca-certs ]; then cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt; fi \
 && sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add --no-cache \
        build-base \
        cmake make pkgconf \
        python3 \
        linux-headers

# 2/3  Rust toolchain — libclamav_rust is a first-class part of the engine
#      since 1.0 and the build fails without it (find_package(Rust REQUIRED)).
RUN apk add --no-cache rust cargo

# 3/3  Engine dependencies + download/verify tools
#      ncurses-dev is here only because clamdtop is built unconditionally;
#      the binary is never shipped, so ncurses never reaches the closure.
RUN apk add --no-cache \
        openssl-dev \
        zlib-dev bzip2-dev \
        libxml2-dev \
        pcre2-dev \
        curl-dev \
        json-c-dev \
        ncurses-dev \
        curl file \
        ca-certificates \
        gnupg

WORKDIR /src

# Cisco Talos signs every release tarball with a detached .sig. The key is
# committed next to this Dockerfile rather than fetched from a keyserver at
# build time: importing a key and then verifying with that same key proves
# nothing, so the pinned fingerprint below is the actual trust anchor and
# `gpg --list-keys` fails the build if the committed file is ever swapped.
#
# Two facts worth knowing about this signature:
#   - the key expires 2027-03-13, and verification fails once it does;
#   - the .sig uses a SHA-1 digest, which gnupg 2.4.9 (Alpine 3.24) still
#     accepts for data signatures but a future gnupg may refuse.
COPY clamav-talos.gpg.asc /tmp/clamav-talos.gpg.asc
ARG CLAMAV_FPR=5BADCA2665EF59DCF8A23D8B707F0DB480836771
RUN --mount=type=secret,id=ca-certs,required=false \
    if [ -f /run/secrets/ca-certs ]; then cat /run/secrets/ca-certs >> /etc/ssl/certs/ca-certificates.crt; fi \
 && CLAMAV_URL="https://github.com/Cisco-Talos/clamav/releases/download/clamav-${CLAMAV_VERSION}" \
 && curl -fsSL "${CLAMAV_URL}/clamav-${CLAMAV_VERSION}.tar.gz"     -o clamav.tar.gz \
 && curl -fsSL "${CLAMAV_URL}/clamav-${CLAMAV_VERSION}.tar.gz.sig" -o clamav.tar.gz.sig \
 && GNUPGHOME="$(mktemp -d)" && export GNUPGHOME \
 && gpg --batch --import /tmp/clamav-talos.gpg.asc \
 && gpg --batch --list-keys "${CLAMAV_FPR}" > /dev/null \
 && gpg --batch --verify clamav.tar.gz.sig clamav.tar.gz \
 && gpgconf --kill gpg-agent \
 && rm -rf "$GNUPGHOME" clamav.tar.gz.sig /tmp/clamav-talos.gpg.asc \
 && tar -xzf clamav.tar.gz \
 && mv "clamav-${CLAMAV_VERSION}" clamav \
 && rm -f clamav.tar.gz

WORKDIR /src/clamav/build

# Distribution layout (/etc/clamav, /var/lib/clamav) instead of the upstream
# default of prefix-relative etc/ and share/clamav.
#
# Everything the final image does not run is turned off at configure time
# rather than deleted afterwards: milter, clamonacc, examples, tests, man
# pages and the static libclamav.a would otherwise be built and installed
# only to be thrown away, and the static archive alone is tens of megabytes.
RUN cmake .. \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX=/usr \
        -DCMAKE_INSTALL_LIBDIR=lib \
        -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
        -DCMAKE_EXE_LINKER_FLAGS="-pie -Wl,-z,relro,-z,now,-z,noexecstack" \
        -DCMAKE_SHARED_LINKER_FLAGS="-Wl,-z,relro,-z,now,-z,noexecstack" \
        -DCMAKE_MODULE_LINKER_FLAGS="-Wl,-z,relro,-z,now,-z,noexecstack" \
        -DAPP_CONFIG_DIRECTORY=/etc/clamav \
        -DDATABASE_DIRECTORY=/var/lib/clamav \
        -DCVD_CERTS_DIRECTORY=/usr/share/clamav/certs \
        -DCLAMAV_USER=clamav \
        -DCLAMAV_GROUP=clamav \
        -DENABLE_APP=ON \
        -DENABLE_SHARED_LIB=ON \
        -DENABLE_STATIC_LIB=OFF \
        -DENABLE_JSON_SHARED=ON \
        -DENABLE_UNRAR=ON \
        -DENABLE_MILTER=OFF \
        -DENABLE_CLAMONACC=OFF \
        -DENABLE_EXAMPLES=OFF \
        -DENABLE_TESTS=OFF \
        -DENABLE_MAN_PAGES=OFF \
        -DENABLE_DOXYGEN=OFF \
        -DENABLE_SYSTEMD=OFF \
        -DOPTIMIZE=ON \
 && cmake --build . --parallel "$(nproc)" \
 && DESTDIR=/out cmake --install . \
 && rm -f  /out/usr/lib/libclamav_rust.a /out/usr/bin/clamav-config \
 && rm -rf /out/usr/lib/pkgconfig

# ENABLE_STATIC_LIB=OFF does not stop the Rust half of the engine from being
# installed as a 41 MB static archive; it is already linked into libclamav.so,
# and pkgconfig/clamav-config are build-time metadata. The runtime closure
# would never reach any of them, but leaving them in /out inflates the prep
# stage and invites a later COPY by name -- the libmodsecurity.a lesson.
#
# Strip every ELF file that will ship. `find -exec` ignores the exit status of
# what it runs, so the candidates are filtered into a list first and handed to
# xargs: a strip failure then actually fails the build instead of shipping an
# unstripped or truncated binary.
RUN find /out -type f \( -name '*.so*' -o -perm -111 \) > /tmp/cand.list \
 && : > /tmp/elf.list \
 && while IFS= read -r f; do \
      case "$(file -b "$f")" in *ELF*) echo "$f" >> /tmp/elf.list ;; esac; \
    done < /tmp/cand.list \
 && test -s /tmp/elf.list \
 && xargs -r strip --strip-unneeded < /tmp/elf.list \
 && rm -f /tmp/cand.list /tmp/elf.list

# ---------- Stage 2 : Go builder (entrypoint + healthcheck) ---------
FROM golang:1.26-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS gobuilder
WORKDIR /build
COPY go.mod init.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /init .

# ---------- Stage 3 : prep (runtime filesystem) ---------------------
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS prep

SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# Runtime shared libraries only — no -dev package, no compiler. This is the
# stage Trivy scans for Alpine CVEs, so it must contain exactly what ships.
#
# `libbz2`, not `bzip2`: on Alpine the CLI package does NOT depend on the
# shared library, so `apk add bzip2` leaves libclamav without libbz2.so.1.
# libstdc++ is here for libclamunrar, which is C++.
RUN sed -i 's|https://|http://|g' /etc/apk/repositories \
 && apk add --no-cache \
        ca-certificates \
        tini-static \
        tzdata \
        openssl \
        zlib libbz2 \
        libxml2 \
        pcre2 \
        libcurl \
        json-c \
        libstdc++

# ClamAV is compiled from source here, so there is no apk-created account to
# reuse: the uid/gid are ours and are the ones init --setup-dirs chowns to.
RUN addgroup -S -g 4000 clamav \
 && adduser  -S -D -H -G clamav -u 4000 -s /sbin/nologin clamav

COPY --from=builder /out/ /

# Config files (set ownership + permissions while we have a shell). The whole
# of /etc/clamav ships, so the upstream *.conf.sample files installed next to
# them are dropped rather than carried into a zero-shell image that can never
# read them.
#
# Le certificat de verification des CVD n'est PAS dans /etc/clamav : depuis
# ClamAV 1.5 la verification de signature est obligatoire, et un deploiement
# qui bind-monte sa configuration sur /etc/clamav masquerait le certificat
# livre par l'image. Constate en production le 2026-08-29 : clamd sortait en
# boucle sur « Invalid certs directory '/etc/clamav/certs' », 3110 redemarrages,
# alors que le fichier existait bien dans l'image. Le materiel de verification
# appartient a l'image, il vit donc sous /usr/share/clamav/certs, hors de tout
# chemin qu'un operateur monte.
COPY conf/clamd.conf      /etc/clamav/clamd.conf
COPY conf/freshclam.conf  /etc/clamav/freshclam.conf
RUN rm -f /etc/clamav/*.conf.sample \
 && chown root:clamav /etc/clamav/clamd.conf /etc/clamav/freshclam.conf \
 && chmod 0640        /etc/clamav/clamd.conf /etc/clamav/freshclam.conf

# Collect exactly the shared objects that ship. Copying /lib and /usr/lib whole
# would drag in libapk.so and the apk database itself. lddtree lists each
# binary, its transitive dependencies, symlinks with their targets, and the
# loader for the architecture being built, so arm64 needs no special case.
#
# libclamunrar_iface is dlopen'd by libclamav at scan time, so no closure of
# the binaries lists it -- it and libclamunrar are named as roots explicitly.
# Computed BEFORE apk is deleted below: lddtree comes from a package.
#
# The "Not found" guard is not decoration: lddtree reports a missing library
# on stderr and still EXITS 0, so without it the build happily ships a closure
# with a hole in it and the failure only surfaces at container start.
#
# lddtree prints each binary it is handed, so this list holds the roots as well
# as their dependencies -- and every one of those roots is copied again, on its
# own COPY line, in the final stage. Layers are not deduplicated, so clamd, freshclam and clamdscan was
# going out twice: 0,47 Mo of this image. The roots keep their individual COPY
# and are filtered out of the tar input here; what this archive carries is the
# dependencies and the loader.
#
# Note for whoever adds a file capability here one day: in this image the
# `COPY /rootfs/ /` comes AFTER the three binaries, the reverse of every other
# image in the fleet, so the archive's copies are the ones that win. Harmless
# today (no setcap here), fatal the day there is one -- busybox tar carries no
# xattr.
#
# The completeness check runs on the UNFILTERED list, above: a filter must
# never be able to hide a missing dependency.
RUN --mount=type=cache,target=/var/cache/apk \
    apk add --no-cache lddtree \
 && mkdir -p /rootfs \
 && test -n "$(find /usr/lib -name 'libclamunrar*.so*' -print -quit)" \
 && { lddtree -l /usr/sbin/clamd /usr/bin/freshclam /usr/bin/clamdscan; \
      find /usr/lib -name 'libclamunrar*.so*' -exec lddtree -l {} +; } \
      > /tmp/closure.list 2> /tmp/closure.err \
 && if grep -q 'Not found' /tmp/closure.list /tmp/closure.err; then \
      echo "closure incomplete -- a dependency is missing from this stage:" >&2; \
      grep 'Not found' /tmp/closure.list /tmp/closure.err >&2; \
      exit 1; \
    fi \
 && sort -u /tmp/closure.list -o /tmp/closure.list \
 && grep -v -E '^/usr/sbin/clamd$|^/usr/bin/(freshclam|clamdscan)$' /tmp/closure.list > /tmp/closure.deps \
 && tar -cf /tmp/closure.tar -T /tmp/closure.deps \
 && tar -xf /tmp/closure.tar -C /rootfs \
 && rm -f /tmp/closure.list /tmp/closure.deps /tmp/closure.err /tmp/closure.tar

# freshclam fetches signatures over TLS and OpenSSL providers are dlopen'd, so
# no closure lists them. The 1.x engines (engines-3/) are deprecated and unused.
RUN mkdir -p /rootfs/usr/lib \
 && cp -a /usr/lib/ossl-modules /rootfs/usr/lib/

# Strip APK/package-manager artifacts (won't be copied to scratch)
RUN rm -rf /lib/apk /lib/libapk* /var/cache/apk /etc/apk /sbin/apk

# ---------- Stage 4 : FROM scratch (final hardened image) -----------
FROM scratch

LABEL org.opencontainers.image.title="clamav-hardened" \
      org.opencontainers.image.description="ClamAV FROM scratch, non-root, zero shell" \
      org.opencontainers.image.vendor="jbsky" \
      org.opencontainers.image.licenses="GPL-2.0-or-later AND unRAR" \
      org.opencontainers.image.source="https://github.com/jbsky/squid-hardened" \
      security.hardening.tier="platine" \
      security.hardening.features="from-scratch,go-init,tini-pid1,zero-shell,non-root,cosign-signed,sbom,slsa-provenance"

# User accounts
COPY --link --from=prep /etc/passwd /etc/passwd
COPY --link --from=prep /etc/group  /etc/group

# ClamAV binaries
COPY --link --from=prep /usr/sbin/clamd    /usr/sbin/clamd
COPY --link --from=prep /usr/bin/freshclam /usr/bin/freshclam
COPY --link --from=prep /usr/bin/clamdscan /usr/bin/clamdscan

# Dynamic linker (musl) + shared libraries
COPY --link --from=prep /rootfs/ /

# Config + CVD signing certificates (freshclam refuses unverifiable databases
# without them since 1.4), with correct ownership from the prep stage
COPY --link --from=prep /etc/clamav/ /etc/clamav/
# Certificat de verification des CVD, hors /etc/clamav a dessein (voir prep)
COPY --link --from=prep /usr/share/clamav/ /usr/share/clamav/

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

USER 4000:4000

EXPOSE 3310/tcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD ["/usr/local/bin/init", "--healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/init"]
CMD ["clamd", "--foreground=true", "--config-file=/etc/clamav/clamd.conf"]
