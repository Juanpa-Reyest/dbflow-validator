#!/usr/bin/env bash
# install.sh — Install dbflow-validator from the dist/ directory.
#
# Usage:
#   ./install.sh [--prefix <dir>]
#
# The script auto-detects OS and architecture, selects the correct binary from
# dist/, and copies it to <prefix>/dbflow-validator (default: /usr/local/bin or
# $HOME/.local/bin when /usr/local/bin is not writable).
#
# Windows users: copy dist/dbflow-validator-windows-amd64.exe to a directory
# that is on your PATH (e.g. C:\Windows\System32 or a personal bin dir).
#
# Prerequisites: Docker + Git on PATH.  Maven and JVM are NOT required on the host.
set -euo pipefail

# ---------- detect OS / arch ----------

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    echo "Supported: x86_64 (amd64), aarch64/arm64." >&2
    exit 1
    ;;
esac

case "$OS" in
  linux)   PLATFORM="linux-${ARCH}"  ;;
  darwin)  PLATFORM="darwin-${ARCH}" ;;
  *)
    echo "Unsupported OS: $OS" >&2
    echo "For Windows, copy dist/dbflow-validator-windows-amd64.exe to a directory on your PATH." >&2
    exit 1
    ;;
esac

# ---------- locate binary ----------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_SRC="${SCRIPT_DIR}/dist/dbflow-validator-${PLATFORM}"

if [[ ! -f "$BINARY_SRC" ]]; then
  echo "Binary not found: $BINARY_SRC" >&2
  echo "Run 'make build-all' first to cross-compile all platforms." >&2
  exit 1
fi

# ---------- select install prefix ----------

PREFIX="/usr/local/bin"
if [[ -n "${1:-}" && "${1:-}" == "--prefix" && -n "${2:-}" ]]; then
  PREFIX="$2"
elif [[ ! -w "$PREFIX" ]]; then
  PREFIX="${HOME}/.local/bin"
  mkdir -p "$PREFIX"
fi

DEST="${PREFIX}/dbflow-validator"

# ---------- install ----------

cp "$BINARY_SRC" "$DEST"
chmod +x "$DEST"

echo "Installed: $DEST"
echo "Version:   $("$DEST" --version 2>/dev/null || echo "(run dbflow-validator --help)")"
echo ""
echo "Prerequisites: Docker daemon running + git on PATH."
echo "Maven and JVM are NOT required on the host — they run inside a container."
