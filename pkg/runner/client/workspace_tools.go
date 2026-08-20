package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const (
	workspaceGitDiffLimit              = 512 * 1024
	workspaceGitErrorLimit             = 64 * 1024
	workspaceTerminalReplayBufferLimit = 1024 * 1024
	workspaceTerminalDefaultRows       = 28
	workspaceTerminalDefaultCols       = 100
	workspaceTerminalMaxRows           = 400
	workspaceTerminalMaxCols           = 400
	workspaceTerminalDefaultReadBytes  = 64 * 1024
	workspaceTerminalMaxReadBytes      = 256 * 1024
	workspaceTerminalDefaultReadWait   = 20 * time.Second
	workspaceTerminalMaxReadWait       = 25 * time.Second
	workspaceTerminalMaxInputBytes     = 64 * 1024
	workspaceTerminalInputBufferLength = 128
	workspaceTerminalProcessKillWait   = 2 * time.Second
	workspaceTerminalShutdownWait      = 4 * time.Second
	workspaceTerminalPTYCloseWait      = time.Second
	workspaceTerminalExitPollInterval  = 100 * time.Millisecond
	workspaceTerminalCleanupRetryDelay = 100 * time.Millisecond
)

type cappedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(payload []byte) (int, error) {
	written := len(payload)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		_, _ = b.buffer.Write(payload[:remaining])
	}
	if remaining < len(payload) {
		b.truncated = true
	}
	return written, nil
}

func (b *cappedBuffer) String() string {
	return b.buffer.String()
}

func (s *Service) workspaceGitDiff(ctx context.Context) (protocol.WorkspaceGitDiffResult, error) {
	if s == nil {
		return protocol.WorkspaceGitDiffResult{}, errors.New("runner service is required")
	}
	s.mu.Lock()
	closed := s.closed
	workspace := s.workspace
	s.mu.Unlock()
	if closed {
		return protocol.WorkspaceGitDiffResult{}, errors.New("runner service is closed")
	}

	gitRoot, err := resolveWorkspaceGitRoot(ctx, workspace)
	if err != nil {
		return protocol.WorkspaceGitDiffResult{}, err
	}
	diff, exitCode, truncated, err := readWorkspaceGitDiff(ctx, gitRoot)
	if err != nil {
		return protocol.WorkspaceGitDiffResult{}, err
	}
	return protocol.WorkspaceGitDiffResult{
		CWD:       workspace,
		Diff:      diff,
		HasDiff:   strings.TrimSpace(diff) != "",
		GitRoot:   gitRoot,
		ExitCode:  exitCode,
		Truncated: truncated,
	}, nil
}

func resolveWorkspaceGitRoot(ctx context.Context, cwd string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd

	stdout := &cappedBuffer{limit: workspaceGitErrorLimit}
	stderr := &cappedBuffer{limit: workspaceGitErrorLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.Wrap(err, message)
	}

	root := strings.TrimSpace(stdout.String())
	if root == "" {
		return "", errors.New("git root is empty")
	}
	return osutil.CanonicalizePath(root), nil
}

func readWorkspaceGitDiff(ctx context.Context, cwd string) (string, int, bool, error) {
	cmd := exec.CommandContext(
		ctx,
		"git",
		"diff",
		"--no-ext-diff",
		"--no-textconv",
		"--submodule=diff",
		"--src-prefix=a/",
		"--dst-prefix=b/",
	)
	cmd.Dir = cwd

	stdout := &cappedBuffer{limit: workspaceGitDiffLimit}
	stderr := &cappedBuffer{limit: workspaceGitErrorLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return "", exitCode, false, errors.Wrap(err, "failed to execute git diff")
		}
	}
	if exitCode != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = "git diff exited with non-zero status"
		}
		return "", exitCode, false, errors.New(message)
	}
	return stdout.String(), exitCode, stdout.truncated, nil
}

