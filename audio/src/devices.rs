use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{FromSample, SampleFormat, SizedSample, Stream, StreamConfig};
use crossbeam_queue::ArrayQueue;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::time::{Duration, Instant};

pub const BLOCK: usize = 480;
const QUEUE_BLOCKS: usize = 128;

pub struct Frame {
    pub samples: [f32; BLOCK],
    pub len: usize,
    pub at: Instant,
    pub generation: u64,
}

pub struct Buffers {
    pub capture: ArrayQueue<Frame>,
    pub render: ArrayQueue<Frame>,
    pub playback: ArrayQueue<f32>,
    pub failed: AtomicBool,
    pub generation: AtomicU64,
    started: Instant,
    capture_after_ns: AtomicU64,
}

impl Buffers {
    pub fn new() -> Self {
        Self {
            capture: ArrayQueue::new(QUEUE_BLOCKS),
            render: ArrayQueue::new(QUEUE_BLOCKS),
            playback: ArrayQueue::new(192_000),
            failed: AtomicBool::new(false),
            generation: AtomicU64::new(0),
            started: Instant::now(),
            capture_after_ns: AtomicU64::new(0),
        }
    }

    pub fn mute(&self, muted: bool) {
        let old = self.generation.load(Ordering::Acquire);
        // Odd epochs are muted. Every transition invalidates old capture blocks.
        if old % 2 == u64::from(muted) {
            return;
        }
        if !muted {
            self.capture_after_ns
                .store(self.started.elapsed().as_nanos() as u64, Ordering::Release);
        }
        self.generation.fetch_add(1, Ordering::AcqRel);
        while self.capture.pop().is_some() {}
    }

    pub fn accepts_capture(&self, at: Instant, generation: u64) -> bool {
        generation.is_multiple_of(2)
            && self.generation.load(Ordering::Acquire) == generation
            && at
                >= self.started
                    + Duration::from_nanos(self.capture_after_ns.load(Ordering::Acquire))
    }

    pub fn check(&self) -> Result<(), &'static str> {
        if self.failed.load(Ordering::Acquire) {
            Err("audio device failed or processing fell behind")
        } else {
            Ok(())
        }
    }
}

pub struct Devices {
    input: Option<Stream>,
    _output: Option<Stream>,
    pub buffers: Arc<Buffers>,
    pub input_rate: u32,
    pub output_rate: u32,
}

impl Devices {
    pub fn open(duplex: bool) -> Result<Self, &'static str> {
        let host = cpal::default_host();
        let input = host
            .default_input_device()
            .ok_or("no microphone is available")?;
        let input_config = input
            .default_input_config()
            .map_err(|_| "microphone configuration unavailable")?;
        let buffers = Arc::new(Buffers::new());
        buffers.mute(duplex);
        let input_stream = match input_config.sample_format() {
            SampleFormat::F32 => capture::<f32>(&input, &config(&input_config)?, buffers.clone()),
            SampleFormat::I16 => capture::<i16>(&input, &config(&input_config)?, buffers.clone()),
            SampleFormat::U16 => capture::<u16>(&input, &config(&input_config)?, buffers.clone()),
            _ => return Err("microphone sample format is unsupported"),
        }
        .map_err(|_| "could not open microphone; check microphone permission and device access")?;
        // Opening the input may change a Bluetooth device's output format. Query output afterwards.
        let (output_stream, output_rate) = if duplex {
            let output = host
                .default_output_device()
                .ok_or("no speaker is available")?;
            let supported = output
                .default_output_config()
                .map_err(|_| "speaker configuration unavailable")?;
            let stream = match supported.sample_format() {
                SampleFormat::F32 => {
                    playback::<f32>(&output, &config(&supported)?, buffers.clone())
                }
                SampleFormat::I16 => {
                    playback::<i16>(&output, &config(&supported)?, buffers.clone())
                }
                SampleFormat::U16 => {
                    playback::<u16>(&output, &config(&supported)?, buffers.clone())
                }
                _ => return Err("speaker sample format is unsupported"),
            }
            .map_err(|_| "could not open speaker")?;
            (Some(stream), supported.sample_rate().0)
        } else {
            (None, 48_000)
        };
        if let Some(output) = &output_stream {
            output.play().map_err(|_| "could not start speaker")?;
        }
        input_stream
            .play()
            .map_err(|_| "could not start microphone")?;
        Ok(Self {
            input: Some(input_stream),
            _output: output_stream,
            buffers,
            input_rate: input_config.sample_rate().0,
            output_rate,
        })
    }

    pub fn stop_capture(&mut self) {
        self.input.take();
    }
}

