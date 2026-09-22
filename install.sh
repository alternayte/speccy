#!/bin/sh
# Install Speccy: download the release for this machine, check it against the published
# checksums, and put the binary on the PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/alternayte/speccy/main/install.sh | sh
#
# Environment:
#   SPECCY_VERSION   the version to install, such as 0.11.0. The default is the latest release.
#   SPECCY_BIN_DIR   where the binary goes. The default is /usr/local/bin, or ~/.local/bin
#                    when /usr/local/bin needs a password and sudo is not there.
set -eu

REPO=alternayte/speccy

say() { printf '%s\n' "$*"; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is not installed. Speccy's installer needs curl, tar, and one of shasum or sha256sum."; }
need curl
need tar

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) die "Speccy has no release for $os. Windows: download the .zip from https://github.com/$REPO/releases and put speccy.exe on your PATH." ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "Speccy has no release for $arch." ;;
esac

version=${SPECCY_VERSION:-}
if [ -z "$version" ]; then
	# The latest release, by its tag. The API answers without a token for a public repo.
	version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$version" ] || die "Speccy could not read the latest version. Set SPECCY_VERSION, or download from https://github.com/$REPO/releases."
fi
version=${version#v}

archive="speccy_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/v${version}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading Speccy $version for $os/$arch."
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "$archive is not in release v$version."
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "The checksums of release v$version did not download."

# The checksum is the point of this script: a download nobody checked is a download nobody
# can trust.
say "Checking the download against the published checksum."
(
	cd "$tmp"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum --ignore-missing --check --status checksums.txt
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 --ignore-missing --check --status checksums.txt
	else
		die "Speccy needs shasum or sha256sum to check the download."
	fi
) || die "The download does not match its published checksum. Nothing was installed."

tar -xzf "$tmp/$archive" -C "$tmp" speccy || die "The archive holds no speccy binary."
chmod +x "$tmp/speccy"

dir=${SPECCY_BIN_DIR:-}
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then
		dir=/usr/local/bin
	elif command -v sudo >/dev/null 2>&1; then
		dir=/usr/local/bin
		sudo_needed=1
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir" 2>/dev/null || true

if [ "${sudo_needed:-0}" = "1" ]; then
	say "Installing to $dir. sudo asks for your password."
	sudo install -m 0755 "$tmp/speccy" "$dir/speccy"
else
	install -m 0755 "$tmp/speccy" "$dir/speccy" || die "Speccy could not write to $dir. Set SPECCY_BIN_DIR to a folder you can write."
fi

say "Speccy $version is in $dir."
case ":$PATH:" in
*":$dir:"*) say "Run: speccy" ;;
*) say "Add $dir to your PATH, then run: speccy" ;;
esac
