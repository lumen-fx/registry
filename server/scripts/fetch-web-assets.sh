#!/usr/bin/env bash
# Downloads the browser libraries the UI renders README markdown with. They are
# not committed: a build fetches them, checks them against the digests below,
# and the server embeds what it fetched. Run this before building or testing
# the server module, or the embed finds nothing and the build stops.
#
# The digests are of the files npm published; the CDN is a mirror of them and
# is checked against the digest like anything else. To move a version, change
# the pin and the digest together, and rename the file: the name is what a
# browser caches on, and the page asks for it by name.
set -euo pipefail

assets="$(cd "$(dirname "${BASH_SOURCE[0]}")/../web/assets" 2>/dev/null && pwd || true)"
if [ -z "$assets" ]; then
  assets="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/web/assets"
  mkdir -p "$assets"
fi

# name url sha256
pinned=(
  "marked-18.0.13.esm.js https://cdn.jsdelivr.net/npm/marked@18.0.13/lib/marked.esm.js 2e70fea3ee49f98ab67ee395e5af51cc6bee4fafed15910da9ccb7f650df8014"
  "marked-18.0.13.LICENSE https://cdn.jsdelivr.net/npm/marked@18.0.13/LICENSE 8e3a3f82f59a60958f56ca08f445647c32a4733dc7ca6c2c46f6eb898471ab9c"
  "purify-3.4.15.esm.js https://cdn.jsdelivr.net/npm/dompurify@3.4.15/dist/purify.es.mjs e7d8182ea0aae9daa46c3294a486067b3f4461bd18f8ca76e499c623e9bda6e3"
  "purify-3.4.15.LICENSE https://cdn.jsdelivr.net/npm/dompurify@3.4.15/LICENSE cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"
)

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

for entry in "${pinned[@]}"; do
  read -r name url want <<<"$entry"
  path="$assets/$name"

  # Already the right bytes, so nothing is downloaded. A rebuild with a warm
  # checkout costs no network at all.
  if [ -f "$path" ] && [ "$(digest "$path")" = "$want" ]; then
    continue
  fi

  curl -fsSL --retry 3 --retry-delay 2 -o "$path.part" "$url"
  got="$(digest "$path.part")"
  if [ "$got" != "$want" ]; then
    rm -f "$path.part"
    echo "$name: got sha256 $got, expected $want" >&2
    echo "Refusing to build against bytes that are not the pinned release." >&2
    exit 1
  fi
  mv "$path.part" "$path"
  echo "fetched $name"
done
