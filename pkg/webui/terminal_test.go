package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebsocketWriterWritesMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		writer := &websocketWriter{conn: conn}
		require.NoError(t, writer.writeJSON(terminalMessage{Type: "info", Text: "hello"}))
		require.NoError(t, writer.Write(websocket.BinaryMessage, []byte("bytes")))
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()

	messageType, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, messageType)
	var message terminalMessage
	require.NoError(t, json.Unmarshal(payload, &message))
	assert.Equal(t, terminalMessage{Type: "info", Text: "hello"}, message)

	messageType, payload, err = conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, messageType)
	assert.Equal(t, []byte("bytes"), payload)
}

func TestWebsocketWriterRejectsNilConnection(t *testing.T) {
	var writer *websocketWriter
	require.ErrorContains(t, writer.Write(websocket.TextMessage, nil), "websocket writer is not initialized")

	writer = &websocketWriter{}
	require.ErrorContains(t, writer.Write(websocket.TextMessage, nil), "websocket writer is not initialized")
}

func TestReadTerminalWebsocketForwardsMessagesAndReadErrors(t *testing.T) {
	serverReadCh := make(chan terminalSocketRead, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		readTerminalWebsocket(r.Context(), conn, serverReadCh)
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"input","data":"echo hi\n"}`)))
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("raw")))
	require.NoError(t, conn.Close())

	first := receiveTerminalSocketRead(t, serverReadCh)
	assert.Equal(t, websocket.TextMessage, first.MessageType)
	assert.Equal(t, []byte(`{"type":"input","data":"echo hi\n"}`), first.Payload)
	require.NoError(t, first.Err)

	second := receiveTerminalSocketRead(t, serverReadCh)
	assert.Equal(t, websocket.BinaryMessage, second.MessageType)
	assert.Equal(t, []byte("raw"), second.Payload)
	require.NoError(t, second.Err)

	third := receiveTerminalSocketRead(t, serverReadCh)
	require.Error(t, third.Err)
}

func TestParseTerminalDimension(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty", raw: "", want: 0},
		{name: "whitespace", raw: "   ", want: 0},
		{name: "invalid", raw: "wide", want: 0},
		{name: "valid", raw: " 120 ", want: 120},
		{name: "negative", raw: "-3", want: -3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseTerminalDimension(tt.raw))
		})
	}
}

func TestResolveTerminalShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/local/bin/fish")
	shell, name := resolveTerminalShell()
	assert.Equal(t, "/usr/local/bin/fish", shell)
	assert.Equal(t, "fish", name)

	t.Setenv("SHELL", "   ")
	shell, name = resolveTerminalShell()
	assert.Equal(t, "/bin/bash", shell)
	assert.Equal(t, "bash", name)
}

func TestTerminalEnvAddsMissingTermAndShell(t *testing.T) {
	unsetEnvForTest(t, "TERM")
	unsetEnvForTest(t, "SHELL")

	env := terminalEnv("/bin/zsh")
	assert.Contains(t, env, "TERM=xterm-256color")
	assert.Contains(t, env, "SHELL=/bin/zsh")
}

func TestTerminalEnvPreservesExistingTermAndShell(t *testing.T) {
	t.Setenv("TERM", "screen-256color")
	t.Setenv("SHELL", "/bin/fish")

	env := terminalEnv("/bin/zsh")
	assert.Contains(t, env, "TERM=screen-256color")
	assert.Contains(t, env, "SHELL=/bin/fish")
	assert.NotContains(t, env, "TERM=xterm-256color")
	assert.NotContains(t, env, "SHELL=/bin/zsh")
}

func TestParseTerminalSignalVariants(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want syscall.Signal
	}{
		{name: "int", raw: "INT", want: syscall.SIGINT},
		{name: "term", raw: "sigterm", want: syscall.SIGTERM},
		{name: "hup", raw: " HUP ", want: syscall.SIGHUP},
		{name: "quit", raw: "SIGQUIT", want: syscall.SIGQUIT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signal, ok := parseTerminalSignal(tt.raw)
			require.True(t, ok)
			assert.Equal(t, tt.want, signal)
		})
	}
}

func TestTerminalOriginAllowedBranches(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://localhost:8080")
	assert.True(t, terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://localhost:3000")
	assert.False(t, terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "://bad-url")
	assert.False(t, terminalOriginAllowed(req))

	req = httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Host = ""
	req.Header.Set("Origin", "http://localhost:3000")
	assert.False(t, terminalOriginAllowed(req))
}

func TestNormalizedHostPort(t *testing.T) {
	assert.Equal(t, "", normalizedHostPort("   "))
	assert.Equal(t, "example.com:8080", normalizedHostPort("Example.COM:8080"))
	assert.Equal(t, "[::1]:8080", normalizedHostPort("[::1]:8080"))
	assert.Equal(t, "example.com", normalizedHostPort("Example.COM"))
	assert.Equal(t, "::1", normalizedHostPort("[::1"))
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()

	oldValue, hadValue := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if hadValue {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func receiveTerminalSocketRead(t *testing.T, ch <-chan terminalSocketRead) terminalSocketRead {
	t.Helper()

	select {
	case read := <-ch:
		return read
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal websocket read")
		return terminalSocketRead{}
	}
}

func TestReadTerminalWebsocketStopsWhenContextIsCanceledBeforeSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readCh := make(chan terminalSocketRead)
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := terminalUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		readTerminalWebsocket(ctx, conn, readCh)
		close(done)
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("blocked")))
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal websocket reader to stop after context cancellation")
	}
}
