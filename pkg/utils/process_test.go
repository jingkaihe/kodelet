package utils

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsProcessAlive(t *testing.T) {
	tests := []struct {
		name     string
		pid      int
		expected bool
	}{
		{
			name:     "current process should be alive",
			pid:      os.Getpid(),
			expected: true,
		},
		{
			name:     "process 1 should be alive on unix systems",
			pid:      1,
			expected: true,
		},
		{
			name:     "very high PID should not exist",
			pid:      99999999,
			expected: false,
		},
		{
			name:     "negative PID should not exist",
			pid:      -1,
			expected: false,
		},
		{
			name:     "zero PID should not exist",
			pid:      0,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsProcessAlive(tc.pid)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestIsProcessAliveWithShortLivedProcess(t *testing.T) {
	// Create a short-lived process
	cmd := exec.Command("sleep", "0.1")
	err := cmd.Start()
	require.NoError(t, err)

	pid := cmd.Process.Pid

	// Process should be alive initially
	assert.True(t, IsProcessAlive(pid))

	// Wait for process to finish
	err = cmd.Wait()
	require.NoError(t, err)

	// Give it a moment to ensure the process is cleaned up
	time.Sleep(100 * time.Millisecond)

	// Process should no longer be alive
	assert.False(t, IsProcessAlive(pid))
}

func TestReattachProcess(t *testing.T) {
	// Create a long-lived process for testing
	cmd := exec.Command("sleep", "1")
	err := cmd.Start()
	require.NoError(t, err)

	originalPID := cmd.Process.Pid

	// Clean up the process when test finishes
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	tests := []struct {
		name           string
		savedProcess   tools.BackgroundProcess
		expectError    bool
		validateResult func(t *testing.T, result tools.BackgroundProcess)
	}{
		{
			name: "reattach to existing process",
			savedProcess: tools.BackgroundProcess{
				PID:       originalPID,
				Command:   "sleep 1",
				LogPath:   "/tmp/test.log",
				StartTime: time.Now(),
				Process:   nil, // This will be set by ReattachProcess
			},
			expectError: false,
			validateResult: func(t *testing.T, result tools.BackgroundProcess) {
				assert.Equal(t, originalPID, result.PID)
				assert.Equal(t, "sleep 1", result.Command)
				assert.Equal(t, "/tmp/test.log", result.LogPath)
				assert.NotNil(t, result.Process)
				assert.Equal(t, originalPID, result.Process.Pid)
			},
		},
		{
			name: "reattach to non-existent process",
			savedProcess: tools.BackgroundProcess{
				PID:       99999999,
				Command:   "nonexistent command",
				LogPath:   "/tmp/nonexistent.log",
				StartTime: time.Now(),
				Process:   nil,
			},
			expectError: true,
			validateResult: func(t *testing.T, result tools.BackgroundProcess) {
				// Result should be empty on error
				assert.Equal(t, 0, result.PID)
				assert.Equal(t, "", result.Command)
				assert.Equal(t, "", result.LogPath)
				assert.Nil(t, result.Process)
			},
		},
		{
			name: "reattach with negative PID",
			savedProcess: tools.BackgroundProcess{
				PID:       -1,
				Command:   "invalid command",
				LogPath:   "/tmp/invalid.log",
				StartTime: time.Now(),
				Process:   nil,
			},
			expectError: true,
			validateResult: func(t *testing.T, result tools.BackgroundProcess) {
				assert.Equal(t, 0, result.PID)
				assert.Equal(t, "", result.Command)
				assert.Equal(t, "", result.LogPath)
				assert.Nil(t, result.Process)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ReattachProcess(tc.savedProcess)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "failed to find process")
			} else {
				assert.NoError(t, err)
			}

			tc.validateResult(t, result)
		})
	}
}

func TestReattachProcessPreservesOriginalData(t *testing.T) {
	// Create a long-lived process
	cmd := exec.Command("sleep", "1")
	err := cmd.Start()
	require.NoError(t, err)

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	originalTime := time.Now().Add(-1 * time.Hour) // Set to 1 hour ago
	originalProcess := tools.BackgroundProcess{
		PID:       cmd.Process.Pid,
		Command:   "test command",
		LogPath:   "/tmp/test.log",
		StartTime: originalTime,
		Process:   nil,
	}

	result, err := ReattachProcess(originalProcess)
	require.NoError(t, err)

	// Verify that all original data is preserved
	assert.Equal(t, originalProcess.PID, result.PID)
	assert.Equal(t, originalProcess.Command, result.Command)
	assert.Equal(t, originalProcess.LogPath, result.LogPath)
	assert.Equal(t, originalProcess.StartTime, result.StartTime)

	// Only the Process field should be updated
	assert.NotNil(t, result.Process)
	assert.Equal(t, originalProcess.PID, result.Process.Pid)
}

func TestReattachProcessAfterProcessTermination(t *testing.T) {
	// Create a short-lived process
	cmd := exec.Command("sleep", "0.1")
	err := cmd.Start()
	require.NoError(t, err)

	pid := cmd.Process.Pid

	// Wait for process to finish
	err = cmd.Wait()
	require.NoError(t, err)

	// Give it a moment to ensure the process is cleaned up
	time.Sleep(100 * time.Millisecond)

	savedProcess := tools.BackgroundProcess{
		PID:       pid,
		Command:   "sleep 0.1",
		LogPath:   "/tmp/finished.log",
		StartTime: time.Now(),
		Process:   nil,
	}

	result, err := ReattachProcess(savedProcess)

	// On most systems, trying to find a terminated process should fail
	// However, behavior might vary by OS, so we'll check both cases
	if err != nil {
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find process")
		assert.Equal(t, 0, result.PID)
	} else {
		// If os.FindProcess succeeds (some systems return a process even if it's dead),
		// we should still get back the original data with the process reference
		assert.Equal(t, pid, result.PID)
		assert.NotNil(t, result.Process)
	}
}
