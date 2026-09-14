package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/browser"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This subprocess is a tiny fake Chrome. It listens only on an ephemeral loopback
// socket and writes its discovery file only inside the manager's temporary profile.
func TestBrowserRunnerHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_BROWSER_RUNNER_HELPER") != "1" {
		return
	}
	var profile string
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--user-data-dir=") {
			profile = strings.TrimPrefix(arg, "--user-data-dir=")
		}
	}
	require.NotEmpty(t, profile)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{
			"id": "test-page", "type": "page", "url": "about:blank", "webSocketDebuggerUrl": "ws://" + listener.Addr().String() + "/devtools/page/test",
		}})
	})
	mux.HandleFunc("/devtools/page/test", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, payload); err != nil {
				return
			}
		}
	})
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(fmt.Sprintf("%d\n/devtools/browser/test\n", port)), 0o600))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = server.Serve(listener)
}

func fakeBrowserExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(wrapper, fmt.Appendf(nil,
		"#!/bin/sh\nKODELET_BROWSER_RUNNER_HELPER=1 exec %q -test.run=^TestBrowserRunnerHelperProcess$ -- \"$@\"\n", executable), 0o700))
	return wrapper
}

func TestBrowserRunnerLifecycleAndRelaySurviveRequestCompletion(t *testing.T) {
	workspace := t.TempDir()
	remote := make(chan *websocket.Conn, 1)
	relayDone := make(chan struct{})
	defer close(relayDone)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, protocol.BrowserRelayEndpoint, r.URL.Path)
		assert.Equal(t, "Bearer "+strings.Repeat("a", 43), r.Header.Get("Authorization"))
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		remote <- conn
		<-relayDone
	}))
	defer server.Close()
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		ArtifactBaseURL: server.URL,
		Browser:         browser.Config{Executable: fakeBrowserExecutable(t)},
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	service.Attach(&recordingPeer{})
	opened, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserOpen, json.RawMessage(`{}`))
	require.Nil(t, rpcErr)
	info := opened.(browser.Info)
	assert.Equal(t, workspace, info.CWD)

	requestCtx, cancel := context.WithCancel(t.Context())
	require.NoError(t, service.connectBrowser(requestCtx, protocol.WorkspaceBrowserConnectParams{
		CWD: workspace, SessionID: info.SessionID, RelayToken: strings.Repeat("a", 43),
	}))
	cancel() // Completion of the control RPC must not close the streaming attachment.
	var conn *websocket.Conn
	select {
	case conn = <-remote:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "runner did not establish its separate relay connection")
	}
	payload := []byte(`{"id":1,"method":"Page.enable"}`)
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, payload))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, response, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, payload, response)

	require.NoError(t, service.AbortActiveRun(t.Context()))
	_, _, err = conn.ReadMessage()
	require.Error(t, err, "control disconnect must terminate the relay")
	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.browserRelays) == 0
	}, time.Second, time.Millisecond)
	reattached, err := service.BrowserManager().Open(t.Context(), workspace)
	require.NoError(t, err)
	assert.Equal(t, info.SessionID, reattached.SessionID, "connection loss must not kill the workspace browser")
	require.NoError(t, service.Close())
	_, err = service.BrowserManager().Open(t.Context(), workspace)
	assert.Error(t, err)
}

func TestBrowserRunnerDispatchAndValidation(t *testing.T) {
	workspace := t.TempDir()
	assets := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(assets, "inspector.html"), []byte("<!doctype html>"), 0o600))
	service, err := NewService(t.Context(), workspace, ServiceOptions{Browser: browser.Config{
		Executable: fakeBrowserExecutable(t), DevToolsDir: assets,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	for _, method := range []string{protocol.MethodWorkspaceBrowserOpen, protocol.MethodWorkspaceBrowserConnect, protocol.MethodWorkspaceBrowserStop, protocol.MethodWorkspaceBrowserAsset} {
		_, rpcErr := service.HandleRequest(t.Context(), method, json.RawMessage(`{`))
		require.NotNil(t, rpcErr)
		assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
	}
	_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserStop, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "session ID")
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserOpen, json.RawMessage(`{"cwd":"/no/such/runner-directory"}`))
	require.NotNil(t, rpcErr)
	chunk, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserAsset, json.RawMessage(`{"path":"inspector.html","offset":0}`))
	require.Nil(t, rpcErr)
	assert.Equal(t, []byte("<!doctype html>"), chunk.(browser.AssetChunk).Data)
	assert.True(t, chunk.(browser.AssetChunk).EOF)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserAsset, json.RawMessage(`{"path":"../private","offset":0}`))
	require.NotNil(t, rpcErr)

	opened, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserOpen, json.RawMessage(`{}`))
	require.Nil(t, rpcErr)
	info := opened.(browser.Info)
	data, err := json.Marshal(protocol.WorkspaceBrowserParams{CWD: workspace, SessionID: info.SessionID})
	require.NoError(t, err)
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserStop, data)
	require.Nil(t, rpcErr)
	_, _, err = service.BrowserManager().Connect(t.Context(), workspace, info.SessionID)
	assert.Error(t, err)
	require.NoError(t, service.Close())
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserOpen, json.RawMessage(`{}`))
	require.NotNil(t, rpcErr)
	assert.Contains(t, rpcErr.Message, "closed")
}

func TestBrowserRunnerDisabledAndFactoryOptIn(t *testing.T) {
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			config := browser.Config{}
			if enabled {
				// Manifest discovery must not launch or require an installed executable.
				config.Executable = "/not/an/installed/browser"
			}
			service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{Browser: config})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, service.Close()) })
			environment := service.environmentFactory(service.workspace, nil)
			manifest, err := environment.Open(t.Context(), agentenv.RunSpec{})
			require.NoError(t, err)
			defer environment.Close(t.Context())
			found := false
			for _, tool := range manifest.Tools {
				found = found || tool.Name == "browser"
			}
			assert.Equal(t, enabled, found)
			if !enabled {
				_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceBrowserOpen, json.RawMessage(`{}`))
				require.NotNil(t, rpcErr)
				assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
			}
		})
	}
	custom := &failingOpenEnvironment{}
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		Browser: browser.Config{Executable: "/not/an/installed/browser"},
		EnvironmentFactory: func(string, *extensions.Runtime) agentenv.Environment {
			return custom
		},
	})
	require.NoError(t, err)
	defer service.Close()
	assert.Same(t, custom, service.environmentFactory(service.workspace, nil))
}

func TestBrowserRunnerRelayRejectsInvalidTicketAndEndpoint(t *testing.T) {
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{Browser: browser.Config{Executable: "unused"}})
	require.NoError(t, err)
	defer service.Close()
	err = service.connectBrowser(t.Context(), protocol.WorkspaceBrowserConnectParams{})
	assert.ErrorContains(t, err, "ticket")
	err = service.connectBrowser(t.Context(), protocol.WorkspaceBrowserConnectParams{SessionID: "session", RelayToken: strings.Repeat("t", 43)})
	assert.ErrorContains(t, err, "relay server")
	service.artifactBaseURL = "http://public.example"
	err = service.connectBrowser(t.Context(), protocol.WorkspaceBrowserConnectParams{SessionID: "session", RelayToken: strings.Repeat("t", 43)})
	assert.ErrorContains(t, err, "require https")
}
