#!/usr/bin/env bash
#
# install.sh — download, verify, and install the latest syscheckr release.
#
# Quick install:
#   curl -fsSL https://raw.githubusercontent.com/Krakaw/syscheckr/main/scripts/install.sh | bash
#
# Options (environment variables):
#   VERSION    Tag to install (e.g. v0.1.2). Default: latest release.
#   INSTALL_DIR  Where to put the binary. Default: /usr/local/bin
#               (uses sudo if not writable; set to e.g. "$HOME/.local/bin" to avoid sudo).
#
# Example:
#   VERSION=v0.1.2 INSTALL_DIR="$HOME/.local/bin" \
#     bash -c "$(curl -fsSL https://raw.githubusercontent.com/Krakaw/syscheckr/main/scripts/install.sh)"

set -euo pipefail

REPO="Krakaw/syscheckr"
BIN="syscheckr"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

err() { printf 'error: %s\n' "$1" >&2; exit 1; }

for tool in curl tar; do
  command -v "$tool" >/dev/null 2>&1 || err "$tool is required but not installed"
done

# --- detect OS / arch ---------------------------------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$os" = "linux" ] || err "only linux is published; got '$os'"

case "$(uname -m)" in
  x86_64|amd64)   arch=amd64 ;;
  aarch64|arm64)  arch=arm64 ;;
  *) err "unsupported architecture '$(uname -m)' (expected x86_64 or arm64)" ;;
esac

# --- resolve version ----------------------------------------------------------
tag="${VERSION:-}"
if [ -z "$tag" ]; then
  tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
    | head -1 | sed 's/.*"\([^"]*\)"$/\1/')"
fi
[ -n "$tag" ] || err "could not determine the release tag"

asset="${BIN}-${tag}-${os}-${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${tag}"

echo "Installing ${BIN} ${tag} (${os}/${arch})"

# --- download into a temp dir -------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "${tmp}/${asset}" "${base}/${asset}" \
  || err "failed to download ${asset} — does release ${tag} have a ${os}/${arch} build?"

# --- verify checksum (best effort: skip if checksums.txt absent) --------------
if curl -fsSL -o "${tmp}/checksums.txt" "${base}/checksums.txt" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$tmp" && grep " ${asset}\$" checksums.txt | sha256sum -c - ) \
      || err "checksum verification failed for ${asset}"
    echo "Checksum OK"
  else
    echo "warning: sha256sum not found — skipping checksum verification" >&2
  fi
else
  echo "warning: checksums.txt not found for ${tag} — skipping verification" >&2
fi

# --- extract & install --------------------------------------------------------
tar -xzf "${tmp}/${asset}" -C "$tmp"
src="${tmp}/${BIN}-${tag}-${os}-${arch}/${BIN}"
[ -f "$src" ] || err "binary not found in archive"

mkdir -p "$INSTALL_DIR" 2>/dev/null || true
if [ -w "$INSTALL_DIR" ]; then
  install -m755 "$src" "${INSTALL_DIR}/${BIN}"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)"
  sudo install -m755 "$src" "${INSTALL_DIR}/${BIN}"
fi

echo "Installed to ${INSTALL_DIR}/${BIN}"
"${INSTALL_DIR}/${BIN}" version 2>/dev/null || true
