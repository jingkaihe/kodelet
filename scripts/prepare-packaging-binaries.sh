#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
OUTPUT_DIR="$REPO_ROOT/.build/package-binaries"

cd "$REPO_ROOT"

compute_sha256() {
  local file="$1"

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return 0
  fi

  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return 0
  fi

  echo "sha256sum or shasum is required to verify packaged binaries" >&2
  exit 1
}

download_and_extract() {
  local binary="$1"
  local binary_name="$2"
  local goarch="$3"
  local dest_dir="$4"

  local metadata=()
  while IFS= read -r line; do
    metadata+=("$line")
  done < <(go run ./scripts/package-binary-metadata --binary "$binary" --goos linux --goarch "$goarch")
  if [[ "${#metadata[@]}" -ne 2 ]]; then
    echo "failed to resolve download metadata for $binary linux/$goarch" >&2
    exit 1
  fi

  local url="${metadata[0]}"
  local expected_checksum="${metadata[1]}"

  local archive_path
  archive_path="$(mktemp)"
  local extract_dir
  extract_dir="$(mktemp -d)"

  curl -fsSL "$url" -o "$archive_path"

  local actual_checksum
  actual_checksum="$(compute_sha256 "$archive_path")"
  if [[ "$actual_checksum" != "$expected_checksum" ]]; then
    echo "checksum mismatch for $binary linux/$goarch: expected $expected_checksum, got $actual_checksum" >&2
    exit 1
  fi

  tar -xzf "$archive_path" -C "$extract_dir"

  local extracted_binary
  extracted_binary="$(find "$extract_dir" -type f -name "$binary_name" | head -n 1)"
  if [[ -z "$extracted_binary" ]]; then
    echo "failed to find extracted binary $binary_name from $url" >&2
    exit 1
  fi

  mkdir -p "$dest_dir"
  install -m 0755 "$extracted_binary" "$dest_dir/$binary_name"

  rm -f "$archive_path"
  rm -rf "$extract_dir"
}

rm -rf "$OUTPUT_DIR"

download_and_extract "ripgrep" "rg" "amd64" "$OUTPUT_DIR/linux-amd64"
download_and_extract "ripgrep" "rg" "arm64" "$OUTPUT_DIR/linux-arm64"
download_and_extract "fd" "fd" "amd64" "$OUTPUT_DIR/linux-amd64"
download_and_extract "fd" "fd" "arm64" "$OUTPUT_DIR/linux-arm64"
