package agent

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const helperEnv = "KODELET_SDK_GO_TEST_HELPER"

// TestACPHelperProcess is a subprocess for the tests below, not a test.
func TestACPHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "oversized" {
		t.Skip("helper process")
	}
	// One line longer than the ACP limit, then keep running and writing.
	_, _ = os.Stdout.Write(bytes.Repeat([]byte("a"), acpMessageLimit+1<<20))
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestOversizedACPOutputFailsFastAndTheProcessIsReaped(t *testing.T) {
	process, err := startProcess(os.Args[0], []string{"-test.run=^TestACPHelperProcess$"}, append(os.Environ(), helperEnv+"=oversized"))
	require.NoError(t, err)
	conn := newACPConn(process, nil, nil)
	started := time.Now()
	_, err = conn.request(t.Context(), "initialize", nil)
	require.ErrorIs(t, err, jsonrpc.ErrMessageTooLarge)
	assert.Less(t, time.Since(started), stdoutExitGrace, "a protocol error must not wait for the exit grace period")
	require.NoError(t, conn.close())
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the killed process was never reaped")
	}
}

func TestStartProcessReportsOutputExitCodeAndStderr(t *testing.T) {
	process, err := startProcess("sh", []string{"-c", "echo out; echo err >&2; exit 3"}, nil)
	require.NoError(t, err)
	output, err := io.ReadAll(process.Stdout())
	require.NoError(t, err)
	assert.Equal(t, "out\n", string(output))
	<-process.Done()
	exit := process.Exit()
	assert.Equal(t, 3, exit.Code)
	assert.Equal(t, "err\n", exit.Stderr)
	assert.Equal(t, "err", exit.runError().Message)
}

func TestStartProcessReportsTerminatingSignal(t *testing.T) {
	process, err := startProcess("sleep", []string{"30"}, nil)
	require.NoError(t, err)
	require.NoError(t, process.Terminate())
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	exit := process.Exit()
	assert.Equal(t, -1, exit.Code)
	assert.Equal(t, "SIGTERM", exit.Signal)
	assert.Equal(t, "kodelet acp exited with status -1 (SIGTERM)", exit.runError().Message)
	require.NoError(t, process.Kill(), "signalling an exited process is harmless")
}

func TestStartProcessFailsForMissingExecutables(t *testing.T) {
	_, err := startProcess("kodelet-definitely-missing", nil, nil)
	require.Error(t, err)
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	buffer := &tailBuffer{limit: 4}
	_, _ = buffer.Write([]byte("abc"))
	_, _ = buffer.Write([]byte("def"))
	assert.Equal(t, "cdef", buffer.String())
}
