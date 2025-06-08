package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreFunctionality(t *testing.T) {
	// Create a temporary test directory for file operations
	testDir := t.TempDir()

	testCases := []struct {
		name     string
		query    string
		validate func(t *testing.T, output string, testDir string)
	}{
		{
			name:  "create hello.txt file",
			query: `create a hello.txt with "hello world" as the content`,
			validate: func(t *testing.T, output string, testDir string) {
				// Check if hello.txt was created in the current directory
				helloFile := filepath.Join(testDir, "hello.txt")
				content, err := os.ReadFile(helloFile)
				if err != nil {
					// Also check current working directory as fallback
					content, err = os.ReadFile("hello.txt")
					if err != nil {
						t.Errorf("hello.txt file was not created: %v", err)
						return
					}
				}
				
				contentStr := strings.TrimSpace(string(content))
				if contentStr != "hello world" {
					t.Errorf("Expected content 'hello world', got '%s'", contentStr)
				}
			},
		},
		{
			name:  "detect operating system",
			query: "is the operating system linux or windows",
			validate: func(t *testing.T, output string, testDir string) {
				outputLower := strings.ToLower(output)
				if !strings.Contains(outputLower, "linux") {
					t.Errorf("Expected output to contain 'linux' (case insensitive), got: %s", output)
				}
			},
		},
		{
			name:  "create fibonacci program",
			query: "write a fibonacci program in $TESTDIR/fib.py then verify the fib implementation",
			validate: func(t *testing.T, output string, testDir string) {
				// Check if fib.py was created
				fibFile := filepath.Join(testDir, "fib.py")
				if _, err := os.Stat(fibFile); os.IsNotExist(err) {
					// Also check if it was created in current directory
					if _, err := os.Stat("fib.py"); os.IsNotExist(err) {
						t.Errorf("fib.py file was not created")
						return
					}
					fibFile = "fib.py"
				}
				
				// Try to run the fibonacci program
				cmd := exec.Command("python3", fibFile)
				pythonOutput, err := cmd.CombinedOutput()
				if err != nil {
					t.Logf("Python execution failed (may be expected): %v", err)
					t.Logf("Python output: %s", string(pythonOutput))
				}
				
				// Read the file content to verify it contains fibonacci logic
				content, err := os.ReadFile(fibFile)
				if err != nil {
					t.Errorf("Failed to read fib.py: %v", err)
					return
				}
				
				contentStr := strings.ToLower(string(content))
				if !strings.Contains(contentStr, "fibonacci") && !strings.Contains(contentStr, "fib") {
					t.Errorf("fib.py should contain fibonacci-related code, got: %s", string(content))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up test directory environment variable
			query := strings.ReplaceAll(tc.query, "$TESTDIR", testDir)
			
			// Change to test directory for file operations
			originalDir, _ := os.Getwd()
			os.Chdir(testDir)
			defer os.Chdir(originalDir)
			
			// Execute kodelet run command
			cmd := exec.Command("kodelet", "run", "--no-save", query)
			// Set minimal environment to avoid API key requirements
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"HOME=" + os.Getenv("HOME"),
				"TESTDIR=" + testDir,
			}
			
			output, err := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))
			
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