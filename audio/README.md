# Experimental native audio helper

Local microphone capture and realtime audio for Kodelet's experimental TUI voice support. The helper is built separately and published as an optional companion binary in GitHub releases. Supports macOS and Linux on amd64/arm64. The main Kodelet executable and its existing packages remain independent of the audio runtime.

## Release binaries

Each release includes these additional assets, covered by the release's `checksums.txt`:

| Asset | Build environment and runtime requirements |
| --- | --- |
| `kodelet-audio-linux-amd64` | Ubuntu 24.04 amd64, with compatible system audio libraries. |
| `kodelet-audio-linux-arm64` | Ubuntu 24.04 arm64, with compatible system audio libraries. |
| `kodelet-audio-darwin-amd64` | macOS 15 Intel, with Homebrew dependencies under `/usr/local`. |
| `kodelet-audio-darwin-arm64` | macOS 15 Apple Silicon, with Homebrew dependencies under `/opt/homebrew`. |

These are dynamically linked executables, not self-contained bundles. Older operating systems, custom Homebrew prefixes, and other Linux distributions are not guaranteed to work with these builds; build from source below when needed.

Install runtime dependencies before running a downloaded binary. On macOS:

```sh
brew install opus gstreamer
```

On Ubuntu 24.04:

```sh
sudo apt-get update
sudo apt-get install libasound2t64 libopus0 libgstreamer1.0-0 \
  libgstreamer-plugins-base1.0-0 \
  gstreamer1.0-plugins-base gstreamer1.0-plugins-good
```

Download the matching asset and `checksums.txt` from the same release, verify its SHA-256 against that file, then install it as `kodelet-audio` on your `PATH`. For example, on Apple Silicon:

```sh
shasum -a 256 kodelet-audio-darwin-arm64
mkdir -p ~/.local/bin
install -m 755 kodelet-audio-darwin-arm64 ~/.local/bin/kodelet-audio
kodelet-audio --version
```

Ensure `~/.local/bin` is on `PATH`. The existing Kodelet installer does not install this optional helper. macOS artifacts have an ad-hoc signature, not Developer ID signing or notarization; downloaded files remain subject to macOS security policy. Microphone consent is still required.

## Build prerequisites

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

## Release infrastructure

`.github/workflows/audio.yml` builds and tests all four targets on native GitHub-hosted runners. Its tests use synthetic audio without opening microphones or contacting providers. The workflow runs for relevant pull requests and main-branch changes, can be dispatched manually, and is reused by the tag-release workflow.

`mise run audio-package` builds the current host's release binary and stages it as `.build/audio/kodelet-audio-<os>-<arch>`. On macOS it applies and verifies an ad-hoc signature without certificates or signing secrets. Opus, GStreamer, and their plugins are not copied into the artifact.

The release job waits for all native builds, downloads their artifacts into `.build/audio/`, and passes them to GoReleaser as release and checksum extra files. `scripts/run-github-release.sh` requires all four binaries before publishing. Ordinary Go builds and `mise run release-snapshot` do not require Rust or audio libraries; a snapshot includes any audio artifacts already staged locally. Native dependencies must be installed on the build hosts as well as the destination machine.

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
