use crate::devices::{BLOCK, Frame};
use rubato::{
    Resampler, SincFixedIn, SincInterpolationParameters, SincInterpolationType, WindowFunction,
};
use sonora::config::{EchoCanceller, GainController2, NoiseSuppression};
use sonora::{AudioProcessing, StreamConfig};
use std::collections::VecDeque;
use std::time::{Duration, Instant};

pub struct Converter {
    resampler: SincFixedIn<f32>,
    pending: VecDeque<f32>,
    skip: usize,
    received: usize,
    emitted: usize,
    input_rate: usize,
    output_rate: usize,
}

impl Converter {
    pub fn new(input_rate: u32, output_rate: u32) -> Result<Self, &'static str> {
        let resampler = SincFixedIn::new(
            f64::from(output_rate) / f64::from(input_rate),
            1.0,
            SincInterpolationParameters {
                sinc_len: 128,
                f_cutoff: 0.95,
                interpolation: SincInterpolationType::Linear,
                oversampling_factor: 128,
                window: WindowFunction::BlackmanHarris2,
            },
            (input_rate / 100) as usize,
            1,
        )
        .map_err(|_| "could not initialize audio resampler")?;
        let skip = resampler.output_delay();
        Ok(Self {
            resampler,
            pending: VecDeque::new(),
            skip,
            received: 0,
            emitted: 0,
            input_rate: input_rate as usize,
            output_rate: output_rate as usize,
        })
    }

    pub fn push(&mut self, samples: &[f32]) -> Result<Vec<f32>, &'static str> {
        if samples.iter().any(|sample| !sample.is_finite()) {
            return Err("invalid device audio");
        }
        self.received += samples.len();
        self.pending.extend(samples);
        self.process()
    }

    fn process(&mut self) -> Result<Vec<f32>, &'static str> {
        let mut output = Vec::new();
        while self.pending.len() >= self.resampler.input_frames_next() {
            let input: Vec<f32> = self
                .pending
                .drain(..self.resampler.input_frames_next())
                .collect();
            let result = self
                .resampler
                .process(&[input], None)
                .map_err(|_| "audio resampling failed")?;
            let discarded = self.skip.min(result[0].len());
            self.skip -= discarded;
            output.extend_from_slice(&result[0][discarded..]);
        }
        self.emitted += output.len();
        Ok(output)
    }

    pub fn finish(&mut self) -> Result<Vec<f32>, &'static str> {
        let remaining =
            (self.received * self.output_rate / self.input_rate).saturating_sub(self.emitted);
        let mut output = Vec::new();
        while output.len() < remaining {
            self.pending.resize(self.resampler.input_frames_next(), 0.0);
            output.extend(self.process()?);
        }
        output.truncate(remaining);
        Ok(output)
    }
}

pub fn pcm16(samples: &[f32]) -> Vec<u8> {
    samples
        .iter()
        .flat_map(|sample| {
            let clamped = sample.clamp(-1.0, 1.0);
            let value = (clamped * if clamped < 0.0 { 32768.0 } else { 32767.0 }).round() as i16;
            value.to_le_bytes()
        })
        .collect()
}

pub struct Processor {
    capture: Converter,
    render: Converter,
    near: VecDeque<f32>,
    far: VecDeque<f32>,
    pending: Vec<f32>,
    apm: AudioProcessing,
    encoder: opus::Encoder,
    render_delay_ms: i64,
}

impl Processor {
    pub fn new(input_rate: u32, output_rate: u32) -> Result<Self, &'static str> {
        let apm = AudioProcessing::builder()
            .config(sonora::Config {
                echo_canceller: Some(EchoCanceller::default()),
                noise_suppression: Some(NoiseSuppression::default()),
                gain_controller2: Some(GainController2::default()),
                ..Default::default()
            })
            .capture_config(StreamConfig::new(48_000, 1))
            .render_config(StreamConfig::new(48_000, 1))
            .build();
        Ok(Self {
            capture: Converter::new(input_rate, 48_000)?,
            render: Converter::new(output_rate, 48_000)?,
            near: VecDeque::new(),
            far: VecDeque::new(),
            pending: Vec::with_capacity(960),
            apm,
            encoder: opus::Encoder::new(48_000, opus::Channels::Mono, opus::Application::Voip)
                .map_err(|_| "could not initialize Opus encoder")?,
            render_delay_ms: 0,
        })
    }

    pub fn render(&mut self, frame: &Frame) -> Result<(), &'static str> {
        self.far
            .extend(self.render.push(&frame.samples[..frame.len])?);
        let now = Instant::now();
        self.render_delay_ms = if frame.at > now {
            frame.at.duration_since(now).as_millis() as i64
        } else {
            -(now.duration_since(frame.at).as_millis() as i64)
        };
        while self.far.len() >= BLOCK {
            let input: Vec<f32> = self.far.drain(..BLOCK).collect();
            let mut out = [0.0; BLOCK];
            self.apm
                .process_render_f32(&[&input], &mut [&mut out])
                .map_err(|_| "echo reference processing failed")?;
        }
        Ok(())
    }

    pub fn capture(&mut self, frame: &Frame) -> Result<Vec<Vec<u8>>, &'static str> {
        let age = Instant::now().saturating_duration_since(frame.at);
        if age > Duration::from_millis(500) {
            return Err("microphone processing fell behind");
        }
        self.apm
            .set_stream_delay_ms(
                (age.as_millis() as i64 + self.render_delay_ms).clamp(0, 500) as i32,
            )
            .map_err(|_| "invalid echo processing delay")?;
        self.near
            .extend(self.capture.push(&frame.samples[..frame.len])?);
        let mut packets = Vec::new();
        while self.near.len() >= BLOCK {
            let input: Vec<f32> = self.near.drain(..BLOCK).collect();
            let mut output = [0.0; BLOCK];
            self.apm
                .process_capture_f32(&[&input], &mut [&mut output])
                .map_err(|_| "microphone processing failed")?;
            self.pending.extend_from_slice(&output);
            if self.pending.len() == 960 {
                let mut packet = vec![0; 1275];
                let len = self
                    .encoder
                    .encode_float(&self.pending, &mut packet)
                    .map_err(|_| "Opus encoding failed")?;
                packet.truncate(len);
                packets.push(packet);
                self.pending.clear();
            }
        }
        Ok(packets)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn pcm_is_signed_little_endian_and_clipped() {
        let result = pcm16(&[-2.0, -1.0, 0.0, 1.0, 2.0]);
        assert_eq!(result, [0, 128, 0, 128, 0, 0, 255, 127, 255, 127]);
    }
    #[test]
    fn resampling_flushes_partial_final_audio() {
        for rate in [8_000, 16_000, 44_100, 48_000, 96_000] {
            let mut converter = Converter::new(rate, 16_000).unwrap();
            let input = vec![0.2; rate as usize / 3];
            let mut result = Vec::new();
            for chunk in input.chunks(137) {
                result.extend(converter.push(chunk).unwrap());
            }
            result.extend(converter.finish().unwrap());
            assert_eq!(result.len(), input.len() * 16_000 / rate as usize);
            assert!(result.iter().all(|sample| sample.is_finite()));
            assert!((result[result.len() / 2] - 0.2).abs() < 0.01);
        }
    }
}
