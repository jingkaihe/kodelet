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
	workspaceTerminalGracefulWait      = 500 * time.Millisecond
	workspaceTerminalCleanupPollWait   = 20 * time.Millisecond
	workspaceTerminalProcessKillWait   = 2 * time.Second
	workspaceTerminalShutdownWait      = 4 * time.Second
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
	current  *workspaceTerminalSession
	closed   bool
	closeErr error
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
	processDone  chan workspaceTerminalProcessResult
	shutdownCh   chan struct{}
	done         chan struct{}
	doneOnce     sync.Once
	finishOnce   sync.Once
	shutdownOnce sync.Once
	cleanupErr   error
}

type workspaceTerminalProcessResult struct {
	exitCode int
	err      error
}

func newWorkspaceTerminalManager(ctx context.Context, cwd string) *workspaceTerminalManager {
	if ctx == nil {
		ctx = context.Background()
	}
	manager := &workspaceTerminalManager{ctx: ctx, cwd: cwd}
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
		previous := m.current
		if err := previous.terminate(); err != nil {
			m.mu.Unlock()
			return protocol.WorkspaceTerminalOpenResult{}, errors.Wrap(err, "previous workspace terminal cleanup is incomplete")
		}
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
	m.current = session
	session.start(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		terminateErr := session.terminate()
		if terminateErr == nil {
			m.current = nil
		}
		if terminateErr != nil {
			m.mu.Unlock()
			return protocol.WorkspaceTerminalOpenResult{}, errors.Wrapf(ctxErr, "terminal open was canceled; cleanup failed: %v", terminateErr)
		}
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
	if m.current == nil || m.current.id != sessionID {
		return nil, errors.New("terminal session was not found")
	}
	return m.current, nil
}

func (m *workspaceTerminalManager) Close() error {
	if m == nil {
		return nil
	}
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return m.closeErr
	}
	m.closed = true
	session := m.current
	m.mu.Unlock()

	if session != nil {
		if err := session.terminate(); err != nil {
			m.closeErr = errors.Wrap(err, "failed to stop workspace terminal")
		}
		m.mu.Lock()
		if m.current == session {
			m.current = nil
		}
		m.mu.Unlock()
	}
	return m.closeErr
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
		id:          id,
		cwd:         cwd,
		shellName:   shellName,
		gitRepo:     gitRepo,
		ctx:         sessionCtx,
		cancel:      cancel,
		cmd:         cmd,
		ptmx:        ptmx,
		updated:     make(chan struct{}),
		inputCh:     make(chan []byte, workspaceTerminalInputBufferLength),
		ptyDone:     make(chan struct{}),
		processDone: make(chan workspaceTerminalProcessResult, 1),
		shutdownCh:  make(chan struct{}),
		done:        make(chan struct{}),
	}, nil
}

func (s *workspaceTerminalSession) start(ctx context.Context) {
	go s.readPTY()
	go s.writePTY()
	go func() {
		s.processDone <- s.waitProcess()
	}()
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
	select {
	case result := <-s.processDone:
		s.finishSupervision(ctx, &result)
	case <-s.ptyDone:
		s.finishSupervision(ctx, nil)
	case <-s.shutdownCh:
		s.finishSupervision(ctx, nil)
	}
}

func (s *workspaceTerminalSession) finishSupervision(ctx context.Context, processResult *workspaceTerminalProcessResult) {
	processGroups := s.processGroups()
	s.beginEnding()
	s.cancel()
	graceDeadline := time.Now().Add(workspaceTerminalGracefulWait)
	cleanupErr := signalWorkspaceTerminalProcessGroups(processGroups, syscall.SIGHUP)
	s.closePTY()
	ptyStopped := waitWorkspaceTerminalDone(s.ptyDone, time.Until(graceDeadline))

	result, exited := workspaceTerminalProcessResult{}, false
	if processResult != nil {
		result = *processResult
		exited = true
	} else {
		result, exited = waitWorkspaceTerminalProcess(s.processDone, time.Until(graceDeadline))
	}
	processGroupsStopped := waitWorkspaceTerminalProcessGroups(processGroups, time.Until(graceDeadline))
	if !processGroupsStopped {
		cleanupErr = combineWorkspaceTerminalErrors(cleanupErr, signalWorkspaceTerminalProcessGroups(processGroups, syscall.SIGKILL))
	}
	if !exited || !processGroupsStopped || !ptyStopped {
		killDeadline := time.Now().Add(workspaceTerminalProcessKillWait)
		if !exited {
			result, exited = waitWorkspaceTerminalProcess(s.processDone, time.Until(killDeadline))
		}
		if !processGroupsStopped {
			processGroupsStopped = waitWorkspaceTerminalProcessGroups(processGroups, time.Until(killDeadline))
		}
		if !ptyStopped {
			ptyStopped = waitWorkspaceTerminalDone(s.ptyDone, time.Until(killDeadline))
		}
	}
	if !exited {
		cleanupErr = combineWorkspaceTerminalErrors(cleanupErr, errors.New("workspace terminal process did not stop before the cleanup deadline"))
	}
	if !processGroupsStopped {
		cleanupErr = combineWorkspaceTerminalErrors(cleanupErr, errors.New("workspace terminal process groups did not stop before the cleanup deadline"))
	}
	if !ptyStopped {
		cleanupErr = combineWorkspaceTerminalErrors(cleanupErr, errors.New("workspace terminal PTY did not close before the cleanup deadline"))
	}
	if result.err != nil {
		logger.G(ctx).WithError(result.err).Warn("workspace terminal process could not be reaped cleanly")
		cleanupErr = combineWorkspaceTerminalErrors(cleanupErr, result.err)
	}
	s.finish(result.exitCode, cleanupErr)
}