fn config(supported: &cpal::SupportedStreamConfig) -> Result<StreamConfig, &'static str> {
    if !(8_000..=192_000).contains(&supported.sample_rate().0)
        || !(1..=8).contains(&supported.channels())
    {
        return Err("audio device format is unsupported");
    }
    let mut config = supported.config();
    if let cpal::SupportedBufferSize::Range { min, max } = supported.buffer_size() {
        config.buffer_size =
            cpal::BufferSize::Fixed((config.sample_rate.0 / 100).clamp(*min, *max));
    }
    Ok(config)
}

fn capture<T>(
    device: &cpal::Device,
    config: &StreamConfig,
    buffers: Arc<Buffers>,
) -> Result<Stream, cpal::BuildStreamError>
where
    T: SizedSample,
    f32: FromSample<T>,
{
    let channels = usize::from(config.channels);
    let rate = config.sample_rate.0;
    let errors = buffers.clone();
    device.build_input_stream(
        config,
        move |input: &[T], info: &cpal::InputCallbackInfo| {
            let epoch = buffers.generation.load(Ordering::Acquire);
            if epoch % 2 == 1 {
                return;
            }
            if input.len() > 8192 * channels {
                buffers.failed.store(true, Ordering::Release);
                return;
            }
            let timing = info.timestamp();
            let age = timing
                .callback
                .duration_since(&timing.capture)
                .unwrap_or_default();
            let at = Instant::now().checked_sub(age).unwrap_or_else(Instant::now);
            // A callback arriving after unmute can still contain audio captured while muted.
            if !buffers.accepts_capture(at, epoch) {
                return;
            }
            for (i, chunk) in input.chunks(BLOCK * channels).enumerate() {
                let mut frame = Frame {
                    samples: [0.0; BLOCK],
                    len: chunk.len() / channels,
                    at: at + Duration::from_secs_f64((i * BLOCK) as f64 / f64::from(rate)),
                    generation: epoch,
                };
                for (out, sample) in frame.samples.iter_mut().zip(chunk.chunks_exact(channels)) {
                    *out = sample
                        .iter()
                        .map(|sample| sample.to_sample::<f32>())
                        .sum::<f32>()
                        / channels as f32;
                }
                if buffers.capture.push(frame).is_err() {
                    buffers.failed.store(true, Ordering::Release);
                    break;
                }
            }
        },
        move |_| {
            errors.failed.store(true, Ordering::Release);
        },
        None,
    )
}

fn playback<T>(
    device: &cpal::Device,
    config: &StreamConfig,
    buffers: Arc<Buffers>,
) -> Result<Stream, cpal::BuildStreamError>
where
    T: SizedSample + FromSample<f32>,
{
    let channels = usize::from(config.channels);
    let rate = config.sample_rate.0;
    let errors = buffers.clone();
    device.build_output_stream(
        config,
        move |output: &mut [T], info: &cpal::OutputCallbackInfo| {
            output.fill(T::EQUILIBRIUM);
            if output.len() > 8192 * channels {
                buffers.failed.store(true, Ordering::Release);
                return;
            }
            let timing = info.timestamp();
            let delay = timing
                .playback
                .duration_since(&timing.callback)
                .unwrap_or_default();
            let at = Instant::now() + delay;
            for (i, chunk) in output.chunks_mut(BLOCK * channels).enumerate() {
                let mut frame = Frame {
                    samples: [0.0; BLOCK],
                    len: chunk.len() / channels,
                    at: at + Duration::from_secs_f64((i * BLOCK) as f64 / f64::from(rate)),
                    generation: 0,
                };
                for (reference, sample) in frame
                    .samples
                    .iter_mut()
                    .zip(chunk.chunks_exact_mut(channels))
                {
                    *reference = buffers.playback.pop().unwrap_or(0.0);
                    sample.fill(T::from_sample(*reference));
                }
                if buffers.render.push(frame).is_err() {
                    buffers.failed.store(true, Ordering::Release);
                    break;
                }
            }
        },
        move |_| {
            errors.failed.store(true, Ordering::Release);
        },
        None,
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn mute_invalidates_capture_epoch() {
        let buffers = Buffers::new();
        let before_mute = Instant::now();
        assert!(buffers.accepts_capture(before_mute, 0));
        buffers.mute(true);
        assert_eq!(buffers.generation.load(Ordering::Acquire), 1);
        assert!(!buffers.accepts_capture(Instant::now(), 0));
        assert!(!buffers.accepts_capture(Instant::now(), 1));
        buffers.mute(true);
        buffers.mute(false);
        assert_eq!(buffers.generation.load(Ordering::Acquire), 2);
        assert!(!buffers.accepts_capture(before_mute, 2));
        assert!(buffers.accepts_capture(Instant::now(), 2));
    }
}
