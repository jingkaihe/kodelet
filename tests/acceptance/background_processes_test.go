package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestBashToolBackgroundParameter(t *testing.T) {
	// Create a temporary test directory for file operations
	testDir := t.TempDir()

	testCases := []struct {
		name     string
		query    string
		validate func(t *testing.T, output string, testDir string)
	}{
		{
			name:  "start simple background process",
			query: `run "sleep 2" in the background`,
			validate: func(t *testing.T, output string, testDir string) {
				// Should contain PID information
				assert.Contains(t, output, "Process ID:", "Expected output to contain PID information")

				// Should mention background process started
				outputLower := strings.ToLower(output)
				assert.Contains(t, outputLower, "background", "Expected output to mention background process")

				// Should contain log file path
				assert.True(t, strings.Contains(output, ".kodelet") || strings.Contains(output, "out.log"), "Expected output to contain log file path, got: %s", output)
			},
		},
		{
			name:  "start python http server and curl endpoint",
			query: `create index.html with "hello world" content, start a python http server on port 8080 in the background, then curl the endpoint and write the result to hello.txt`,
			validate: func(t *testing.T, output string, testDir string) {
				// Should contain PID information for the background process
				assert.Contains(t, output, "Process ID:", "Expected output to contain PID information")

				// Wait for the server to start and curl to complete
				indexFile := filepath.Join(testDir, "index.html")
				helloFile := filepath.Join(testDir, "hello.txt")

				assert.True(t, utils.WaitForFiles(10*time.Second, 100*time.Millisecond, indexFile, helloFile), "Expected files to be created within timeout")

				// Check if index.html was created
				indexContent, err := os.ReadFile(indexFile)
				assert.NoError(t, err, "index.html should have been created")

				indexStr := strings.TrimSpace(string(indexContent))
				assert.Contains(t, indexStr, "hello world", "Expected index.html content 'hello world'")

				// Check if hello.txt was created with the curl result
				helloContent, err := os.ReadFile(helloFile)
				assert.NoError(t, err, "hello.txt should have been created by curl")

				helloStr := strings.TrimSpace(string(helloContent))
				assert.Contains(t, helloStr, "hello world", "Expected hello.txt to contain 'hello world' from curl response")
			},
		},
		{
			name:  "start background process that runs for longer duration",
			query: `run a background process that writes current time every second for 2 iterations: "for i in {1..2}; do echo $(date); sleep 1; done"`,
			validate: func(t *testing.T, output string, testDir string) {
				// Should contain PID information
				assert.Contains(t, output, "Process ID:", "Expected output to contain PID information")

				// Extract PID from output
				lines := strings.Split(output, "\n")
				var pid string
				for _, line := range lines {
					if strings.HasPrefix(line, "Process ID:") {
						pid = strings.TrimSpace(strings.TrimPrefix(line, "Process ID:"))
						break
					}
				}

				assert.NotEmpty(t, pid, "Could not extract PID from output: %s", output)

				// Wait for the process to complete and log to contain expected content
				logPath := filepath.Join(testDir, ".kodelet", pid, "out.log")
				assert.True(t, utils.WaitForCondition(10*time.Second, 200*time.Millisecond, func() bool {
					if logContent, err := os.ReadFile(logPath); err == nil {
						logStr := string(logContent)
						// Should contain at least 2 date entries (one per iteration)
						return strings.Count(logStr, "202") >= 2
					}
					return false
				}), "Expected log file to contain at least 2 date entries within timeout")

				// Check if log file exists and contains expected output
				assert.FileExists(t, logPath, "Log file should exist at %s", logPath)

				logContent, err := os.ReadFile(logPath)
				assert.NoError(t, err, "Failed to read log File")

				logStr := string(logContent)
				// Should contain at least 2 date entries (one per iteration)
				dateCount := strings.Count(logStr, "202") // Assuming we're in the 2020s
				assert.GreaterOrEqual(t, dateCount, 2, "Expected at least 2 date entries in log, got %d. Log content: %s", dateCount, logStr)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Change to test directory for file operations
			originalDir, _ := os.Getwd()
			os.Chdir(testDir)
			defer os.Chdir(originalDir)

			// Execute kodelet run command
			cmd := exec.Command("kodelet", "run", "--no-save", tc.query)
			cmd.Dir = testDir

			output, err := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))
			t.Logf("output: %s", outputStr)

			// For these tests, we mainly care that the command doesn't crash
			// and produces reasonable output
			assert.False(t, strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal"), "Command should not panic or crash: %s", outputStr)

			// Skip validation if command failed due to missing API keys
			if err != nil && (strings.Contains(outputStr, "API key") || strings.Contains(outputStr, "api key")) {
				t.Skipf("Skipping test due to missing API key: %v", err)
				return
			}

			// Run custom validation
			tc.validate(t, outputStr, testDir)
		})
	}
}

