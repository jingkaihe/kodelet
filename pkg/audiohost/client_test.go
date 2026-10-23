package audiohost

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test executable doubles as the helper through a kodelet-audio symlink in
// PATH, without test-only process arguments or production hooks.
func TestMain(m *testing.M) {
	if mode := os.Getenv("KODELET_AUDIOHOST_TEST_FIXTURE"); mode != "" {
		if err := fixture(mode); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(42)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixture(mode string) error {
	if path := os.Getenv("KODELET_AUDIOHOST_TEST_PID"); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return err
		}
	}
	command, err := fixtureCommand()
	if err != nil {
		return err
	}
	if command.Type != "hello" || command.Protocol != 1 {
		return errors.New("expected hello protocol 1")
	}
	switch mode {
	case "handshake-stall":
		time.Sleep(time.Minute)
		return nil
	case "handshake-mismatch":
		return fixtureEvent(Event{Type: "ready", Protocol: 2})
	case "handshake-audio":
		_, err := os.Stdout.Write(fixtureFrame(pcmFrame, []byte{0, 0}))
		return err
	case "handshake-eof":
		return nil
	case "handshake-error":
		return fixtureEvent(Event{Type: "error", Message: "Audio runtime unavailable."})
	}
	if err := fixtureEvent(Event{Type: "ready", Protocol: 1}); err != nil {
		return err
	}
	if mode == "no-read" {
		// Send an offer after the parent has admitted createOffer, without
		// reading any stdin, to exercise a blocked large answer write.
		for {
			if _, err := os.Stat(os.Getenv("KODELET_AUDIOHOST_TEST_TRIGGER")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if err := fixtureEvent(Event{Type: "offer", SDP: "offer"}); err != nil {
			return err
		}
		time.Sleep(time.Minute)
		return nil
	}
	finished := false
	for {
		command, err := fixtureCommand()
		if err != nil {
			return err
		}
		switch command.Type {
		case "record":
			switch mode {
			case "crash":
				return errors.New("private-native-diagnostic")
			case "eof":
				return nil
			case "unexpected":
				return fixtureEvent(Event{Type: "connected"})
			case "error":
				return fixtureEvent(Event{Type: "error", Message: "Microphone unavailable."})
			case "malformed":
				_, err := os.Stdout.Write([]byte{0xff, 0xff, 0xff, 0xff})
				return err
			}
			if err := fixtureEvent(Event{Type: "recording"}); err != nil {
				return err
			}
			count := 1
			if mode == "overflow" {
				count = eventQueueSize * 2
			}
			for i := range count {
				frame := fixtureFrame(pcmFrame, []byte{byte(i), 0, 0xff, 0x7f})
				if _, err := os.Stdout.Write(frame); err != nil {
					return err
				}
			}
		case "finish":
			if finished {
				continue
			}
			finished = true
			if err := fixtureEvent(Event{Type: "recorded"}); err != nil {
				return err
			}
		case "createOffer":
			if err := fixtureEvent(Event{Type: "offer", SDP: "v=0\r\no=fixture\r\n"}); err != nil {
				return err
			}
		case "answer":
			if command.SDP == "" {
				return errors.New("empty answer")
			}
			if err := fixtureEvent(Event{Type: "connected"}); err != nil {
				return err
			}
		case "mute":
			if err := fixtureEvent(Event{Type: "muted", Muted: command.Muted}); err != nil {
				return err
			}
		case "close":
			if mode == "close-stall" {
				time.Sleep(time.Minute)
			}
			if mode == "close-slow" {
				// Like a helper closing its WebRTC peer within its own one-second bound.
				time.Sleep(1500 * time.Millisecond)
			}
			if err := fixtureEvent(Event{Type: "closed"}); err != nil {
				return err
			}
			if mode == "exit-stall" {
				time.Sleep(time.Minute)
			}
			return nil
		default:
			return errors.New("unexpected command")
		}
	}
}

func fixtureCommand() (Command, error) {
	var command Command
	var header [4]byte
	if _, err := io.ReadFull(os.Stdin, header[:]); err != nil {
		return command, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 1 || size > maxFrameBytes {
		return command, errors.New("invalid fixture input frame size")
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(os.Stdin, frame); err != nil {
		return command, err
	}
	if frame[0] != jsonFrame {
		return command, errors.New("commands must be JSON frames")
	}
	err := json.Unmarshal(frame[1:], &command)
	return command, err
}

func fixtureFrame(kind byte, payload []byte) []byte {
	frame := make([]byte, len(payload)+5)
	binary.BigEndian.PutUint32(frame, uint32(len(payload)+1))
	frame[4] = kind
	copy(frame[5:], payload)
	return frame
}

func fixtureEvent(event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(fixtureFrame(jsonFrame, payload))
	return err
}

func installFixtureHelper(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, os.Symlink(executable, filepath.Join(directory, helperName)))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return directory
}

var recordCommand = Command{Type: "record", MaxAudioBytes: 32000, MaxDurationSeconds: 1}

func setupFixture(t *testing.T, mode string) string {
	t.Helper()
	installFixtureHelper(t)
	t.Setenv("KODELET_AUDIOHOST_TEST_FIXTURE", mode)
	pidPath := filepath.Join(t.TempDir(), "pid")
	t.Setenv("KODELET_AUDIOHOST_TEST_PID", pidPath)
	// Avoid the race detector's default one-second sleep delaying every child exit.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	return pidPath
}

func startFixture(t *testing.T, mode string) *Client {
	t.Helper()
	setupFixture(t, mode)
	c, err := Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func nextEvent(t *testing.T, c *Client) Event {
	t.Helper()
	select {
	case event, ok := <-c.Events():
		require.True(t, ok, "event stream closed")
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for audio event")
		return Event{}
	}
}

func awaitExit(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("audio helper did not terminate")
	}
	require.NotNil(t, c.cmd.ProcessState, "child must have been waited on")
	assert.ErrorIs(t, c.cmd.Process.Signal(syscall.Signal(0)), os.ErrProcessDone)
}

func assertReapedPID(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	var status syscall.WaitStatus
	_, err = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	assert.ErrorIs(t, err, syscall.ECHILD, "Start must reap failed startup children")
}

func TestFraming(t *testing.T) {
	frame, err := encodeCommand(Command{Type: "answer", SDP: "v=0\r\n"})
	require.NoError(t, err)
	assert.Equal(t, uint32(len(frame)-4), binary.BigEndian.Uint32(frame[:4]))
	assert.Equal(t, byte(jsonFrame), frame[4])
	assert.JSONEq(t, `{"type":"answer","sdp":"v=0\r\n","muted":false}`, string(frame[5:]))

	for _, data := range [][]byte{
		fixtureFrame(jsonFrame, []byte(`{"type":"offer","sdp":"v=0"}`)),
		fixtureFrame(pcmFrame, []byte{0, 0, 0xff, 0x7f}),
		fixtureFrame(pcmFrame, make([]byte, maxFrameBytes-2)),
	} {
		// One-byte reads verify streaming, not just whole-buffer parsing.
		event, err := readEvent(&fragmentedReader{data: data})
		require.NoError(t, err)
		if data[4] == pcmFrame {
			assert.Equal(t, "audio", event.Type)
			assert.Equal(t, data[5:], event.Audio)
		} else {
			assert.Equal(t, "v=0", event.SDP)
		}
	}
	_, err = encodeCommand(Command{Type: "answer", SDP: strings.Repeat("s", maxSDPBytes)})
	require.NoError(t, err)
	_, err = encodeCommand(Command{Type: "answer", SDP: strings.Repeat("s", maxSDPBytes+1)})
	assert.ErrorContains(t, err, "SDP")
	_, err = encodeCommand(Command{Type: strings.Repeat("x", maxFrameBytes)})
	assert.ErrorContains(t, err, "frame size")
}

type fragmentedReader struct{ data []byte }

func (r *fragmentedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data[:1])
	r.data = r.data[n:]
	return n, nil
}

func TestRejectMalformedFrames(t *testing.T) {
	tests := map[string][]byte{
		"empty":            nil,
		"truncated header": {0, 0, 0},
		"zero size":        {0, 0, 0, 0},
		"hostile size":     {0xff, 0xff, 0xff, 0xff},
		"missing kind":     {0, 0, 0, 1},
		"truncated PCM":    {0, 0, 0, 5, pcmFrame, 0, 0},
		"unknown kind":     fixtureFrame(99, []byte{0, 0}),
		"odd PCM":          fixtureFrame(pcmFrame, []byte{0}),
		"empty PCM":        fixtureFrame(pcmFrame, nil),
		"empty JSON":       fixtureFrame(jsonFrame, nil),
		"null JSON":        fixtureFrame(jsonFrame, []byte(`null`)),
		"invalid JSON":     fixtureFrame(jsonFrame, []byte(`{private-native-diagnostic`)),
		"two JSON values":  fixtureFrame(jsonFrame, []byte(`{"type":"closed"} {}`)),
		"missing type":     fixtureFrame(jsonFrame, []byte(`{}`)),
		"JSON audio":       fixtureFrame(jsonFrame, []byte(`{"type":"audio"}`)),
		"oversized SDP": fixtureFrame(jsonFrame, []byte(
			`{"type":"offer","sdp":"`+strings.Repeat("x", maxSDPBytes+1)+`"}`,
		)),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := readEvent(bytes.NewReader(input))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-native-diagnostic")
		})
	}
}

