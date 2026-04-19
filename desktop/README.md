# Kodelet Desktop

Kodelet Desktop packages the existing Kodelet web experience as a native desktop app. You can use it with a local workspace or connect to a remote Kodelet server.

## Installation

### Build from source

From the repository root:

```bash
mise install
mise run install
mise run desktop-install
```

This installs the toolchain managed by `mise`, the main repository dependencies, and the Electron desktop shell dependencies.

### Install a packaged app

Published desktop builds are attached to tagged GitHub Releases. Pull request CI only builds desktop artifacts for validation; it does not publish a release.

- **macOS**: download the `.zip`, extract it, and move `Kodelet.app` into `/Applications` if you want a normal app install.
- **Linux**: download either the `.AppImage` or `.tar.gz`. For AppImage, make it executable first with `chmod +x Kodelet-*.AppImage` and then run it.

Current release automation publishes macOS and Linux desktop artifacts. The Electron builder config also has a Windows portable target for local packaging, but Windows artifacts are not currently published through GitHub Releases.

Release builds bundle the `kodelet` binary needed for local mode.

## Development

From the repository root:

```bash
mise run desktop-install
mise run desktop-dev
```

This starts the desktop app against the local repository build.

If you want to use a specific binary, pass `--kodelet-path`:

```bash
cd desktop
npm run dev -- --kodelet-path /absolute/path/to/kodelet
```

## Packaging

```bash
mise run desktop-package
```

This packages the desktop app using the repository's current release build configuration.

If you already have a specific sidecar directory you want to bundle, set `KODELET_SIDECAR_DIR=/absolute/path/to/bin-dir` before running `npm run package` or `npm run package:ci`.

GitHub Actions keeps desktop packaging split by trigger:

- `.github/workflows/desktop-build.yml` runs desktop packaging checks for pull requests.
- `.github/workflows/release.yml` handles `v*` tag pushes, creates the GitHub release, rebuilds the desktop artifacts, and attaches them to that same release.
