#!/bin/sh
# Install glci.
#   curl -sSfL https://raw.githubusercontent.com/ttopias/glci/main/install.sh | sh -s -- -b $(go env GOPATH)/bin
#   curl -sSfL ... | sh -s -- -b /usr/local/bin v0.1.0
set -e

usage() {
  echo "Usage: install.sh [-b bindir] [-d] [tag]" >&2
  echo "  -b    install directory (default: ./bin or GOPATH/bin)" >&2
  echo "  -d    debug" >&2
  echo "  tag   version (default: latest). Example: v0.1.0" >&2
  exit 2
}

BINDIR=""
DEBUG=""
while getopts "b:dh" arg; do
  case "$arg" in
    b) BINDIR="$OPTARG" ;;
    d) DEBUG=1 ;;
    h) usage ;;
    *) usage ;;
  esac
done
shift $((OPTIND - 1))
TAG="${1:-latest}"
REPO="ttopias/glci"

log() { [ -n "$DEBUG" ] && echo "$@" >&2 || true; }

if [ -z "$BINDIR" ]; then
  if command -v go >/dev/null 2>&1 && [ -n "$(go env GOPATH 2>/dev/null)" ]; then
    BINDIR="$(go env GOPATH)/bin"
  else
    BINDIR="./bin"
  fi
fi
mkdir -p "$BINDIR"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin|linux) ;;
  mingw*|msys*|cygwin*) os=windows ;;
  *) echo "unsupported os: $os" >&2; exit 1 ;;
esac

ext=""
[ "$os" = windows ] && ext=".zip" || ext=".tar.gz"
asset="glci_${os}_${arch}${ext}"

download() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -sSfL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$dest" "$url"
  else
    return 1
  fi
}

install_from_release() {
  if [ "$TAG" = latest ]; then
    api="https://api.github.com/repos/${REPO}/releases/latest"
    tag=$(curl -sSfL "$api" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
    [ -n "$tag" ] || return 1
  else
    tag="$TAG"
  fi
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
  sums_url="https://github.com/${REPO}/releases/download/${tag}/checksums.txt"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  log "download $url"
  download "$url" "$tmp/$asset" || return 1
  download "$sums_url" "$tmp/checksums.txt" || return 1
  if ! verify_checksum "$tmp/$asset" "$tmp/checksums.txt"; then
    echo "checksum verification failed for $asset" >&2
    return 1
  fi
  if [ "$os" = windows ]; then
    unzip -l "$tmp/$asset" | awk 'NR>3 {print $4}' | while IFS= read -r f; do
      case "$f" in *..*) echo "unsafe zip member: $f" >&2; exit 1 ;; esac
    done
    unzip -o -q "$tmp/$asset" -d "$tmp"
  else
    tar -tzf "$tmp/$asset" | while IFS= read -r f; do
      case "$f" in *..*|/*) echo "unsafe tar member: $f" >&2; exit 1 ;; esac
    done
    tar -xzf "$tmp/$asset" -C "$tmp" --no-same-owner
  fi
  bin=$(find "$tmp" -type f \( -name glci -o -name glci.exe \) | head -n1)
  [ -n "$bin" ] || return 1
  install -m 755 "$bin" "$BINDIR/glci"
  echo "installed $BINDIR/glci ($tag)"
}

verify_checksum() {
  file="$1"
  sums="$2"
  want=$(grep -E "[ *]$asset\$" "$sums" | awk '{print $1}' | head -n1)
  [ -n "$want" ] || return 1
  if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$file" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    got=$(shasum -a 256 "$file" | awk '{print $1}')
  else
    echo "need sha256sum or shasum to verify release" >&2
    return 1
  fi
  [ "$got" = "$want" ]
}

install_with_go() {
  command -v go >/dev/null 2>&1 || return 1
  ver="@latest"
  [ "$TAG" != latest ] && ver="@$TAG"
  log "go install github.com/${REPO}/cmd/glci$ver"
  GOBIN="$BINDIR" go install "github.com/${REPO}/cmd/glci$ver"
  echo "installed $BINDIR/glci via go install"
}

if ! install_from_release; then
  echo "no GitHub release asset for $TAG; falling back to go install" >&2
  install_with_go
fi
