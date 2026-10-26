#!/usr/bin/env bash
# Builds Ledger into server/bin: the web UI (web/) first, embedded into the server binary.
#   scripts/build.sh            then run: server/bin/ledger serve
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"

echo "== web UI"
cd "$root/web"
[ -d node_modules ] || npm ci
npm run build
dist="$root/server/internal/api/dist"
find "$dist" -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
cp -r "$root/web/dist/." "$dist/"

echo "== server"
out="$root/server/bin/ledger"
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    out="$out.exe"
    bash "$root/scripts/go.sh" build -o "$out" ./cmd/ledger
    # The DuckDB DLL goes next to the program so that it runs from anywhere.
    cp "$root/server/third_party/duckdb/duckdb.dll" "$root/server/bin/"
    ;;
  *)
    bash "$root/scripts/go.sh" build -o "$out" ./cmd/ledger
    ;;
esac
echo "built $out"
