# Security Audit Status

The weekly `security-audit.yml` workflow (Trivy + Grype, `--fail-on high --only-fixed`)
scans the published `:latest` images every Tuesday. This file tracks known,
investigated exceptions so the CI state doesn't need to be re-diagnosed from scratch
each time it comes up.

## Old vulnerable image tags left publicly pullable (found 2026-07-21, fixed)

Same root cause as `nginx-hardened`: `build-push.yml` pushes a brand new immutable
version tag per image (`squid-hardened:<ver>`, `c-icap-hardened:<ver>`,
`clamav-hardened:<ver>`) on every run, in addition to `:latest`, on both Docker Hub
and GHCR, and never retired the previous one. Confirmed via a direct `grype` scan
against the old published tags (not just `:latest`, which `security-audit.yml` alone
covers):

| Tag | Finding |
|---|---|
| `squid-hardened:7.5` | Real CVEs in the embedded Go stdlib (`GO-2026-5037/5038/5039/5856/4970`), fixed by the golang digest bump that landed for `7.6` (`46d6843`). |
| `c-icap-hardened:0.6.4` | Same Go stdlib CVEs (`GO-2026-4970/5856`), fixed for `0.6.5` by the same commit. |
| `clamav-hardened:1.4.2` | Same Go stdlib CVEs (`GO-2026-4970/5856`), fixed for `1.4.4` by the same commit. |

Fixed by `registry-cleanup.yml` (`scripts/prune-registry-tags.sh` for Docker Hub,
`scripts/prune-ghcr-tags.sh` for GHCR), called as a job from `build-push.yml` after
every push (matrixed over all three images), and directly `workflow_dispatch`-able.
Keeps the last 3 semver tags + `:latest` per image, deletes older semver tags. Only
ever deletes a package version by its own named tag -- untagged manifest-list
children, attestations, and cosign signatures are left alone to avoid risking a
still-live manifest reference.

**Important caveat** (hit on `nginx-hardened`'s first run, applies here too): "keep the
last 3 semver tags" is a *generic hygiene* policy, not a CVE-aware one. After any prune
run, cross-check the surviving semver tags for each image with a direct `grype
<image>:<tag> --fail-on high --only-fixed` scan -- if one inside the keep-window is
still flagged, delete it explicitly via `gh api --method DELETE
/users/<owner>/packages/container/<image>/versions/<id>` (GHCR) or the Docker Hub REST
API. Don't assume the automated retention alone guarantees no vulnerable tag survives.

**Real incident, same day**: the first live run of these scripts wiped `squid-hardened`
down to just `:latest` on both registries -- a two-component version scheme (`7.5`,
`7.6`) didn't match the scripts' `X.Y.Z`-only semver regex, so it fell into the
"always delete" bucket alongside `auto-*` snapshot tags. On GHCR, `latest` was deleted
too, because GHCR groups tags sharing a digest into one "version" object and the
script classifies by the first listed tag only -- `7.6` happened to be listed before
`latest` on that object, so deleting it took both. Restored the same day via `docker
buildx imagetools create` from the still-intact digest, and again via a full rebuild
once the fix landed (the version tag never disappeared from `versions.json`, only
from the registries). Full writeup and the sort-direction bug that also hit
`php-fpm-hardened` are in `nginx-hardened`'s `SECURITY.md`. Fixed in `7759157`.
