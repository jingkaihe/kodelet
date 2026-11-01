package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// acpProcess is a running kodelet acp process. It is an interface so tests
// can substitute an in-memory ACP server.
type acpProcess interface {
	// Write writes to the process's stdin.
	Write(p []byte) (int, error)
	// Stdout must be closed when reading stops, so the process can be reaped
	// even if it is still writing.
	Stdout() io.ReadCloser
	Terminate() error
	Kill() error
	// Done is closed after the process exits and its output is drained.
	Done() <-chan struct{}
	// Exit describes how the process ended; it is valid after Done.
	Exit() processExit
}

type processExit struct {
	Code   int
	Signal string
	Stderr string
}

type starter func(command string, args, env []string) (acpProcess, error)

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *io.PipeReader
	done   chan struct{}
	exit   processExit
}

const stderrLimit = 1 << 20

func startProcess(command string, args, env []string) (acpProcess, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutReader, stdoutWriter := io.Pipe()
	cmd.Stdout = stdoutWriter
	stderr := &tailBuffer{limit: stderrLimit}
	cmd.Stderr = stderr
	// Do not wait forever for output held open by a grandchild after exit.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &execProcess{cmd: cmd, stdin: stdin, stdout: stdoutReader, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		process.exit = exitFrom(cmd.ProcessState, stderr.String())
		_ = stdoutWriter.Close()
		close(process.done)
	}()
	return process, nil
}

func exitFrom(state *os.ProcessState, stderr string) processExit {
	exit := processExit{Code: -1, Stderr: stderr}
	if state == nil {
		return exit
	}
	exit.Code = state.ExitCode()
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		exit.Signal = signalName(status.Signal())
	}
	return exit
}

func signalName(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	default:
		return signal.String()
	}
}

func (p *execProcess) Write(data []byte) (int, error) { return p.stdin.Write(data) }
func (p *execProcess) Stdout() io.ReadCloser          { return p.stdout }
func (p *execProcess) Done() <-chan struct{}          { return p.done }
func (p *execProcess) Exit() processExit              { return p.exit }

func (p *execProcess) Terminate() error {
	return ignoreProcessDone(p.cmd.Process.Signal(syscall.SIGTERM))
}

func (p *execProcess) Kill() error {
	return ignoreProcessDone(p.cmd.Process.Kill())
}

func ignoreProcessDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (e processExit) runError() *RunError {
	message := strings.TrimSpace(e.Stderr)
	if message == "" {
		message = fmt.Sprintf("kodelet acp exited with status %d", e.Code)
		if e.Signal != "" {
			message += " (" + e.Signal + ")"
		}
	}
	return &RunError{Message: message, ExitCode: e.Code, Signal: e.Signal, Stderr: e.Stderr}
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if overflow := len(b.data) - b.limit; overflow > 0 {
		b.data = append([]byte(nil), b.data[overflow:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