type workspaceTerminalManager struct {
	ctx      context.Context
	cwd      string
	mu       sync.Mutex
	closeMu  sync.Mutex
	sessions map[string]*workspaceTerminalSession
	current  *workspaceTerminalSession
	closed   bool
}

type workspaceTerminalSession struct {
	id        string
	cwd       string
	shellName string
	gitRepo   bool

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	ptmx   *os.File

	mu          sync.Mutex
	ptyMu       sync.Mutex
	buffer      []byte
	baseCursor  uint64
	writeCursor uint64
	ending      bool
	exited      bool
	exitCode    int
	updated     chan struct{}
	inputCh     chan []byte

	ptyDone      chan struct{}
	signalCh     chan workspaceTerminalSignalRequest
	shutdownCh   chan struct{}
	done         chan struct{}
	doneOnce     sync.Once
	finishOnce   sync.Once
	shutdownOnce sync.Once
	cleanupErr   error
}

type workspaceTerminalSignalRequest struct {
	signal syscall.Signal
	result chan error
}

type workspaceTerminalProcess struct {
	PID       int
	PGID      int
	SessionID int
	Zombie    bool
	pidfd     int
	identity  string
}

func newWorkspaceTerminalManager(ctx context.Context, cwd string) *workspaceTerminalManager {
	if ctx == nil {
		ctx = context.Background()
	}
	manager := &workspaceTerminalManager{
		ctx:      ctx,
		cwd:      cwd,
		sessions: make(map[string]*workspaceTerminalSession),
	}
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			_ = manager.Close()
		}()
	}
	return manager
}

