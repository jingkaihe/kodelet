# Experimental native audio helper

This self-contained Rust project supplies local microphone capture and realtime media for Kodelet's experimental TUI voice support. It is not included in distributed Kodelet binaries. The helper has no credential handling, transcription client, or provider tool-execution logic. Kodelet owns authentication, SDP signaling, and server-side realtime session management.

## Local prerequisites

Supported targets are macOS and Linux on amd64/arm64. Build natively for the host; these tasks do not configure cross-compilation or bundle shared libraries. Rust 1.99.1 is pinned in `rust-toolchain.toml` and in the root mise tasks. A first build downloads Rust crates and compiles WebRTC/DSP code, so allow several minutes.

On macOS, install Xcode Command Line Tools (`xcode-select --install` if absent), Homebrew, then:

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

GStreamer 1.20 or newer is required, with `rtpjitterbuffer`, `rtpopusdepay`, `opusdec`, `audioconvert`, `audioresample`, `appsrc`, and `appsink`. On macOS the Homebrew GStreamer formula provides these plugins; on Debian/Ubuntu they come from the base/good packages above. Opus and GStreamer are dynamically linked local dependencies and must remain installed. The Opus binding discovers system Opus through pkg-config; without it the upstream binding attempts a CMake fallback, which is not the intended local setup. Nonstandard library installations may require `PKG_CONFIG_PATH` and GStreamer plugin search configuration. No external WebRTC service, C++ WebRTC SDK, Node.js, or ffmpeg is required.

From the repository root:

```sh
mise run audio-build
mise run audio-test
mise run audio-lint
# Also install the helper into ~/.local/bin, which must be on PATH:
mise run audio-install
```

Or build independently with `cd audio && cargo build --release --locked`. The executable is `audio/target/release/kodelet-audio`. Kodelet runs the first `kodelet-audio` executable found in an absolute `PATH` entry; it never downloads the helper. The root install task applies a local ad-hoc macOS signature with identifier `com.kodelet.audio`; no Developer ID, hardened runtime, or notarization is used. The macOS executable embeds a matching bundle identifier and `NSMicrophoneUsageDescription` in `__TEXT,__info_plist`. Actual microphone permission is still controlled by macOS/TCC, often attributed to the launching terminal. Grant the relevant app microphone access in System Settings; an ad-hoc signature does not bypass consent.

## Protocol 1

stdin/stdout carry binary frames: a four-byte big-endian unsigned length followed by a one-byte kind and payload. The length includes the kind byte but excludes the length prefix and must be at most 131072 bytes. Kind 1 is UTF-8 JSON; kind 2 is audio. Commands use kind 1 only. stdout is reserved for framed events, except for the standalone `--version` flag. Errors return a fixed, sanitized JSON message without SDP, device names, or credentials.

| Command | Response and behavior |
| --- | --- |
| `{"type":"hello","protocol":1}` | `{"type":"ready","protocol":1}`. Required first, within five seconds. No device or network initialization. |
| `{"type":"record","maxAudioBytes":24000000,"maxDurationSeconds":60}` | `{"type":"recording"}`, then kind-2 PCM chunks: 16000 Hz, mono, signed 16-bit little-endian. |
| `{"type":"finish"}` | Stop capture, flush the resampler's final partial block, then `{"type":"recorded"}`. Byte/duration limits also finish automatically. |
| `{"type":"createOffer"}` | Initialize GStreamer, then `{"type":"offer","sdp":"..."}` with gathered ICE candidates, Opus media, and an `oai-events` data channel. No microphone access yet. |
| `{"type":"answer","sdp":"..."}` | Apply the answer, wait for the data channel to open over ICE/DTLS/SCTP, start devices, then `{"type":"connected"}`. Negotiation has a 20-second deadline. |
| `{"type":"mute","muted":true}` | `{"type":"muted","muted":true}`. Discard capture and queued pre-mute audio, reset processing, and send encoded silence. False resumes only newly captured input. Playback continues. |
| `{"type":"close"}` | Release the devices and peer, emit `{"type":"closed"}`, then exit. |

Recording byte limits count raw PCM only; Kodelet reserves space for its own WAV header before sending the limit. The helper caps the request at 20 MiB and 600 seconds, enforces even PCM lengths, and does not store recordings on disk. Optional `{"type":"level","level":0.0}` events report peak input levels in [0,1]. Fatal failures emit `{"type":"error","message":"..."}` and exit nonzero. Invalid states, malformed frames, and unknown command types are rejected; unused fields on known commands are ignored to support Kodelet’s shared command envelope. A late finish after recorded is ignored to accommodate races with automatic limits. A process supports one dictation or one realtime call; start a new process for another session.

CPAL callbacks only move samples through bounded queues: they do not write pipes, block on locks, perform network I/O, or run codecs. Rubato resamples capture; Sonora supplies echo cancellation, noise suppression, and gain control using the actual speaker render reference; Opus encodes 20 ms frames for Rust WebRTC. Incoming RTP goes through GStreamer's jitter buffer, packet-loss concealment, decoding, and resampling before CPAL playback. Slow consumers or failed devices terminate rather than growing an unbounded queue; commands arriving faster than the session can process them are reported as an error. stdin EOF triggers cleanup; a two-second process watchdog bounds shutdown even if native device startup is stalled. Provider data-channel JSON is deliberately not interpreted or executed.

The ICE dependency is pinned to include the consent-freshness fix: STUN checks continue even while audio is flowing. Older versions suppress these checks during continuous media traffic, which can cause the remote peer to end an otherwise healthy call after about 30 seconds. Calls are independent of individual agent runs and remain open until explicitly stopped, a connection/device failure, or Kodelet's session time limit. A temporary ICE disconnection does not end the call; it ends if ICE reports failure, about 30 seconds after the last packet from the provider.

## Verification and limitations

The first GStreamer use may scan all installed plugins and take substantially longer than later runs (about 90 seconds with a cold Homebrew registry during development, with warnings from unrelated Python/GTK plugins). Run `mise run audio-test` once after installing or upgrading GStreamer to populate its registry before a live session. The helper initializes this registry before creating the offer, so a slow first scan finishes before Kodelet asks the server to allocate a call. The TUI allows three minutes for the offer, then 90 seconds to connect.

`mise run audio-test` runs lightweight framing/lifecycle, mute-boundary, PCM/resampler, and synthetic signal-path checks. It does not open a microphone, speaker, or provider connection. `kodelet-audio --version` and hello/close are safe installation smoke checks with no audio/network access. These checks do not validate microphone permissions, device switching, real-device latency/echo cancellation, or interoperability with a live provider. Those require an explicit manual session; headphones are recommended for initial testing. Default devices only are supported, and a device failure requires restarting the session. No TURN service is configured, so restrictive networks may prevent realtime connectivity.
