#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(CDPATH='' cd -- "$SCRIPT_DIR/.." && pwd -P)"

# GoReleaser accepts unmatched extra-file globs. Never publish a tagged release
# with a silently missing audio target. Snapshot builds remain Go-only by default.
for target in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
  artifact="$REPO_ROOT/.build/audio/kodelet-audio-$target"
  if [[ ! -s "$artifact" ]]; then
    echo "Missing audio release binary: $artifact" >&2
    echo "Build each target natively with mise run audio-package before publishing." >&2
    exit 1
  fi
done

RELEASE_NOTES_FILE="$(mktemp)"

cleanup() {
  rm -f "$RELEASE_NOTES_FILE"
}

trap cleanup EXIT

"$SCRIPT_DIR/extract-release-notes.sh" > "$RELEASE_NOTES_FILE"

cd "$REPO_ROOT"
goreleaser release --clean --release-notes "$RELEASE_NOTES_FILE"