func (m *workspaceTerminalManager) Open(ctx context.Context, rows, cols int) (protocol.WorkspaceTerminalOpenResult, error) {
	if m == nil {
		return protocol.WorkspaceTerminalOpenResult{}, errors.New("workspace terminal manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return protocol.WorkspaceTerminalOpenResult{}, errors.New("workspace terminal manager is closed")
	}
	if m.current != nil && m.current.isAlive() {
		if err := m.current.resize(rows, cols); err != nil {
			m.mu.Unlock()
			return protocol.WorkspaceTerminalOpenResult{}, err
		}
		result := m.current.openResult()
		m.mu.Unlock()
		return result, nil
	}
	if m.current != nil {
		if err := m.current.terminate(); err != nil {
			m.mu.Unlock()
			return protocol.WorkspaceTerminalOpenResult{}, errors.Wrap(err, "previous workspace terminal cleanup is incomplete")
		}
		delete(m.sessions, m.current.id)
		m.current = nil
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return protocol.WorkspaceTerminalOpenResult{}, err
	}

	sessionID, err := newWorkspaceTerminalSessionID()
	if err != nil {
		m.mu.Unlock()
		return protocol.WorkspaceTerminalOpenResult{}, err
	}
	shell, shellName := resolveWorkspaceTerminalShell()
	gitRepo := false
	if _, gitErr := resolveWorkspaceGitRoot(ctx, m.cwd); gitErr == nil {
		gitRepo = true
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		m.mu.Unlock()
		return protocol.WorkspaceTerminalOpenResult{}, ctxErr
	}
	session, err := newWorkspaceTerminalSession(m.ctx, sessionID, m.cwd, shell, shellName, gitRepo, rows, cols)
	if err != nil {
		m.mu.Unlock()
		return protocol.WorkspaceTerminalOpenResult{}, err
	}
	m.sessions[sessionID] = session
	m.current = session
	session.start(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		if terminateErr := session.terminate(); terminateErr != nil {
			m.mu.Unlock()
			return protocol.WorkspaceTerminalOpenResult{}, errors.Wrapf(ctxErr, "terminal open was canceled; cleanup failed: %v", terminateErr)
		}
		delete(m.sessions, sessionID)
		m.current = nil
		m.mu.Unlock()
		return protocol.WorkspaceTerminalOpenResult{}, ctxErr
	}
	result := session.openResult()
	m.mu.Unlock()
	return result, nil
}

func (m *workspaceTerminalManager) Read(ctx context.Context, params protocol.WorkspaceTerminalReadParams) (protocol.WorkspaceTerminalReadResult, error) {
	session, err := m.session(params.SessionID)
	if err != nil {
		return protocol.WorkspaceTerminalReadResult{}, err
	}
	return session.read(ctx, params.Cursor, params.MaxBytes, params.WaitMS)
}

func (m *workspaceTerminalManager) Write(ctx context.Context, params protocol.WorkspaceTerminalInputParams) error {
	if len(params.Data) > workspaceTerminalMaxInputBytes {
		return errors.Errorf("terminal input exceeds %d bytes", workspaceTerminalMaxInputBytes)
	}
	session, err := m.session(params.SessionID)
	if err != nil {
		return err
	}
	return session.writeInput(ctx, params.Data)
}

func (m *workspaceTerminalManager) Resize(params protocol.WorkspaceTerminalResizeParams) error {
	session, err := m.session(params.SessionID)
	if err != nil {
		return err
	}
	return session.resize(params.Rows, params.Cols)
}

func (m *workspaceTerminalManager) Signal(ctx context.Context, params protocol.WorkspaceTerminalSignalParams) error {
	signal, ok := parseWorkspaceTerminalSignal(params.Name)
	if !ok {
		return errors.Errorf("unsupported terminal signal %q", params.Name)
	}
	session, err := m.session(params.SessionID)
	if err != nil {
		return err
	}
	return session.signal(ctx, signal)
}

func (m *workspaceTerminalManager) session(sessionID string) (*workspaceTerminalSession, error) {
	if m == nil {
		return nil, errors.New("workspace terminal manager is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("terminal session id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("workspace terminal manager is closed")
	}
	session := m.sessions[sessionID]
	if session == nil {
		return nil, errors.New("terminal session was not found")
	}
	return session, nil
}

func (m *workspaceTerminalManager) Close() error {
	if m == nil {
		return nil
	}
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	m.mu.Lock()
	m.closed = true
	sessions := make([]*workspaceTerminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	var firstErr error
	for _, session := range sessions {
		if err := session.terminate(); err != nil {
			if firstErr == nil {
				firstErr = errors.Wrap(err, "failed to stop workspace terminal")
			}
			continue
		}
		m.mu.Lock()
		delete(m.sessions, session.id)
		if m.current == session {
			m.current = nil
		}
		m.mu.Unlock()
	}
	return firstErr
}

func newWorkspaceTerminalSession(ctx context.Context, id, cwd, shell, shellName string, gitRepo bool, rows, cols int) (*workspaceTerminalSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	cmd := exec.Command(shell)
	cmd.Dir = cwd
	cmd.Env = workspaceTerminalEnv(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(boundedWorkspaceTerminalRows(rows)),
		Cols: uint16(boundedWorkspaceTerminalCols(cols)),
	})
	if err != nil {
		cancel()
		return nil, err
	}
	return &workspaceTerminalSession{
		id:         id,
		cwd:        cwd,
		shellName:  shellName,
		gitRepo:    gitRepo,
		ctx:        sessionCtx,
		cancel:     cancel,
		cmd:        cmd,
		ptmx:       ptmx,
		updated:    make(chan struct{}),
		inputCh:    make(chan []byte, workspaceTerminalInputBufferLength),
		ptyDone:    make(chan struct{}),
		signalCh:   make(chan workspaceTerminalSignalRequest),
		shutdownCh: make(chan struct{}),
		done:       make(chan struct{}),
	}, nil
}

func (s *workspaceTerminalSession) start(ctx context.Context) {
	go s.readPTY()
	go s.writePTY()
	go s.supervise(ctx)
}

func (s *workspaceTerminalSession) openResult() protocol.WorkspaceTerminalOpenResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}
	return protocol.WorkspaceTerminalOpenResult{
		SessionID:    s.id,
		CWD:          s.cwd,
		Name:         s.shellName,
		Git:          s.gitRepo,
		PID:          pid,
		ReplayCursor: s.baseCursor,
		WriteCursor:  s.writeCursor,
	}
}