func TestRecordingAndRealtimeModes(t *testing.T) {
	c := startFixture(t, "normal")
	assert.Empty(t, c.events, "Start consumes ready and sends no device command")
	assert.Error(t, c.Send(Command{Type: "finish"}))
	assert.Error(t, c.Send(Command{Type: "hello", Protocol: 1}))
	for _, invalid := range []Command{
		{Type: "record", MaxAudioBytes: -1, MaxDurationSeconds: 1},
		{Type: "record", MaxDurationSeconds: 1},
		{Type: "record", MaxAudioBytes: 1, MaxDurationSeconds: 1},
		{Type: "record", MaxAudioBytes: 32000},
	} {
		assert.Error(t, c.Send(invalid), "the helper would reject %+v", invalid)
	}
	require.NoError(t, c.Send(recordCommand))
	assert.Equal(t, "recording", nextEvent(t, c).Type)
	assert.Equal(t, Event{Type: "audio", Audio: []byte{0, 0, 0xff, 0x7f}}, nextEvent(t, c))
	assert.Error(t, c.Send(recordCommand))
	assert.Error(t, c.Send(Command{Type: "createOffer"}))
	require.NoError(t, c.Send(Command{Type: "finish"}))
	assert.Equal(t, "recorded", nextEvent(t, c).Type)
	require.NoError(t, c.Send(Command{Type: "finish"}), "a late finish is harmless")
	assert.Error(t, c.Send(Command{Type: "createOffer"}), "one mode per helper process")
	require.NoError(t, c.Close())

	c = startFixture(t, "normal")
	require.NoError(t, c.Send(Command{Type: "createOffer"}))
	assert.Equal(t, Event{Type: "offer", SDP: "v=0\r\no=fixture\r\n"}, nextEvent(t, c))
	assert.Error(t, c.Send(Command{Type: "answer"}))
	assert.Error(t, c.Send(Command{Type: "answer", SDP: strings.Repeat("x", maxSDPBytes+1)}))
	require.NoError(t, c.Send(Command{Type: "answer", SDP: "v=0\r\n"}))
	assert.Equal(t, "connected", nextEvent(t, c).Type)
	for _, muted := range []bool{true, false} {
		require.NoError(t, c.Send(Command{Type: "mute", Muted: muted}))
		assert.Equal(t, Event{Type: "muted", Muted: muted}, nextEvent(t, c))
	}
	assert.Error(t, c.Send(recordCommand))
	require.NoError(t, c.Close())
	assert.Equal(t, "closed", nextEvent(t, c).Type)
	_, ok := <-c.Events()
	assert.False(t, ok)
	assert.ErrorContains(t, c.Send(Command{Type: "mute"}), "closed")
	assert.NoError(t, c.Close())
	awaitExit(t, c)
}

