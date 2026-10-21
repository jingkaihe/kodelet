use crate::devices::Buffers;
use gst::prelude::*;
use gstreamer as gst;
use std::sync::Arc;
use std::sync::atomic::Ordering;

pub struct Playout {
    pipeline: gst::Pipeline,
    source: gstreamer_app::AppSrc,
}

pub fn initialize() -> Result<(), &'static str> {
    gst::init().map_err(|_| "GStreamer initialization failed")
}

impl Playout {
    pub fn new(rate: u32, buffers: Arc<Buffers>) -> Result<Self, &'static str> {
        initialize()?;
        let source = gstreamer_app::AppSrc::builder()
            .is_live(true)
            .format(gst::Format::Time)
            .block(false)
            .max_buffers(64)
            .max_bytes(256 * 1024)
            .caps(
                &gst::Caps::builder("application/x-rtp")
                    .field("media", "audio")
                    .field("encoding-name", "OPUS")
                    .field("clock-rate", 48000i32)
                    .field("payload", 111i32) // The sole codec offered by our WebRTC peer.
                    .build(),
            )
            .build();
        let jitter = gst::ElementFactory::make("rtpjitterbuffer")
            .property("latency", 60u32)
            .property("drop-on-latency", true)
            .property("do-lost", true)
            .build()
            .map_err(|_| "GStreamer RTP plugin is missing")?;
        let depay = element("rtpopusdepay")?;
        let decoder = gst::ElementFactory::make("opusdec")
            .property("plc", true)
            .build()
            .map_err(|_| "GStreamer Opus plugin is missing")?;
        let convert = element("audioconvert")?;
        let resample = element("audioresample")?;
        let sink_buffers = buffers.clone();
        let sink = gstreamer_app::AppSink::builder()
            .sync(true)
            .max_buffers(4)
            .drop(true)
            .caps(
                &gst::Caps::builder("audio/x-raw")
                    .field("format", "F32LE")
                    .field("layout", "interleaved")
                    .field("channels", 1i32)
                    .field("rate", rate as i32)
                    .build(),
            )
            .callbacks(
                gstreamer_app::AppSinkCallbacks::builder()
                    .new_sample(move |sink| {
                        let sample = sink.pull_sample().map_err(|_| gst::FlowError::Eos)?;
                        let buffer = sample.buffer().ok_or(gst::FlowError::Error)?;
                        let map = buffer.map_readable().map_err(|_| gst::FlowError::Error)?;
                        for bytes in map.as_slice().as_chunks::<4>().0 {
                            let sample = f32::from_le_bytes(*bytes);
                            if !sample.is_finite() || sink_buffers.playback.push(sample).is_err() {
                                sink_buffers.failed.store(true, Ordering::Release);
                                return Err(gst::FlowError::Error);
                            }
                        }
                        Ok(gst::FlowSuccess::Ok)
                    })
                    .build(),
            )
            .build();
        let pipeline = gst::Pipeline::new();
        let elements = [
            source.upcast_ref(),
            &jitter,
            &depay,
            &decoder,
            &convert,
            &resample,
            sink.upcast_ref(),
        ];
        pipeline
            .add_many(elements)
            .map_err(|_| "could not assemble audio playback")?;
        gst::Element::link_many(elements).map_err(|_| "could not connect audio playback")?;
        pipeline
            .bus()
            .ok_or("audio playback bus unavailable")?
            .set_sync_handler(move |_, message| {
                #[cfg(test)]
                if let gst::MessageView::Error(error) = message.view() {
                    eprintln!(
                        "GStreamer test pipeline: {} ({:?})",
                        error.error(),
                        error.debug()
                    );
                }
                if matches!(
                    message.view(),
                    gst::MessageView::Error(_)
                        | gst::MessageView::Eos(_)
                        | gst::MessageView::ClockLost(_)
                ) {
                    buffers.failed.store(true, Ordering::Release);
                }
                gst::BusSyncReply::Drop
            });
        pipeline
            .set_state(gst::State::Playing)
            .map_err(|_| "could not start audio playback")?;
        Ok(Self { pipeline, source })
    }

    pub fn push(&self, bytes: Vec<u8>) -> Result<(), &'static str> {
        if self.source.current_level_buffers() >= 64
            || self.source.current_level_bytes() >= 256 * 1024
        {
            return Err("audio playback fell behind");
        }
        let mut buffer = gst::Buffer::from_mut_slice(bytes);
        buffer
            .get_mut()
            .ok_or("invalid audio packet")?
            .set_pts(self.pipeline.current_running_time());
        self.source
            .push_buffer(buffer)
            .map_err(|_| "audio playback failed")?;
        Ok(())
    }
}

fn element(name: &str) -> Result<gst::Element, &'static str> {
    gst::ElementFactory::make(name)
        .build()
        .map_err(|_| "a required GStreamer audio plugin is missing")
}

impl Drop for Playout {
    fn drop(&mut self) {
        let _ = self.pipeline.set_state(gst::State::Null);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::devices::{BLOCK, Frame};
    use crate::processing::Processor;
    use std::time::{Duration, Instant};

    #[test]
    fn synthetic_signal_roundtrips_processing_opus_and_gstreamer_without_devices() {
        let buffers = Arc::new(Buffers::new());
        let playout = Playout::new(48_000, buffers.clone()).unwrap();
        let mut processor = Processor::new(48_000, 48_000).unwrap();
        let mut sequence = 0u16;
        for block in 0..30 {
            let mut frame = Frame {
                samples: [0.0; BLOCK],
                len: BLOCK,
                at: Instant::now(),
                generation: 0,
            };
            processor.render(&frame).unwrap();
            for (i, sample) in frame.samples.iter_mut().enumerate() {
                *sample = 0.2
                    * (std::f32::consts::TAU * 440.0 * (block * BLOCK + i) as f32 / 48_000.0).sin();
            }
            for payload in processor.capture(&frame).unwrap() {
                // Minimal RTP v2, Opus payload type 111, continuous 20 ms timestamps.
                let mut packet = vec![0x80, 111];
                packet.extend_from_slice(&sequence.to_be_bytes());
                packet.extend_from_slice(&(u32::from(sequence) * 960).to_be_bytes());
                packet.extend_from_slice(&1u32.to_be_bytes());
                packet.extend_from_slice(&payload);
                playout.push(packet).unwrap();
                sequence += 1;
            }
            std::thread::sleep(Duration::from_millis(10));
        }
        let deadline = Instant::now() + Duration::from_secs(2);
        while buffers.playback.len() < 4800 && Instant::now() < deadline {
            buffers.check().unwrap();
            std::thread::sleep(Duration::from_millis(10));
        }
        buffers.check().unwrap();
        assert!(
            buffers.playback.len() >= 4800,
            "no decoded audio from native plugins"
        );
        let mut peak = 0.0_f32;
        while let Some(sample) = buffers.playback.pop() {
            assert!(sample.is_finite());
            peak = peak.max(sample.abs());
        }
        assert!(peak > 0.001, "signal unexpectedly became silent");
    }
}
