package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs setup and teardown for acceptance tests
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func commandEnv() []string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	return []string{
		"HOME=" + home,
		"PATH=" + path,
	}
}

type coreDaemon struct {
	serverURL string
	authToken string
	workspace string
	clientDir string
	clientEnv []string
}

// startCoreDaemon starts the installed binary, not an in-process server. Its
// environment intentionally differs from the client and owns all provider keys.
func startCoreDaemon(t *testing.T, providerEnv []string) coreDaemon {
	t.Helper()
	root := t.TempDir()
	daemonHome := filepath.Join(root, "daemon")
	clientDir := filepath.Join(root, "client")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{daemonHome, clientDir, workspace} {
		require.NoError(t, os.MkdirAll(path, 0o700))
	}
	// Even an accidental client store initialization must fail.
	clientState := filepath.Join(clientDir, "not-a-state-directory")
	require.NoError(t, os.WriteFile(clientState, []byte("client must not open a store"), 0o600))
	result := coreDaemon{
		authToken: "acceptance-client-secret", workspace: workspace, clientDir: clientDir,
		clientEnv: []string{"HOME=" + clientDir, "PATH=" + os.Getenv("PATH"), "KODELET_BASE_PATH=" + clientState},
	}
	logPath := filepath.Join(root, "daemon.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, logFile.Close()) })
	process := exec.Command("kodelet", "serve", "--host=127.0.0.1", "--port=0",
		"--auth-token="+result.authToken, "--runner-auth-token=acceptance-runner-secret",
		"--embedded-runner", "--runner-workspace="+workspace)
	process.Dir = daemonHome
	process.Env = append([]string{
		"HOME=" + daemonHome, "PATH=" + os.Getenv("PATH"),
		"KODELET_BASE_PATH=" + filepath.Join(daemonHome, "state"),
	}, providerEnv...)
	process.Stdout, process.Stderr = logFile, logFile
	require.NoError(t, process.Start())
	stopped := make(chan struct{})
	var processErr error
	go func() {
		processErr = process.Wait()
		close(stopped)
	}()
	t.Cleanup(func() {
		_ = process.Process.Signal(os.Interrupt)
		select {
		case <-stopped:
			assert.NoError(t, processErr, "daemon exited unsuccessfully")
		case <-time.After(10 * time.Second):
			_ = process.Process.Kill()
			<-stopped
			assert.Fail(t, "daemon did not shut down within ten seconds")
		}
		if t.Failed() {
			logs, _ := os.ReadFile(logPath)
			if len(logs) > 8192 {
				logs = logs[len(logs)-8192:]
			}
			t.Logf("daemon log: %s", logs)
		}
	})
	// Port zero avoids reserving and racing to rebind a client-selected port.
	address := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`)
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stopped:
			t.Fatalf("daemon exited before embedded runner readiness: %v", processErr)
		case <-deadline.C:
			t.Fatal("daemon embedded runner was not ready within thirty seconds")
		case <-tick.C:
			if result.serverURL == "" {
				logs, err := os.ReadFile(logPath)
				require.NoError(t, err)
				result.serverURL = address.FindString(string(logs))
				if result.serverURL == "" {
					continue
				}
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, result.serverURL+"/api/chat/settings", nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+result.authToken)
			response, err := client.Do(request)
			if err != nil {
				continue
			}
			var settings struct {
				DefaultRunnerReady bool `json:"defaultRunnerReady"`
			}
			err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&settings)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && err == nil && settings.DefaultRunnerReady {
				return result
			}
		}
	}
}

func TestCoreDaemonThinClientSetup(t *testing.T) {
	// Read-only readiness and usage need no live provider or usable client store.
	daemon := startCoreDaemon(t, []string{"KODELET_PROVIDER=openai", "OPENAI_API_KEY=unused-test-key"})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kodelet", "usage", "--format=json",
		"--server="+daemon.serverURL, "--auth-token="+daemon.authToken)
	cmd.Dir, cmd.Env = daemon.clientDir, daemon.clientEnv
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "thin client setup failed: %s", output)
	assert.True(t, json.Valid(output), "usage must return JSON: %s", output)
	assert.NoDirExists(t, filepath.Join(daemon.clientDir, ".kodelet"))
}