func (s *workspaceTerminalSession) read(ctx context.Context, cursor uint64, maxBytes, waitMS int) (protocol.WorkspaceTerminalReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	maxBytes = boundedWorkspaceTerminalReadBytes(maxBytes)
	wait := boundedWorkspaceTerminalReadWait(waitMS)
	truncated := false

	for {
		s.mu.Lock()
		if cursor < s.baseCursor {
			cursor = s.baseCursor
			truncated = true
		}
		if cursor > s.writeCursor {
			s.mu.Unlock()
			return protocol.WorkspaceTerminalReadResult{}, errors.New("terminal cursor is ahead of available output")
		}
		if cursor < s.writeCursor {
			offset := int(cursor - s.baseCursor)
			available := int(s.writeCursor - cursor)
			if available > maxBytes {
				available = maxBytes
			}
			data := append([]byte(nil), s.buffer[offset:offset+available]...)
			nextCursor := cursor + uint64(available)
			result := protocol.WorkspaceTerminalReadResult{
				Data:       data,
				NextCursor: nextCursor,
				Truncated:  truncated,
				Exited:     s.exited && nextCursor >= s.writeCursor,
				ExitCode:   s.exitCode,
			}
			s.mu.Unlock()
			return result, nil
		}
		if s.exited {
			result := protocol.WorkspaceTerminalReadResult{
				NextCursor: cursor,
				Truncated:  truncated,
				Exited:     true,
				ExitCode:   s.exitCode,
			}
			s.mu.Unlock()
			return result, nil
		}
		updated := s.updated
		s.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			stopWorkspaceTerminalTimer(timer)
			return protocol.WorkspaceTerminalReadResult{}, ctx.Err()
		case <-updated:
			stopWorkspaceTerminalTimer(timer)
		case <-timer.C:
			return protocol.WorkspaceTerminalReadResult{NextCursor: cursor, Truncated: truncated}, nil
		}
	}
}

func (s *workspaceTerminalSession) isAlive() bool {
	select {
	case <-s.done:
		return false
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.ending && !s.exited
}

func (s *workspaceTerminalSession) writeInput(ctx context.Context, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if !s.isAlive() {
		return errors.New("terminal session is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	input := append([]byte(nil), payload...)
	select {
	case s.inputCh <- input:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return errors.New("terminal session is closed")
	}
}

func (s *workspaceTerminalSession) resize(rows, cols int) error {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()
	if !s.isAlive() {
		return errors.New("terminal session is closed")
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: uint16(boundedWorkspaceTerminalRows(rows)),
		Cols: uint16(boundedWorkspaceTerminalCols(cols)),
	})
}

func (s *workspaceTerminalSession) signal(ctx context.Context, signal syscall.Signal) error {
	if !s.isAlive() {
		return errors.New("terminal session is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := workspaceTerminalSignalRequest{signal: signal, result: make(chan error, 1)}
	select {
	case s.signalCh <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("terminal session is closed")
	}
	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("terminal session is closed")
	}
}

func (s *workspaceTerminalSession) readPTY() {
	defer close(s.ptyDone)
	buffer := make([]byte, 4096)
	for {
		count, err := s.ptmx.Read(buffer)
		if count > 0 {
			s.appendOutput(buffer[:count])
		}
		if err != nil {
			return
		}
	}
}

func (s *workspaceTerminalSession) writePTY() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case payload := <-s.inputCh:
			if _, err := s.ptmx.Write(payload); err != nil {
				s.requestShutdown()
				return
			}
		}
	}
}

