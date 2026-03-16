#!/usr/bin/env bash

# Extract the top release notes from RELEASE.md
# Returns the content between the first ## heading and the second ## heading

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
RELEASE_FILE="$REPO_ROOT/RELEASE.md"

if [ ! -f "$RELEASE_FILE" ]; then
    echo "Error: $RELEASE_FILE not found" >&2
    exit 1
fi

# Use awk to extract content between first and second ## headings
awk '
    /^## / {
        if (found_first) {
            exit  # Stop at second ## heading
        }
        found_first = 1
        next  # Skip the first ## heading line itself
    }
    found_first {
        print
    }
' "$RELEASE_FILE" | sed '/^$/N;/^\n$/d'  # Remove leading/trailing empty lines
