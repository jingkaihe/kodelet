# Kodelet Desktop (Electron MVP)

This is a thin Electron shell around the existing `kodelet serve` web UI.

The desktop app does not reimplement the chat UI. Instead, it either:

- starts a local `kodelet serve` sidecar on `127.0.0.1`, waits for the HTTP API to come up, and then loads that URL inside Electron, or
- connects directly to a remote `kodelet serve` base URL.

The Electron shell itself is written in TypeScript and compiled with `tsc` into `desktop/build/`.

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

Packaged desktop builds are published through GitHub Releases.

- **macOS**: download the `.zip`, extract it, and move `Kodelet.app` into `/Applications` if you want a normal app install.
- **Linux**: download either the `.AppImage` or `.tar.gz`. For AppImage, make it executable first with `chmod +x Kodelet-*.AppImage` and then run it.

## Development

From the repository root:

```bash
mise run desktop-install
mise run desktop-dev
```

By default, the Electron shell launches `kodelet` from your `PATH`.

If you want to force a specific binary, pass `--kodelet-path`:

```bash
cd desktop
npm run dev -- --kodelet-path /absolute/path/to/kodelet
```

For repository development, `mise run desktop-dev` builds `./bin/kodelet` first and launches Electron with `--kodelet-path ./bin/kodelet`.

## Behavior

- The last selected workspace directory is persisted in Electron user data.
- The last remote server URL is also persisted in Electron user data.
- The sidecar process is launched with that workspace as both the process working directory and `--cwd`, so repo-level `kodelet-config.yaml` files continue to work.
- The app remembers whether you last used a local workspace or a remote server.
- Remote servers must be reachable at the origin root because the current web UI expects `/api/*` on the same origin.

## Packaging

```bash
mise run desktop-package
```

This now builds the sidecar with `goreleaser build --single-target`, stages the resulting `kodelet` binary under `desktop/.sidecar/bin/`, and then packages the Electron app using that staged binary. That keeps the desktop bundle aligned with the release binary flags and stripping settings defined in `.goreleaser.yaml`.

If you already have a specific sidecar directory you want to bundle, set `KODELET_SIDECAR_DIR=/absolute/path/to/bin-dir` before running `npm run package` or `npm run package:ci`.

For local development packaging, macOS signing and notarization are explicitly disabled so stray `APPLE_*` environment variables do not cause `electron-builder` to fail. Keep signing/notarization as a separate release/CI concern.

Desktop packaging assets live under `desktop/assets/`, including the checked-in platform icon files used by `electron-builder`.

## Current caveats

- API keys provided only via shell startup files may not be available when launching a packaged GUI app directly from Finder/Explorer.
- This MVP still uses the loopback HTTP server for local mode and does not add desktop-specific request authentication yet.
