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
				if !strings.Contains(output, "Process ID:") {
					t.Errorf("Expected output to contain PID information, got: %s", output)
				}

				// Should mention background process started
				outputLower := strings.ToLower(output)
				if !strings.Contains(outputLower, "background") {
					t.Errorf("Expected output to mention background process, got: %s", output)
				}

				// Should contain log file path
				if !strings.Contains(output, ".kodelet") && !strings.Contains(output, "out.log") {
					t.Errorf("Expected output to contain log file path, got: %s", output)
				}
			},
		},
		{
			name:  "start python http server and curl endpoint",
			query: `create index.html with "hello world" content, start a python http server on port 8080 in the background, then curl the endpoint and write the result to hello.txt`,
			validate: func(t *testing.T, output string, testDir string) {
				// Should contain PID information for the background process
				if !strings.Contains(output, "Process ID:") {
					t.Errorf("Expected output to contain PID information, got: %s", output)
				}

				// Wait for the server to start and curl to complete
				indexFile := filepath.Join(testDir, "index.html")
				helloFile := filepath.Join(testDir, "hello.txt")
				
				if !utils.WaitForFiles(10*time.Second, 100*time.Millisecond, indexFile, helloFile) {
					t.Errorf("Expected files to be created within timeout")
					return
				}

				// Check if index.html was created
				indexContent, err := os.ReadFile(indexFile)
				if err != nil {
					t.Errorf("index.html should have been created: %v", err)
					return
				}

				indexStr := strings.TrimSpace(string(indexContent))
				if !strings.Contains(indexStr, "hello world") {
					t.Errorf("Expected index.html content 'hello world', got '%s'", indexStr)
				}

				// Check if hello.txt was created with the curl result
				helloContent, err := os.ReadFile(helloFile)
				if err != nil {
					t.Errorf("hello.txt should have been created by curl: %v", err)
					return
				}

				helloStr := strings.TrimSpace(string(helloContent))
				if !strings.Contains(helloStr, "hello world") {
					t.Errorf("Expected hello.txt to contain 'hello world' from curl response, got '%s'", helloStr)
				}
			},
		},
		{
			name:  "start background process that runs for longer duration",
			query: `run a background process that writes current time every second for 2 iterations: "for i in {1..2}; do echo $(date); sleep 1; done"`,
			validate: func(t *testing.T, output string, testDir string) {
				// Should contain PID information
				if !strings.Contains(output, "Process ID:") {
					t.Errorf("Expected output to contain PID information, got: %s", output)
				}

				// Extract PID from output
				lines := strings.Split(output, "\n")
				var pid string
				for _, line := range lines {
					if strings.HasPrefix(line, "Process ID:") {
						pid = strings.TrimSpace(strings.TrimPrefix(line, "Process ID:"))
						break
					}
				}

				if pid == "" {
					t.Errorf("Could not extract PID from output: %s", output)
					return
				}

				// Wait for the process to complete and log to contain expected content
				logPath := filepath.Join(testDir, ".kodelet", pid, "out.log")
				if !utils.WaitForCondition(10*time.Second, 200*time.Millisecond, func() bool {
					if logContent, err := os.ReadFile(logPath); err == nil {
						logStr := string(logContent)
						// Should contain at least 2 date entries (one per iteration)
						return strings.Count(logStr, "202") >= 2
					}
					return false
				}) {
					t.Errorf("Expected log file to contain at least 2 date entries within timeout")
					return
				}

				// Check if log file exists and contains expected output
				if _, err := os.Stat(logPath); os.IsNotExist(err) {
					t.Errorf("Log file should exist at %s", logPath)
					return
				}

				logContent, err := os.ReadFile(logPath)
				if err != nil {
					t.Errorf("Failed to read log File: %v", err)
					return
				}

				logStr := string(logContent)
				// Should contain at least 2 date entries (one per iteration)
				dateCount := strings.Count(logStr, "202") // Assuming we're in the 2020s
				if dateCount < 2 {
					t.Errorf("Expected at least 2 date entries in log, got %d. Log content: %s", dateCount, logStr)
				}
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
			if strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal") {
				t.Errorf("Command should not panic or crash: %s", outputStr)
				return
			}

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
				if !strings.Contains(outputLower, "no background processes") && !strings.Contains(outputLower, "no processes") {
					t.Errorf("Expected output to indicate no background processes, got: %s", output)
				}
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
				if !strings.Contains(output, "PID") || !strings.Contains(output, "Status") || !strings.Contains(output, "Command") {
					t.Errorf("Expected output to contain table headers (PID, Status, Command), got: %s", output)
				}

				// Should contain background processes information
				outputLower := strings.ToLower(output)
				if !strings.Contains(outputLower, "background processes") {
					t.Errorf("Expected output to mention background processes, got: %s", output)
				}

				// If we have expected PIDs, check that they appear in the output
				for _, expectedPID := range expectedPIDs {
					if expectedPID != "" && !strings.Contains(output, expectedPID) {
						t.Logf("Expected PID %s not found in output (this might be ok if process completed quickly): %s", expectedPID, output)
					}
				}

				// Should contain status information (running/stopped)
				if !strings.Contains(outputLower, "running") && !strings.Contains(outputLower, "stopped") {
					t.Errorf("Expected output to contain status information (running/stopped), got: %s", output)
				}
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
			if strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal") {
				t.Errorf("Command should not panic or crash: %s", outputStr)
				return
			}

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
		if strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal") {
			t.Errorf("Command should not panic or crash: %s", outputStr)
			return
		}

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

		if pid == "" {
			t.Errorf("Could not extract PID from output: %s", outputStr)
			return
		}

		if logPath == "" {
			t.Errorf("Could not extract log path from output: %s", outputStr)
			return
		}

		// Wait for the background process to complete and log to contain all expected lines
		expectedLines := []string{"Hello from background", "Line 2", "Line 3"}
		
		if !utils.WaitForFileContent(5*time.Second, 100*time.Millisecond, logPath, expectedLines) {
			t.Errorf("Expected log file to contain all expected lines within timeout")
			return
		}

		// Check if log file exists
		if _, err := os.Stat(logPath); os.IsNotExist(err) {
			t.Errorf("Log file should exist at %s", logPath)
			return
		}

		// Read log file content
		logContent, err := os.ReadFile(logPath)
		if err != nil {
			t.Errorf("Failed to read log File: %v", err)
			return
		}

		logStr := string(logContent)
		// expectedLines already defined above

		for _, expectedLine := range expectedLines {
			if !strings.Contains(logStr, expectedLine) {
				t.Errorf("Expected log to contain '%s', but log content is: %s", expectedLine, logStr)
			}
		}

		// Verify the log file is in the expected location (.kodelet/{PID}/out.log)
		expectedLogPath := filepath.Join(testDir, ".kodelet", pid, "out.log")
		if logPath != expectedLogPath {
			t.Errorf("Expected log path to be %s, got %s", expectedLogPath, logPath)
		}

		// Cleanup
		if pidInt, err := strconv.Atoi(pid); err == nil {
			if process, err := os.FindProcess(pidInt); err == nil {
				process.Kill()
			}
		}
	})
}
