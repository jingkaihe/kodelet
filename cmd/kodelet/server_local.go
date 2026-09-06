package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

const localServerTimeout = 30 * time.Second

type localServerConnection struct {
	SchemaVersion int    `json:"schemaVersion"`
	URL           string `json:"url"`
	PID           int    `json:"pid"`
	InstanceID    string `json:"instanceId"`
	Version       string `json:"version"`
	ConfigFile    string `json:"configFile,omitempty"`
	ConfigMode    string `json:"configMode"`
	Managed       bool   `json:"managed"`
}

type localServerStatus struct {
	APIReady       bool                              `json:"apiReady"`
	InstanceID     string                            `json:"instanceId"`
	Version        string                            `json:"version"`
	ActiveRuns     int                               `json:"activeRuns"`
	EmbeddedRunner controlplane.EmbeddedRunnerStatus `json:"embeddedRunner"`
}

// localServerDirectory does not create state during explicit remote connections.
func localServerDirectory() (string, error) {
	base := os.Getenv("KODELET_BASE_PATH")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.Wrap(err, "failed to locate local server state")
		}
		base = filepath.Join(home, ".kodelet")
	}
	return filepath.Abs(filepath.Join(base, "server"))
}

// tryLocalServerLock returns a nil file when another process owns the lock.
// Never unlink lock files: doing so would allow multiple owners of different inodes.
func tryLocalServerLock(directory, name string) (*os.File, error) {
	if err := osutil.EnsurePrivateDir(directory); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err == nil {
		return file, nil
	}
	_ = file.Close()
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return nil, nil //nolint:nilnil // A held advisory lock is not an error.
	}
	return nil, errors.Wrap(err, "failed to lock local server state")
}

func waitLocalServerLock(ctx context.Context, directory string) (*os.File, error) {
	for {
		file, err := tryLocalServerLock(directory, "startup.lock")
		if err != nil || file != nil {
			return file, err
		}
		if err := waitLocalServerPoll(ctx); err != nil {
			return nil, errors.Wrap(err, "timed out waiting for another server lifecycle command")
		}
	}
}

func waitLocalServerPoll(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func readLocalServerConnection(directory string) (localServerConnection, error) {
	var connection localServerConnection
	data, err := os.ReadFile(filepath.Join(directory, "connection.json"))
	if err != nil {
		return connection, err
	}
	if err := json.Unmarshal(data, &connection); err != nil {
		return connection, errors.Wrap(err, "invalid local server connection state")
	}
	parsed, err := url.Parse(connection.URL)
	if err != nil || parsed.Scheme != "http" || !localServerHost(parsed.Hostname()) || parsed.Port() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return connection, errors.New("invalid local server endpoint; expected a loopback HTTP address")
	}
	if connection.SchemaVersion != 1 || connection.InstanceID == "" {
		return connection, errors.New("unsupported local server connection state")
	}
	return connection, nil
}