func (s *workspaceTerminalSession) supervise(ctx context.Context) {
	ticker := time.NewTicker(workspaceTerminalExitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ptyDone:
			s.finishSupervision(ctx)
			return
		case <-s.shutdownCh:
			s.cancel()
			s.finishSupervision(ctx)
			return
		case request := <-s.signalCh:
			request.result <- s.signalForeground(request.signal)
		case <-ticker.C:
			exited, err := workspaceTerminalLeaderExited(s.cmd.Process.Pid)
			if err != nil {
				logger.G(ctx).WithError(err).Debug("failed to inspect workspace terminal process")
				continue
			}
			if exited {
				s.finishSupervision(ctx)
				return
			}
		}
	}
}

func (s *workspaceTerminalSession) finishSupervision(ctx context.Context) {
	s.beginEnding()
	s.cancel()
	for {
		cleanupErr := terminateWorkspaceTerminalSession(s.cmd.Process.Pid, workspaceTerminalProcessKillWait)
		s.setCleanupError(cleanupErr)
		if cleanupErr == nil {
			break
		}
		logger.G(ctx).WithError(cleanupErr).Warn("workspace terminal cleanup is incomplete; retrying")
		timer := time.NewTimer(workspaceTerminalCleanupRetryDelay)
		<-timer.C
	}
	s.closePTY()
	if !waitWorkspaceTerminalDone(s.ptyDone, workspaceTerminalPTYCloseWait) {
		logger.G(ctx).Warn("workspace terminal PTY did not close before process reap")
	}
	exitCode, waitErr := s.waitProcess(ctx)
	if waitErr != nil {
		logger.G(ctx).WithError(waitErr).Warn("workspace terminal process could not be reaped cleanly")
	}
	s.finish(exitCode)
}

func (s *workspaceTerminalSession) signalForeground(signal syscall.Signal) error {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return errors.New("terminal session is closed")
	}
	s.ptyMu.Lock()
	foregroundGroup, foregroundErr := unix.IoctlGetInt(int(s.ptmx.Fd()), unix.TIOCGPGRP)
	s.ptyMu.Unlock()
	processes, err := listWorkspaceTerminalSessionProcesses(s.cmd.Process.Pid)
	if err != nil {
		return errors.Wrap(err, "failed to inspect terminal foreground job")
	}
	defer closeWorkspaceTerminalProcesses(processes)

	if foregroundErr == nil && foregroundGroup > 0 {
		matched := false
		var firstErr error
		for _, process := range processes {
			if process.Zombie || process.PGID != foregroundGroup {
				continue
			}
			matched = true
			if signalErr := signalWorkspaceTerminalProcess(process, signal); signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
				firstErr = combineWorkspaceTerminalErrors(firstErr, signalErr)
			}
		}
		if matched {
			return firstErr
		}
	}
	for _, process := range processes {
		if process.PID != s.cmd.Process.Pid || process.Zombie {
			continue
		}
		if err := signalWorkspaceTerminalProcess(process, signal); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				break
			}
			return err
		}
		return nil
	}
	return errors.New("terminal session is closed")
}

func (s *workspaceTerminalSession) waitProcess(ctx context.Context) (int, error) {
	waitErr := s.cmd.Wait()
	if waitErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	logger.G(ctx).WithError(waitErr).Warn("workspace terminal process ended with error")
	return 0, waitErr
}

func (s *workspaceTerminalSession) appendOutput(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		return
	}
	newWriteCursor := s.writeCursor + uint64(len(chunk))
	if len(chunk) >= workspaceTerminalReplayBufferLimit {
		s.buffer = append(s.buffer[:0], chunk[len(chunk)-workspaceTerminalReplayBufferLimit:]...)
		s.baseCursor = newWriteCursor - uint64(len(s.buffer))
	} else {
		overflow := len(s.buffer) + len(chunk) - workspaceTerminalReplayBufferLimit
		if overflow > 0 {
			s.buffer = append(s.buffer[:0], s.buffer[overflow:]...)
			s.baseCursor += uint64(overflow)
		}
		s.buffer = append(s.buffer, chunk...)
	}
	s.writeCursor = newWriteCursor
	s.notifyUpdatedLocked()
}

