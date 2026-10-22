package audiohost

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const (
	helperName      = "kodelet-audio"
	startupTimeout  = 5 * time.Second
	shutdownTimeout = time.Second
	writeTimeout    = 3 * time.Second
	eventQueueSize  = 32

	// A graceful close may spend one second closing the peer and another second
	// flushing events and stopping the runtime after releasing devices.
	closeTimeout = 3 * time.Second
)

// Client owns one local helper. Drain Events while using it. An overflowing
// event queue terminates the session explicitly rather than dropping audio.
// Recording and realtime modes are mutually exclusive.
type Client struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
	events chan Event
	ready  chan error
	done   chan struct{}
	abort  chan error
	stop   chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	mode    string
	closing bool
	ended   bool
	err     error
}

// Start launches the first kodelet-audio executable in PATH, ignoring relative
// PATH entries. It never downloads a helper. Startup only exchanges hello/ready
// protocol 1; it does not open audio devices. The context owns the process until
// exit, including after Start returns. The ready event is consumed by Start, not
// exposed on Events.
func Start(ctx context.Context) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := helperPath()
	if err != nil {
		return nil, err
	}
	input, stdin, err := os.Pipe()
	if err != nil {
		return nil, errors.Wrap(err, "create audio command pipe")
	}
	stdout, output, err := os.Pipe()
	if err != nil {
		input.Close()
		stdin.Close()
		return nil, errors.Wrap(err, "create audio event pipe")
	}
	c := &Client{
		cmd:    exec.Command(path),
		stdin:  stdin,
		stdout: stdout,
		// One reserved slot guarantees delivery of a terminal error after overflow.
		events: make(chan Event, eventQueueSize+1),
		ready:  make(chan error, 1),
		done:   make(chan struct{}),
		abort:  make(chan error, 1),
		stop:   make(chan struct{}),
	}
	c.cmd.Stdin, c.cmd.Stdout = input, output
	// Drain diagnostics without retaining or displaying arbitrary native output.
	c.cmd.Stderr = io.Discard
	c.cmd.WaitDelay = shutdownTimeout
	err = c.cmd.Start()
	input.Close()
	output.Close()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, errors.Wrap(err, "start audio helper (build and install the experimental helper first)")
	}
	go c.run(ctx)
	if err := c.write(Command{Type: "hello", Protocol: protocolVersion}); err != nil {
		c.fail(err)
		<-c.done
		return nil, err
	}
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()
	select {
	case err := <-c.ready:
		if err != nil {
			<-c.done
			return nil, c.result()
		}
		if err := ctx.Err(); err != nil {
			<-c.done
			return nil, err
		}
		return c, nil
	case <-c.done:
		return nil, c.result()
	case <-timer.C:
		err := errors.New("audio helper handshake timed out")
		c.fail(err)
		<-c.done
		return nil, err
	case <-ctx.Done():
		<-c.done
		return nil, ctx.Err()
	}
}

func helperPath() (string, error) {
	// LookPath rejects matches from relative PATH entries such as ".".
	path, err := exec.LookPath(helperName)
	if err != nil {
		return "", errors.Wrap(err, "find kodelet-audio in PATH (build and install it with mise run audio-install)")
	}
	return path, nil
}

// Send serializes a control command. It returns after writing, not after the
// helper's acknowledgement; responses arrive on Events. hello is owned by Start.
// Sending close initiates shutdown; Close also waits for exit and reaping.
func (c *Client) Send(command Command) error {
	if command.Type == "close" {
		c.requestClose()
		return nil
	}
	frame, err := encodeCommand(command)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.closing || c.ended {
		c.mu.Unlock()
		return errors.New("audio helper is closed")
	}
	err = c.acceptCommand(command)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err := c.writeFrame(frame); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

// acceptCommand is called with mu held. States reserve the mode before a write,
// so fast responses cannot race command admission.
func (c *Client) acceptCommand(command Command) error {
	switch command.Type {
	case "record":
		// Mirror the helper's minimums; zero limits are also omitted from JSON.
		if c.mode == "" && command.MaxAudioBytes >= 2 && command.MaxDurationSeconds > 0 {
			c.mode = "recording"
			return nil
		}
	case "finish":
		if c.mode == "recorded" {
			return nil // A user finish can race the helper's automatic sample limit.
		}
		if c.mode == "recording" {
			c.mode = "finishing"
			return nil
		}
	case "createOffer":
		if c.mode == "" {
			c.mode = "offering"
			return nil
		}
	case "answer":
		if c.mode == "offered" && command.SDP != "" {
			c.mode = "connecting"
			return nil
		}
	case "mute":
		if c.mode == "connecting" || c.mode == "connected" {
			return nil
		}
	}
	return errors.New("audio command is invalid for the current session mode")
}

// Events returns ordered notifications and owned PCM chunks. It closes after the
// helper has been reaped. Transport failures use a final error event; Close also
// returns that failure, including cancellation or forced shutdown.
func (c *Client) Events() <-chan Event { return c.events }

// Close requests graceful shutdown, then kills a stalled child after a bounded
// deadline and waits for reaping. Concurrent/repeated calls return the same result.
func (c *Client) Close() error {
	c.requestClose()
	<-c.done
	return c.result()
}

func (c *Client) requestClose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closing && !c.ended {
		c.closing = true
		close(c.stop)
	}
}

