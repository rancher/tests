#!/usr/bin/env bash
set -euo pipefail

REPO="rancher/rancher-cli"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

if [ -z "${GH_TOKEN:-}" ]; then
  echo "GH_TOKEN is required"
  exit 1
fi

echo "Fetching latest Rancher CLI release from GitHub API..."

RELEASE_JSON=$(curl -fsSL \
  -H "Authorization: Bearer ${GH_TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/${REPO}/releases/latest")

LATEST_RELEASE=$(echo "$RELEASE_JSON" | jq -r '.tag_name')

if [ -z "$LATEST_RELEASE" ] || [ "$LATEST_RELEASE" = "null" ]; then
  echo "Failed to determine latest Rancher CLI release"
  exit 1
fi

echo "Latest Rancher CLI version found: $LATEST_RELEASE"

case "$(uname -s)" in
  Linux)
    OS="linux"
    EXT=""
    ;;
  Darwin)
    OS="darwin"
    EXT=""
    ;;
  MINGW* | MSYS* | CYGWIN*)
    OS="windows"
    EXT=".exe"
    ;;
  *)
    echo "Unsupported OS: $(uname -s)"
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64)
    ARCH="amd64"
    ;;
  arm64 | aarch64)
    ARCH="arm64"
    ;;
  s390x)
    ARCH="s390x"
    ;;
  *)
    echo "Unsupported architecture: $(uname -m)"
    exit 1
    ;;
esac

if [ "$OS" = "windows" ] && [ "$ARCH" != "amd64" ]; then
  echo "Unsupported Windows architecture: $ARCH"
  exit 1
fi

ASSET_NAME="rancher-${OS}-${ARCH}${EXT}"
BIN_NAME="rancher${EXT}"

echo "Looking for release asset: $ASSET_NAME"

ASSET_ID=$(echo "$RELEASE_JSON" | jq -r \
  --arg asset_name "$ASSET_NAME" \
  '.assets[] | select(.name == $asset_name) | .id' | head -n 1)

if [ -z "$ASSET_ID" ] || [ "$ASSET_ID" = "null" ]; then
  echo "Failed to find release asset: $ASSET_NAME"
  echo "Available assets:"
  echo "$RELEASE_JSON" | jq -r '.assets[].name'
  exit 1
fi

mkdir -p "$INSTALL_DIR"

TMP_BIN="$(mktemp)"

echo "Downloading Rancher CLI asset: $ASSET_NAME"

curl -fsSL \
  -H "Authorization: Bearer ${GH_TOKEN}" \
  -H "Accept: application/octet-stream" \
  "https://api.github.com/repos/${REPO}/releases/assets/${ASSET_ID}" \
  -o "$TMP_BIN"

chmod +x "$TMP_BIN"

echo "Installing Rancher CLI to ${INSTALL_DIR}/${BIN_NAME}"
mv "$TMP_BIN" "${INSTALL_DIR}/${BIN_NAME}"

if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$INSTALL_DIR" >> "$GITHUB_PATH"
  echo "Added ${INSTALL_DIR} to GITHUB_PATH"
fi

echo "Installed Rancher CLI:"
"${INSTALL_DIR}/${BIN_NAME}" --version

if ! command -v rancher >/dev/null 2>&1; then
  echo
  echo "Rancher CLI was installed to ${INSTALL_DIR}/${BIN_NAME}, but ${INSTALL_DIR} is not in PATH for this shell."
  echo "For local use, run:"
  echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
fi