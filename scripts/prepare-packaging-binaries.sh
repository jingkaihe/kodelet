#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="$REPO_ROOT/dist/package-binaries"

extract_version() {
  local file="$1"
  local const_name="$2"

  sed -n "s/.*${const_name} = \"\([^"]*\)\".*/\1/p" "$file"
}

download_and_extract() {
  local url="$1"
  local binary_name="$2"
  local dest_dir="$3"

  local archive_path
  archive_path="$(mktemp)"
  local extract_dir
  extract_dir="$(mktemp -d)"

  curl -fsSL "$url" -o "$archive_path"
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

RIPGREP_VERSION="$(extract_version "$REPO_ROOT/pkg/binaries/ripgrep.go" "RipgrepVersion")"
FD_VERSION="$(extract_version "$REPO_ROOT/pkg/binaries/fd.go" "FdVersion")"

if [[ -z "$RIPGREP_VERSION" || -z "$FD_VERSION" ]]; then
  echo "failed to resolve packaged binary versions" >&2
  exit 1
fi

rm -rf "$OUTPUT_DIR"

download_and_extract \
  "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
  "rg" \
  "$OUTPUT_DIR/linux-amd64"

download_and_extract \
  "https://github.com/BurntSushi/ripgrep/releases/download/${RIPGREP_VERSION}/ripgrep-${RIPGREP_VERSION}-aarch64-unknown-linux-gnu.tar.gz" \
  "rg" \
  "$OUTPUT_DIR/linux-arm64"

download_and_extract \
  "https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/fd-v${FD_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
  "fd" \
  "$OUTPUT_DIR/linux-amd64"

download_and_extract \
  "https://github.com/sharkdp/fd/releases/download/v${FD_VERSION}/fd-v${FD_VERSION}-aarch64-unknown-linux-gnu.tar.gz" \
  "fd" \
  "$OUTPUT_DIR/linux-arm64"
