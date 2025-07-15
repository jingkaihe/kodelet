package utils

import (
	"os"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// IsProcessAlive checks if a process with the given PID is still running
func IsProcessAlive(pid int) bool {
	// Use kill syscall with signal 0 to check if process exists
	// This doesn't actually send a signal, just checks process existence
	err := syscall.Kill(pid, 0)
	return err == nil
}

// ReattachProcess attempts to reattach to an existing process
func ReattachProcess(savedProcess tools.BackgroundProcess) (tools.BackgroundProcess, error) {
	// Try to find the process
	process, err := os.FindProcess(savedProcess.PID)
	if err != nil {
		return tools.BackgroundProcess{}, errors.Wrapf(err, "failed to find process %d", savedProcess.PID)
	}

	// Update the process reference
	savedProcess.Process = process
	return savedProcess, nil
}
