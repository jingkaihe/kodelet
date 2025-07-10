# Scripts Directory

This directory contains automation scripts for building and releasing Kodelet.

## Scripts

### `extract-release-notes.sh`

Extracts the top release notes from `RELEASE.md` for use in GitHub releases.

**Usage:**
```bash
./scripts/extract-release-notes.sh
```

**Output:** The content between the first `##` heading and the second `##` heading in `RELEASE.md`, with leading/trailing empty lines removed.

**Used by:** The `github-release` make target to automatically include release notes in GitHub releases.

## Usage in Make Targets

- `make github-release` - Uses `extract-release-notes.sh` to create GitHub releases with proper release notes from `RELEASE.md`