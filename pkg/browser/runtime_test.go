package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake browser runs only as an explicitly selected child of these tests. Its
// discovery endpoint, CDP sockets, profile, and workspace never touch a real daemon.
func TestBrowserHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_BROWSER_HELPER_PROCESS") != "1" {
		return
	}
	profile := ""
	for _, arg := range os.Args {
		if value, ok := strings.CutPrefix(arg, "--user-data-dir="); ok {
			profile = value
		}
	}
	require.NotEmpty(t, profile)
	mode, _ := os.ReadFile("browser-test-mode")
	if string(mode) == "stderr" {
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("x", maxStderrBytes*3)+"\nChrome cannot start: test sandbox failure\n")
		os.Exit(7)
	}
	require.NoError(t, os.WriteFile(filepath.Join(profile, "started"), []byte(strconv.Itoa(os.Getpid())), 0o600))
	if string(mode) == "wait" {
		_, _ = fmt.Fprintln(os.Stderr, "Chrome is waiting for test startup readiness")
		for {
			if _, err := os.Stat("browser-test-release"); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, arg := range os.Args {
		if arg == "--browser-test-child" {
			signal.Ignore(syscall.SIGTERM)
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			require.NoError(t, os.WriteFile(filepath.Join(profile, "child-address"), []byte(strings.TrimPrefix(server.URL, "http://")), 0o600))
			select {}
		}
	}
	if string(mode) == "descendant" {
		executable, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(executable, "-test.run=^TestBrowserHelperProcess$", "--", "--user-data-dir="+profile, "--browser-test-child")
		require.NoError(t, cmd.Start())
		go func() { _ = cmd.Wait() }()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		if string(mode) == "redirect" {
			redirect, err := os.ReadFile("browser-test-redirect")
			require.NoError(t, err)
			http.Redirect(w, r, string(redirect), http.StatusFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"type": "page", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/page/test"}})
	})
	mux.HandleFunc("/devtools/page/test", func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat("browser-test-stall-upgrade"); err == nil {
			require.NoError(t, os.WriteFile(filepath.Join(profile, "upgrade-started"), nil, 0o600))
			<-r.Context().Done()
			return
		}
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if conn.ReadJSON(&request) != nil {
				return
			}
			switch request.Method {
			case "Test.error":
				_ = conn.WriteJSON(map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "unknown test method"}})
			case "Test.hang":
				continue
			case "Test.noResult":
				_ = conn.WriteJSON(map[string]any{"id": request.ID})
			case "Test.invalid":
				_ = conn.WriteMessage(websocket.TextMessage, []byte("{"))
			case "Test.large":
				_ = conn.WriteJSON(map[string]any{"id": request.ID, "result": strings.Repeat("x", maxCDPMessageBytes+1)})
			case "Test.disconnect":
				return
			default:
				_ = conn.WriteJSON(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{}})
				_ = conn.WriteJSON(map[string]any{"id": request.ID + 1, "result": map[string]bool{"wrong": true}})
				_ = conn.WriteJSON(map[string]any{"id": request.ID, "result": map[string]any{"method": request.Method, "params": request.Params}})
			}
		}
	})
	server := httptest.NewServer(mux)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(u.Port()+"\n/devtools/browser/test\n"), 0o600))
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	<-stop
	os.Exit(0)
}

func fakeBrowserConfig(t *testing.T) (Config, string) {
	t.Helper()
	profiles := t.TempDir()
	t.Setenv("TMPDIR", profiles)
	t.Setenv("KODELET_BROWSER_HELPER_PROCESS", "1")
	executable, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "fake-chrome")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'"
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+quoted+" -test.run='^TestBrowserHelperProcess$' -- \"$@\"\n"), 0o700))
	return Config{Executable: wrapper}, profiles
}

func testManager(t *testing.T, config Config) *Manager {
	t.Helper()
	m := NewManager(t.Context(), config)
	t.Cleanup(func() { assert.NoError(t, m.Close()) })
	return m
}

func openedSession(t *testing.T, m *Manager, info Info) *session {
	t.Helper()
	m.mu.Lock()
	s := m.sessions[info.CWD]
	m.mu.Unlock()
	require.NotNil(t, s)
	<-s.ready
	return s
}

func waitSessionDone(t *testing.T, s *session) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "browser session did not finish cleanup")
	}
	assert.NoError(t, s.closeErr)
	assert.NoDirExists(t, s.profile)
}

func assertSocketClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
	var timeout net.Error
	if errors.As(err, &timeout) {
		assert.False(t, timeout.Timeout(), "socket was not closed before the read deadline")
	}
}

func TestDisabledAndInvalidConfiguration(t *testing.T) {
	m := testManager(t, Config{})
	assert.False(t, m.Enabled())
	assert.Equal(t, 15*time.Minute, m.config.IdleTimeout)
	_, err := m.Open(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "disabled")
	assert.Empty(t, m.sessions)

	m = testManager(t, Config{Executable: filepath.Join(t.TempDir(), "missing-chrome")})
	assert.True(t, m.Enabled())
	_, err = m.Open(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "executable was not found")
	for _, cwd := range []string{"", filepath.Join(t.TempDir(), "missing"), os.Args[0]} {
		_, err := m.Open(t.Context(), cwd)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = m.Open(ctx, t.TempDir())
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, m.Close())
	_, err = m.Open(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "closed")
}

func TestOpenReusesCanonicalWorkspaceAndRequestLifetime(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	config.DevToolsDir = t.TempDir()
	m := testManager(t, config)
	assert.Empty(t, m.sessions, "manager construction must be lazy")
	cwd := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(cwd, alias))
	ctx, cancel := context.WithCancel(t.Context())
	info, err := m.Open(ctx, cwd)
	require.NoError(t, err)
	cancel()
	assert.True(t, info.DevTools)
	s := openedSession(t, m, info)
	assert.NotEqual(t, cwd, s.profile)
	profile, err := os.Stat(s.profile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), profile.Mode().Perm())
	assert.Equal(t, info.CWD, s.cmd.Dir)
	assert.Contains(t, s.cmd.Args, "--remote-debugging-port=0")
	assert.Contains(t, s.cmd.Args, "--remote-debugging-address=127.0.0.1")
	assert.Contains(t, s.cmd.Args, "--user-data-dir="+s.profile)
	assert.Contains(t, s.cmd.Args, "about:blank")
	assert.NotContains(t, s.cmd.Args, "--no-sandbox")
	pgid, err := syscall.Getpgid(s.cmd.Process.Pid)
	require.NoError(t, err)
	assert.Equal(t, s.cmd.Process.Pid, pgid)
	got, err := m.Open(t.Context(), alias)
	require.NoError(t, err)
	assert.Equal(t, info, got)
	require.NoError(t, m.Close())
	waitSessionDone(t, s)
	assert.NotNil(t, s.cmd.ProcessState)
	require.NoError(t, m.Close())
}

func TestConcurrentOpenAndSessionLimit(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	type result struct {
		info Info
		err  error
	}
	results := make(chan result, 12)
	for range cap(results) {
		go func() {
			info, err := m.Open(t.Context(), cwd)
			results <- result{info, err}
		}()
	}
	first := <-results
	require.NoError(t, first.err)
	for range cap(results) - 1 {
		got := <-results
		require.NoError(t, got.err)
		assert.Equal(t, first.info, got.info)
	}
	for range maxSessions - 1 {
		_, err := m.Open(t.Context(), t.TempDir())
		require.NoError(t, err)
	}
	_, err := m.Open(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "limit reached")
	require.NoError(t, m.Stop(cwd, first.info.SessionID))
	replacement, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	assert.NotEqual(t, first.info.SessionID, replacement.SessionID)
	require.ErrorContains(t, m.Stop(cwd, first.info.SessionID), "stale")
	conn, release, err := m.Connect(t.Context(), cwd, first.info.SessionID)
	require.ErrorContains(t, err, "stale")
	assert.Nil(t, conn)
	assert.Nil(t, release)
	got, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	assert.Equal(t, replacement, got)
}

