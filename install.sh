#!/bin/sh
# Install the latest goreleaser binary of hive.
#   curl -fsSL https://raw.githubusercontent.com/lum1n/hive/master/install.sh | sh
#   BINDIR=/usr/local/bin sh install.sh
set -eu

REPO="lum1n/hive"
BASE="https://github.com/${REPO}/releases/latest/download"
BINDIR="${BINDIR:-${HOME}/.local/bin}"

need() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "hive-install: need $1 on PATH" >&2
		exit 1
	fi
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
	echo "hive-install: unsupported arch: $(uname -m)" >&2
	exit 1
	;;
esac
case "$os" in
linux | darwin) ;;
*)
	echo "hive-install: unsupported OS: $(uname -s) (linux and macOS only)" >&2
	exit 1
	;;
esac

need curl
need tar
need install

sums=$(curl -fsSL "${BASE}/checksums.txt")
asset=$(printf '%s\n' "$sums" | awk -v o="$os" -v a="$arch" '
	$2 ~ ("^hive_.+_" o "_" a "\\.tar\\.gz$") { print $2; exit }
')
if [ -z "$asset" ]; then
	echo "hive-install: no release asset for ${os}/${arch}" >&2
	exit 1
fi
want=$(printf '%s\n' "$sums" | awk -v f="$asset" '$2 == f { print $1; exit }')

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "${BASE}/${asset}" -o "${tmp}/${asset}"

got=""
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "${tmp}/${asset}" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "${tmp}/${asset}" | awk '{ print $1 }')
else
	echo "hive-install: need sha256sum or shasum" >&2
	exit 1
fi
if [ "$got" != "$want" ]; then
	echo "hive-install: checksum mismatch for ${asset}" >&2
	exit 1
fi

tar -xzf "${tmp}/${asset}" -C "$tmp" hive
mkdir -p "$BINDIR"
install -m 755 "${tmp}/hive" "${BINDIR}/hive"

echo "installed ${BINDIR}/hive"
"${BINDIR}/hive" -version
if ! command -v hive >/dev/null 2>&1; then
	echo "hive-install: add ${BINDIR} to PATH" >&2
fi
