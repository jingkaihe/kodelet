//go:build darwin

package client

import (
	"strconv"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const darwinProcessStateZombie = 5

func workspaceTerminalProcessExited(pid int) (bool, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EIO) {
			return true, nil
		}
		return false, err
	}
	if process == nil || int(process.Proc.P_pid) != pid {
		return true, nil
	}
	return process.Proc.P_stat == darwinProcessStateZombie, nil
}

func listWorkspaceTerminalSessionProcesses(sessionID int) ([]workspaceTerminalProcess, error) {
	processList, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, errors.Wrap(err, "failed to enumerate Darwin processes")
	}
	processes := make([]workspaceTerminalProcess, 0)
	for _, process := range processList {
		pid := int(process.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		sid, sidErr := unix.Getsid(pid)
		if sidErr == unix.ESRCH || sidErr == unix.EPERM || sidErr == unix.EACCES {
			continue
		}
		if sidErr != nil {
			return nil, errors.Wrapf(sidErr, "failed to inspect process %d session", pid)
		}
		if sid != sessionID {
			continue
		}
		processes = append(processes, workspaceTerminalProcess{
			PID:       pid,
			PGID:      int(process.Eproc.Pgid),
			SessionID: sid,
			Zombie:    process.Proc.P_stat == darwinProcessStateZombie,
			pidfd:     -1,
			identity:  darwinWorkspaceTerminalProcessIdentity(process.Proc.P_starttime),
		})
	}
	return processes, nil
}

func signalWorkspaceTerminalProcess(process workspaceTerminalProcess, signal syscall.Signal) error {
	current, err := unix.SysctlKinfoProc("kern.proc.pid", process.PID)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EIO) {
			return syscall.ESRCH
		}
		return err
	}
	if current == nil || int(current.Proc.P_pid) != process.PID ||
		darwinWorkspaceTerminalProcessIdentity(current.Proc.P_starttime) != process.identity {
		return syscall.ESRCH
	}
	sid, err := unix.Getsid(process.PID)
	if err != nil {
		return err
	}
	if sid != process.SessionID {
		return syscall.ESRCH
	}
	return syscall.Kill(process.PID, signal)
}

func closeWorkspaceTerminalProcess(workspaceTerminalProcess) {}

func darwinWorkspaceTerminalProcessIdentity(startTime unix.Timeval) string {
	return strconv.FormatInt(startTime.Sec, 10) + ":" + strconv.FormatInt(int64(startTime.Usec), 10)
}
