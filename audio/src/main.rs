#[cfg(not(all(
    any(target_os = "macos", target_os = "linux"),
    any(target_arch = "x86_64", target_arch = "aarch64")
)))]
compile_error!("kodelet-audio supports macOS and Linux on amd64/arm64");

mod devices;
mod playout;
mod processing;
mod protocol;
mod realtime;

use devices::Devices;
use processing::{Converter, Processor, pcm16};
use protocol::{Command, Output};
use serde_json::json;
use std::sync::atomic::Ordering;
use std::sync::{Arc, OnceLock};
use std::time::{Duration, Instant};
use tokio::sync::mpsc;
use tokio::sync::mpsc::error::TrySendError;

type Result<T> = std::result::Result<T, &'static str>;

const COMMANDS_FELL_BEHIND: &str =
    "audio commands arrived faster than the helper could process them";

fn main() {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args == ["--version"] {
        println!(
            "kodelet-audio {} (experimental; protocol 1)",
            env!("CARGO_PKG_VERSION")
        );
        return;
    }
    if !args.is_empty() {
        eprintln!("usage: kodelet-audio [--version]");
        std::process::exit(2);
    }
    let (output, finished) = Output::start();
    let (sender, mut commands) = mpsc::channel(16);
    // Set once the reader has reported a fatal input failure itself.
    let input_failure = Arc::new(OnceLock::new());
    let reader_failure = input_failure.clone();
    let reporter = output.clone();
    std::thread::spawn(move || {
        if let Err(message) = forward_commands(&mut std::io::stdin().lock(), &sender) {
            // The session loop is behind, so report directly rather than queueing.
            let _ = reader_failure.set(message);
            let _ = reporter.event(json!({"type":"error","message":message}));
        }
        drop((sender, reporter));
        // Parent EOF must also terminate stalled native startup or a blocked stdout writer.
        std::thread::sleep(Duration::from_secs(2));
        std::process::exit(1);
    });
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(2)
        .enable_all()
        .build()
        .expect("audio runtime");
    let result = runtime.block_on(async {
        let hello = tokio::time::timeout(Duration::from_secs(5), commands.recv())
            .await
            .map_err(|_| "audio handshake timed out")?;
        match hello {
            Some(Ok(Command::Hello { protocol: 1 })) => {
                output.event(json!({"type":"ready","protocol":1}))?
            }
            _ => return Err("expected audio protocol 1 handshake"),
        }
        let mut session = Session::default();
        let result = session.run(&output, &mut commands, &input_failure).await;
        session.close().await;
        result
    });
    // The reader reports its own failure; do not repeat or contradict it.
    if let Err(message) = result
        && input_failure.get().is_none()
    {
        let _ = output.event(json!({"type":"error","message":message}));
    }
    output.finish();
    let _ = finished.recv_timeout(Duration::from_millis(500));
    runtime.shutdown_timeout(Duration::from_millis(500));
    if result.is_err() || input_failure.get().is_some() {
        std::process::exit(1);
    }
}

/// Forwards parent commands without blocking, so stdin EOF always reaches the
/// shutdown watchdog. Returns an error when the bounded queue is full because the
/// session loop has fallen behind; the caller must report it.
fn forward_commands(
    input: &mut impl std::io::Read,
    sender: &mpsc::Sender<Result<Command>>,
) -> Result<()> {
    loop {
        let (item, last) = match protocol::read_command(input) {
            Ok(Some(command)) => (Ok(command), false),
            Ok(None) => return Ok(()),
            // Queued in order, so the session rejects it after earlier commands.
            Err(_) => (Err("invalid audio command frame"), true),
        };
        match sender.try_send(item) {
            Ok(()) if !last => {}
            Ok(()) | Err(TrySendError::Closed(_)) => return Ok(()),
            Err(TrySendError::Full(item)) => {
                return Err(item.err().unwrap_or(COMMANDS_FELL_BEHIND));
            }
        }
    }
}

#[derive(Default)]
struct Session {
    used: bool,
    recording: Option<Recording>,
    call: Option<Call>,
}