func TestHandshakeFailureReapsChild(t *testing.T) {
	for _, mode := range []string{
		"handshake-mismatch",
		"handshake-audio",
		"handshake-eof",
		"handshake-error",
		"handshake-stall",
	} {
		t.Run(mode, func(t *testing.T) {
			pid := setupFixture(t, mode)
			c, err := Start(t.Context())
			require.Error(t, err)
			assert.Nil(t, c)
			if mode == "handshake-stall" {
				assert.ErrorContains(t, err, "handshake timed out")
			}
			if mode == "handshake-error" {
				assert.ErrorContains(t, err, "Audio runtime unavailable")
			}
			assertReapedPID(t, pid)
		})
	}
}

func TestContextCancellation(t *testing.T) {
	t.Run("startup", func(t *testing.T) {
		pid := setupFixture(t, "handshake-stall")
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		result := make(chan error, 1)
		go func() {
			_, err := Start(ctx)
			result <- err
		}()
		require.Eventually(t, func() bool {
			_, err := os.Stat(pid)
			return err == nil
		}, 5*time.Second, time.Millisecond)
		cancel()
		select {
		case err := <-result:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("startup did not cancel")
		}
		assertReapedPID(t, pid)
	})
	t.Run("active", func(t *testing.T) {
		setupFixture(t, "normal")
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		c, err := Start(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Send(recordCommand))
		cancel()
		awaitExit(t, c)
		assert.ErrorIs(t, c.Close(), context.Canceled)
		var last Event
		for event := range c.Events() {
			last = event
		}
		assert.Equal(t, "error", last.Type)
		assert.Contains(t, last.Message, "context canceled")
	})
}

