#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd -P)"
DIST_DIR="${KODELET_GORELEASER_DIST_DIR:-$REPO_ROOT/dist}"
STAGING_DIR="${KODELET_SIDECAR_STAGING_DIR:-$REPO_ROOT/desktop/.sidecar/bin}"

if [[ "${KODELET_SIDECAR_SKIP_BUILD:-0}" != "1" ]]; then
  rm -rf "$DIST_DIR"
  if command -v goreleaser >/dev/null 2>&1; then
    goreleaser build --snapshot --clean --single-target --id kodelet
  elif command -v mise >/dev/null 2>&1; then
    mise exec goreleaser -- goreleaser build --snapshot --clean --single-target --id kodelet
  else
    echo "goreleaser is required to build the desktop sidecar" >&2
    exit 1
  fi
fi

rm -rf "$STAGING_DIR"
mkdir -p "$STAGING_DIR"

declare -a candidates=()
while IFS= read -r candidate; do
  candidates+=("$candidate")
done < <(find "$DIST_DIR" -type f \( -name 'kodelet' -o -name 'kodelet.exe' \) | sort)

if [[ "${#candidates[@]}" -eq 0 ]]; then
  echo "failed to find goreleaser-built kodelet binary under $DIST_DIR" >&2
  exit 1
fi

if [[ "${#candidates[@]}" -gt 1 ]]; then
  echo "found multiple goreleaser-built kodelet binaries under $DIST_DIR" >&2
  printf '  %s\n' "${candidates[@]}" >&2
  exit 1
fi

SOURCE_BINARY="${candidates[0]}"
TARGET_BINARY="$STAGING_DIR/$(basename "$SOURCE_BINARY")"

install -m 0755 "$SOURCE_BINARY" "$TARGET_BINARY"
printf '%s\n' "$STAGING_DIR"
