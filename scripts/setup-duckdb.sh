#!/usr/bin/env bash
# Windows only: fetches the official DuckDB shared library that the Go driver is pinned to, checks
# its SHA-256, and unpacks it into server/third_party/duckdb. On Windows the Go build links DuckDB
# dynamically (the static archive's C++ runtime does not match current MinGW toolchains); Linux
# and macOS use the driver's static libraries and need none of this.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dest="$root/server/third_party/duckdb"
version="v2.0.0"
sha="2b83169128239de5f45fe21de80f99bf436e53aa4bb24020dd951a1ba8d4b95f"
url="https://github.com/duckdb/duckdb/releases/download/$version/duckdb-shared-libs-windows-amd64.tar.gz"
if [ -f "$dest/duckdb.dll" ]; then
  echo "DuckDB $version is already in $dest"
  exit 0
fi
mkdir -p "$dest"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "downloading $url"
curl -fsSL -o "$tmp/duckdb.tar.gz" "$url"
echo "$sha  $tmp/duckdb.tar.gz" | sha256sum -c -
tar -xzf "$tmp/duckdb.tar.gz" -C "$tmp"
find "$tmp" -iname 'duckdb.dll' -exec cp {} "$dest/" \;
find "$tmp" -iname 'duckdb.lib' -exec cp {} "$dest/" \; || true
ls -la "$dest"
