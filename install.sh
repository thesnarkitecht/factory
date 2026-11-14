#!/bin/sh
# Install factory:  curl -fsSL https://raw.githubusercontent.com/thesnarkitecht/factory/main/install.sh | sh
set -eu
REPO=thesnarkitecht/factory
BIN_DIR=${FACTORY_BIN_DIR:-$HOME/.local/bin}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "factory: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac
case $os in
  linux | darwin) ;;
  *) echo "factory: unsupported OS $os (on Windows, use WSL)" >&2; exit 1 ;;
esac

mkdir -p "$BIN_DIR"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
if curl -fsSL "https://github.com/$REPO/releases/latest/download/factory-$os-$arch" -o "$tmp"; then
  chmod +x "$tmp"
  mv "$tmp" "$BIN_DIR/factory"
elif command -v go >/dev/null 2>&1; then
  echo "factory: no prebuilt binary found, building with Go..."
  GOBIN="$BIN_DIR" go install "github.com/$REPO@latest"
else
  echo "factory: no prebuilt binary for $os-$arch and Go is not installed" >&2
  exit 1
fi

echo "Installed $BIN_DIR/factory"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Add it to your PATH:  export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
echo "Play:  factory"
