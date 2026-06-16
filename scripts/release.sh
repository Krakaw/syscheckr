#!/usr/bin/env bash
#
# release.sh — bump the version, commit, tag, and push.
#
# Usage:
#   scripts/release.sh [major|minor|patch|<x.y.z>]
#
#   major|minor|patch   Bump the corresponding component of VERSION (default: patch).
#   <x.y.z>             Set an explicit semantic version.
#
# The VERSION file is the source of truth. This script bumps it, commits the
# change as "Release vX.Y.Z", creates an annotated tag vX.Y.Z, and pushes both
# the commit and the tag — which triggers the release GitHub Action.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

VERSION_FILE="VERSION"
BUMP="${1:-patch}"

err() { printf 'error: %s\n' "$1" >&2; exit 1; }

# --- preflight ----------------------------------------------------------------
[ -f "$VERSION_FILE" ] || err "$VERSION_FILE not found"

if ! git diff --quiet || ! git diff --cached --quiet; then
  err "working tree has uncommitted changes — commit or stash them first"
fi

current="$(tr -d '[:space:]' < "$VERSION_FILE")"
[[ "$current" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || err "current version '$current' is not x.y.z"
IFS='.' read -r major minor patch <<< "$current"

# --- compute next version -----------------------------------------------------
case "$BUMP" in
  major) next="$((major + 1)).0.0" ;;
  minor) next="${major}.$((minor + 1)).0" ;;
  patch) next="${major}.${minor}.$((patch + 1))" ;;
  [0-9]*.[0-9]*.[0-9]*)
    [[ "$BUMP" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || err "explicit version '$BUMP' is not x.y.z"
    next="$BUMP"
    ;;
  *) err "unknown bump '$BUMP' — use major|minor|patch|<x.y.z>" ;;
esac

tag="v${next}"
git rev-parse -q --verify "refs/tags/${tag}" >/dev/null && err "tag ${tag} already exists"

# --- apply --------------------------------------------------------------------
echo "Releasing ${current} -> ${next}"
printf '%s\n' "$next" > "$VERSION_FILE"

git add "$VERSION_FILE"
git commit -m "Release ${tag}"
git tag -a "$tag" -m "Release ${tag}"

branch="$(git rev-parse --abbrev-ref HEAD)"
git push origin "$branch"
git push origin "$tag"

echo "Pushed ${tag} on ${branch} — release workflow will build the binaries."
