package utils

import (
	"os"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/shirou/gopsutil/v4/process"
)

// IsProcessAlive checks if a process with the given PID is still running
func IsProcessAlive(pid int) bool {
	found, _ := process.PidExists(int32(pid))
	return found
}

// ReattachProcess attempts to reattach to an existing process
func ReattachProcess(savedProcess tools.BackgroundProcess) (tools.BackgroundProcess, error) {
	if !IsProcessAlive(savedProcess.PID) {
		return tools.BackgroundProcess{}, errors.Errorf("failed to find process %d", savedProcess.PID)
	}
	// no point of checking error as unix FindProcess won't return an error if the process is not found
	process, _ := os.FindProcess(savedProcess.PID)

	// Update the process reference
	savedProcess.Process = process
	return savedProcess, nil
}
