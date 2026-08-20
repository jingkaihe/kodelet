//go:build linux

package client

import (
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

type linuxWorkspaceTerminalProcessStat struct {
	state      string
	pgid       int
	sessionID  int
	startTicks uint64
}

func workspaceTerminalProcessExited(pid int) (bool, error) {
	stat, err := readLinuxWorkspaceTerminalProcessStat(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return stat.state == "Z" || stat.state == "X", nil
}

func listWorkspaceTerminalSessionProcesses(sessionID int) ([]workspaceTerminalProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, errors.Wrap(err, "failed to read /proc")
	}
	processes := make([]workspaceTerminalProcess, 0)
	complete := false
	defer func() {
		if !complete {
			closeWorkspaceTerminalProcesses(processes)
		}
	}()
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 0 {
			continue
		}
		stat, statErr := readLinuxWorkspaceTerminalProcessStat(pid)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) || errors.Is(statErr, os.ErrPermission) {
				continue
			}
			return nil, statErr
		}
		if stat.sessionID != sessionID {
			continue
		}

		pidfd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			switch {
			case errors.Is(err, unix.ESRCH), errors.Is(err, unix.ENOENT):
				continue
			case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EPERM):
				pidfd = -1
			default:
				return nil, errors.Wrapf(err, "failed to open process handle for %d", pid)
			}
		}

		verified, verifyErr := readLinuxWorkspaceTerminalProcessStat(pid)
		if verifyErr != nil {
			if pidfd >= 0 {
				_ = unix.Close(pidfd)
			}
			if errors.Is(verifyErr, os.ErrNotExist) || errors.Is(verifyErr, os.ErrPermission) {
				continue
			}
			return nil, verifyErr
		}
		if verified.sessionID != sessionID || verified.startTicks != stat.startTicks {
			if pidfd >= 0 {
				_ = unix.Close(pidfd)
			}
			continue
		}
		processes = append(processes, workspaceTerminalProcess{
			PID:       pid,
			PGID:      verified.pgid,
			SessionID: verified.sessionID,
			Zombie:    verified.state == "Z" || verified.state == "X",
			pidfd:     pidfd,
			identity:  strconv.FormatUint(verified.startTicks, 10),
		})
	}
	complete = true
	return processes, nil
}

func signalWorkspaceTerminalProcess(process workspaceTerminalProcess, signal syscall.Signal) error {
	if process.pidfd >= 0 {
		return unix.PidfdSendSignal(process.pidfd, signal, nil, 0)
	}
	current, err := readLinuxWorkspaceTerminalProcessStat(process.PID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return syscall.ESRCH
		}
		return err
	}
	if current.sessionID != process.SessionID || strconv.FormatUint(current.startTicks, 10) != process.identity {
		return syscall.ESRCH
	}
	return syscall.Kill(process.PID, signal)
}

func closeWorkspaceTerminalProcess(process workspaceTerminalProcess) {
	if process.pidfd >= 0 {
		_ = unix.Close(process.pidfd)
	}
}

func readLinuxWorkspaceTerminalProcessStat(pid int) (linuxWorkspaceTerminalProcessStat, error) {
	payload, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return linuxWorkspaceTerminalProcessStat{}, err
	}
	closingParen := strings.LastIndexByte(string(payload), ')')
	if closingParen < 0 || closingParen+1 >= len(payload) {
		return linuxWorkspaceTerminalProcessStat{}, errors.Errorf("process %d returned malformed stat data", pid)
	}
	fields := strings.Fields(string(payload[closingParen+1:]))
	if len(fields) <= 19 {
		return linuxWorkspaceTerminalProcessStat{}, errors.Errorf("process %d returned incomplete stat data", pid)
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil {
		return linuxWorkspaceTerminalProcessStat{}, errors.Wrapf(err, "process %d returned invalid process group", pid)
	}
	sessionID, err := strconv.Atoi(fields[3])
	if err != nil {
		return linuxWorkspaceTerminalProcessStat{}, errors.Wrapf(err, "process %d returned invalid session id", pid)
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return linuxWorkspaceTerminalProcessStat{}, errors.Wrapf(err, "process %d returned invalid start time", pid)
	}
	return linuxWorkspaceTerminalProcessStat{
		state:      fields[0],
		pgid:       pgid,
		sessionID:  sessionID,
		startTicks: startTicks,
	}, nil
}
