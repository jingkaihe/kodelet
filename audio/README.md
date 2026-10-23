# Experimental native audio helper

Local microphone capture and realtime audio for Kodelet's experimental TUI voice support. The helper is built separately and published as an optional companion binary in GitHub releases. Supports macOS and Linux on amd64/arm64.

## Release binaries

The release workflow reads the supported platforms from `.goreleaser.yaml` and runs `mise run audio-build` on native runners. GoReleaser publishes `kodelet-audio-{linux,darwin}-{amd64,arm64}` alongside `checksums.txt`. Verify the matching asset's SHA-256, then install it as `kodelet-audio` on your `PATH`; the existing Kodelet installer does not install this optional helper.

These binaries require the system libraries below; they are not self-contained bundles. Linux builds target Ubuntu 24.04. macOS builds use macOS 15 and Homebrew's standard prefix (`/usr/local` on Intel, `/opt/homebrew` on Apple Silicon), without Developer ID signing or notarization. Build from source for older systems or custom library locations.

## Prerequisites

On macOS, install Xcode Command Line Tools (`xcode-select --install`) and Homebrew, then:

```sh
brew install cmake pkgconf opus gstreamer
```

On Debian/Ubuntu:

```sh
sudo apt-get update
sudo apt-get install build-essential cmake pkg-config libasound2-dev libopus-dev \
  libgstreamer1.0-dev libgstreamer-plugins-base1.0-dev \
  gstreamer1.0-plugins-base gstreamer1.0-plugins-good
```

Keep Opus and GStreamer (1.20+) installed; the helper needs them at runtime. Rust is pinned in `rust-toolchain.toml`.

## Build and install

From the repository root:

```sh
mise run audio-build
mise run audio-test
mise run audio-lint
mise run audio-install
```

Installation puts `kodelet-audio` in `~/.local/bin`; ensure that directory is on `PATH`. On macOS, allow microphone access for your terminal in System Settings.

Alternatively, run `cd audio && cargo build --release --locked`. The executable is `audio/target/release/kodelet-audio`.

## Protocol 1

stdin/stdout use binary frames: a four-byte unsigned big-endian length, a one-byte kind, then the payload. Length includes the kind but not the prefix; maximum 131072 bytes. Kind 1 is UTF-8 JSON; kind 2 is audio. Commands use kind 1. stdout contains only frames, except with `--version`.

| Command | Response and behavior |
| --- | --- |
| `{"type":"hello","protocol":1}` | `{"type":"ready","protocol":1}`. Send first, within five seconds. |
| `{"type":"record","maxAudioBytes":1048576,"maxDurationSeconds":60}` | `{"type":"recording"}`, then kind-2 PCM: 16000 Hz, mono, signed 16-bit little-endian. |
| `{"type":"finish"}` | Flush capture, then `{"type":"recorded"}`. Recording limits also finish automatically. |
| `{"type":"createOffer"}` | `{"type":"offer","sdp":"..."}`. Prepare realtime audio without opening the microphone. |
| `{"type":"answer","sdp":"..."}` | Apply the remote answer and start audio; emit `{"type":"connected"}`. |
| `{"type":"mute","muted":true}` | `{"type":"muted","muted":true}`. Silence input, including queued audio; playback continues. Use `false` to resume. |
| `{"type":"close"}` | Release resources, emit `{"type":"closed"}`, then exit. |

- One recording or realtime call per process; stdin EOF also triggers cleanup.
- Recording limits count raw PCM and are capped at 20 MiB and 600 seconds.
- Optional `{"type":"level","level":0.0}` events report input peaks in [0,1].
- Fatal errors emit `{"type":"error","message":"..."}` and exit nonzero. Invalid states, malformed frames, and unknown commands are rejected; unused JSON fields are ignored.

## Checks and limitations

- The first build and GStreamer startup can take several minutes. Run `mise run audio-test` after installing or upgrading GStreamer, before starting a live session.
- Tests do not open audio devices or contact a provider. Check installation with `kodelet-audio --version`; test permissions, playback, and connectivity in a manual session. Headphones are recommended initially.
- Only default audio devices are supported. Restart the session after a device failure.
- Restrictive networks may prevent realtime connections; no TURN relay is configured.
