use serde_json::{Value, json};
use std::io::{Read, Write};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

fn frame(value: Value) -> Vec<u8> {
    let payload = serde_json::to_vec(&value).unwrap();
    let mut frame = ((payload.len() + 1) as u32).to_be_bytes().to_vec();
    frame.push(1);
    frame.extend(payload);
    frame
}

// Every command in this suite is device- and network-free, including --version.
fn run(input: &[u8]) -> (bool, Vec<Value>) {
    let mut child = Command::new(env!("CARGO_BIN_EXE_kodelet-audio"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child.stdin.take().unwrap().write_all(input).unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    let status = loop {
        if let Some(status) = child.try_wait().unwrap() {
            break status;
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            panic!("helper did not release its process on EOF");
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    let mut bytes = Vec::new();
    child
        .stdout
        .take()
        .unwrap()
        .read_to_end(&mut bytes)
        .unwrap();
    let mut stderr = String::new();
    child
        .stderr
        .take()
        .unwrap()
        .read_to_string(&mut stderr)
        .unwrap();
    assert!(stderr.is_empty(), "unexpected stderr: {stderr}");
    let mut events = Vec::new();
    let mut remaining = bytes.as_slice();
    while !remaining.is_empty() {
        assert!(remaining.len() >= 5);
        let len = u32::from_be_bytes(remaining[..4].try_into().unwrap()) as usize;
        assert!((2..=128 * 1024).contains(&len));
        assert!(remaining.len() >= 4 + len);
        assert_eq!(remaining[4], 1);
        events.push(serde_json::from_slice(&remaining[5..4 + len]).unwrap());
        remaining = &remaining[4 + len..];
    }
    (status.success(), events)
}

#[test]
fn handshake_close_and_eof_are_hardware_free() {
    let hello = frame(json!({"type":"hello","protocol":1,"muted":false}));
    let mut input = hello.clone();
    input.extend(frame(json!({"type":"close"})));
    let (success, events) = run(&input);
    assert!(success);
    assert_eq!(
        events,
        [
            json!({"type":"ready","protocol":1}),
            json!({"type":"closed"})
        ]
    );
    let (success, events) = run(&hello);
    assert!(success);
    assert_eq!(events, [json!({"type":"ready","protocol":1})]);
}

#[test]
fn rejects_wrong_version_invalid_lifecycle_and_oversized_frames() {
    let (success, events) = run(&frame(json!({"type":"hello","protocol":2})));
    assert!(!success);
    assert_eq!(events.len(), 1);
    assert_eq!(events[0]["type"], "error");
    for command in [
        frame(json!({"type":"finish"})),
        frame(json!({"type":"mute","muted":true})),
        frame(json!({"type":"record","maxAudioBytes":1,"maxDurationSeconds":1})),
        (128 * 1024 + 1_u32).to_be_bytes().to_vec(),
    ] {
        let mut input = frame(json!({"type":"hello","protocol":1}));
        input.extend(command);
        let (success, events) = run(&input);
        assert!(!success);
        assert_eq!(events.len(), 2);
        assert_eq!(events[1]["type"], "error");
    }
}

#[test]
fn version_needs_no_devices() {
    let output = Command::new(env!("CARGO_BIN_EXE_kodelet-audio"))
        .arg("--version")
        .output()
        .unwrap();
    assert!(output.status.success());
    assert!(
        String::from_utf8(output.stdout)
            .unwrap()
            .contains("protocol 1")
    );
    assert!(output.stderr.is_empty());
}