func (s *workspaceTerminalSession) processGroups() []int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	groups := make([]int, 0, 2)
	s.ptyMu.Lock()
	if s.ptmx != nil {
		if foregroundGroup, err := unix.IoctlGetInt(int(s.ptmx.Fd()), unix.TIOCGPGRP); err == nil && foregroundGroup > 0 {
			groups = append(groups, foregroundGroup)
		}
	}
	s.ptyMu.Unlock()
	if processGroup := s.cmd.Process.Pid; processGroup > 0 && (len(groups) == 0 || groups[0] != processGroup) {
		groups = append(groups, processGroup)
	}
	return groups
}

func signalWorkspaceTerminalProcessGroups(groups []int, signal syscall.Signal) error {
	var firstErr error
	for _, group := range groups {
		if group <= 0 {
			continue
		}
		if err := syscall.Kill(-group, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			firstErr = combineWorkspaceTerminalErrors(firstErr, errors.Wrapf(err, "failed to signal workspace terminal process group %d", group))
		}
	}
	return firstErr
}

func (s *workspaceTerminalSession) waitProcess() workspaceTerminalProcessResult {
	waitErr := s.cmd.Wait()
	if waitErr == nil {
		return workspaceTerminalProcessResult{}
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return workspaceTerminalProcessResult{exitCode: exitErr.ExitCode()}
	}
	return workspaceTerminalProcessResult{err: waitErr}
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

func (s *workspaceTerminalSession) finish(exitCode int, cleanupErr error) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.ending = true
		s.exited = true
		s.exitCode = exitCode
		s.cleanupErr = cleanupErr
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
		if s.shutdownCh != nil {
			close(s.shutdownCh)
		}
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

func (s *workspaceTerminalSession) closePTY() {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
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

func waitWorkspaceTerminalProcess(done <-chan workspaceTerminalProcessResult, timeout time.Duration) (workspaceTerminalProcessResult, bool) {
	if done == nil {
		return workspaceTerminalProcessResult{}, false
	}
	if timeout <= 0 {
		select {
		case result := <-done:
			return result, true
		default:
			return workspaceTerminalProcessResult{}, false
		}
	}
	timer := time.NewTimer(timeout)
	defer stopWorkspaceTerminalTimer(timer)
	select {
	case result := <-done:
		return result, true
	case <-timer.C:
		return workspaceTerminalProcessResult{}, false
	}
}

func waitWorkspaceTerminalProcessGroups(groups []int, timeout time.Duration) bool {
	if workspaceTerminalProcessGroupsStopped(groups) {
		return true
	}
	if timeout <= 0 {
		return false
	}

	timer := time.NewTimer(timeout)
	ticker := time.NewTicker(workspaceTerminalCleanupPollWait)
	defer stopWorkspaceTerminalTimer(timer)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if workspaceTerminalProcessGroupsStopped(groups) {
				return true
			}
		case <-timer.C:
			return workspaceTerminalProcessGroupsStopped(groups)
		}
	}
}

func workspaceTerminalProcessGroupsStopped(groups []int) bool {
	for _, group := range groups {
		if group <= 0 {
			continue
		}
		err := syscall.Kill(-group, 0)
		if err == nil || errors.Is(err, syscall.EPERM) {
			return false
		}
	}
	return true
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
