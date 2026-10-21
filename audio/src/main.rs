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
use std::time::{Duration, Instant};

type Result<T> = std::result::Result<T, &'static str>;

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
    let (sender, mut commands) = tokio::sync::mpsc::channel(16);
    std::thread::spawn(move || {
        let mut input = std::io::stdin().lock();
        loop {
            match protocol::read_command(&mut input) {
                Ok(Some(command)) => {
                    if sender.try_send(Ok(command)).is_err() {
                        break;
                    }
                }
                Ok(None) => break,
                Err(_) => {
                    let _ = sender.try_send(Err("invalid audio command frame"));
                    break;
                }
            }
        }
        drop(sender);
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
        let result = session.run(&output, &mut commands).await;
        session.close().await;
        result
    });
    if let Err(message) = result {
        let _ = output.event(json!({"type":"error","message":message}));
    }
    drop(output);
    let _ = finished.recv_timeout(Duration::from_millis(500));
    runtime.shutdown_timeout(Duration::from_millis(500));
    if result.is_err() {
        std::process::exit(1);
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
        commands: &mut tokio::sync::mpsc::Receiver<Result<Command>>,
    ) -> Result<()> {
        let mut tick = tokio::time::interval(Duration::from_millis(10));
        tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut last_level = Instant::now();
        loop {
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
                        let (peer, sdp) = realtime::Peer::offer().await?;
                        self.used = true;
                        self.call = Some(Call { peer, audio: None });
                        output.event(json!({"type":"offer","sdp":sdp}))?;
                    }
                    Some(Ok(Command::Answer { sdp })) if self.call.as_ref().is_some_and(|call| call.audio.is_none()) => {
                        let call = self.call.as_mut().unwrap();
                        // Cold plugin discovery can be slow; do it before starting media or devices.
                        playout::initialize()?;
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
    fn recording_limits_count_raw_pcm_and_are_bounded() {
        assert_eq!(recording_limit(32_000, 60).unwrap(), 16_000);
        assert_eq!(recording_limit(3, 60).unwrap(), 1);
        assert_eq!(recording_limit(u64::MAX, u64::MAX).unwrap(), 600 * 16_000);
        assert!(recording_limit(1, 10).is_err());
        assert!(recording_limit(1_000, 0).is_err());
    }
}