impl Session {
    async fn run(
        &mut self,
        output: &Output,
        commands: &mut mpsc::Receiver<Result<Command>>,
        input_failure: &OnceLock<&'static str>,
    ) -> Result<()> {
        let mut tick = tokio::time::interval(Duration::from_millis(10));
        tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut last_level = Instant::now();
        loop {
            // Do not act on commands queued before the reader gave up.
            if let Some(message) = input_failure.get() {
                return Err(message);
            }
            tokio::select! {
                biased;
                command = commands.recv() => match command {
                    None => return Ok(()),
                    Some(Err(message)) => return Err(message),
                    Some(Ok(Command::Close {})) => {
                        self.close().await;
                        output.event(json!({"type":"closed"}))?;
                        return Ok(());
                    }
                    Some(Ok(Command::Record { max_audio_bytes, max_duration_seconds })) if !self.used => {
                        let limit = recording_limit(max_audio_bytes, max_duration_seconds)?;
                        let devices = Devices::open(false)?;
                        self.recording = Some(Recording { converter: Converter::new(devices.input_rate, 16_000)?, devices, remaining: limit, deadline: Instant::now() + Duration::from_secs(max_duration_seconds.min(600)), peak: 0.0 });
                        self.used = true;
                        output.event(json!({"type":"recording"}))?;
                    }
                    Some(Ok(Command::Finish {})) if self.recording.is_some() => self.finish(output)?,
                    // The parent's finish can race the automatic byte/duration limit.
                    Some(Ok(Command::Finish {})) if self.used && self.call.is_none() => {},
                    Some(Ok(Command::CreateOffer {})) if !self.used => {
                        // Cold plugin discovery can be slow; finish it before the parent allocates a call.
                        playout::initialize()?;
                        let (peer, sdp) = realtime::Peer::offer().await?;
                        self.used = true;
                        self.call = Some(Call { peer, audio: None });
                        output.event(json!({"type":"offer","sdp":sdp}))?;
                    }
                    Some(Ok(Command::Answer { sdp })) if self.call.as_ref().is_some_and(|call| call.audio.is_none()) => {
                        let call = self.call.as_mut().unwrap();
                        call.peer.answer(sdp).await?;
                        let devices = Devices::open(true)?;
                        let processor = Processor::new(devices.input_rate, devices.output_rate)?;
                        let playout = playout::Playout::new(devices.output_rate, devices.buffers.clone())?;
                        let mut encoder = opus::Encoder::new(48_000, opus::Channels::Mono, opus::Application::Voip).map_err(|_| "could not create silence encoder")?;
                        let mut silence = vec![0; 1275];
                        let len = encoder.encode_float(&[0.0; 960], &mut silence).map_err(|_| "could not encode silence")?;
                        silence.truncate(len);
                        devices.buffers.check()?;
                        devices.buffers.mute(false);
                        call.audio = Some(CallAudio { devices, processor, playout, silence, next_silence: Instant::now(), peak: 0.0 });
                        output.event(json!({"type":"connected"}))?;
                    }
                    Some(Ok(Command::Mute { muted })) if self.call.as_ref().is_some_and(|call| call.audio.is_some()) => {
                        let audio = self.call.as_mut().unwrap().audio.as_mut().unwrap();
                        audio.devices.buffers.mute(muted);
                        audio.processor = Processor::new(audio.devices.input_rate, audio.devices.output_rate)?;
                        audio.peak = 0.0;
                        output.event(json!({"type":"muted","muted":muted}))?;
                    }
                    _ => return Err("audio command is invalid in the current state"),
                },
                _ = tick.tick() => {
                    if let Some(recording) = self.recording.as_mut() {
                        recording.drain(output)?;
                        if recording.remaining == 0 || Instant::now() >= recording.deadline { self.finish(output)?; }
                    }
                    if let Some(call) = self.call.as_mut() { call.service().await?; }
                    if last_level.elapsed() >= Duration::from_millis(100) {
                        let peak = if let Some(recording) = self.recording.as_mut() { Some(std::mem::take(&mut recording.peak)) } else { self.call.as_mut().and_then(|call| call.audio.as_mut()).map(|audio| std::mem::take(&mut audio.peak)) };
                        if let Some(peak) = peak { output.event(json!({"type":"level","level":peak.clamp(0.0, 1.0)}))?; }
                        last_level = Instant::now();
                    }
                }
            }
        }
    }

    fn finish(&mut self, output: &Output) -> Result<()> {
        let mut recording = self.recording.take().ok_or("no recording is active")?;
        recording.devices.stop_capture();
        recording.drain(output)?;
        let tail = recording.converter.finish()?;
        recording.emit(&tail, output)?;
        output.event(json!({"type":"recorded"}))
    }

    async fn close(&mut self) {
        self.recording.take();
        if let Some(mut call) = self.call.take() {
            call.audio.take();
            call.peer.close().await;
        }
    }
}

fn recording_limit(bytes: u64, seconds: u64) -> Result<usize> {
    if bytes < 2 || seconds == 0 {
        return Err("recording limits are invalid");
    }
    Ok((bytes.min(20 * 1024 * 1024) / 2).min(seconds.min(600) * 16_000) as usize)
}

struct Recording {
    devices: Devices,
    converter: Converter,
    remaining: usize,
    deadline: Instant,
    peak: f32,
}

