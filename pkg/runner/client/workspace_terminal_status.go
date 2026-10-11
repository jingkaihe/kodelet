package client

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

const (
	workspaceTerminalStatusInterval = time.Second
	workspaceTerminalStatusTimeout  = 500 * time.Millisecond
	workspaceTerminalCommandLimit   = 100
)

// readStatus probes only on attached reads. The separate lock keeps inspection
// off the output/input locks and shares one cached observation across readers.
func (s *workspaceTerminalSession) readStatus(ctx context.Context) *protocol.WorkspaceTerminalStatus {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.status.CWD == "" {
		s.status.CWD = s.cwd
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.isAlive() {
		s.status.ForegroundCommand = ""
	} else if ctx.Err() == nil && time.Since(s.statusChecked) >= workspaceTerminalStatusInterval {
		s.statusChecked = time.Now()
		probeCtx, cancel := context.WithTimeout(ctx, workspaceTerminalStatusTimeout)
		s.probeStatus(probeCtx)
		cancel()
	}
	// Never expose the cached value to callers after releasing statusMu.
	status := s.status
	return &status
}

// probeStatus retains the last observation on transient inspection failures.
// cwd is display metadata only: s.cwd remains the original canonical scope.
func (s *workspaceTerminalSession) probeStatus(ctx context.Context) {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	shellPID := s.cmd.Process.Pid
	if shell, err := process.NewProcessWithContext(ctx, int32(shellPID)); err == nil && ctx.Err() == nil {
		if cwd, err := shell.CwdWithContext(ctx); err == nil && cwd != "" {
			s.status.CWD = cwd
		}
	}
	foreground, err := s.foregroundProcessGroup()
	if err != nil || foreground <= 0 {
		return
	}
	if foreground == shellPID {
		// Background jobs must not keep a command title when the shell owns the TTY.
		s.status.ForegroundCommand = ""
		return
	}
	if ctx.Err() != nil {
		return
	}
	// Job-control shells make the foreground job leader its process-group leader.
	// Inspect it directly rather than scanning all descendants (including jobs
	// that are not foreground). If that leader has exited, retain the last title.
	proc, err := process.NewProcessWithContext(ctx, int32(foreground))
	if err != nil || ctx.Err() != nil {
		return
	}
	command := ""
	if args, err := proc.CmdlineSliceWithContext(ctx); err == nil {
		command = workspaceTerminalCommand(args)
	}
	if command == "" && ctx.Err() == nil {
		if name, err := proc.NameWithContext(ctx); err == nil {
			command = workspaceTerminalCommand([]string{name})
		}
	}
	if command != "" {
		s.status.ForegroundCommand = command
	}
}

func (s *workspaceTerminalSession) foregroundProcessGroup() (int, error) {
	s.ptyMu.Lock()
	defer s.ptyMu.Unlock()
	if s.ptmx == nil {
		return 0, errWorkspaceSessionClosed
	}
	return unix.IoctlGetInt(int(s.ptmx.Fd()), unix.TIOCGPGRP)
}

func workspaceTerminalCommand(args []string) string {
	parts := make([]string, 0, len(args))
	for i, arg := range args {
		arg = strings.Join(strings.Fields(arg), " ")
		if arg == "" {
			continue
		}
		if i == 0 {
			arg = filepath.Base(arg)
		}
		if strings.ContainsAny(arg, " \"\\") {
			arg = strconv.Quote(arg)
		}
		parts = append(parts, arg)
	}
	runes := []rune(strings.Join(parts, " "))
	if len(runes) > workspaceTerminalCommandLimit {
		return string(runes[:workspaceTerminalCommandLimit-1]) + "…"
	}
	return string(runes)
}
