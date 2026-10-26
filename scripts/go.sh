#!/usr/bin/env bash
# Runs the go command for the server with DuckDB wired up for this platform.
#   scripts/go.sh test ./...        scripts/go.sh build -o bin/ledger ./cmd/ledger
# Linux and macOS link the driver's static DuckDB (nothing to set). On Windows the server links the
# official DuckDB DLL (scripts/setup-duckdb.sh fetches it): the build tag, the linker flags, and the
# DLL on the PATH for tests and for running.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/server"
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    lib="$root/server/third_party/duckdb"
    [ -f "$lib/duckdb.dll" ] || bash "$root/scripts/setup-duckdb.sh"
    export CGO_ENABLED=1 CGO_LDFLAGS="-L$lib -lduckdb" PATH="$lib:$PATH"
    cmd="$1"
    shift
    exec go "$cmd" -tags=duckdb_use_lib "$@"
    ;;
  *)
    exec go "$@"
    ;;
esac
