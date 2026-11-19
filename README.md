# Squid + c-icap + ClamAV Hardened

[![Build](https://github.com/jbsky/squid-hardened/actions/workflows/build-push.yml/badge.svg)](https://github.com/jbsky/squid-hardened/actions/workflows/build-push.yml)
[![Docker Hub squid](https://img.shields.io/docker/v/jbsky/squid-hardened?sort=semver&label=squid)](https://hub.docker.com/r/jbsky/squid-hardened)
[![Docker Hub c-icap](https://img.shields.io/docker/v/jbsky/c-icap-hardened?sort=semver&label=c-icap)](https://hub.docker.com/r/jbsky/c-icap-hardened)
[![Docker Hub clamav](https://img.shields.io/docker/v/jbsky/clamav-hardened?sort=semver&label=clamav)](https://hub.docker.com/r/jbsky/clamav-hardened)
[![Hardening](https://img.shields.io/badge/hardening-platine-blueviolet)](https://github.com/jbsky/squid-hardened#hardening)

A filtering, antivirus web proxy stack in three `FROM scratch` images -- Squid
<!--v:squid-hardened-->7.8<!--/v-->, c-icap <!--v:c-icap-hardened-->0.6.5<!--/v--> with squidclamav, and ClamAV
<!--v:clamav-hardened-->1.5.4<!--/v--> -- all built from source, ready to replace an existing Squid
behind VyOS.

Two operating modes:

| Mode | Network | Port | Characteristics |
|---|---|---|---|
| **Transparent** | LAN 1 | 3128 (intercept) | DNAT from VyOS, HTTP scanned, HTTPS passed through |
| **Explicit + SSL Bump** | LAN 2 | 3128 / 3129 (bump) | WPAD through DHCP option 252, HTTPS decrypted and scanned |

## What is built

- **Squid**: release tarball verified against the squid-cache PGP key
  ([`squid/squid-cache.pgp.asc`](squid/squid-cache.pgp.asc)), built with
  OpenSSL, the SSL Bump certificate generator (`--enable-ssl-crtd`) and the
  ICAP client. Configurations for both modes and for VyOS production live in
  [`squid/`](squid).
- **c-icap and squidclamav**: release tarballs verified against their sha256,
  pinned in [`versions.json`](versions.json).
- **ClamAV**: Cisco Talos release tarball, verified against its signature
  ([`clamav/clamav-talos.gpg.asc`](clamav/clamav-talos.gpg.asc)). Its
  configuration ([`clamav/conf/`](clamav/conf)) is mounted on `/etc/clamav`,
  as in the deployment.

The three images are **one pipeline call** of
[hardened-ci](https://github.com/jbsky/hardened-ci), not three: the EICAR test
exercises them together, and none is promoted if the Squid -> c-icap -> ClamAV
chain fails. This repository only describes what sets it apart, in
[`.github/hardened-ci.json`](.github/hardened-ci.json).

## Hardening

Tier **platine** (OCI label `security.hardening.tier`): a `FROM scratch` image
with no shell and no package manager, a non-root user, a static Go init behind
`tini` as PID 1, and binaries built with hardening flags.

| Measure | Squid | c-icap | ClamAV |
|---|---|---|---|
| Multi-stage build, final image `FROM scratch` | yes | yes | yes |
| Non-root user | uid 3128 | uid 4100 | uid 4000 |
| `cap_drop: ALL` (compose) | yes, plus `NET_BIND_SERVICE` | yes | yes |
| `no-new-privileges` (compose) | yes | yes | yes |
| Stripped, full RELRO, PIE | yes | yes | yes |
| `tini` as PID 1, Go init healthcheck | yes | yes | yes |
| Read-only rootfs (compose) | no (Squid writes its cache spool) | yes | yes |
| OCI labels | yes | yes | yes |

Details in [`docs/hardening.md`](docs/hardening.md).

## Tags

Every image is published under three tags: `latest` (last build of `main`),
the upstream version alone, and the upstream version followed by a **revision
counter**. The first two are **overwritten in place** on every rebuild (Alpine
update, CVE fix, configuration change), and the build they pointed to can no
longer be pulled by them. **In production, pin the tag that carries the
counter.**

<!-- BEGIN:tags (genere par la CI -- ne pas editer a la main) -->
| Image | Version amont | Tag immuable a epingler |
|-------|---------------|-------------------------|
| `jbsky/squid-hardened` | `7.8` | `7.8.5` |
| `jbsky/c-icap-hardened` | `0.6.5` | `0.6.5.41` |
| `jbsky/clamav-hardened` | `1.5.4` | `1.5.4.26` |
<!-- END:tags -->

This table, the versions quoted in the text and the tags in the examples are
rendered by `update-readme-tags.sh` ([hardened-ci](https://github.com/jbsky/hardened-ci))
after each publication: do not edit them by hand.

The counter counts the commits that touch the image inputs (the `revision`
list of each image in [`.github/hardened-ci.json`](.github/hardened-ci.json):
its own directory plus `versions.json`) since the last upstream version
change, and restarts at `0` with each new upstream version. A commit that only
touches `.github/` does not increment it. Each image has its own counter,
independent of the other two. Squid's upstream version has two components, so
its immutable tag has three.

`docker.io` and `ghcr.io` publish the same tags with the same digest, for
`linux/amd64`. Each registry keeps the last six immutable tags of an image, so
that superseded builds do not stay pullable: move your pins forward.

`version-check` looks for new Squid, c-icap, squidclamav and ClamAV releases
every Monday, bumps `versions.json` on `main` and republishes.

## Usage

### Prerequisites

- A Docker host (separate from VyOS recommended): Linux, Docker 24 or later,
  Compose v2.
- VyOS 1.4 (Sagitta) or 1.5 (Circinus) with the nftables firewall.
- `openssl`, `curl` and `bash` on the Docker host.
- Recommended audit tools: `trivy`, `syft`, `dive`.
- Internet access, to build (Alpine, Squid, c-icap and ClamAV sources) and
  for the ClamAV signatures.

### Quick start

```bash
cp .env.example .env

# 1) SSL Bump CA (explicit mode only)
./scripts/generate-ca.sh

# 2) Build the images
./scripts/deploy.sh build

# 3) Start one profile -- each one publishes port 3128
./scripts/deploy.sh up explicit      # HTTPS + SSL Bump
./scripts/deploy.sh up transparent   # transparent HTTP

# 4) Check the chain
./scripts/test-eicar.sh 127.0.0.1:3128

# 5) Security audit
./scripts/deploy.sh scan
./scripts/deploy.sh sbom
```

The compose file also has a `multimode` profile, which runs the VyOS
production configuration (`squid/squid-vyos.conf`).

### VyOS configuration

The `vyos/*.config` files use the `set` syntax, ready to paste in
`configure` mode:

```vyos
configure
load /config/scripts/squid-stack/vyos-explicit.config
# check the variables (IPs, interfaces) before committing!
commit
save
exit
```

Adapt the IPs and interfaces (`eth0/1/2`, subnets) to your actual topology.
Transparent HTTPS interception with policy-based routing is described in
[`docs/vyos-transparent-proxy.md`](docs/vyos-transparent-proxy.md).

### Switching over from an existing Squid

1. Deploy the new stack on **another IP** than the old one (192.168.99.10 by
   default).
2. Test from a test client (curl + EICAR).
3. Update the VyOS DNAT rule (transparent mode) or the WPAD file (explicit
   mode) to switch.
4. Keep the old Squid on standby for 24-48 h.
5. Decommission it once the logs are validated.

## SSL Bump

- **The generated CA must be installed on every client** of the explicit LAN
  (Windows GPO, iOS/Android MDM profile, Linux system store, NSS for Firefox):
  see [`docs/ca-deployment.md`](docs/ca-deployment.md).
- Banking and health sites are listed in `nobump_domains.acl` (splice =
  pass-through).
- Applications with **certificate pinning** (Chrome, mobile banking apps,
  system updates) must be allow-listed.
- **Legal compliance**: inform the users (IT charter, GDPR, labour law).

## Logs

Squid writes **no log file**: the shipped configurations send `access_log` to
`stdio:/dev/stdout` and `cache_log` to `stdio:/dev/stderr`. The image is
`FROM scratch` -- a file under `/var/log/squid` would end up in the
container's ephemeral overlay layer: unreadable (no shell to open it), never
rotated, and lost on every re-creation. Collection is therefore the runtime's:
`docker logs` / `podman logs`, or journald -> rsyslog -> SIEM.

> The image `CMD` deliberately does not use `-d <level>`: with
> `cache_log stdio:/dev/stderr`, `-d` prints **every message twice** on
> stderr. If you override the `CMD`, do not bring it back.

The extended Squid format (explicit mode) includes the TLS SNI
(`%ssl::>sni`) and the bump mode used (`%ssl::bump_mode`). For a SIEM, prefer
a dedicated `access_log udp://<collector>:514` (commented example in
`squid/squid-vyos.conf`) over a sidecar tailing a file.

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| `SECURITY ALERT: Host header forgery` | missing conntrack in intercept mode | `modprobe nf_conntrack`, check that VyOS tracks the DNAT |
| SSL error on the client | CA not installed | push `certs/bump.crt` to the client |
| `clamd timeout` | database not downloaded yet | `docker logs clamav` -- the first freshclam takes 5-15 min |
| Sites break under bump | certificate pinning | add them to `nobump_domains.acl` + reload Squid |
| EICAR still goes through | ICAP disabled / clamd down | check `icap_service_failure_limit` and the c-icap logs |

## Documentation

[`docs/`](docs), in French except for the transparent-proxy guide, which also
exists in English:

- [`vyos-transparent-proxy.md`](docs/vyos-transparent-proxy.md) -- transparent
  HTTPS interception with PBR and Podman containers
  ([FR](docs/vyos-transparent-proxy.fr.md));
- [`architecture.md`](docs/architecture.md) -- diagrams and flows;
- [`hardening.md`](docs/hardening.md) -- hardening measures in detail;
- [`vyos-howto.md`](docs/vyos-howto.md) -- step-by-step VyOS guide (simple
  DNAT mode);
- [`ca-deployment.md`](docs/ca-deployment.md) -- pushing the CA to
  Windows/macOS/iOS/Android/Linux;
- [`ci-secrets.md`](docs/ci-secrets.md) -- secrets the CI needs.

## Build and test

```bash
make build   # build args come from versions.json
make test    # scripts/test-eicar.sh: the EICAR file must be blocked
make scan    # Trivy on the three images
```

`scripts/test-eicar.sh` downloads the official EICAR test file through the
proxy and requires it to be blocked: it proves the whole Squid -> c-icap ->
ClamAV chain, not just three healthy containers.

Versions live only in [`versions.json`](versions.json): the Dockerfiles have
no defaults, `make build` and CI pass them through
`scripts/versions-build-args.py`, and a bare `docker build` stops at the guard.

## Verification

Images are signed with [cosign](https://github.com/sigstore/cosign) (keyless,
GitHub OIDC) by the shared [hardened-ci](https://github.com/jbsky/hardened-ci)
pipeline. The certificate names that workflow and, in an extension, this
repository: the commands below require both.

```bash
for ref in jbsky/squid-hardened:7.8.5 jbsky/c-icap-hardened:0.6.5.41 jbsky/clamav-hardened:1.5.4.26; do
  # ghcr.io
  cosign verify "ghcr.io/$ref" \
    --certificate-identity-regexp '^https://github.com/(jbsky/squid-hardened|jbsky/hardened-ci)/' \
    --certificate-github-workflow-repository jbsky/squid-hardened \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  # Docker Hub (the signature is stored on ghcr.io)
  COSIGN_REPOSITORY="ghcr.io/${ref%%:*}" cosign verify "docker.io/$ref" \
    --certificate-identity-regexp '^https://github.com/(jbsky/squid-hardened|jbsky/hardened-ci)/' \
    --certificate-github-workflow-repository jbsky/squid-hardened \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
done
```

Each published digest also carries a CycloneDX SBOM (the `prep` stage the
image is assembled from, plus its Go binaries), attested with cosign, and a
GitHub SLSA build provenance attestation. The pipeline lints, builds the three
images, scans their `prep` stages with Trivy, runs the EICAR test on the exact
digests it is about to publish, then promotes and signs them. Base image
digests follow upstream daily (`base-watch`), a fixable Alpine package CVE
triggers a rebuild (`cve-watch`), and each published image is audited weekly
(`security-audit`: Trivy, Grype, cosign). Audit status and documented
exceptions: [`SECURITY.md`](SECURITY.md).

## License

The contents of this repository (Dockerfiles, Go `init` programs, scripts and
CI) are licensed under **Apache-2.0**, see [`LICENSE`](LICENSE). Copyright
2026 jbsky.

The software shipped in the three images keeps its own license: **Squid and
ClamAV are under GPL-2.0-or-later**, **c-icap under LGPL-2.1-or-later**. The
two never mix: each `init` is a separate program that executes its service
without being linked to it and without reusing its code.

Contributions: see [`CONTRIBUTING.md`](CONTRIBUTING.md); a `Signed-off-by` is
required.