func TestViewBackgroundProcessesTool(t *testing.T) {
	// Create a temporary test directory for file operations
	testDir := t.TempDir()

	testCases := []struct {
		name     string
		setup    func(t *testing.T, testDir string) []string // Returns PIDs of started processes
		query    string
		validate func(t *testing.T, output string, testDir string, expectedPIDs []string)
	}{
		{
			name: "view background processes when none are running",
			setup: func(t *testing.T, testDir string) []string {
				return []string{} // No processes to start
			},
			query: "show me all background processes",
			validate: func(t *testing.T, output string, testDir string, expectedPIDs []string) {
				outputLower := strings.ToLower(output)
				assert.True(t, strings.Contains(outputLower, "no background processes") || strings.Contains(outputLower, "no processes"), "Expected output to indicate no background processes, got: %s", output)
			},
		},
		{
			name: "view background processes with running processes",
			setup: func(t *testing.T, testDir string) []string {
				return []string{} // No separate setup needed - everything is done in the query
			},
			query: `run "sleep 3" in the background, also run "sleep 2" in the background, then show me all background processes`,
			validate: func(t *testing.T, output string, testDir string, expectedPIDs []string) {
				// Should contain table headers
				assert.True(t, strings.Contains(output, "PID") && strings.Contains(output, "Status") && strings.Contains(output, "Command"), "Expected output to contain table headers (PID, Status, Command), got: %s", output)

				// Should contain background processes information
				outputLower := strings.ToLower(output)
				assert.Contains(t, outputLower, "background processes", "Expected output to mention background processes")

				// If we have expected PIDs, check that they appear in the output
				for _, expectedPID := range expectedPIDs {
					if expectedPID != "" && !strings.Contains(output, expectedPID) {
						t.Logf("Expected PID %s not found in output (this might be ok if process completed quickly): %s", expectedPID, output)
					}
				}

				// Should contain status information (running/stopped)
				assert.True(t, strings.Contains(outputLower, "running") || strings.Contains(outputLower, "stopped"), "Expected output to contain status information (running/stopped), got: %s", output)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Change to test directory for file operations
			originalDir, _ := os.Getwd()
			os.Chdir(testDir)
			defer os.Chdir(originalDir)

			// Setup any required background processes
			expectedPIDs := tc.setup(t, testDir)

			// Execute kodelet run command to view background processes
			cmd := exec.Command("kodelet", "run", "--no-save", tc.query)
			cmd.Dir = testDir

			output, err := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))
			t.Logf("output: %s", outputStr)

			// For these tests, we mainly care that the command doesn't crash
			// and produces reasonable output
			assert.False(t, strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal"), "Command should not panic or crash: %s", outputStr)

			// Skip validation if command failed due to missing API keys
			if err != nil && (strings.Contains(outputStr, "API key") || strings.Contains(outputStr, "api key")) {
				t.Skipf("Skipping test due to missing API key: %v", err)
				return
			}

			// Run custom validation
			tc.validate(t, outputStr, testDir, expectedPIDs)

			// Cleanup: try to kill any remaining processes
			for _, pidStr := range expectedPIDs {
				if pidStr != "" {
					if pid, err := strconv.Atoi(pidStr); err == nil {
						if process, err := os.FindProcess(pid); err == nil {
							process.Kill()
						}
					}
				}
			}
		})
	}
}

func TestBackgroundProcessLogFiles(t *testing.T) {
	// Create a temporary test directory for file operations
	testDir := t.TempDir()

	t.Run("background process creates log file", func(t *testing.T) {
		// Change to test directory for file operations
		originalDir, _ := os.Getwd()
		os.Chdir(testDir)
		defer os.Chdir(originalDir)

		// Start a background process that produces output
		query := `run "echo 'Hello from background'; echo 'Line 2'; echo 'Line 3'" in the background`
		cmd := exec.Command("kodelet", "run", "--no-save", query)
		cmd.Dir = testDir

		output, err := cmd.CombinedOutput()
		outputStr := strings.TrimSpace(string(output))
		t.Logf("output: %s", outputStr)

		// Skip if no API key
		if err != nil && (strings.Contains(outputStr, "API key") || strings.Contains(outputStr, "api key")) {
			t.Skipf("Skipping test due to missing API key: %v", err)
			return
		}

		// Should not crash
		assert.False(t, strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal"), "Command should not panic or crash: %s", outputStr)

		// Extract PID and log path from output
		lines := strings.Split(outputStr, "\n")
		var pid, logPath string
		for _, line := range lines {
			if strings.HasPrefix(line, "Process ID:") {
				pid = strings.TrimSpace(strings.TrimPrefix(line, "Process ID:"))
			}
			if strings.HasPrefix(line, "Log File:") {
				logPath = strings.TrimSpace(strings.TrimPrefix(line, "Log File:"))
			}
		}

		assert.NotEmpty(t, pid, "Could not extract PID from output: %s", outputStr)
		assert.NotEmpty(t, logPath, "Could not extract log path from output: %s", outputStr)

		// Wait for the background process to complete and log to contain all expected lines
		expectedLines := []string{"Hello from background", "Line 2", "Line 3"}

		assert.True(t, utils.WaitForFileContent(5*time.Second, 100*time.Millisecond, logPath, expectedLines), "Expected log file to contain all expected lines within timeout")

		// Check if log file exists
		assert.FileExists(t, logPath, "Log file should exist at %s", logPath)

		// Read log file content
		logContent, err := os.ReadFile(logPath)
		assert.NoError(t, err, "Failed to read log File")

		logStr := string(logContent)
		// expectedLines already defined above

		for _, expectedLine := range expectedLines {
			assert.Contains(t, logStr, expectedLine, "Expected log to contain '%s', but log content is: %s", expectedLine, logStr)
		}

		// Verify the log file is in the expected location (.kodelet/{PID}/out.log)
		expectedLogPath := filepath.Join(testDir, ".kodelet", pid, "out.log")
		assert.Equal(t, expectedLogPath, logPath, "Expected log path to be %s, got %s", expectedLogPath, logPath)

		// Cleanup
		if pidInt, err := strconv.Atoi(pid); err == nil {
			if process, err := os.FindProcess(pidInt); err == nil {
				process.Kill()
			}
		}
	})
}
