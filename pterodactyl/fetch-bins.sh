#!/usr/bin/env bash
# Bakes the MTProto sidecars (mtg / mtg-multi) into the image bin folder.
# Xray-core + geo data are fetched separately by the repo's own DockerInit.sh,
# which already produces the exact filenames the panel expects.
#
# Usage: fetch-bins.sh <docker-target-arch> <bin-dir>
#   arch: amd64 | arm64 | arm | 386   (Docker buildx TARGETARCH)
#
# Failures are non-fatal: MTProto is optional, so a missing binary only means
# MTProto inbounds won't start — the panel and every other protocol still work.
set -uo pipefail

ARCH="${1:-amd64}"
BIN="${2:-build/bin}"
MTG_VER="2.2.8"           # pinned 9seconds/mtg (single-secret fallback)
mkdir -p "$BIN"

# Map Docker TARGETARCH -> panel filename suffix (runtime.GOARCH), mtg release
# arch, and mtg-multi arch (mtg-multi ships prebuilt only for amd64/arm64).
case "$ARCH" in
  amd64) FNAME=amd64; MREL=amd64; MULTI=amd64 ;;
  arm64) FNAME=arm64; MREL=arm64; MULTI=arm64 ;;
  arm)   FNAME=arm;   MREL=armv7; MULTI= ;;
  386)   FNAME=386;   MREL=386;   MULTI= ;;
  *)     FNAME=amd64; MREL=amd64; MULTI=amd64 ;;
esac

# fetch_tar <url> <inner-binary-name> <output-path>
fetch_tar() {
  local url="$1" name="$2" out="$3" tmp
  tmp="$(mktemp -d)"
  if curl -4fLRo "$tmp/a.tgz" "$url"; then
    if tar -xzf "$tmp/a.tgz" -C "$tmp" 2>/dev/null; then
      local f
      f="$(find "$tmp" -type f -name "$name" | head -n1)"
      if [ -n "$f" ]; then
        mv -f "$f" "$out"
        chmod +x "$out"
        echo "  installed $(basename "$out")"
        rm -rf "$tmp"
        return 0
      fi
    fi
  fi
  echo "  WARN: could not fetch $url"
  rm -rf "$tmp"
  return 1
}

got_multi=0
if [ -n "$MULTI" ]; then
  MV="$(curl -4Ls https://api.github.com/repos/dolonet/mtg-multi/releases/latest \
        | grep '"tag_name":' | sed -E 's/.*"v?([^"]+)".*/\1/' | head -n1)"
  if [ -n "${MV:-}" ]; then
    echo "Fetching mtg-multi ${MV} (multi-user MTProto) for ${MULTI}..."
    if fetch_tar "https://github.com/dolonet/mtg-multi/releases/download/v${MV}/mtg-multi-${MV}-linux-${MULTI}.tar.gz" \
                 mtg-multi "$BIN/mtg-multi-linux-${FNAME}"; then
      got_multi=1
    fi
  fi
fi

if [ "$got_multi" != 1 ]; then
  echo "Fetching mtg ${MTG_VER} (single-secret MTProto) for ${MREL}..."
  fetch_tar "https://github.com/9seconds/mtg/releases/download/v${MTG_VER}/mtg-${MTG_VER}-linux-${MREL}.tar.gz" \
            mtg "$BIN/mtg-linux-${FNAME}" || true
fi

# cloudflared (Cloudflare Tunnel) — a plain, statically-linked binary named to
# match the panel's lookup (bin/cloudflared-linux-<GOARCH>). Non-fatal.
echo "Fetching cloudflared (Cloudflare Tunnel) for ${FNAME}..."
if curl -4fLRo "$BIN/cloudflared-linux-${FNAME}" \
     "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${FNAME}"; then
  chmod +x "$BIN/cloudflared-linux-${FNAME}"
  echo "  installed cloudflared-linux-${FNAME}"
else
  echo "  WARN: could not fetch cloudflared — Cloudflare Tunnel will be unavailable"
fi

echo "fetch-bins: done for ${ARCH}"
exit 0
