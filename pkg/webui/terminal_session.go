package webui

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const (
	terminalReplayBufferLimit      = 1024 * 1024
	terminalAttachmentBufferLength = 128
)

var (
	errTerminalSessionClosed = errors.New("terminal session is closed")
	errTerminalClientSlow    = errors.New("terminal websocket client is not consuming output")
)

type terminalSessionManager struct {
	ctx       context.Context
	mu        sync.Mutex
	sessions  map[string]*terminalSession
	closed    bool
	closeOnce sync.Once
}

type terminalSession struct {
	key       string
	cwd       string
	shellName string
	gitRepo   bool

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	ptmx   *os.File

	mu          sync.Mutex
	ptyMu       sync.Mutex
	attachments map[*terminalAttachment]struct{}
	buffer      []byte
	exited      bool
	exitCode    int

	done       chan struct{}
	doneOnce   sync.Once
	finishOnce sync.Once
	onExit     func()
}

type terminalAttachment struct {
	session  *terminalSession
	outputCh chan []byte
	exitCh   chan int
	errCh    chan error
}

func newTerminalSessionManager(ctx context.Context) *terminalSessionManager {
	if ctx == nil {
		ctx = context.Background()
	}

	manager := &terminalSessionManager{
		ctx:      ctx,
		sessions: make(map[string]*terminalSession),
	}

	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			manager.Close()
		}()
	}

	return manager
}

func terminalSessionKey(cwd string) string {
	return cwd
}

func (m *terminalSessionManager) getOrCreate(ctx context.Context, key, cwd string, rows, cols int) (*terminalSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, errTerminalSessionClosed
	}

	if session := m.sessions[key]; session != nil {
		if session.isAlive() {
			return session, nil
		}
		delete(m.sessions, key)
	}

	shell, shellName := resolveTerminalShell()
	gitRepo := false
	if _, gitErr := resolveGitRoot(ctx, cwd); gitErr == nil {
		gitRepo = true
	}

	session, err := newTerminalSession(m.ctx, key, cwd, shell, shellName, gitRepo, rows, cols)
	if err != nil {
		return nil, err
	}

	session.onExit = func() {
		m.remove(key, session)
	}
	m.sessions[key] = session
	session.start(ctx)

	return session, nil
}

func (m *terminalSessionManager) remove(key string, session *terminalSession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessions[key] == session {
		delete(m.sessions, key)
	}
}

func (m *terminalSessionManager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		sessions := make([]*terminalSession, 0, len(m.sessions))
		for _, session := range m.sessions {
			sessions = append(sessions, session)
		}
		m.sessions = make(map[string]*terminalSession)
		m.mu.Unlock()

		for _, session := range sessions {
			session.terminate()
		}
	})
}

func newTerminalSession(ctx context.Context, key, cwd, shell, shellName string, gitRepo bool, rows, cols int) (*terminalSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(sessionCtx, shell)
	cmd.Dir = cwd
	cmd.Env = terminalEnv(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		cancel()
		return nil, err
	}

	return &terminalSession{
		key:         key,
		cwd:         cwd,
		shellName:   shellName,
		gitRepo:     gitRepo,
		ctx:         sessionCtx,
		cancel:      cancel,
		cmd:         cmd,
		ptmx:        ptmx,
		attachments: make(map[*terminalAttachment]struct{}),
		done:        make(chan struct{}),
	}, nil
}

func (s *terminalSession) start(ctx context.Context) {
	go s.readPTY()
	go s.wait(ctx)
}

func (s *terminalSession) readyMessage() terminalMessage {
	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}

	return terminalMessage{
		Type: "ready",
		CWD:  s.cwd,
		Name: s.shellName,
		Git:  s.gitRepo,
		PID:  pid,
	}
}