impl Recording {
    fn drain(&mut self, output: &Output) -> Result<()> {
        self.devices.buffers.check()?;
        while let Some(frame) = self.devices.buffers.capture.pop() {
            self.peak = self.peak.max(peak(&frame.samples[..frame.len]));
            let audio = self.converter.push(&frame.samples[..frame.len])?;
            self.emit(&audio, output)?;
        }
        Ok(())
    }

    fn emit(&mut self, samples: &[f32], output: &Output) -> Result<()> {
        let len = samples.len().min(self.remaining);
        for chunk in samples[..len].chunks((protocol::MAX_FRAME - 1) / 2) {
            output.send(protocol::PCM, &pcm16(chunk))?;
        }
        self.remaining -= len;
        Ok(())
    }
}

struct Call {
    peer: realtime::Peer,
    audio: Option<CallAudio>,
}
struct CallAudio {
    devices: Devices,
    processor: Processor,
    playout: playout::Playout,
    silence: Vec<u8>,
    next_silence: Instant,
    peak: f32,
}

impl Call {
    async fn service(&mut self) -> Result<()> {
        if self.peer.failed.load(Ordering::Acquire) {
            return Err("voice connection ended");
        }
        let Some(audio) = self.audio.as_mut() else {
            return Ok(());
        };
        audio.devices.buffers.check()?;
        for _ in 0..128 {
            let Ok(packet) = self.peer.packets.try_recv() else {
                break;
            };
            audio.playout.push(packet)?;
        }
        while let Some(frame) = audio.devices.buffers.render.pop() {
            audio.processor.render(&frame)?;
        }
        let generation = audio.devices.buffers.generation.load(Ordering::Acquire);
        while let Some(frame) = audio.devices.buffers.capture.pop() {
            if !audio
                .devices
                .buffers
                .accepts_capture(frame.at, frame.generation)
            {
                continue;
            }
            audio.peak = audio.peak.max(peak(&frame.samples[..frame.len]));
            for packet in audio.processor.capture(&frame)? {
                self.peer.send(packet).await?;
            }
        }
        if generation % 2 == 1 && Instant::now() >= audio.next_silence {
            self.peer.send(audio.silence.clone()).await?;
            audio.next_silence = Instant::now() + Duration::from_millis(20);
        }
        Ok(())
    }
}

fn peak(samples: &[f32]) -> f32 {
    samples
        .iter()
        .fold(0.0_f32, |peak, sample| peak.max(sample.abs()))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn command_reader_reports_a_stalled_queue_instead_of_blocking() {
        let hello = protocol::frame(protocol::JSON, br#"{"type":"hello","protocol":1}"#).unwrap();
        let invalid = [0, 0, 0, 0];

        // Nothing drains the queue, as when the session loop is stalled.
        let (sender, mut queue) = mpsc::channel(1);
        assert_eq!(
            forward_commands(&mut hello.repeat(2).as_slice(), &sender),
            Err(COMMANDS_FELL_BEHIND)
        );
        assert!(matches!(
            queue.try_recv(),
            Ok(Ok(Command::Hello { protocol: 1 }))
        ));
        let (sender, _queue) = mpsc::channel(1);
        assert_eq!(
            forward_commands(
                &mut [hello.as_slice(), &invalid].concat().as_slice(),
                &sender
            ),
            Err("invalid audio command frame"),
            "a full queue still reports the more specific failure"
        );

        // With room, an invalid frame follows earlier commands, and EOF is not an error.
        let (sender, mut queue) = mpsc::channel(4);
        assert_eq!(
            forward_commands(
                &mut [hello.as_slice(), &invalid].concat().as_slice(),
                &sender
            ),
            Ok(())
        );
        assert!(matches!(
            queue.try_recv(),
            Ok(Ok(Command::Hello { protocol: 1 }))
        ));
        assert!(matches!(
            queue.try_recv(),
            Ok(Err("invalid audio command frame"))
        ));
        assert_eq!(forward_commands(&mut hello.as_slice(), &sender), Ok(()));
        drop(queue);
        assert_eq!(
            forward_commands(&mut hello.repeat(3).as_slice(), &sender),
            Ok(()),
            "a finished session ignores further input"
        );
    }

    #[test]
    fn recording_limits_count_raw_pcm_and_are_bounded() {
        assert_eq!(recording_limit(32_000, 60).unwrap(), 16_000);
        assert_eq!(recording_limit(3, 60).unwrap(), 1);
        assert_eq!(recording_limit(u64::MAX, u64::MAX).unwrap(), 600 * 16_000);
        assert!(recording_limit(1, 10).is_err());
        assert!(recording_limit(1_000, 0).is_err());
    }
}
