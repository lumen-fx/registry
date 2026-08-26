#!/bin/sh
# Installs the lpm CLI: asks this registry for the newest lpm release, then
# downloads the binary for this platform from the matching GitHub release.
#
#   curl -fsSL https://registry.lumenfx.dev/install.sh | sh
#
# LPM_REGISTRY overrides the registry, LPM_INSTALL_DIR the destination
# (default ~/.local/bin).
set -eu

registry="${LPM_REGISTRY:-https://registry.lumenfx.dev}"
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

version="$(curl -fsSL "$registry/packages/lpm/releases" \
  | grep -o '"version":"[^"]*"' | head -n 1 | cut -d '"' -f 4)"
if [ -z "$version" ]; then
  echo "the registry at $registry has no lpm release yet" >&2
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
