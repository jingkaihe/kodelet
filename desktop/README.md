# Kodelet Desktop (Electron MVP)

This is a thin Electron shell around the existing `kodelet serve` web UI.

The desktop app does not reimplement the chat UI. Instead, it either:

- starts a local `kodelet serve` sidecar on `127.0.0.1`, waits for the HTTP API to come up, and then loads that URL inside Electron, or
- connects directly to a remote `kodelet serve` base URL.

The Electron shell itself is written in TypeScript and compiled with `tsc` into `desktop/build/`.

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

This packages the Electron app and bundles `bin/kodelet*` into the app resources.

For local development packaging, macOS signing and notarization are explicitly disabled so stray `APPLE_*` environment variables do not cause `electron-builder` to fail. Keep signing/notarization as a separate release/CI concern.

Desktop packaging assets live under `desktop/assets/`, including the checked-in platform icon files used by `electron-builder`.

## Current caveats

- API keys provided only via shell startup files may not be available when launching a packaged GUI app directly from Finder/Explorer.
- This MVP still uses the loopback HTTP server for local mode and does not add desktop-specific request authentication yet.
