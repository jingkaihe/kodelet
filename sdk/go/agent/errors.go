package agent

import (
	"errors"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

// RPCError is a JSON-RPC error returned by kodelet acp.
type RPCError = jsonrpc.Error

// RunError reports that the kodelet acp process exited.
type RunError struct {
	Message string
	// ExitCode is the exit status, or -1 if the process was killed by a signal.
	ExitCode int
	// Signal names the terminating signal, such as SIGTERM.
	Signal string
	// Stderr is the tail of the process's standard error.
	Stderr string
}

func (e *RunError) Error() string {
	return e.Message
}

var (
	errConnectionClosed = errors.New("kodelet acp process is closed")
	errStdoutEnded      = errors.New("ACP stdout ended before the client closed the session")
)