func TestProcessAndProtocolFailures(t *testing.T) {
	for _, mode := range []string{"crash", "eof", "error", "malformed", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			c := startFixture(t, mode)
			require.NoError(t, c.Send(recordCommand))
			event := nextEvent(t, c)
			assert.Equal(t, "error", event.Type)
			assert.NotEmpty(t, event.Message)
			assert.NotContains(t, event.Message, "private-native-diagnostic")
			if mode == "error" {
				assert.Equal(t, "Microphone unavailable.", event.Message)
			}
			awaitExit(t, c)
			assert.Error(t, c.Close())
			_, ok := <-c.Events()
			assert.False(t, ok, "only one terminal error")
		})
	}
}

func TestEventOverflowIsExplicitAndPreservesQueuedAudio(t *testing.T) {
	c := startFixture(t, "overflow")
	require.NoError(t, c.Send(recordCommand))
	awaitExit(t, c) // Deliberately do not consume events until overflow.
	assert.ErrorContains(t, c.Close(), "queue overflow")
	assert.Equal(t, "recording", nextEvent(t, c).Type)
	for i := range eventQueueSize - 1 {
		event := nextEvent(t, c)
		assert.Equal(t, "audio", event.Type)
		assert.Equal(t, []byte{byte(i), 0, 0xff, 0x7f}, event.Audio)
	}
	assert.Contains(t, nextEvent(t, c).Message, "queue overflow")
	_, ok := <-c.Events()
	assert.False(t, ok)
}

func TestCloseIsConcurrentAndBounded(t *testing.T) {
	for _, mode := range []string{"normal", "close-slow", "close-stall", "exit-stall"} {
		t.Run(mode, func(t *testing.T) {
			c := startFixture(t, mode)
			results := make(chan error, 8)
			for range cap(results) {
				go func() { results <- c.Close() }()
			}
			awaitExit(t, c)
			for range cap(results) {
				err := <-results
				if mode == "normal" || mode == "close-slow" {
					assert.NoError(t, err)
				} else {
					require.Error(t, err)
					assert.Equal(t, c.result(), err)
					assert.NotContains(t, err.Error(), "private-native-diagnostic")
				}
			}
		})
	}
}

func TestCloseInterruptsBlockedSend(t *testing.T) {
	trigger := filepath.Join(t.TempDir(), "offer")
	t.Setenv("KODELET_AUDIOHOST_TEST_TRIGGER", trigger)
	c := startFixture(t, "no-read")
	require.NoError(t, c.Send(Command{Type: "createOffer"}))
	require.NoError(t, os.WriteFile(trigger, nil, 0o600))
	assert.Equal(t, "offer", nextEvent(t, c).Type)
	result := make(chan error, 1)
	go func() {
		// JSON escaping makes this larger than a 64 KiB pipe, while remaining
		// within both the decoded SDP limit and the encoded frame limit.
		result <- c.Send(Command{Type: "answer", SDP: strings.Repeat(`"`, maxSDPBytes-128)})
	}()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.mode == "connecting"
	}, time.Second, time.Millisecond)
	require.NoError(t, c.Send(Command{Type: "close"}))
	awaitExit(t, c)
	assert.Error(t, <-result)
	assert.Error(t, c.Close())
}

func TestHelperPaths(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := Start(t.Context())
		assert.ErrorContains(t, err, "kodelet-audio in PATH")
		assert.ErrorContains(t, err, "mise run audio-install")
	})
	t.Run("first PATH match", func(t *testing.T) {
		setupFixture(t, "normal")
		first := installFixtureHelper(t)
		path, err := helperPath()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(first, helperName), path)
		c, err := Start(t.Context())
		require.NoError(t, err)
		assert.NoError(t, c.Close())
	})
	t.Run("relative PATH entries are ignored", func(t *testing.T) {
		directory := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(directory, helperName),
			[]byte("#!/bin/sh\n"),
			0o755,
		))
		t.Chdir(directory)
		t.Setenv("PATH", ".")
		_, err := helperPath()
		assert.ErrorContains(t, err, "kodelet-audio in PATH")
	})
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := Start(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
