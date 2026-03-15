#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
RELEASE_NOTES_FILE="$(mktemp)"

cleanup() {
  rm -f "$RELEASE_NOTES_FILE"
}

trap cleanup EXIT

"$SCRIPT_DIR/extract-release-notes.sh" > "$RELEASE_NOTES_FILE"

cd "$REPO_ROOT"
goreleaser release --clean --release-notes "$RELEASE_NOTES_FILE"