func TestAttachmentsReleaseCancellationAndStop(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	_, _, err := m.Connect(t.Context(), cwd, "stale")
	require.ErrorContains(t, err, "stale")
	assert.Empty(t, m.sessions, "connecting must never create a browser")
	info, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	s := openedSession(t, m, info)
	first, releaseFirst, err := m.Connect(t.Context(), cwd, info.SessionID)
	require.NoError(t, err)
	defer releaseFirst()
	ctx, cancel := context.WithCancel(t.Context())
	second, releaseSecond, err := m.Connect(ctx, cwd, info.SessionID)
	require.NoError(t, err)
	defer releaseSecond()
	releaseFirst()
	releaseFirst()
	assertSocketClosed(t, first)
	got, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	assert.Equal(t, info, got, "one detach must not close the shared session")
	cancel()
	assertSocketClosed(t, second)
	releaseSecond()
	m.mu.Lock()
	assert.Zero(t, s.attachments)
	m.mu.Unlock()
	third, releaseThird, err := m.Connect(t.Context(), cwd, info.SessionID)
	require.NoError(t, err)
	defer releaseThird()
	require.NoError(t, m.Stop(cwd, info.SessionID))
	assertSocketClosed(t, third)
	waitSessionDone(t, s)
	_, _, err = m.Connect(t.Context(), cwd, info.SessionID)
	require.ErrorContains(t, err, "stale")
	assert.Empty(t, m.sessions)
}

func TestUnattachedIdleCleanup(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	config.IdleTimeout = 100 * time.Millisecond
	m := testManager(t, config)
	info, err := m.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	s := openedSession(t, m, info)
	_, release, err := m.Connect(t.Context(), info.CWD, info.SessionID)
	require.NoError(t, err)
	defer release()
	time.Sleep(3 * config.IdleTimeout)
	assert.NoError(t, s.ctx.Err(), "an attached session must not idle out")
	release()
	waitSessionDone(t, s)
	got, err := m.Open(t.Context(), info.CWD)
	require.NoError(t, err)
	assert.NotEqual(t, info.SessionID, got.SessionID)
}

func TestCanceledAttachmentInterruptsStalledUpgrade(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	info, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	s := openedSession(t, m, info)
	stall := filepath.Join(cwd, "browser-test-stall-upgrade")
	require.NoError(t, os.WriteFile(stall, nil, 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, release, err := m.Connect(ctx, cwd, info.SessionID)
		if release != nil {
			release()
		}
		result <- err
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(s.profile, "upgrade-started"))
		return err == nil
	}, 3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "canceled WebSocket upgrade was not interrupted promptly")
	}
	m.mu.Lock()
	assert.Zero(t, s.attachments, "failed dials must release their idle lease")
	m.mu.Unlock()
	assert.NoError(t, s.ctx.Err())
	require.NoError(t, os.Remove(stall))
	_, release, err := m.Connect(t.Context(), cwd, info.SessionID)
	require.NoError(t, err)
	release()
}

func TestStartupDoesNotBlockOtherWorkspacesAndCanBeCanceled(t *testing.T) {
	config, profiles := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "browser-test-mode"), []byte("wait"), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := m.Open(ctx, cwd)
		result <- err
	}()
	require.Eventually(t, func() bool {
		files, _ := filepath.Glob(filepath.Join(profiles, "kodelet-browser-*", "started"))
		return len(files) == 1
	}, 3*time.Second, 10*time.Millisecond)
	otherCtx, otherCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer otherCancel()
	other, err := m.Open(otherCtx, t.TempDir())
	require.NoError(t, err)
	canonical, err := canonicalCWD(cwd)
	require.NoError(t, err)
	m.mu.Lock()
	s := m.sessions[canonical]
	m.mu.Unlock()
	require.NotNil(t, s)
	_, _, err = m.Connect(t.Context(), cwd, s.info.SessionID)
	require.ErrorContains(t, err, "still starting")
	waitCtx, waitCancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer waitCancel()
	_, err = m.Open(waitCtx, cwd)
	require.Error(t, err)
	assert.NoError(t, s.ctx.Err(), "canceling a waiting opener must not cancel the initiating request")
	cancel()
	err = <-result
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "Chrome is waiting for test startup readiness")
	waitSessionDone(t, s)
	got, err := m.Open(t.Context(), other.CWD)
	require.NoError(t, err)
	assert.Equal(t, other, got)
}