func (s *workspaceTerminalSession) finish(exitCode int) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.ending = true
		s.exited = true
		s.exitCode = exitCode
		s.cleanupErr = nil
		s.notifyUpdatedLocked()
		s.mu.Unlock()
		s.cancel()
		s.closePTY()
		s.doneOnce.Do(func() { close(s.done) })
	})
}

func (s *workspaceTerminalSession) terminate() error {
	if s == nil {
		return nil
	}
	if workspaceTerminalDone(s.done) {
		return s.cleanupError()
	}
	s.requestShutdown()
	if waitWorkspaceTerminalDone(s.done, workspaceTerminalShutdownWait) {
		return s.cleanupError()
	}
	return errors.New("workspace terminal session did not stop before the cleanup deadline")
}

func (s *workspaceTerminalSession) requestShutdown() {
	if s == nil {
		return
	}
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
	})
}

func (s *workspaceTerminalSession) cleanupError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupErr
}

func (s *workspaceTerminalSession) beginEnding() {
	s.mu.Lock()
	if !s.ending {
		s.ending = true
		s.notifyUpdatedLocked()
	}
	s.mu.Unlock()
}

func (s *workspaceTerminalSession) setCleanupError(err error) {
	s.mu.Lock()
	s.cleanupErr = err
	s.mu.Unlock()
}

func (s *workspaceTerminalSession) closePTY() {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()
	_ = s.ptmx.Close()
}

func (s *workspaceTerminalSession) notifyUpdatedLocked() {
	close(s.updated)
	s.updated = make(chan struct{})
}

func (s *Service) openWorkspaceTerminal(ctx context.Context, params protocol.WorkspaceTerminalOpenParams) (protocol.WorkspaceTerminalOpenResult, error) {
	if s == nil || s.workspaceTerminals == nil {
		return protocol.WorkspaceTerminalOpenResult{}, errors.New("workspace terminal is unavailable")
	}
	return s.workspaceTerminals.Open(ctx, params.Rows, params.Cols)
}

func (s *Service) readWorkspaceTerminal(ctx context.Context, params protocol.WorkspaceTerminalReadParams) (protocol.WorkspaceTerminalReadResult, error) {
	if s == nil || s.workspaceTerminals == nil {
		return protocol.WorkspaceTerminalReadResult{}, errors.New("workspace terminal is unavailable")
	}
	return s.workspaceTerminals.Read(ctx, params)
}

func (s *Service) writeWorkspaceTerminal(ctx context.Context, params protocol.WorkspaceTerminalInputParams) error {
	if s == nil || s.workspaceTerminals == nil {
		return errors.New("workspace terminal is unavailable")
	}
	return s.workspaceTerminals.Write(ctx, params)
}

func (s *Service) resizeWorkspaceTerminal(params protocol.WorkspaceTerminalResizeParams) error {
	if s == nil || s.workspaceTerminals == nil {
		return errors.New("workspace terminal is unavailable")
	}
	return s.workspaceTerminals.Resize(params)
}

func (s *Service) signalWorkspaceTerminal(ctx context.Context, params protocol.WorkspaceTerminalSignalParams) error {
	if s == nil || s.workspaceTerminals == nil {
		return errors.New("workspace terminal is unavailable")
	}
	return s.workspaceTerminals.Signal(ctx, params)
}

func newWorkspaceTerminalSessionID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", errors.Wrap(err, "failed to generate terminal session id")
	}
	return "terminal-" + hex.EncodeToString(value), nil
}

func resolveWorkspaceTerminalShell() (string, string) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/bash"
	}
	name := filepath.Base(shell)
	if name == "." || name == string(os.PathSeparator) || name == "" {
		name = shell
	}
	return shell, name
}

func workspaceTerminalEnv(shell string) []string {
	environment := os.Environ()
	hasTerm := false
	hasShell := false
	for _, entry := range environment {
		hasTerm = hasTerm || strings.HasPrefix(entry, "TERM=")
		hasShell = hasShell || strings.HasPrefix(entry, "SHELL=")
	}
	if !hasTerm {
		environment = append(environment, "TERM=xterm-256color")
	}
	if !hasShell {
		environment = append(environment, "SHELL="+shell)
	}
	return environment
}

