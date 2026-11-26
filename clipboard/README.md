# Clipboard helper

`kodelet-clipboard` reads the desktop clipboard on the machine running `kodelet chat`. The TUI starts it only when you use the clipboard shortcut. It does not need to be installed on a remote server or runner.

## Install

The standalone installer (`install.sh --binary`) and Kodelet’s Linux RPM and deb packages install the helper alongside `kodelet`.

For other installation methods, download `kodelet-clipboard-<os>-<arch>` from the matching [Kodelet release](https://github.com/jingkaihe/kodelet/releases) (`linux` or `darwin`; `amd64` or `arm64`). Verify its SHA-256 against the release’s `checksums.txt`, then install it as `kodelet-clipboard` on your `PATH`. For example, on an Apple Silicon Mac:

```sh
mkdir -p "$HOME/.local/bin"
install -m 755 kodelet-clipboard-darwin-arm64 "$HOME/.local/bin/kodelet-clipboard"
```

To build and install from a checkout instead, run `mise run clipboard-install`. Ensure `$HOME/.local/bin` is on your `PATH` and run `kodelet-clipboard --version` to verify the installation.

## Protocol

`kodelet-clipboard read-v1` writes a single tag byte to stdout followed immediately by its payload, then exits. EOF marks the end; there is no newline delimiter or base64 encoding.

| Tag | Payload |
| --- | --- |
| `I` | Encoded image bytes (up to 32 MiB) |
| `T` | Text (up to 16 MiB) |
| `F` | JSON array of original absolute file paths (up to 1 MiB) |
| `N` | Empty: no supported data or no desktop clipboard |
| `E` | UTF-8 error message (up to 4 KiB) |

A complete response, including `E`, exits with status zero. The TUI stops waiting after five seconds; the helper independently exits after ten seconds if a read stalls. The TUI, not the helper, checks the runner and writes temporary image files.
