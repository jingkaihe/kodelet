package termimage

import (
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
	var draws atomic.Int32
	draw := func(name string) func() string {
		return func() string {
			draws.Add(1)
			return name
		}
	}

	output.Present(1, draw("<image 1>"))
	time.Sleep(2 * maxSettleDelay)
	assert.Zero(t, draws.Load(), "nothing is drawn outside the alternate screen")

	write(t, output, enter+"frame")
	drawn := ansi.SetModeSynchronizedOutput + ansi.SaveCursor + "<image 1>" + ansi.RestoreCursor + ansi.ResetModeSynchronizedOutput
	require.Eventually(t, func() bool { return written() == enter+"frame"+drawn }, time.Second, 5*time.Millisecond)

	output.Present(1, draw("<unused>"))
	write(t, output, "spinner")
	time.Sleep(2 * maxSettleDelay)
	assert.Equal(t, int32(1), draws.Load(), "unchanged frames are not redrawn")

	write(t, output, ansi.EraseEntireScreen+"repaint")
	require.Eventually(t, func() bool { return strings.HasSuffix(written(), "repaint"+drawn) }, time.Second, 5*time.Millisecond)

	for signature := uint64(2); signature < 6; signature++ {
		output.Present(signature, draw("<image "+string(rune('0'+signature))+">"))
	}
	require.Eventually(t, func() bool {
		return strings.HasSuffix(written(), "<image 5>"+ansi.RestoreCursor+ansi.ResetModeSynchronizedOutput)
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(3), draws.Load(), "rapid changes are drawn once")

	require.NoError(t, output.Close())
	output.Present(6, draw("<closed>"))
	write(t, output, "after close")
	time.Sleep(2 * maxSettleDelay)
	assert.NotContains(t, written(), "<closed>")
}

func TestOutputDrawsWhileFramesKeepChanging(t *testing.T) {
	output, _ := newTestOutput(t, ITerm2)
	write(t, output, enter)
	var draws atomic.Int32
	start := time.Now()
	for signature := uint64(1); time.Since(start) < 4*maxSettleDelay; signature++ {
		output.Present(signature, func() string {
			draws.Add(1)
			return "<image>"
		})
		time.Sleep(10 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, draws.Load(), int32(2), "scrolling does not postpone drawing past maxSettleDelay")
}

func TestOutputRedrawsAFrameDrawnTooSoon(t *testing.T) {
	output, written := newTestOutput(t, ITerm2)
	write(t, output, enter)
	output.Present(1, func() string { return "<image>" })
	output.drawSettled() // as the deadline would, before the frame may show
	require.Eventually(t, func() bool { return strings.Count(written(), "<image>") == 2 }, time.Second, 5*time.Millisecond)
}

func TestOutputDropsSupersededITerm2Draws(t *testing.T) {
	output, written := newTestOutput(t, ITerm2)
	write(t, output, enter)
	release := make(chan struct{})
	output.Present(1, func() string {
		<-release
		return "<stale>"
	})
	time.Sleep(2 * settleDelay)
	output.Present(2, func() string { return "<fresh>" })
	close(release)

	require.Eventually(t, func() bool { return strings.Contains(written(), "<fresh>") }, time.Second, 5*time.Millisecond)
	assert.NotContains(t, written(), "<stale>")
}

func TestOutputDrawsOnceAProtocolIsSet(t *testing.T) {
	output, written := newTestOutput(t, None)
	write(t, output, enter)
	output.Present(1, func() string { return "<before>" })
	time.Sleep(2 * maxSettleDelay)
	assert.NotContains(t, written(), "<before>")

	output.SetProtocol(ITerm2)
	output.Present(2, func() string { return "<after>" })
	require.Eventually(t, func() bool { return strings.Contains(written(), "<after>") }, time.Second, 5*time.Millisecond)
}

func TestOutputImplementsTerminalFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "tty")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	output := NewOutput(file, Kitty)
	assert.Equal(t, file.Fd(), output.Fd())
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