func parseWorkspaceTerminalSignal(name string) (syscall.Signal, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "INT", "SIGINT":
		return syscall.SIGINT, true
	case "TERM", "SIGTERM":
		return syscall.SIGTERM, true
	case "HUP", "SIGHUP":
		return syscall.SIGHUP, true
	case "QUIT", "SIGQUIT":
		return syscall.SIGQUIT, true
	default:
		return 0, false
	}
}

func boundedWorkspaceTerminalRows(value int) int {
	if value <= 0 {
		return workspaceTerminalDefaultRows
	}
	if value > workspaceTerminalMaxRows {
		return workspaceTerminalMaxRows
	}
	return value
}

func boundedWorkspaceTerminalCols(value int) int {
	if value <= 0 {
		return workspaceTerminalDefaultCols
	}
	if value > workspaceTerminalMaxCols {
		return workspaceTerminalMaxCols
	}
	return value
}

func boundedWorkspaceTerminalReadBytes(value int) int {
	if value <= 0 {
		return workspaceTerminalDefaultReadBytes
	}
	if value > workspaceTerminalMaxReadBytes {
		return workspaceTerminalMaxReadBytes
	}
	return value
}

func boundedWorkspaceTerminalReadWait(waitMS int) time.Duration {
	if waitMS <= 0 {
		return workspaceTerminalDefaultReadWait
	}
	wait := time.Duration(waitMS) * time.Millisecond
	if wait > workspaceTerminalMaxReadWait {
		return workspaceTerminalMaxReadWait
	}
	return wait
}

func stopWorkspaceTerminalTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func waitWorkspaceTerminalDone(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer stopWorkspaceTerminalTimer(timer)
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func workspaceTerminalDone(done <-chan struct{}) bool {
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func combineWorkspaceTerminalErrors(first, second error) error {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	default:
		return errors.Wrapf(first, "additional workspace terminal cleanup error: %v", second)
	}
}

func workspaceTerminalLeaderExited(sessionID int) (bool, error) {
	return workspaceTerminalProcessExited(sessionID)
}

func terminateWorkspaceTerminalSession(sessionID int, timeout time.Duration) error {
	if sessionID <= 0 {
		return errors.New("workspace terminal session id is invalid")
	}
	deadline := time.Now().Add(timeout)
	var firstErr error
	for {
		processes, err := listWorkspaceTerminalSessionProcesses(sessionID)
		if err != nil {
			if leaderErr := syscall.Kill(sessionID, syscall.SIGKILL); leaderErr != nil && !errors.Is(leaderErr, syscall.ESRCH) {
				firstErr = combineWorkspaceTerminalErrors(firstErr, leaderErr)
			}
			return combineWorkspaceTerminalErrors(errors.Wrap(err, "failed to enumerate workspace terminal session"), firstErr)
		}

		live := false
		for _, leader := range []bool{false, true} {
			for _, process := range processes {
				if process.Zombie || (process.PID == sessionID) != leader {
					continue
				}
				live = true
				if killErr := signalWorkspaceTerminalProcess(process, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
					firstErr = combineWorkspaceTerminalErrors(firstErr, errors.Wrapf(killErr, "failed to kill workspace terminal process %d", process.PID))
				}
			}
		}
		closeWorkspaceTerminalProcesses(processes)
		if !live {
			return firstErr
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return combineWorkspaceTerminalErrors(firstErr, errors.New("workspace terminal processes did not stop before the cleanup deadline"))
		}
		timer := time.NewTimer(20 * time.Millisecond)
		<-timer.C
	}
}

func closeWorkspaceTerminalProcesses(processes []workspaceTerminalProcess) {
	for _, process := range processes {
		closeWorkspaceTerminalProcess(process)
	}
}
