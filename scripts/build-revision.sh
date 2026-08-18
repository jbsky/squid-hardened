#!/usr/bin/env bash
# =====================================================================
#  build-revision.sh -- how many commits have touched this image's build
#  inputs since its upstream version last changed.
#
#  The upstream version alone is not a unique tag: rebuilding squid 7.6 on a
#  new Alpine, or after a Dockerfile change, overwrites :7.6 in place and the
#  build it replaced becomes unreachable -- there is no way to say "the 7.6
#  that ran on alpine 3.21" or to roll back to it. Appending this counter
#  gives every distinct build an immutable tag (7.6.3) while :7.6 and :latest
#  keep floating to the newest one.
#
#  The counter resets to 0 on every upstream version bump, so it stays small
#  and readable, and it is derived purely from git: re-running the same
#  workflow on the same commit produces the same tag rather than burning a
#  new number for identical content.
#
#  Usage: build-revision.sh <version_key> <path>...
#    version_key  key to read from versions.json (squid, c-icap, clamav...)
#    path...      that image's build inputs (squid/ versions.json)
# =====================================================================
set -euo pipefail

KEY="${1:?usage: build-revision.sh <version_key> <path>...}"
shift
[ "$#" -gt 0 ] || { echo "build-revision: no input paths given" >&2; exit 1; }

# A shallow checkout silently yields 0 for every image -- actions/checkout
# defaults to fetch-depth 1, so fail loudly rather than tag everything .0.
if [ "$(git rev-parse --is-shallow-repository)" = "true" ]; then
  echo "build-revision: shallow clone; checkout needs fetch-depth: 0" >&2
  exit 1
fi

CURRENT=$(jq -r --arg k "$KEY" '.[$k] // empty' versions.json)
[ -n "$CURRENT" ] || { echo "build-revision: no '$KEY' key in versions.json" >&2; exit 1; }

# Walk versions.json backwards to the oldest commit that already carries the
# current value: that commit is where this upstream version was introduced.
BASE=""
while read -r sha; do
  value=$(git show "${sha}:versions.json" 2>/dev/null | jq -r --arg k "$KEY" '.[$k] // empty' 2>/dev/null || true)
  [ "$value" = "$CURRENT" ] || break
  BASE="$sha"
done < <(git log --format=%H -- versions.json)

# No bump in history (key added in the initial commit): count from the root.
if [ -n "$BASE" ]; then
  git rev-list --count "${BASE}..HEAD" -- "$@"
else
  git rev-list --count HEAD -- "$@"
fi
