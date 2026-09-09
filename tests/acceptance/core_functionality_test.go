package acceptance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoreFunctionality(t *testing.T) {
	// Missing credentials skip only before startup. A configured provider failure
	// is a test failure, never evidence that the scenario should be skipped.
	daemon := startCoreDaemon(t, coreProviderEnv(t))

	testCases := []struct {
		name     string
		query    string
		validate func(t *testing.T, output string, testDir string)
	}{
		{
			name:  "create hello.txt file",
			query: `create a hello.txt with "hello world" as the content`,
			validate: func(t *testing.T, _ string, testDir string) {
				helloFile := filepath.Join(testDir, "hello.txt")
				content, err := os.ReadFile(helloFile)
				require.NoError(t, err, "hello.txt must be created in the runner workspace")

				contentStr := strings.TrimSpace(string(content))
				assert.Equal(t, "hello world", contentStr)
			},
		},
		{
			name:  "detect operating system",
			query: "identify the operating system by running a command",
			validate: func(t *testing.T, output string, _ string) {
				outputLower := strings.ToLower(output)
				assert.Contains(t, outputLower, runtime.GOOS)
			},
		},
		{
			name:  "create fibonacci program",
			query: "write a fibonacci program in $TESTDIR/fib.py the fib.py should take a zero-based index as an argument and return the fibonacci number of the index",
			validate: func(t *testing.T, _ string, testDir string) {
				fibFile := filepath.Join(testDir, "fib.py")
				require.FileExists(t, fibFile, "fib.py must be created in the runner workspace")

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
						ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, "python3", fibFile, tc.input)
						output, err := cmd.CombinedOutput()
						require.NoError(t, err, "Python execution failed: %s", output)
						assert.Equal(t, tc.output, strings.TrimSpace(string(output)))
					})
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			query := strings.ReplaceAll(tc.query, "$TESTDIR", daemon.workspace)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "kodelet", "run",
				"--server="+daemon.serverURL, "--auth-token="+daemon.authToken,
				"--cwd="+daemon.workspace, "--no-extensions", "--no-skills", query)
			cmd.Dir = daemon.clientDir
			cmd.Env = daemon.clientEnv
			output, err := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))
			require.NoError(t, err, "daemon-backed run failed: %s", outputStr)
			tc.validate(t, outputStr, daemon.workspace)
			assert.NoFileExists(t, filepath.Join(daemon.clientDir, "hello.txt"))
			assert.NoFileExists(t, filepath.Join(daemon.clientDir, "fib.py"))
		})
	}
}

func coreProviderEnv(t *testing.T) []string {
	t.Helper()
	provider := os.Getenv("KODELET_PROVIDER")
	if provider == "" {
		provider = "anthropic"
		if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") != "" {
			provider = "openai"
		}
	}
	keyName := ""
	switch provider {
	case "anthropic":
		keyName = "ANTHROPIC_API_KEY"
	case "openai":
		keyName = "OPENAI_API_KEY"
	default:
		t.Fatalf("live core acceptance requires KODELET_PROVIDER=anthropic or openai, got %q", provider)
	}
	key := os.Getenv(keyName)
	if strings.TrimSpace(key) == "" {
		t.Skipf("live core acceptance requires configured %s for the isolated daemon", keyName)
	}
	env := []string{"KODELET_PROVIDER=" + provider, keyName + "=" + key}
	if model := os.Getenv("KODELET_MODEL"); model != "" {
		env = append(env, "KODELET_MODEL="+model)
	}
	return env
}
