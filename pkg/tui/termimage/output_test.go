package termimage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	enter = ansi.SetModeAltScreenSaveCursor
	exit  = ansi.ResetModeAltScreenSaveCursor
)

func TestOutputWritesKittyUploadsBeforeFrames(t *testing.T) {
	output, written := newTestOutput(t, Kitty)

	output.Upload(1, "<upload 1>")
	assert.True(t, output.Uploaded(1))
	assert.Empty(t, written(), "uploads wait for the alternate screen")

	write(t, output, "init"+enter+"frame 1")
	assert.Equal(t, "init"+enter+"<upload 1>frame 1", written())

	output.Upload(2, "<upload 2>")
	write(t, output, "frame 2")
	require.Eventually(t, func() bool { return strings.HasSuffix(written(), "<upload 2>frame 2") }, time.Second, 5*time.Millisecond)

	output.Release(2, 99)
	assert.False(t, output.Uploaded(2))
	write(t, output, "frame 3")
	assert.True(t, strings.HasSuffix(written(), KittyDelete(2)+"frame 3"), "only uploaded images are deleted")
	assert.NotContains(t, written(), KittyDelete(99))

	write(t, output, "bye"+exit)
	assert.True(t, strings.HasSuffix(written(), "bye"+KittyDelete(1)+exit), "leaving the screen frees its images")
	assert.False(t, output.Uploaded(1))
}

func TestOutputDrawsITerm2ImagesAfterFramesSettle(t *testing.T) {
	output, written := newTestOutput(t, ITerm2)
	var encodes atomic.Int32
	encode := func(placement Placement) string {
		encodes.Add(1)
		return fmt.Sprintf("<image %d>", placement.ID)
	}
	frame := func(id uint32) []Placement { return []Placement{{ID: id, X: 2, Y: 1, Rows: 2, Cols: 3}} }
	drawn := ansi.SetModeSynchronizedOutput + ansi.SaveCursor + ansi.CursorPosition(3, 2) + "<image 1>" +
		ansi.RestoreCursor + ansi.ResetModeSynchronizedOutput

	output.Present(1, frame(1), encode)
	time.Sleep(2 * maxSettleDelay)
	assert.Zero(t, encodes.Load(), "nothing is drawn outside the alternate screen")

	write(t, output, enter+"frame")
	require.Eventually(t, func() bool { return written() == enter+"frame"+drawn }, time.Second, 5*time.Millisecond)

	output.Present(1, frame(1), encode)
	write(t, output, "spinner")
	time.Sleep(2 * maxSettleDelay)
	assert.Equal(t, enter+"frame"+drawn+"spinner", written(), "unchanged frames are not redrawn")

	write(t, output, ansi.EraseEntireScreen+"repaint")
	require.Eventually(t, func() bool { return strings.HasSuffix(written(), "repaint"+drawn) }, time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(1), encodes.Load(), "repaints reuse the encoded image")

	for signature := uint64(2); signature < 6; signature++ {
		output.Present(signature, frame(uint32(signature)), encode)
	}
	require.Eventually(t, func() bool { return strings.Contains(written(), "<image 5>") }, time.Second, 5*time.Millisecond)
	assert.NotContains(t, written(), "<image 4>", "rapid changes are drawn once")

	require.NoError(t, output.Close())
	output.Present(6, frame(6), encode)
	write(t, output, "after close")
	time.Sleep(2 * maxSettleDelay)
	assert.NotContains(t, written(), "<image 6>")
}

func TestOutputDrawsWhileFramesKeepChanging(t *testing.T) {
	output, written := newTestOutput(t, ITerm2)
	write(t, output, enter)
	var active, maxActive atomic.Int32
	encode := func(placement Placement) string {
		maxActive.Store(max(maxActive.Load(), active.Add(1)))
		time.Sleep(30 * time.Millisecond)
		active.Add(-1)
		return fmt.Sprintf("<image %d rows %d>", placement.ID, placement.FirstRow)
	}
	// Scrolling moves a visible image and crops a clipped one differently every frame.
	start := time.Now()
	for frame := 0; time.Since(start) < 4*maxSettleDelay; frame++ {
		output.Present(uint64(frame+1), []Placement{
			{ID: 1, Y: frame % 20, Rows: 4, Cols: 3},
			{ID: 2, FirstRow: frame, Rows: 2, Cols: 3},
		}, encode)
		time.Sleep(8 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, strings.Count(written(), "<image 1 rows 0>"), 2, "images are drawn while frames keep changing")
	assert.Equal(t, int32(1), maxActive.Load(), "one frame is encoded at a time")
}

func TestOutputRedrawsAFrameDrawnTooSoon(t *testing.T) {
	output, written := newTestOutput(t, ITerm2)
	write(t, output, enter)
	output.Present(1, []Placement{{ID: 1, Rows: 1, Cols: 1}}, func(Placement) string { return "<image>" })
	output.drawSettled() // as the deadline would, before the frame may show
	require.Eventually(t, func() bool { return strings.Count(written(), "<image>") == 2 }, time.Second, 5*time.Millisecond)
}

func TestOutputDropsSupersededITerm2Draws(t *testing.T) {
	output, written := newTestOutput(t, ITerm2)
	write(t, output, enter)
	release := make(chan struct{})
	encode := func(placement Placement) string {
		if placement.ID == 1 {
			<-release
			return "<stale>"
		}
		return "<fresh>"
	}
	output.Present(1, []Placement{{ID: 1, Rows: 1, Cols: 1}}, encode)
	time.Sleep(2 * settleDelay)
	output.Present(2, []Placement{{ID: 2, Rows: 1, Cols: 1}}, encode)
	close(release)

	require.Eventually(t, func() bool { return strings.Contains(written(), "<fresh>") }, time.Second, 5*time.Millisecond)
	assert.NotContains(t, written(), "<stale>")
}

func TestOutputDrawsOnceAProtocolIsSet(t *testing.T) {
	output, written := newTestOutput(t, None)
	write(t, output, enter)
	encode := func(placement Placement) string { return fmt.Sprintf("<image %d>", placement.ID) }
	output.Present(1, []Placement{{ID: 1, Rows: 1, Cols: 1}}, encode)
	time.Sleep(2 * maxSettleDelay)
	assert.NotContains(t, written(), "<image 1>")

	output.SetProtocol(ITerm2)
	output.Present(2, []Placement{{ID: 2, Rows: 1, Cols: 1}}, encode)
	require.Eventually(t, func() bool { return strings.Contains(written(), "<image 2>") }, time.Second, 5*time.Millisecond)
}

func newTestOutput(t *testing.T, protocol Protocol) (*Output, func() string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tty")
	file, err := os.Create(path)
	require.NoError(t, err)
	output := NewOutput(file, protocol)
	t.Cleanup(func() {
		_ = output.Close()
		_ = file.Close()
	})
	return output, func() string {
		output.mu.Lock()
		defer output.mu.Unlock()
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(data)
	}
}

func write(t *testing.T, output *Output, text string) {
	t.Helper()
	n, err := output.Write([]byte(text))
	require.NoError(t, err)
	require.Equal(t, len(text), n)
}
