use serde::Deserialize;
use serde_json::Value;
use std::io::{self, Read, Write};
use std::sync::mpsc::{self, SyncSender};

pub const MAX_FRAME: usize = 128 * 1024;
pub const JSON: u8 = 1;
pub const PCM: u8 = 2;

#[derive(Debug, Deserialize)]
#[serde(
    tag = "type",
    rename_all = "camelCase",
    rename_all_fields = "camelCase"
)]
pub enum Command {
    Hello {
        protocol: u32,
    },
    Record {
        max_audio_bytes: u64,
        max_duration_seconds: u64,
    },
    Finish {},
    CreateOffer {},
    Answer {
        sdp: String,
    },
    Mute {
        muted: bool,
    },
    Close {},
}

pub fn read_command(reader: &mut impl Read) -> io::Result<Option<Command>> {
    let mut length = [0; 4];
    match reader.read_exact(&mut length[..1]) {
        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        result => result?,
    }
    reader.read_exact(&mut length[1..])?;
    let length = u32::from_be_bytes(length) as usize;
    if !(2..=MAX_FRAME).contains(&length) {
        return Err(io::Error::other("invalid frame length"));
    }
    let mut payload = vec![0; length];
    reader.read_exact(&mut payload)?;
    if payload[0] != JSON {
        return Err(io::Error::other("expected JSON command"));
    }
    serde_json::from_slice(&payload[1..])
        .map(Some)
        .map_err(|_| io::Error::other("invalid command"))
}

pub fn frame(kind: u8, payload: &[u8]) -> io::Result<Vec<u8>> {
    let size = payload.len() + 1;
    if size > MAX_FRAME {
        return Err(io::Error::other("output frame too large"));
    }
    let mut result = Vec::with_capacity(size + 4);
    result.extend_from_slice(&(size as u32).to_be_bytes());
    result.push(kind);
    result.extend_from_slice(payload);
    Ok(result)
}

// A stalled parent must never block audio processing. Queue saturation ends the session.
#[derive(Clone)]
pub struct Output {
    sender: SyncSender<Vec<u8>>,
}

impl Output {
    pub fn start() -> (Self, mpsc::Receiver<()>) {
        Self::spawn(io::stdout())
    }

    fn spawn(mut writer: impl Write + Send + 'static) -> (Self, mpsc::Receiver<()>) {
        let (sender, receiver) = mpsc::sync_channel::<Vec<u8>>(32);
        let (done, finished) = mpsc::channel();
        std::thread::spawn(move || {
            // An empty buffer is never a frame: it is finish's stop request.
            for bytes in receiver.iter().take_while(|bytes| !bytes.is_empty()) {
                if writer
                    .write_all(&bytes)
                    .and_then(|_| writer.flush())
                    .is_err()
                {
                    break;
                }
            }
            // Later events from remaining clones fail instead of being buffered.
            drop(receiver);
            let _ = done.send(());
        });
        (Self { sender }, finished)
    }

    /// Stops the writer after already queued events, even while clones remain.
    pub fn finish(self) {
        let _ = self.sender.try_send(Vec::new());
    }

    pub fn event(&self, value: Value) -> Result<(), &'static str> {
        self.send(
            JSON,
            &serde_json::to_vec(&value).map_err(|_| "cannot encode event")?,
        )
    }

    pub fn send(&self, kind: u8, bytes: &[u8]) -> Result<(), &'static str> {
        let frame = frame(kind, bytes).map_err(|_| "invalid output frame")?;
        self.sender
            .try_send(frame)
            .map_err(|_| "parent is not reading audio events")
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::sync::{Arc, Mutex};
    use std::time::Duration;

    #[derive(Clone, Default)]
    struct Captured(Arc<Mutex<Vec<u8>>>);

    impl Write for Captured {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn finish_flushes_and_stops_the_writer_while_clones_remain() {
        let written = Captured::default();
        let (output, finished) = Output::spawn(written.clone());
        let reader = output.clone(); // The stdin reader keeps a clone for fatal input errors.
        output.event(json!({"type":"closed"})).unwrap();
        output.finish();
        finished
            .recv_timeout(Duration::from_secs(1))
            .expect("finish must not wait for every clone to drop");
        assert_eq!(
            *written.0.lock().unwrap(),
            frame(JSON, br#"{"type":"closed"}"#).unwrap()
        );
        assert!(
            reader
                .event(json!({"type":"error","message":"late"}))
                .is_err()
        );
    }

    #[test]
    fn framing_and_eof() {
        let bytes = frame(JSON, br#"{"type":"hello","protocol":1}"#).unwrap();
        assert_eq!(
            u32::from_be_bytes(bytes[..4].try_into().unwrap()) as usize,
            bytes.len() - 4
        );
        assert!(matches!(
            read_command(&mut bytes.as_slice()).unwrap(),
            Some(Command::Hello { protocol: 1 })
        ));
        assert!(read_command(&mut [].as_slice()).unwrap().is_none());
    }

    #[test]
    fn rejects_malformed_frames_and_unknown_commands() {
        for bytes in [
            vec![0],
            (MAX_FRAME as u32 + 1).to_be_bytes().to_vec(),
            vec![0, 0, 0, 0],
            frame(PCM, b"{}").unwrap(),
            frame(JSON, br#"{"type":"unknown"}"#).unwrap(),
        ] {
            assert!(read_command(&mut bytes.as_slice()).is_err());
        }
        assert!(frame(PCM, &vec![0; MAX_FRAME]).is_err());
    }
}
