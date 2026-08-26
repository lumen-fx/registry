#!/bin/sh
# Installs the lpm CLI: asks GitHub for the newest release of the registry
# repository, then downloads the binary for this platform from it.
#
#   curl -fsSL https://registry.lumenfx.dev/install.sh | sh
#
# LPM_INSTALL_DIR overrides the destination (default ~/.local/bin).
set -eu

dir="${LPM_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *)
    echo "install.sh supports Linux and macOS. Download a binary for this platform from" >&2
    echo "https://github.com/lumen-fx/registry/releases instead." >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "no lpm build for $(uname -m); see https://github.com/lumen-fx/registry/releases" >&2
    exit 1
    ;;
esac

tag="$(curl -fsSL https://api.github.com/repos/lumen-fx/registry/releases/latest \
  | grep -o '"tag_name": *"[^"]*"' | head -n 1 | cut -d '"' -f 4)"
version="${tag#v}"
if [ -z "$version" ]; then
  echo "could not resolve the newest lpm release from GitHub" >&2
  exit 1
fi

url="https://github.com/lumen-fx/registry/releases/download/v${version}/lpm_${version}_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "installing lpm ${version} (${os}/${arch}) to ${dir}"
curl -fsSL "$url" | tar -xzf - -C "$tmp" lpm
mkdir -p "$dir"
install -m 0755 "$tmp/lpm" "$dir/lpm"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: $dir is not on your PATH" ;;
esac
"$dir/lpm" --version