func localServerHost(host string) bool {
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func writeLocalServerFile(directory, name string, data []byte) error {
	file, err := os.CreateTemp(directory, ".server-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, name))
}

func localServerConfigIdentity() (string, string, error) {
	path := strings.TrimSpace(os.Getenv(configFileEnv))
	if path != "" {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return "", "", err
		}
	}
	mode, err := configFileMode()
	return path, mode, err
}

func publishLocalServer(directory, endpoint, token, instanceID string, managed bool) error {
	configFile, configMode, err := localServerConfigIdentity()
	if err != nil {
		return err
	}
	connection := localServerConnection{
		SchemaVersion: 1, URL: endpoint, PID: os.Getpid(), InstanceID: instanceID,
		Version: version.Version, ConfigFile: configFile, ConfigMode: configMode, Managed: managed,
	}
	data, err := json.MarshalIndent(connection, "", "  ")
	if err != nil {
		return err
	}
	if err := writeLocalServerFile(directory, "client-token", []byte(token)); err != nil {
		return err
	}
	return writeLocalServerFile(directory, "connection.json", append(data, '\n'))
}

func localServerRequest(ctx context.Context, connection localServerConnection, token, method, path string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, connection.URL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		// Local discovery must not send its credential through an HTTP proxy.
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
	return client.Do(request)
}

func probeLocalServer(ctx context.Context, connection localServerConnection, token string) (localServerStatus, error) {
	var status localServerStatus
	response, err := localServerRequest(ctx, connection, token, http.MethodGet, "/api/status", nil)
	if err != nil {
		return status, errors.Wrap(err, "could not contact the local server")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return status, errors.Errorf("local server status returned HTTP %d; check authentication and 'kodelet server logs'", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&status); err != nil {
		return status, errors.Wrap(err, "invalid local server status response")
	}
	if status.InstanceID != connection.InstanceID {
		return status, errors.New("local server instance does not match connection state; refusing to use a different server")
	}
	return status, nil
}

// prepareLocalServeConfig validates rather than silently overriding an operator's
// network/auth policy. Request-scoped CLI model flags are never forwarded.
func prepareLocalServeConfig(config *ServeConfig) error {
	if !localServerHost(config.Host) || config.SkipAuth || !config.EmbeddedRunner {
		return errors.New("automatic startup requires a loopback host, authentication, and an embedded runner; use 'kodelet serve' and --server for custom deployments")
	}
	webMode, runnerMode, err := resolveServeAuthModes(config)
	if err != nil {
		return err
	}
	if webMode != controlplane.WebAuthModeToken || runnerMode != controlplane.RunnerAuthModeToken {
		return errors.New("automatic startup requires token authentication; use 'kodelet serve' and --server for custom authentication")
	}
	if config.RunnerWorkspace == "" {
		config.RunnerWorkspace, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	return validateServeConfig(config)
}

var detachedServerCommand = func() (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(executable, "serve", "--managed"), nil
}

func spawnLocalServer(directory string) (<-chan error, error) {
	command, err := detachedServerCommand()
	if err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(directory, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	if err := log.Chmod(0o600); err != nil {
		return nil, err
	}
	// No CommandContext: a client cancellation must not kill the shared daemon.
	// Nil stdin becomes /dev/null; neither output stream retains the client pipe.
	command.Stdout, command.Stderr = log, log
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return nil, errors.Wrap(err, "failed to start detached Kodelet server")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return done, nil
}

func ensureLocalServer(ctx context.Context, output io.Writer) (localServerConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, localServerTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return localServerConnection{}, err
	}
	directory, err := localServerDirectory()
	if err != nil {
		return localServerConnection{}, err
	}
	startup, err := waitLocalServerLock(ctx, directory)
	if err != nil {
		return localServerConnection{}, err
	}
	defer startup.Close()
	lock, err := tryLocalServerLock(directory, "server.lock")
	if err != nil {
		return localServerConnection{}, err
	}
	var childDone <-chan error
	if lock != nil {
		// No owning daemon. Leave stale credentials alone until the old endpoint
		// is confirmed absent; a reachable but unhealthy server is not missing.
		connection, readErr := readLocalServerConnection(directory)
		if readErr == nil {
			token, tokenErr := os.ReadFile(filepath.Join(directory, "client-token"))
			if tokenErr != nil {
				_ = lock.Close()
				return connection, tokenErr
			}
			_, probeErr := probeLocalServer(ctx, connection, string(token))
			if !errors.Is(probeErr, syscall.ECONNREFUSED) {
				_ = lock.Close()
				return connection, errors.New("local server state has no process lock but its endpoint is not confirmed stopped; refusing to start a competing server")
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			_ = lock.Close()
			return connection, readErr
		}
		config := NewServeConfig()
		if err := applyTrustedServeConfig(config); err != nil {
			_ = lock.Close()
			return connection, err
		}
		if err := prepareLocalServeConfig(config); err != nil {
			_ = lock.Close()
			return connection, err
		}
		if err := os.Remove(filepath.Join(directory, "connection.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = lock.Close()
			return connection, err
		}
		_ = lock.Close()
		fmt.Fprintln(output, "Starting local Kodelet server…")
		childDone, err = spawnLocalServer(directory)
		if err != nil {
			return connection, err
		}
	}
	for {
		select {
		case childErr := <-childDone:
			return localServerConnection{}, errors.Errorf("local server exited during startup (%v); check %s", childErr, filepath.Join(directory, "server.log"))
		default:
		}
		connection, readErr := readLocalServerConnection(directory)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return connection, readErr
		}
		if readErr == nil {
			configFile, configMode, err := localServerConfigIdentity()
			if err != nil {
				return connection, err
			}
			if configFile != connection.ConfigFile || configMode != connection.ConfigMode {
				return connection, errors.New("the local server uses a different configuration file; stop it with 'kodelet server stop' before starting with this configuration")
			}
			if connection.Version != version.Version {
				return connection, errors.New("the local server uses a different Kodelet version; run 'kodelet server restart'")
			}
			token, err := os.ReadFile(filepath.Join(directory, "client-token"))
			if err != nil {
				return connection, err
			}
			status, err := probeLocalServer(ctx, connection, string(token))
			if err != nil && !errors.Is(err, syscall.ECONNREFUSED) {
				return connection, err
			}
			if err == nil {
				if !status.EmbeddedRunner.Enabled || status.EmbeddedRunner.Error != "" {
					return connection, errors.Errorf("local server's embedded runner is unavailable: %s; check 'kodelet server logs'", status.EmbeddedRunner.Error)
				}
				if status.APIReady && status.EmbeddedRunner.Ready {
					return connection, nil
				}
			}
		}
		if err := waitLocalServerPoll(ctx); err != nil {
			return localServerConnection{}, errors.Wrapf(err, "local server did not become ready; check %s (an existing server may require an explicit --server)", filepath.Join(directory, "server.log"))
		}
	}
}

func prepareClientServer(ctx context.Context, cmd *cobra.Command) (string, string, error) {
	server, configured := serverFlagOrConfig(cmd)
	if !configured {
		connection, err := ensureLocalServer(ctx, cmd.ErrOrStderr())
		if err != nil {
			return "", "", err
		}
		server = connection.URL
	}
	token, _, err := resolveControlPlaneAuthToken(cmd, server)
	return server, token, err
}
