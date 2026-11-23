package termimage

import (
	"bytes"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const (
	// iTerm2 images are drawn once frames stop changing for settleDelay, and
	// about every maxSettleDelay while they keep changing. minDrawDelay spans a
	// couple of Bubble Tea frames, so the presented frame is on screen first.
	settleDelay    = 60 * time.Millisecond
	maxSettleDelay = 250 * time.Millisecond
	minDrawDelay   = 35 * time.Millisecond
)

var (
	altScreenEnter = []byte(ansi.SetModeAltScreenSaveCursor)
	altScreenExit  = []byte(ansi.ResetModeAltScreenSaveCursor)
	eraseScreen    = []byte(ansi.EraseEntireScreen)
)

// Output wraps the terminal that Bubble Tea renders to and interleaves image
// commands with its frames. It implements Bubble Tea's terminal file interface.
type Output struct {
	file     *os.File
	protocol Protocol

	mu       sync.Mutex
	closed   bool
	onScreen bool // inside the alternate screen, which owns the images

	// Kitty uploads are written before Bubble Tea's next output.
	queued   []byte
	uploaded map[uint32]struct{}

	// iTerm2 draws for the latest frame.
	signature  uint64
	draw       func() string
	dirty      bool
	dirtySince time.Time
	timer      *time.Timer
}

// NewOutput wraps file for protocol.
func NewOutput(file *os.File, protocol Protocol) *Output {
	return &Output{file: file, protocol: protocol, uploaded: map[uint32]struct{}{}}
}

// SetProtocol enables a protocol identified after the program started.
func (o *Output) SetProtocol(protocol Protocol) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.protocol = protocol
}

// Fd returns the terminal's file descriptor.
func (o *Output) Fd() uintptr { return o.file.Fd() }

// Read reads from the terminal.
func (o *Output) Read(p []byte) (int, error) { return o.file.Read(p) }

// Close stops pending image draws. It does not close the terminal.
func (o *Output) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	if o.timer != nil {
		o.timer.Stop()
	}
	return nil
}

// Write writes Bubble Tea output, adding pending image commands.
func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.file.Write(p)
	}

	var out []byte
	if i := bytes.Index(p, altScreenExit); i >= 0 {
		// Images belong to the alternate screen; free them before leaving it.
		out = append(out, p[:i]...)
		for id := range o.uploaded {
			out = append(out, KittyDelete(id)...)
		}
		out = append(out, p[i:]...)
		clear(o.uploaded)
		o.queued = nil
		o.onScreen, o.dirty = false, false
		if o.timer != nil {
			o.timer.Stop()
		}
	} else if i := bytes.Index(p, altScreenEnter); i >= 0 {
		i += len(altScreenEnter)
		out = append(out, p[:i]...)
		out = append(out, o.queued...)
		out = append(out, p[i:]...)
		o.queued = nil
		o.onScreen = true
		o.scheduleLocked()
	} else {
		if o.onScreen && len(o.queued) > 0 {
			out = append(o.queued, p...)
			o.queued = nil
		} else {
			out = p
		}
		if o.onScreen && bytes.Contains(p, eraseScreen) {
			o.scheduleLocked()
		}
	}
	if _, err := o.file.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Uploaded reports whether Kitty image id is on the current screen.
func (o *Output) Uploaded(id uint32) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.uploaded[id]
	return ok
}

// Upload queues commands that upload Kitty image id. They are written before
// any later frame, so placeholders never precede their image.
func (o *Output) Upload(id uint32, commands string) {
	o.mu.Lock()
	o.uploaded[id] = struct{}{}
	o.queued = append(o.queued, commands...)
	onScreen := o.onScreen
	o.mu.Unlock()
	if onScreen {
		go o.flushQueued()
	}
}

// Release deletes uploaded Kitty images.
func (o *Output) Release(ids ...uint32) {
	o.mu.Lock()
	for _, id := range ids {
		if _, ok := o.uploaded[id]; ok {
			delete(o.uploaded, id)
			o.queued = append(o.queued, KittyDelete(id)...)
		}
	}
	onScreen := o.onScreen
	o.mu.Unlock()
	if onScreen {
		go o.flushQueued()
	}
}

func (o *Output) flushQueued() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || !o.onScreen || len(o.queued) == 0 {
		return
	}
	_, _ = o.file.Write(o.queued)
	o.queued = nil
}

// Present sets the iTerm2 images of the latest frame. draw returns cursor-
// positioned drawing commands and runs once the frame has settled.
func (o *Output) Present(signature uint64, draw func() string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if signature == o.signature && o.draw != nil {
		return
	}
	o.signature, o.draw = signature, draw
	o.scheduleLocked()
}

func (o *Output) scheduleLocked() {
	if o.protocol != ITerm2 || o.closed || !o.onScreen || o.draw == nil {
		return
	}
	now := time.Now()
	if !o.dirty {
		o.dirty, o.dirtySince = true, now
	}
	delay := min(settleDelay, max(minDrawDelay, maxSettleDelay-now.Sub(o.dirtySince)))
	if o.timer == nil {
		o.timer = time.AfterFunc(delay, o.drawSettled)
		return
	}
	o.timer.Reset(delay)
}

func (o *Output) drawSettled() {
	o.mu.Lock()
	if o.closed || !o.onScreen || !o.dirty || o.draw == nil {
		o.mu.Unlock()
		return
	}
	signature, draw := o.signature, o.draw
	o.mu.Unlock()

	// Encoding may be slow, so it runs unlocked; a newer frame supersedes it.
	commands := draw()

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || !o.onScreen || o.signature != signature {
		return
	}
	o.dirty = false
	if commands == "" {
		return
	}
	_, _ = o.file.WriteString(ansi.SetModeSynchronizedOutput +
		ansi.SaveCursor +
		commands +
		ansi.RestoreCursor +
		ansi.ResetModeSynchronizedOutput)
}