func (c *Client) result() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) fail(err error) {
	select {
	case c.abort <- err:
	default:
	}
}

func (c *Client) write(command Command) error {
	frame, err := encodeCommand(command)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeFrame(frame)
}

func (c *Client) writeFrame(frame []byte) error {
	if err := c.stdin.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return errors.Wrap(err, "set audio command deadline")
	}
	n, err := c.stdin.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return errors.Wrap(err, "write audio command")
}

type readResult struct {
	err      error
	reported bool
}

func (c *Client) read() readResult {
	event, err := readEvent(c.stdout)
	if err == nil && event.Type == "error" {
		err = errors.New("audio helper startup failed: " + event.Message)
	}
	if err == nil && (event.Type != "ready" || event.Protocol != protocolVersion) {
		err = errors.New("incompatible audio helper handshake (expected ready protocol 1)")
	}
	c.ready <- err
	if err != nil {
		return readResult{err: err}
	}
	for {
		event, err := readEvent(c.stdout)
		if err != nil {
			return readResult{err: errors.Wrap(err, "audio helper event stream ended unexpectedly")}
		}
		c.mu.Lock()
		err = c.acceptEvent(event)
		c.mu.Unlock()
		if err != nil {
			return readResult{err: err}
		}
		if len(c.events) >= eventQueueSize {
			return readResult{err: errors.New("audio event queue overflow; recording or call aborted")}
		}
		c.events <- event
		switch event.Type {
		case "closed":
			return readResult{}
		case "error":
			return readResult{err: errors.New("audio helper reported an error: " + event.Message), reported: true}
		}
	}
}

func (c *Client) acceptEvent(event Event) error {
	switch event.Type {
	case "recording", "audio":
		if c.mode == "recording" || c.mode == "finishing" {
			return nil
		}
	case "recorded":
		if c.mode == "recording" || c.mode == "finishing" {
			c.mode = "recorded"
			return nil
		}
	case "offer":
		if c.mode == "offering" && event.SDP != "" {
			c.mode = "offered"
			return nil
		}
	case "connected":
		if c.mode == "connecting" {
			c.mode = "connected"
			return nil
		}
	case "muted":
		if c.mode == "connecting" || c.mode == "connected" {
			return nil
		}
	case "level":
		if c.mode != "" {
			return nil
		}
	case "closed":
		if c.closing {
			return nil
		}
	case "error":
		return nil
	}
	return errors.New("unexpected audio helper event for the current session mode")
}

func (c *Client) run(ctx context.Context) {
	reads := make(chan readResult, 1)
	waits := make(chan error, 1)
	go func(out chan<- readResult) { out <- c.read() }(reads)
	go func(out chan<- error) { out <- c.cmd.Wait() }(waits)
	var failure error
	var reported bool
	var timer *time.Timer
	var deadline <-chan time.Time
	var closeWritten chan struct{}
	stop, canceled := c.stop, ctx.Done()
	startDeadline := func(timeout time.Duration) {
		if timer == nil {
			timer = time.NewTimer(timeout)
			deadline = timer.C
		}
	}
	kill := func(err error) {
		if failure == nil {
			failure = err
		}
		_ = c.cmd.Process.Kill()
		c.stdin.Close()
		c.stdout.Close()
	}
	for reads != nil || waits != nil {
		select {
		case <-canceled:
			canceled = nil
			kill(ctx.Err())
		case err := <-c.abort:
			kill(err)
		case <-stop:
			stop = nil
			startDeadline(closeTimeout)
			closeWritten = make(chan struct{})
			go func() {
				defer close(closeWritten)
				if err := c.write(Command{Type: "close"}); err != nil {
					c.fail(err)
				}
			}()
		case result := <-reads:
			reads = nil
			reported = result.reported
			if result.err != nil {
				kill(result.err)
			}
			startDeadline(shutdownTimeout)
		case err := <-waits:
			waits = nil
			c.stdin.Close()
			if failure == nil && err != nil {
				failure = errors.Wrap(err, "audio helper exited")
			}
			// Drain buffered stdout before closing it, but do not wait forever for
			// a descendant that inherited a pipe.
			startDeadline(shutdownTimeout)
		case <-deadline:
			deadline = nil
			kill(errors.New("audio helper shutdown timed out"))
		}
	}
	if timer != nil {
		timer.Stop()
	}
	c.stdin.Close()
	c.stdout.Close()
	if closeWritten != nil {
		<-closeWritten
	}
	c.mu.Lock()
	c.ended, c.err = true, failure
	c.mu.Unlock()
	if failure != nil && !reported {
		c.events <- Event{Type: "error", Message: failure.Error()}
	}
	close(c.events)
	close(c.done)
}
