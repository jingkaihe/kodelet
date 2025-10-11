package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
			validate: func(t *testing.T, _ string, testDir string) {
				// Check if hello.txt was created in the current directory
				helloFile := filepath.Join(testDir, "hello.txt")
				content, err := os.ReadFile(helloFile)
				if err != nil {
					// Also check current working directory as fallback
					content, err = os.ReadFile("hello.txt")
					if err != nil {
						assert.Fail(t, "hello.txt file was not created", err.Error())
						return
					}
				}

				contentStr := strings.TrimSpace(string(content))
				assert.Equal(t, "hello world", contentStr)
			},
		},
		{
			name:  "detect operating system",
			query: "is the operating system linux or windows",
			validate: func(t *testing.T, output string, _ string) {
				outputLower := strings.ToLower(output)
				assert.Contains(t, outputLower, "linux", "Expected output to contain 'linux' (case insensitive)")
			},
		},
		{
			name:  "create fibonacci program",
			query: "write a fibonacci program in $TESTDIR/fib.py the fib.py should take a zero-based index as an argument and return the fibonacci number of the index",
			validate: func(t *testing.T, output string, testDir string) {
				// Check if fib.py was created
				fibFile := filepath.Join(testDir, "fib.py")
				if _, err := os.Stat(fibFile); os.IsNotExist(err) {
					// Also check if it was created in current directory
					if _, err := os.Stat("fib.py"); os.IsNotExist(err) {
						assert.Fail(t, "fib.py file was not created")
						return
					}
					fibFile = "fib.py"
				}

				cases := []struct {
					input  string
					output string
				}{
					{
						input:  "1",
						output: "1",
					},
					{
						input:  "2",
						output: "1",
					},
					{
						input:  "10",
						output: "55",
					},
				}

				for _, tc := range cases {
					t.Run(tc.input, func(t *testing.T) {
						cmd := exec.Command("python3", fibFile, tc.input)
						output, err := cmd.CombinedOutput()
						assert.NoError(t, err, "Python execution failed")
						if err != nil {
							return
						}
						assert.Equal(t, tc.output, strings.TrimSpace(string(output)))
					})
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
			cmd.Dir = testDir

			output, err := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))
			t.Logf("output: %s", outputStr)

			// For these tests, we mainly care that the command doesn't crash
			// and produces reasonable output
			if strings.Contains(outputStr, "panic") || strings.Contains(outputStr, "fatal") {
				assert.Fail(t, "Command should not panic or crash", outputStr)
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
