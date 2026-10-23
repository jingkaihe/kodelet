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

**Used by:** The `github-release` mise task to automatically include release notes in GitHub releases.

### `run-github-release.sh`

Requires all four native audio release binaries in `.build/audio/`, then extracts the top `RELEASE.md` entry into a temporary file and passes it to GoReleaser via `--release-notes`. GitHub Actions supplies these binaries using the reusable native audio workflow; for manual publishing, collect the outputs of `mise run audio-package` on each supported platform first. See [audio release infrastructure](../audio/README.md#release-infrastructure).

**Usage:**
```bash
./scripts/run-github-release.sh
```

## Usage in mise Tasks

- `mise run github-release` - Uses `run-github-release.sh` to publish GitHub releases with proper release notes from `RELEASE.md`