func (s *terminalSession) attach() (*terminalAttachment, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.exited {
		return nil, nil, errTerminalSessionClosed
	}

	attachment := &terminalAttachment{
		session:  s,
		outputCh: make(chan []byte, terminalAttachmentBufferLength),
		exitCh:   make(chan int, 1),
		errCh:    make(chan error, 1),
	}
	s.attachments[attachment] = struct{}{}
	replay := append([]byte(nil), s.buffer...)

	return attachment, replay, nil
}

func (s *terminalSession) detach(attachment *terminalAttachment) {
	if attachment == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attachments, attachment)
}

func (s *terminalSession) isAlive() bool {
	select {
	case <-s.done:
		return false
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited
}

func (s *terminalSession) writeInput(payload []byte) error {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()

	if !s.isAlive() {
		return errTerminalSessionClosed
	}

	_, err := s.ptmx.Write(payload)
	return err
}

func (s *terminalSession) resize(rows, cols int) error {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()

	if !s.isAlive() {
		return errTerminalSessionClosed
	}

	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: uint16(boundedTerminalRows(rows)),
		Cols: uint16(boundedTerminalCols(cols)),
	})
}

func (s *terminalSession) signal(sig syscall.Signal) error {
	if !s.isAlive() {
		return errTerminalSessionClosed
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	return syscall.Kill(-s.cmd.Process.Pid, sig)
}

func (s *terminalSession) readPTY() {
	buf := make([]byte, 4096)
	for {
		n, readErr := s.ptmx.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.appendOutput(chunk)
		}
		if readErr != nil {
			if s.isAlive() {
				s.terminate()
			}
			return
		}
	}
}

func (s *terminalSession) wait(ctx context.Context) {
	waitErr := s.cmd.Wait()
	code := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			logger.G(ctx).WithError(waitErr).Warn("terminal process ended with error")
		}
	}

	s.finish(code)
}

func (s *terminalSession) appendOutput(chunk []byte) {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return
	}

	s.appendReplayBufferLocked(chunk)
	attachments := s.attachmentsSnapshotLocked()
	s.mu.Unlock()

	for _, attachment := range attachments {
		attachment.sendOutput(chunk)
	}
}

func (s *terminalSession) appendReplayBufferLocked(chunk []byte) {
	if len(chunk) >= terminalReplayBufferLimit {
		s.buffer = append(s.buffer[:0], chunk[len(chunk)-terminalReplayBufferLimit:]...)
		return
	}

	overflow := len(s.buffer) + len(chunk) - terminalReplayBufferLimit
	if overflow > 0 {
		s.buffer = append(s.buffer[:0], s.buffer[overflow:]...)
	}
	s.buffer = append(s.buffer, chunk...)
}

func (s *terminalSession) attachmentsSnapshotLocked() []*terminalAttachment {
	attachments := make([]*terminalAttachment, 0, len(s.attachments))
	for attachment := range s.attachments {
		attachments = append(attachments, attachment)
	}
	return attachments
}

func (s *terminalSession) finish(code int) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.exited = true
		s.exitCode = code
		attachments := s.attachmentsSnapshotLocked()
		s.mu.Unlock()

		for _, attachment := range attachments {
			attachment.sendExit(code)
		}

		s.cancel()
		s.closePTY()
		if s.onExit != nil {
			s.onExit()
		}
		s.doneOnce.Do(func() {
			close(s.done)
		})
	})
}

func (s *terminalSession) terminate() {
	s.cancel()
	s.closePTY()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
	}
}

func (s *terminalSession) closePTY() {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()
	_ = s.ptmx.Close()
}

func (a *terminalAttachment) sendOutput(chunk []byte) {
	select {
	case a.outputCh <- chunk:
	default:
		a.notify(errTerminalClientSlow)
	}
}

func (a *terminalAttachment) sendExit(code int) {
	select {
	case a.exitCh <- code:
	default:
		a.notify(errTerminalSessionClosed)
	}
}

func (a *terminalAttachment) notify(err error) {
	select {
	case a.errCh <- err:
	default:
	}
}
