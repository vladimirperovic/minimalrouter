#!/bin/sh
set -eu

VERSION="2026.7.2"
ARCH="${1:?usage: fetch-cloudflared.sh <amd64|arm64> <output>}"
OUTPUT="${2:?usage: fetch-cloudflared.sh <amd64|arm64> <output>}"

case "$ARCH" in
  amd64)
    ASSET="cloudflared-linux-amd64"
    SHA256="ec905ea7b7e327ff8abdde8cb64697a2152de74dbcdbf6aec9db8364eb3886cd"
    ;;
  arm64)
    ASSET="cloudflared-linux-arm64"
    SHA256="405df476437e027fc6d18729a5a77155c0a33a6082aeee60a799a688f3052e66"
    ;;
  *)
    echo "unsupported architecture: $ARCH" >&2
    exit 2
    ;;
esac

URL="https://github.com/cloudflare/cloudflared/releases/download/${VERSION}/${ASSET}"
TMP="${OUTPUT}.tmp.$$"
trap 'rm -f "$TMP"' EXIT HUP INT TERM
mkdir -p "$(dirname "$OUTPUT")"
curl --fail --location --silent --show-error "$URL" -o "$TMP"
printf '%s  %s\n' "$SHA256" "$TMP" | sha256sum -c - >/dev/null
chmod 0755 "$TMP"
mv "$TMP" "$OUTPUT"
trap - EXIT HUP INT TERM
