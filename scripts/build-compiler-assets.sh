#!/usr/bin/env bash
#
# Regenerates the compiler's embedded assets:
# - copies the latest App UI bundle into internal/cli/compiler_assets
# - rebuilds the precompiled runtime stubs used by mar compile and mar dev
# This keeps the mar compiler, its embedded App UI files, and packaged runtimes in sync.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ASSET_ROOT="$ROOT/internal/cli/compiler_assets/admin"
STUB_ROOT="$ROOT/internal/cli/runtime_stubs"
GOCACHE_DIR="${GOCACHE:-$ROOT/.gocache}"

if [[ -z "${NO_COLOR:-}" && -t 1 ]]; then
  COLOR_INFO=''
  COLOR_DIM=$'\033[38;5;245m'
  COLOR_RESET=$'\033[0m'
else
  COLOR_INFO=''
  COLOR_DIM=''
  COLOR_RESET=''
fi

human_size() {
  awk -v bytes="$1" 'BEGIN {
    split("B KB MB GB TB", units, " ")
    size = bytes + 0
    unit = 1
    while (size >= 1024 && unit < length(units)) {
      size /= 1024
      unit++
    }
    if (size >= 10 || unit == 1) {
      printf "%.0f %s", size, units[unit]
    } else {
      printf "%.1f %s", size, units[unit]
    }
  }'
}

printf "  %s%s%s\n" "$COLOR_INFO" "Copying minified App UI bundle into embedded assets" "$COLOR_RESET"
mkdir -p "$ASSET_ROOT/dist"
cp "$ROOT/app-ui/index.html" "$ASSET_ROOT/index.html"
cp "$ROOT/app-ui/favicon.svg" "$ASSET_ROOT/favicon.svg"
cp "$ROOT/app-ui/dist/app.js" "$ASSET_ROOT/dist/app.js"

build_stub() {
  local target="$1"
  local goos="$2"
  local goarch="$3"
  local output="$4"
  local bytes
  local size

  mkdir -p "$(dirname "$output")"
  env GOCACHE="$GOCACHE_DIR" CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w" -o "$output" ./cmd/mar-app
  bytes="$(wc -c < "$output" | tr -d '[:space:]')"
  size="$(human_size "$bytes")"
  if [[ -n "$COLOR_DIM" ]]; then
    printf "  %s%s%s %s(%s)%s\n" "$COLOR_INFO" "Building runtime stub for $target" "$COLOR_RESET" "$COLOR_DIM" "$size" "$COLOR_RESET"
  else
    printf "  %s%s%s\n" "$COLOR_INFO" "Building runtime stub for $target ($size)" "$COLOR_RESET"
  fi
}

build_stub "darwin-arm64" "darwin" "arm64" "$STUB_ROOT/darwin-arm64/mar-app"
build_stub "darwin-amd64" "darwin" "amd64" "$STUB_ROOT/darwin-amd64/mar-app"
build_stub "linux-amd64" "linux" "amd64" "$STUB_ROOT/linux-amd64/mar-app"
build_stub "linux-arm64" "linux" "arm64" "$STUB_ROOT/linux-arm64/mar-app"
build_stub "windows-amd64" "windows" "amd64" "$STUB_ROOT/windows-amd64/mar-app.exe"
