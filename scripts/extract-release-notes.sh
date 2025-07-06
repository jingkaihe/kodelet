#!/bin/bash

# Extract the top release notes from RELEASE.md
# Returns the content between the first ## heading and the second ## heading

set -e

RELEASE_FILE="RELEASE.md"

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