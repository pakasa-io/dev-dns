#!/usr/bin/env bash
# Download a CoreDNS release binary into a local bin/ directory.
#
# Usage:
#   scripts/install-coredns.sh [BIN_DIR]      # BIN_DIR defaults to ./bin
#   COREDNS_VERSION=1.11.3 scripts/install-coredns.sh
#
# CoreDNS is only needed to actually serve DNS (devdns start/stop). Generating
# and validating config works without it.
set -euo pipefail

VERSION="${COREDNS_VERSION:-1.11.3}"
BIN_DIR="${1:-bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	*)
		echo "unsupported architecture: $arch" >&2
		exit 1
		;;
esac
case "$os" in
	linux | darwin) ;;
	*)
		echo "unsupported OS: $os (install CoreDNS manually or use Docker)" >&2
		exit 1
		;;
esac

url="https://github.com/coredns/coredns/releases/download/v${VERSION}/coredns_${VERSION}_${os}_${arch}.tgz"
mkdir -p "$BIN_DIR"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading CoreDNS ${VERSION} (${os}/${arch})..."
echo "  $url"
curl -fsSL "$url" -o "$tmp/coredns.tgz"
tar -xzf "$tmp/coredns.tgz" -C "$tmp"
mv "$tmp/coredns" "$BIN_DIR/coredns"
chmod +x "$BIN_DIR/coredns"

echo "Installed to $BIN_DIR/coredns"
"$BIN_DIR/coredns" --version 2>/dev/null | head -1 || true
