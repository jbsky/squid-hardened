#!/bin/sh
# =====================================================================
#  check-versions.sh — Detect new upstream releases
#  Sources:
#    - Squid: GitHub API (squid-cache/squid)
#    - c-icap: GitHub API (c-icap/c-icap-server)
#    - squidclamav: GitHub API (darold/squidclamav)
#    - ClamAV: GitHub API (Cisco-Talos/clamav)
#
#  Usage: ./scripts/check-versions.sh [--update]
#    Without --update: prints diff only (exit 0 = no change, exit 2 = updates available)
#    With --update: writes changes to versions.json + Dockerfiles + .env.example
# =====================================================================
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
VERSIONS_FILE="${ROOT_DIR}/versions.json"

UPDATE=false
[ "${1:-}" = "--update" ] && UPDATE=true

# --- Helpers ---
die() { printf 'ERROR: %s\n' "$1" >&2; exit 1; }

# Extract latest stable version from GitHub releases (skips pre-releases and drafts)
# Usage: github_latest_release <owner/repo> [tag_strip_prefix]
github_latest_release() {
  repo="$1"
  prefix="${2:-v}"

  # Use GitHub API (works without auth for public repos, rate-limited to 60/h)
  url="https://api.github.com/repos/${repo}/releases/latest"
  tag=$(curl -fsSL --retry 3 "$url" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//;s/".*//')

  if [ -z "$tag" ]; then
    # Fallback: list tags and pick highest semver
    url="https://api.github.com/repos/${repo}/tags?per_page=30"
    tag=$(curl -fsSL --retry 3 "$url" 2>/dev/null \
      | grep '"name"' | sed 's/.*"name"[[:space:]]*:[[:space:]]*"//;s/".*//' \
      | grep -E "^${prefix}[0-9]" | head -1)
  fi

  [ -z "$tag" ] && return 1
  # Strip prefix (v, SQUID_, release-, etc.)
  echo "$tag" | sed "s/^${prefix}//"
}

# Extract latest ClamAV stable from GitHub (skip RC/beta)
clamav_latest() {
  url="https://api.github.com/repos/Cisco-Talos/clamav/releases?per_page=20"
  curl -fsSL --retry 3 "$url" 2>/dev/null \
    | grep '"tag_name"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//;s/".*//' \
    | grep -E '^clamav-[0-9]+\.[0-9]+\.[0-9]+$' | head -1 \
    | sed 's/^clamav-//'
}

# Extract latest Squid stable (filter out RC/beta, handle SQUID_x_y_z tags)
squid_latest() {
  # Try GitHub releases first (squid-cache/squid uses release tags like SQUID_7_5)
  url="https://api.github.com/repos/squid-cache/squid/releases?per_page=30"
  version=$(curl -fsSL --retry 3 "$url" 2>/dev/null \
    | grep '"tag_name"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//;s/".*//' \
    | grep -E '^SQUID_[0-9]+_[0-9]+$' | head -1 \
    | sed 's/^SQUID_//;s/_/./g')

  if [ -z "$version" ]; then
    # Fallback: try tags directly
    url="https://api.github.com/repos/squid-cache/squid/tags?per_page=50"
    version=$(curl -fsSL --retry 3 "$url" 2>/dev/null \
      | grep '"name"' | sed 's/.*"name"[[:space:]]*:[[:space:]]*"//;s/".*//' \
      | grep -E '^SQUID_[0-9]+_[0-9]+$' | head -1 \
      | sed 's/^SQUID_//;s/_/./g')
  fi

  [ -z "$version" ] && return 1
  echo "$version"
}

# --- Read current versions ---
current_squid=$(grep '"squid"' "$VERSIONS_FILE" | sed 's/.*: *"//;s/".*//')
current_cicap=$(grep '"c-icap"' "$VERSIONS_FILE" | sed 's/.*: *"//;s/".*//')
current_squidclamav=$(grep '"squidclamav"' "$VERSIONS_FILE" | sed 's/.*: *"//;s/".*//')
current_clamav=$(grep '"clamav"' "$VERSIONS_FILE" | sed 's/.*: *"//;s/".*//')

# --- Fetch latest versions ---
printf 'Checking upstream versions...\n'

latest_squid=$(squid_latest) || latest_squid="$current_squid"
latest_cicap=$(github_latest_release "c-icap/c-icap-server" "CI_") || latest_cicap="$current_cicap"
latest_squidclamav=$(github_latest_release "darold/squidclamav" "v") || latest_squidclamav="$current_squidclamav"
latest_clamav=$(clamav_latest) || latest_clamav="$current_clamav"

# --- Compare ---
CHANGES=""

compare() {
  name="$1"; current="$2"; latest="$3"
  if [ "$current" != "$latest" ]; then
    printf '  %-14s %s → %s\n' "$name" "$current" "$latest"
    CHANGES="${CHANGES}${name} "
  else
    printf '  %-14s %s (up to date)\n' "$name" "$current"
  fi
}

compare "squid" "$current_squid" "$latest_squid"
compare "c-icap" "$current_cicap" "$latest_cicap"
compare "squidclamav" "$current_squidclamav" "$latest_squidclamav"
compare "clamav" "$current_clamav" "$latest_clamav"

if [ -z "$CHANGES" ]; then
  printf '\nAll versions are up to date.\n'
  exit 0
fi

printf '\nUpdates available: %s\n' "$CHANGES"

if [ "$UPDATE" = false ]; then
  exit 2
fi

# --- Apply updates ---
printf 'Applying updates...\n'

# 1. versions.json (read alpine before overwrite)
current_alpine=$(grep '"alpine"' "$VERSIONS_FILE" | sed 's/.*: *"//;s/".*//')
cat > "$VERSIONS_FILE" <<EOF
{
  "squid": "${latest_squid}",
  "c-icap": "${latest_cicap}",
  "squidclamav": "${latest_squidclamav}",
  "clamav": "${latest_clamav}",
  "alpine": "${current_alpine}"
}
EOF

# 2. Dockerfiles ARG defaults
sed -i "s/^ARG SQUID_VERSION=.*/ARG SQUID_VERSION=${latest_squid}/" "${ROOT_DIR}/squid/Dockerfile"
sed -i "s/^ARG CICAP_VERSION=.*/ARG CICAP_VERSION=${latest_cicap}/" "${ROOT_DIR}/c-icap/Dockerfile"
sed -i "s/^ARG SQUIDCLAMAV_VERSION=.*/ARG SQUIDCLAMAV_VERSION=${latest_squidclamav}/" "${ROOT_DIR}/c-icap/Dockerfile"

# 3. .env.example
sed -i "s/^SQUID_VERSION=.*/SQUID_VERSION=${latest_squid}/" "${ROOT_DIR}/.env.example"
sed -i "s/^CICAP_VERSION=.*/CICAP_VERSION=${latest_cicap}/" "${ROOT_DIR}/.env.example"
sed -i "s/^SQUIDCLAMAV_VERSION=.*/SQUIDCLAMAV_VERSION=${latest_squidclamav}/" "${ROOT_DIR}/.env.example"
sed -i "s/^CLAMAV_VERSION=.*/CLAMAV_VERSION=${latest_clamav}/" "${ROOT_DIR}/.env.example"

printf 'Done. Files updated:\n'
printf '  - versions.json\n'
printf '  - squid/Dockerfile\n'
printf '  - c-icap/Dockerfile\n'
printf '  - .env.example\n'
