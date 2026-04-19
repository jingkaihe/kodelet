package webui

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalSessionReplayBufferKeepsTail(t *testing.T) {
	session := newTestTerminalSession(t)
	payload := []byte(strings.Repeat("x", terminalReplayBufferLimit) + "tail")

	session.appendOutput(payload)

	require.Len(t, session.buffer, terminalReplayBufferLimit)
	assert.True(t, strings.HasSuffix(string(session.buffer), "tail"))
}

func TestTerminalSessionAttachReplaysBufferedOutputAndStreamsNewOutput(t *testing.T) {
	session := newTestTerminalSession(t)
	session.buffer = []byte("previous output\n")

	attachment, replay, err := session.attach()
	require.NoError(t, err)
	assert.Equal(t, []byte("previous output\n"), replay)

	session.appendOutput([]byte("new output\n"))
	assert.Equal(t, []byte("new output\n"), <-attachment.outputCh)

	session.detach(attachment)
	session.appendOutput([]byte("detached output\n"))
	select {
	case output := <-attachment.outputCh:
		t.Fatalf("detached attachment received output %q", string(output))
	default:
	}
}

func TestTerminalSessionFinishNotifiesAttachmentsAndClosesDone(t *testing.T) {
	session := newTestTerminalSession(t)
	attachment, _, err := session.attach()
	require.NoError(t, err)

	session.finish(7)

	assert.Equal(t, 7, <-attachment.exitCh)
	assert.False(t, session.isAlive())
	_, _, err = session.attach()
	require.ErrorIs(t, err, errTerminalSessionClosed)
	select {
	case <-session.done:
	default:
		t.Fatal("expected terminal session done channel to close")
	}
}

func TestTerminalSessionManagerCloseTerminatesSessionsAndRejectsNewSessions(t *testing.T) {
	manager := newTerminalSessionManager(context.Background())
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := newTestTerminalSession(t)
	session.cancel = cancel
	session.ctx = sessionCtx
	manager.sessions["cwd"] = session

	manager.Close()

	assert.Empty(t, manager.sessions)
	assert.True(t, manager.closed)
	select {
	case <-sessionCtx.Done():
	default:
		t.Fatal("expected terminal session context to be canceled")
	}

	_, err := manager.getOrCreate(context.Background(), "cwd", t.TempDir(), defaultTerminalRows, defaultTerminalCols)
	require.ErrorIs(t, err, errTerminalSessionClosed)
}

func TestTerminalSessionSignalHandlesMissingAndClosedProcess(t *testing.T) {
	session := newTestTerminalSession(t)
	require.NoError(t, session.signal(syscall.SIGINT))

	session.finish(0)

	require.ErrorIs(t, session.signal(syscall.SIGINT), errTerminalSessionClosed)
}

func TestTerminalAttachmentBackpressureReportsErrors(t *testing.T) {
	attachment := &terminalAttachment{
		outputCh: make(chan []byte, 1),
		exitCh:   make(chan int, 1),
		errCh:    make(chan error, 1),
	}

	attachment.outputCh <- []byte("pending")
	attachment.sendOutput([]byte("dropped"))
	require.ErrorIs(t, <-attachment.errCh, errTerminalClientSlow)

	attachment.exitCh <- 1
	attachment.sendExit(2)
	require.ErrorIs(t, <-attachment.errCh, errTerminalSessionClosed)
}

func newTestTerminalSession(t *testing.T) *terminalSession {
	t.Helper()

	sessionCtx, cancel := context.WithCancel(context.Background())
	ptmx, err := os.CreateTemp(t.TempDir(), "terminal-pty-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		_ = ptmx.Close()
	})

	return &terminalSession{
		cwd:         t.TempDir(),
		shellName:   "test-shell",
		ctx:         sessionCtx,
		cancel:      cancel,
		ptmx:        ptmx,
		attachments: make(map[*terminalAttachment]struct{}),
		done:        make(chan struct{}),
	}
}
