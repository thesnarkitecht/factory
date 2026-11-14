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
gotmp=""
cleanup() {
  rm -f "$tmp"
  # Go's module cache is read-only; make it writable so it can be deleted
  if [ -n "$gotmp" ]; then chmod -R u+w "$gotmp" 2>/dev/null; rm -rf "$gotmp"; fi
}
trap cleanup EXIT

if curl -fsSL "https://github.com/$REPO/releases/latest/download/factory-$os-$arch" -o "$tmp" 2>/dev/null; then
  chmod +x "$tmp"
  mv "$tmp" "$BIN_DIR/factory"
elif command -v go >/dev/null 2>&1; then
  echo "factory: no prebuilt binary yet, building with your Go..."
  GOBIN="$BIN_DIR" go install "github.com/$REPO@latest"
else
  # No sudo needed: Go runs from any folder, so fetch it into a temp dir, build, delete it.
  gover=$(curl -fsSL "https://go.dev/VERSION?m=text" | head -n 1)
  echo "factory: no prebuilt binary yet, building from source with a temporary $gover (~75 MB)..."
  gotmp=$(mktemp -d)
  curl -fsSL "https://go.dev/dl/$gover.$os-$arch.tar.gz" | tar -xz -C "$gotmp"
  GOBIN="$BIN_DIR" GOPATH="$gotmp/gopath" GOCACHE="$gotmp/cache" GOTOOLCHAIN=local \
    "$gotmp/go/bin/go" install "github.com/$REPO@latest"
fi

echo "Installed $BIN_DIR/factory"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Add it to your PATH:  export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
echo "Play:  factory"