func TestCloseDuringStartup(t *testing.T) {
	config, profiles := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "browser-test-mode"), []byte("wait"), 0o600))
	result := make(chan error, 1)
	go func() {
		_, err := m.Open(t.Context(), cwd)
		result <- err
	}()
	require.Eventually(t, func() bool {
		files, _ := filepath.Glob(filepath.Join(profiles, "kodelet-browser-*", "started"))
		return len(files) == 1
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, m.Close())
	require.Error(t, <-result)
	files, err := filepath.Glob(filepath.Join(profiles, "kodelet-browser-*"))
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestStartupErrorsAreBoundedAndCleanProfiles(t *testing.T) {
	config, profiles := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "browser-test-mode"), []byte("stderr"), 0o600))
	_, err := m.Open(t.Context(), cwd)
	require.ErrorContains(t, err, "Chrome cannot start: test sandbox failure")
	assert.Less(t, len(err.Error()), maxStderrBytes+1024)
	files, err := filepath.Glob(filepath.Join(profiles, "kodelet-browser-*"))
	require.NoError(t, err)
	assert.Empty(t, files)

	executable := filepath.Join(t.TempDir(), "bad-interpreter")
	require.NoError(t, os.WriteFile(executable, []byte("#!/does-not-exist\n"), 0o700))
	m = testManager(t, Config{Executable: executable})
	_, err = m.Open(t.Context(), cwd)
	require.ErrorContains(t, err, "failed to launch")
	files, err = filepath.Glob(filepath.Join(profiles, "kodelet-browser-*"))
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestStartupDiscoveryDoesNotFollowRedirects(t *testing.T) {
	config, profiles := fakeBrowserConfig(t)
	m := testManager(t, config)
	redirected := make(chan struct{}, 1)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case redirected <- struct{}{}:
		default:
		}
		_, _ = io.WriteString(w, "[]")
	}))
	defer other.Close()
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "browser-test-mode"), []byte("redirect"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "browser-test-redirect"), []byte(other.URL), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := m.Open(ctx, cwd)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "must not redirect")
	assert.Empty(t, redirected)
	files, err := filepath.Glob(filepath.Join(profiles, "kodelet-browser-*"))
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestCrashRecoveryAndParentCancellation(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := NewManager(ctx, config)
	t.Cleanup(func() { assert.NoError(t, m.Close()) })
	info, err := m.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	s := openedSession(t, m, info)
	conn, release, err := m.Connect(t.Context(), info.CWD, info.SessionID)
	require.NoError(t, err)
	defer release()
	require.NoError(t, s.cmd.Process.Kill())
	// Assert disconnection immediately, not just after cleanup or an idle timeout.
	assertSocketClosed(t, conn)
	waitSessionDone(t, s)
	replacement, err := m.Open(t.Context(), info.CWD)
	require.NoError(t, err)
	assert.NotEqual(t, info.SessionID, replacement.SessionID)
	s = openedSession(t, m, replacement)
	cancel()
	waitSessionDone(t, s)
	require.NoError(t, m.Close())
}

func TestStopKillsBrowserProcessGroup(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "browser-test-mode"), []byte("descendant"), 0o600))
	info, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	s := openedSession(t, m, info)
	var address []byte
	require.Eventually(t, func() bool {
		address, err = os.ReadFile(filepath.Join(s.profile, "child-address"))
		return err == nil && len(address) > 0
	}, 3*time.Second, 10*time.Millisecond)
	conn, err := net.DialTimeout("tcp", string(address), time.Second)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, m.Stop(cwd, info.SessionID))
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF, "the descendant must close even though it ignores SIGTERM")
	waitSessionDone(t, s)
}

