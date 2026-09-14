#!/bin/sh
# Installs the lpm CLI. The newest release is whatever
# github.com/lumen-fx/registry/releases/latest redirects to, so no API token
# and no rate limit stand between you and an install. The archive is checked
# against the checksums.txt published alongside it before anything is unpacked.
#
#   curl -fsSL https://reg.lumenfx.dev/install.sh | sh
#
# LPM_INSTALL_DIR overrides the destination (default ~/.local/bin).
set -eu

dir="${LPM_INSTALL_DIR:-$HOME/.local/bin}"
repo="https://github.com/lumen-fx/registry"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *)
    echo "install.sh supports Linux and macOS. Download a binary for this platform from" >&2
    echo "${repo}/releases instead." >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "no lpm build for $(uname -m); see ${repo}/releases" >&2
    exit 1
    ;;
esac

# Pick the digest tool this platform ships. macOS has shasum, Linux has
# sha256sum, and neither reliably has both.
if command -v sha256sum >/dev/null 2>&1; then
  digest() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  digest() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  echo "install.sh needs sha256sum or shasum to verify the download" >&2
  exit 1
fi

# The redirect names the newest tag. -I asks for the headers alone, and
# without -L curl reports the redirect instead of following it.
tag="$(curl -fsSI "${repo}/releases/latest" \
  | tr -d '\r' \
  | awk 'tolower($1) == "location:" { print $2 }' \
  | tail -n 1)"
tag="${tag##*/}"
version="${tag#v}"
if [ -z "$version" ] || [ "$tag" = "latest" ]; then
  echo "could not resolve the newest lpm release from ${repo}/releases/latest" >&2
  exit 1
fi

archive="lpm_${version}_${os}_${arch}.tar.gz"
base="${repo}/releases/download/${tag}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "installing lpm ${version} (${os}/${arch}) to ${dir}"
curl -fsSL -o "$tmp/$archive" "${base}/${archive}"
curl -fsSL -o "$tmp/checksums.txt" "${base}/checksums.txt"

want="$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1 }' "$tmp/checksums.txt")"
if [ -z "$want" ]; then
  echo "checksums.txt for ${tag} does not list ${archive}" >&2
  exit 1
fi
got="$(digest "$tmp/$archive")"
if [ "$got" != "$want" ]; then
  echo "${archive} hashes to ${got}, and ${tag} published ${want}" >&2
  echo "refusing to install a download that does not match its checksum" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" lpm
mkdir -p "$dir"
install -m 0755 "$tmp/lpm" "$dir/lpm"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "$dir is not on your PATH" ;;
esac
"$dir/lpm" --version
