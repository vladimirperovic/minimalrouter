#!/bin/sh
set -eu

[ "$#" -eq 2 ] || { echo "Usage: checksum-file.sh FILE OUTPUT" >&2; exit 2; }
file="$1"
output="$2"
dir="$(cd "$(dirname "$file")" && pwd)"
name="$(basename "$file")"
output_dir="$(dirname "$output")"
mkdir -p "$output_dir"
output_abs="$(cd "$output_dir" && pwd)/$(basename "$output")"

cd "$dir"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$name" > "$output_abs"
elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$name" > "$output_abs"
else
    echo "ERROR: sha256sum or shasum is required" >&2
    exit 1
fi