func TestCommandCorrelatesResponsesOnIndependentConnections(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	info, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	ui, release, err := m.Connect(t.Context(), cwd, info.SessionID)
	require.NoError(t, err)
	defer release()
	for _, method := range []string{"Runtime.evaluate", "Page.navigate", "Browser.getVersion", "Target.getTargets"} {
		result, err := m.Command(t.Context(), cwd, method, json.RawMessage(`{"value":42}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"method":"`+method+`","params":{"value":42}}`, string(result))
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			result, err := m.Command(t.Context(), cwd, "Runtime.evaluate", nil)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"method":"Runtime.evaluate","params":null}`, string(result))
		})
	}
	wg.Wait()
	s := openedSession(t, m, info)
	m.mu.Lock()
	assert.Equal(t, 1, s.attachments)
	m.mu.Unlock()
	require.NoError(t, ui.WriteJSON(map[string]any{"id": 99, "method": "Runtime.evaluate"}))
	_, _, err = ui.ReadMessage()
	require.NoError(t, err, "commands must not consume or close the UI connection")
	got, err := m.Open(t.Context(), cwd)
	require.NoError(t, err)
	assert.Equal(t, info, got)
}

func TestCommandFailuresAndBounds(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	cwd := t.TempDir()
	for _, method := range []string{"", "Runtime", ".evaluate", "Runtime.", "Runtime. evaluate"} {
		_, err := m.Command(t.Context(), cwd, method, nil)
		require.Error(t, err)
	}
	for _, params := range []json.RawMessage{[]byte("[]"), []byte("null"), []byte("{"), []byte(strings.Repeat("x", maxCDPMessageBytes+1))} {
		_, err := m.Command(t.Context(), cwd, "Runtime.evaluate", params)
		require.Error(t, err)
	}
	assert.Empty(t, m.sessions, "invalid requests must not launch a browser")
	for _, tc := range []struct{ method, message string }{
		{"Test.error", "unknown test method"},
		{"Test.noResult", "missing its result"},
		{"Test.invalid", "failed to read"},
		{"Test.large", "read limit exceeded"},
		{"Test.disconnect", "failed to read"},
	} {
		_, err := m.Command(t.Context(), cwd, tc.method, nil)
		require.ErrorContains(t, err, tc.message)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := m.Command(ctx, cwd, "Test.hang", nil)
	require.Error(t, err)
	result, err := m.Command(t.Context(), cwd, "Runtime.evaluate", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result, "a canceled command must not stop the browser")
}

func TestDiscoveryConfinesPageEndpoint(t *testing.T) {
	var target, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/json/list", r.URL.Path)
		if body != "" {
			_, _ = io.WriteString(w, body)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"type": "worker"}, {"type": "page", "webSocketDebuggerUrl": target}})
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	profile := t.TempDir()
	portFile := filepath.Join(profile, "DevToolsActivePort")
	validPort := u.Port() + "\n/devtools/browser/test\n"
	require.NoError(t, os.WriteFile(portFile, []byte(validPort), 0o600))
	target = "ws://" + u.Host + "/devtools/page/test"
	got, err := discoverPage(t.Context(), server.Client(), profile)
	require.NoError(t, err)
	assert.Equal(t, target, got)
	for _, invalid := range []string{
		"ws://example.invalid:" + u.Port() + "/devtools/page/test",
		"ws://127.0.0.2:" + u.Port() + "/devtools/page/test",
		"ws://localhost:" + u.Port() + "/devtools/page/test",
		"ws://127.0.0.1:1/devtools/page/test",
		"wss://" + u.Host + "/devtools/page/test",
		"ws://user@" + u.Host + "/devtools/page/test",
		"ws://" + u.Host + "/devtools/page/test?token=x",
		"ws://" + u.Host + "/devtools/page/test#fragment",
		"ws://" + u.Host + "/devtools/browser/test",
		"ws://" + u.Host + "/devtools/page/",
		"ws://" + u.Host + "/devtools/page/test/nested",
		"ws://" + u.Host + "/devtools/page/test%2Fnested",
		"://invalid",
	} {
		target = invalid
		_, err := discoverPage(t.Context(), server.Client(), profile)
		require.Error(t, err, invalid)
	}
	for _, invalid := range []string{"{", "[]", strings.Repeat("x", maxTargetListBytes+1)} {
		body = invalid
		_, err := discoverPage(t.Context(), server.Client(), profile)
		require.Error(t, err)
	}
	for _, invalid := range []string{"", "0\n/devtools/browser/test", "65536\n/devtools/browser/test", "abc\n/devtools/browser/test", "1234\nnot-a-browser", strings.Repeat("x", 4097)} {
		require.NoError(t, os.WriteFile(portFile, []byte(invalid), 0o600))
		_, err := discoverPage(t.Context(), server.Client(), profile)
		require.Error(t, err)
	}
}

func TestReadAssetChunksAndConfinement(t *testing.T) {
	root := t.TempDir()
	m := testManager(t, Config{DevToolsDir: root})
	data := []byte(strings.Repeat("x", maxAssetChunkBytes+19))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.js"), data, 0o600))
	first, err := m.ReadAsset("app.js", 0)
	require.NoError(t, err)
	assert.Len(t, first.Data, maxAssetChunkBytes)
	assert.Equal(t, "text/javascript; charset=utf-8", first.ContentType)
	assert.False(t, first.EOF)
	last, err := m.ReadAsset("app.js", int64(len(first.Data)))
	require.NoError(t, err)
	assert.Len(t, last.Data, 19)
	assert.True(t, last.EOF)
	assert.Equal(t, data, append(first.Data, last.Data...))
	end, err := m.ReadAsset("app.js", int64(len(data)))
	require.NoError(t, err)
	assert.Empty(t, end.Data)
	assert.True(t, end.EOF)
	require.NoError(t, os.WriteFile(filepath.Join(root, "empty.css"), nil, 0o600))
	empty, err := m.ReadAsset("empty.css", 0)
	require.NoError(t, err)
	assert.True(t, empty.EOF)
	require.NoError(t, os.WriteFile(filepath.Join(root, "module.mjs"), []byte("export {};"), 0o600))
	module, err := m.ReadAsset("module.mjs", 0)
	require.NoError(t, err)
	assert.Equal(t, "text/javascript; charset=utf-8", module.ContentType)

	require.NoError(t, os.Symlink("app.js", filepath.Join(root, "inside.js")))
	inside, err := m.ReadAsset("inside.js", 0)
	require.NoError(t, err)
	assert.Equal(t, first, inside)
	outside := filepath.Join(t.TempDir(), "outside.js")
	require.NoError(t, os.WriteFile(outside, []byte("not a frontend asset"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "outside.js")))
	relative, err := filepath.Rel(root, outside)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(relative, filepath.Join(root, "relative-outside.js")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "directory.js"), 0o700))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "pipe.js"), 0o600))
	for _, path := range []string{"../outside.js", outside, "outside.js", "relative-outside.js", "missing.js", "directory.js", "pipe.js", "secrets.yaml", "app.js?query=x"} {
		_, err := m.ReadAsset(path, 0)
		require.Error(t, err, path)
	}
	for _, offset := range []int64{-1, int64(len(data) + 1)} {
		_, err := m.ReadAsset("app.js", offset)
		require.Error(t, err)
	}
	large, err := os.Create(filepath.Join(root, "large.wasm"))
	require.NoError(t, err)
	defer large.Close()
	require.NoError(t, large.Truncate(maxAssetBytes))
	chunk, err := m.ReadAsset("large.wasm", maxAssetBytes-maxAssetChunkBytes)
	require.NoError(t, err)
	assert.True(t, chunk.EOF)
	assert.Equal(t, "application/wasm", chunk.ContentType)
	require.NoError(t, large.Truncate(maxAssetBytes+1))
	_, err = m.ReadAsset("large.wasm", 0)
	require.ErrorContains(t, err, "at most 16 MiB")
	require.NoError(t, m.Close())
	_, err = m.ReadAsset("app.js", 0)
	require.ErrorContains(t, err, "closed")
	m = testManager(t, Config{})
	_, err = m.ReadAsset("app.js", 0)
	require.ErrorContains(t, err, "not configured")
}

func TestStderrTail(t *testing.T) {
	var tail stderrTail
	_, err := tail.Write([]byte(strings.Repeat("a", maxStderrBytes-2)))
	require.NoError(t, err)
	n, err := tail.Write([]byte("bcdef"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Len(t, tail.String(), maxStderrBytes)
	assert.True(t, strings.HasSuffix(tail.String(), "bcdef"))
	_, err = tail.Write([]byte(strings.Repeat("z", maxStderrBytes*2)))
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("z", maxStderrBytes), tail.String())
}

func TestRealBrowserSmoke(t *testing.T) {
	executable := os.Getenv("KODELET_BROWSER_TEST_EXECUTABLE")
	if executable == "" {
		t.Skip("set KODELET_BROWSER_TEST_EXECUTABLE to explicitly opt into an isolated real-browser test")
	}
	t.Setenv("TMPDIR", t.TempDir())
	m := testManager(t, Config{Executable: executable})
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<!doctype html><title>Kodelet isolated browser test</title>")
	}))
	defer app.Close()
	cwd := t.TempDir()
	params, err := json.Marshal(map[string]string{"url": app.URL})
	require.NoError(t, err)
	_, err = m.Command(t.Context(), cwd, "Page.navigate", params)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		result, err := m.Command(t.Context(), cwd, "Runtime.evaluate", json.RawMessage(`{"expression":"document.title","returnByValue":true}`))
		return err == nil && strings.Contains(string(result), "Kodelet isolated browser test")
	}, 5*time.Second, 50*time.Millisecond)
	result, err := m.Command(t.Context(), cwd, "Page.captureScreenshot", nil)
	require.NoError(t, err)
	var screenshot struct {
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(result, &screenshot))
	assert.NotEmpty(t, screenshot.Data)
}
