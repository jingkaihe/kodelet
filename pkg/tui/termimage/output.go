package termimage

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const (
	// iTerm2 images are drawn once frames stop changing for settleDelay, and
	// at most maxSettleDelay after the first change while they keep changing.
	settleDelay    = 60 * time.Millisecond
	maxSettleDelay = 250 * time.Millisecond
	// minDrawDelay spans a couple of Bubble Tea frames, so a frame presented
	// this recently may not be on screen yet.
	minDrawDelay = 35 * time.Millisecond
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

	// iTerm2 images of the latest frame. Encoded crops are kept while on
	// screen, so moving images only need repositioning.
	signature   uint64
	placements  []Placement
	encode      func(Placement) string
	encoded     map[crop]string
	encoding    bool
	presentedAt time.Time
	dirty       bool
	dirtySince  time.Time
	timer       *time.Timer
}

// crop identifies a placement's image rows regardless of position.
type crop struct {
	id                   uint32
	firstRow, rows, cols int
}

func (p Placement) crop() crop {
	return crop{id: p.ID, firstRow: p.FirstRow, rows: p.Rows, cols: p.Cols}
}

// NewOutput wraps file for protocol.
func NewOutput(file *os.File, protocol Protocol) *Output {
	return &Output{file: file, protocol: protocol, uploaded: map[uint32]struct{}{}, encoded: map[crop]string{}}
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
		clear(o.encoded)
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

// Present sets the iTerm2 images of the latest frame, drawn once it settles.
// encode returns the command that draws a placement's rows at the cursor; it
// may be slow, so it runs in the background, one frame at a time.
func (o *Output) Present(signature uint64, placements []Placement, encode func(Placement) string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if signature == o.signature && o.encode != nil {
		return
	}
	o.signature, o.placements, o.encode, o.presentedAt = signature, placements, encode, time.Now()
	o.scheduleLocked()
}

func (o *Output) scheduleLocked() {
	if o.protocol != ITerm2 || o.closed || !o.onScreen || o.encode == nil {
		return
	}
	now := time.Now()
	if !o.dirty {
		o.dirty, o.dirtySince = true, now
	}
	delay := min(settleDelay, max(0, maxSettleDelay-now.Sub(o.dirtySince)))
	if o.timer == nil {
		o.timer = time.AfterFunc(delay, o.drawSettled)
		return
	}
	o.timer.Reset(delay)
}

func (o *Output) drawSettled() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || !o.onScreen || !o.dirty || o.encode == nil || o.encoding {
		// A running encode draws the latest frame when it finishes.
		return
	}
	missing := map[crop]Placement{}
	for _, placement := range o.placements {
		if _, ok := o.encoded[placement.crop()]; !ok {
			missing[placement.crop()] = placement
		}
	}
	if len(missing) > 0 {
		encode := o.encode
		o.encoding = true
		o.mu.Unlock()
		commands := make(map[crop]string, len(missing))
		for key, placement := range missing {
			commands[key] = encode(placement)
		}
		o.mu.Lock()
		o.encoding = false
		for key, command := range commands {
			o.encoded[key] = command
		}
		if o.closed || !o.onScreen {
			return
		}
	}
	o.drawLocked()
}

// drawLocked draws the latest frame's encoded images where they now are.
func (o *Output) drawLocked() {
	var commands strings.Builder
	used := map[crop]struct{}{}
	complete := true
	for _, placement := range o.placements {
		used[placement.crop()] = struct{}{}
		command, ok := o.encoded[placement.crop()]
		if !ok {
			complete = false
			continue
		}
		if command != "" {
			commands.WriteString(ansi.CursorPosition(placement.X+1, placement.Y+1))
			commands.WriteString(command)
		}
	}
	for key := range o.encoded {
		if _, ok := used[key]; !ok {
			delete(o.encoded, key)
		}
	}
	if commands.Len() > 0 {
		_, _ = o.file.WriteString(ansi.SetModeSynchronizedOutput +
			ansi.SaveCursor +
			commands.String() +
			ansi.RestoreCursor +
			ansi.ResetModeSynchronizedOutput)
	}
	if !complete {
		// The frame changed while encoding; encode its new crops next.
		o.scheduleLocked()
		return
	}
	o.dirty = false
	if commands.Len() > 0 && time.Since(o.presentedAt) < minDrawDelay {
		// Draw again once the frame is surely on screen.
		o.dirty, o.dirtySince = true, time.Now()
		o.timer.Reset(minDrawDelay)
	}
}
