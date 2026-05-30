package acp

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/acp/bridge"
	"github.com/jingkaihe/kodelet/pkg/acp/session"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

type storedAppend struct {
	sessionID acptypes.SessionID
	update    any
}

type fakeSessionStorage struct {
	appendErr   error
	readErr     error
	flushErr    error
	deleteErr   error
	closeErr    error
	readUpdates []session.StoredUpdate

	appended      []storedAppend
	reads         []acptypes.SessionID
	flushed       []acptypes.SessionID
	deleted       []acptypes.SessionID
	closedSession []acptypes.SessionID
	closed        bool
}

func (f *fakeSessionStorage) AppendUpdate(sessionID acptypes.SessionID, update any) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.appended = append(f.appended, storedAppend{sessionID: sessionID, update: update})
	return nil
}

func (f *fakeSessionStorage) ReadUpdates(sessionID acptypes.SessionID) ([]session.StoredUpdate, error) {
	f.reads = append(f.reads, sessionID)
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.readUpdates, nil
}

func (f *fakeSessionStorage) Flush(sessionID acptypes.SessionID) error {
	f.flushed = append(f.flushed, sessionID)
	return f.flushErr
}

func (f *fakeSessionStorage) Delete(sessionID acptypes.SessionID) error {
	f.deleted = append(f.deleted, sessionID)
	return f.deleteErr
}

func (f *fakeSessionStorage) CloseSession(sessionID acptypes.SessionID) error {
	f.closedSession = append(f.closedSession, sessionID)
	return nil
}

func (f *fakeSessionStorage) Exists(acptypes.SessionID) bool {
	return len(f.readUpdates) > 0
}

func (f *fakeSessionStorage) Close() error {
	f.closed = true
	return f.closeErr
}

func readJSONRPCMessage(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var message map[string]any
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &message))
	output.Reset()
	return message
}

func readJSONRPCMessages(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()

	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	messages := []map[string]any{}
	for scanner.Scan() {
		var message map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &message))
		messages = append(messages, message)
	}
	require.NoError(t, scanner.Err())
	output.Reset()
	return messages
}

func assertRPCErrorCode(t *testing.T, message map[string]any, code int) {
	t.Helper()

	require.NotNil(t, message["error"])
	errObj := message["error"].(map[string]any)
	assert.Equal(t, float64(code), errObj["code"])
}

func mustJSONRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func lockServerTestDatabase(t *testing.T, dbPath string) func() {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)

	_, err = db.Exec("BEGIN EXCLUSIVE")
	require.NoError(t, err)

	return func() {
		_, _ = db.Exec("ROLLBACK")
		_ = db.Close()
	}
}

func TestServer_Initialize(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs": map[string]any{
					"readTextFile":  true,
					"writeTextFile": true,
				},
				"terminal": true,
			},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}

	reqData, err := json.Marshal(initReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])

	result := resp["result"].(map[string]any)
	assert.Equal(t, float64(1), result["protocolVersion"])
	assert.NotNil(t, result["agentCapabilities"])
	assert.NotNil(t, result["agentInfo"])

	agentInfo := result["agentInfo"].(map[string]any)
	assert.Equal(t, "kodelet", agentInfo["name"])
	assert.Equal(t, "Kodelet", agentInfo["title"])

	assert.True(t, server.initialized.Load())
	assert.NotNil(t, server.GetClientCapabilities())
	assert.True(t, server.GetClientCapabilities().Terminal)
}

func TestServer_SessionNew_NotInitialized(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "session/new",
		"params": map[string]any{
			"cwd": "/test",
		},
	}

	reqData, err := json.Marshal(sessionReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeInternalError), errObj["code"])
	assert.Contains(t, errObj["message"], "Not initialized")
}

func TestServer_UnknownMethod(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	unknownReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "unknown/method",
		"params":  map[string]any{},
	}

	reqData, err := json.Marshal(unknownReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeMethodNotFound), errObj["code"])
}

func TestServer_ParseError(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	invalidJSON := []byte("not valid json\n")

	err := server.handleMessage(invalidJSON)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeParseError), errObj["code"])
}

func TestServer_Authenticate(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	authReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "authenticate",
		"params":  map[string]any{},
	}

	reqData, err := json.Marshal(authReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])
}

func TestServer_RunSkipsBlankLinesAndStopsOnEOF(t *testing.T) {
	input := bytes.NewBufferString("\n" + `{"jsonrpc":"2.0","id":1,"method":"authenticate","params":{}}` + "\n")
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	require.NoError(t, server.Run())

	resp := readJSONRPCMessage(t, output)
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.Equal(t, float64(1), resp["id"])
	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])
}

func TestServer_RequestHandlersReturnEarlyErrors(t *testing.T) {
	output := bytes.NewBuffer(nil)
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(output),
		WithContext(context.Background()),
	)

	loadReq := &acptypes.Request{ID: json.RawMessage(`1`), Params: json.RawMessage(`{}`)}
	require.NoError(t, server.handleSessionLoad(loadReq))
	assertRPCErrorCode(t, readJSONRPCMessage(t, output), acptypes.ErrCodeInternalError)

	promptReq := &acptypes.Request{ID: json.RawMessage(`2`), Params: json.RawMessage(`{}`)}
	require.NoError(t, server.handleSessionPrompt(promptReq))
	assertRPCErrorCode(t, readJSONRPCMessage(t, output), acptypes.ErrCodeInternalError)

	server.initialized.Store(true)
	require.NoError(t, server.handleSessionNew(&acptypes.Request{ID: json.RawMessage(`3`), Params: json.RawMessage(`{`)}))
	assertRPCErrorCode(t, readJSONRPCMessage(t, output), acptypes.ErrCodeInvalidParams)

	require.NoError(t, server.handleSessionLoad(&acptypes.Request{ID: json.RawMessage(`4`), Params: json.RawMessage(`{`)}))
	assertRPCErrorCode(t, readJSONRPCMessage(t, output), acptypes.ErrCodeInvalidParams)

	require.NoError(t, server.handleSessionPrompt(&acptypes.Request{ID: json.RawMessage(`5`), Params: json.RawMessage(`{`)}))
	assertRPCErrorCode(t, readJSONRPCMessage(t, output), acptypes.ErrCodeInvalidParams)

	require.NoError(t, server.handleSetMode(&acptypes.Request{ID: json.RawMessage(`6`)}))
	assertRPCErrorCode(t, readJSONRPCMessage(t, output), acptypes.ErrCodeMethodNotFound)
}

func TestServer_WithConfigStoresServerConfig(t *testing.T) {
	config := &ServerConfig{
		Provider:             "openai",
		Model:                "gpt-4.1",
		MaxTokens:            1024,
		NoSkills:             true,
		NoExtensions:         true,
		NoWorkflows:          true,
		DisableFSSearchTools: true,
		DisableSubagent:      true,
		MaxTurns:             3,
		CompactRatio:         0.4,
	}

	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(io.Discard),
		WithContext(context.Background()),
		WithConfig(config),
	)

	assert.Same(t, config, server.config)
}

func TestServer_StoreUpdatePersistsBestEffort(t *testing.T) {
	storage := &fakeSessionStorage{}
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(io.Discard),
		WithContext(context.Background()),
	)
	server.sessionStorage = storage

	update := map[string]any{"sessionUpdate": acptypes.UpdateUserMessageChunk}
	server.storeUpdate(acptypes.SessionID("session-1"), update)

	require.Len(t, storage.appended, 1)
	assert.Equal(t, acptypes.SessionID("session-1"), storage.appended[0].sessionID)
	assert.Equal(t, update, storage.appended[0].update)

	storage.appendErr = errors.New("append failed")
	require.NotPanics(t, func() {
		server.storeUpdate(acptypes.SessionID("session-1"), update)
	})
	assert.Len(t, storage.appended, 1)
}

func TestServer_HandleResponseRoutesPendingClientCalls(t *testing.T) {
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(io.Discard),
		WithContext(context.Background()),
	)
	resultCh := make(chan json.RawMessage, 1)
	server.pendingRequests["1"] = resultCh

	require.NoError(t, server.handleResponse(json.RawMessage(`1`), json.RawMessage(`{"ok":true}`), nil))
	assert.JSONEq(t, `{"ok":true}`, string(<-resultCh))
	_, stillPending := server.pendingRequests["1"]
	assert.False(t, stillPending)

	server.pendingRequests["2"] = resultCh
	require.NoError(t, server.handleResponse(json.RawMessage(`2`), nil, &acptypes.RPCError{Code: acptypes.ErrCodeInternalError, Message: "boom"}))
	assert.Nil(t, <-resultCh)

	require.NoError(t, server.handleResponse(json.RawMessage(`unknown`), json.RawMessage(`{}`), nil))
}

func TestServer_CallClientSendsRequestAndReceivesResponse(t *testing.T) {
	output := bytes.NewBuffer(nil)
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(output),
		WithContext(context.Background()),
	)

	resultCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := server.CallClient(context.Background(), "client/test", map[string]any{"key": "value"})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	require.Eventually(t, func() bool {
		server.pendingMu.Lock()
		defer server.pendingMu.Unlock()
		return len(server.pendingRequests) == 1
	}, time.Second, 10*time.Millisecond)

	request := readJSONRPCMessage(t, output)
	assert.Equal(t, "2.0", request["jsonrpc"])
	assert.Equal(t, float64(1), request["id"])
	assert.Equal(t, "client/test", request["method"])

	require.NoError(t, server.handleResponse(json.RawMessage(`1`), json.RawMessage(`{"answer":42}`), nil))
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case result := <-resultCh:
		assert.JSONEq(t, `{"answer":42}`, string(result))
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client call")
	}
}

func TestServer_CallClientHandlesClientErrorAndContextCancel(t *testing.T) {
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(io.Discard),
		WithContext(context.Background()),
	)

	errCh := make(chan error, 1)
	go func() {
		_, err := server.CallClient(context.Background(), "client/error", nil)
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		server.pendingMu.Lock()
		defer server.pendingMu.Unlock()
		return len(server.pendingRequests) == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, server.handleResponse(json.RawMessage(`1`), nil, &acptypes.RPCError{Code: acptypes.ErrCodeInternalError, Message: "boom"}))
	assert.ErrorContains(t, <-errCh, "client returned error")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := server.CallClient(ctx, "client/cancel", nil)
	assert.ErrorIs(t, err, context.Canceled)
	server.pendingMu.Lock()
	_, stillPending := server.pendingRequests["2"]
	server.pendingMu.Unlock()
	assert.False(t, stillPending)
}

func TestServer_ReplaySessionUpdates(t *testing.T) {
	output := bytes.NewBuffer(nil)
	storage := &fakeSessionStorage{
		readUpdates: []session.StoredUpdate{
			{Update: json.RawMessage(`{`)},
			{Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`)},
		},
	}
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(output),
		WithContext(context.Background()),
	)
	server.sessionStorage = storage

	require.NoError(t, server.replaySessionUpdates(acptypes.SessionID("session-1")))
	assert.Equal(t, []acptypes.SessionID{"session-1"}, storage.reads)

	notif := readJSONRPCMessage(t, output)
	assert.Equal(t, "session/update", notif["method"])
	params := notif["params"].(map[string]any)
	assert.Equal(t, "session-1", params["sessionId"])
	update := params["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])

	storage.readErr = errors.New("read failed")
	assert.ErrorContains(t, server.replaySessionUpdates(acptypes.SessionID("session-1")), "read failed")

	server.sessionStorage = nil
	assert.NoError(t, server.replaySessionUpdates(acptypes.SessionID("session-1")))
}

func TestServer_SendUpdate(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionID := acptypes.SessionID("test-session")
	update := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": "Hello",
		},
	}

	err := server.SendUpdate(sessionID, update)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var notif map[string]any
	err = json.Unmarshal(scanner.Bytes(), &notif)
	require.NoError(t, err)

	assert.Equal(t, "2.0", notif["jsonrpc"])
	assert.Equal(t, "session/update", notif["method"])
	assert.Nil(t, notif["id"])

	params := notif["params"].(map[string]any)
	assert.Equal(t, "test-session", params["sessionId"])
	assert.NotNil(t, params["update"])
}

func TestServer_SendUpdate_WithUnavailableStorage(t *testing.T) {
	tmpDir := t.TempDir()
	unlock := lockServerTestDatabase(t, filepath.Join(tmpDir, "storage.db"))
	defer unlock()
	t.Setenv("KODELET_BASE_PATH", tmpDir)

	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	err := server.SendUpdate(acptypes.SessionID("test-session"), map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": "Hello",
		},
	})
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var notif map[string]any
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &notif))
	assert.Equal(t, "session/update", notif["method"])
}

func TestServer_Notification_Cancel(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	cancelNotif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params": map[string]any{
			"sessionId": "nonexistent-session",
		},
	}

	reqData, err := json.Marshal(cancelNotif)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	// Cancellation is best-effort and doesn't return errors
	// (session may not exist, which is fine for idempotent cancel)
	err = server.handleMessage(reqData)
	assert.NoError(t, err)
}

func TestServer_Shutdown(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	assert.NotNil(t, server.ctx)
	assert.NotNil(t, server.cancel)

	server.Shutdown()

	select {
	case <-server.ctx.Done():
	default:
		t.Error("context should be cancelled after Shutdown")
	}
}

func TestServer_Shutdown_WithUnavailableStorage(t *testing.T) {
	tmpDir := t.TempDir()
	unlock := lockServerTestDatabase(t, filepath.Join(tmpDir, "storage.db"))
	defer unlock()
	t.Setenv("KODELET_BASE_PATH", tmpDir)

	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	require.NotPanics(t, func() {
		server.Shutdown()
	})

	select {
	case <-server.ctx.Done():
	default:
		t.Error("context should be cancelled after Shutdown")
	}
}

func TestServer_ConcurrentPromptLimit(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	// Simulate an active prompt by adding a cancel func to activePrompts
	sessionID := acptypes.SessionID("test-session")
	server.activePromptsMu.Lock()
	server.activePrompts[sessionID] = func() {}
	server.activePromptsMu.Unlock()

	// Mark server as initialized
	server.initialized.Store(true)

	// Try to start another prompt for the same session
	promptReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt": []map[string]any{
				{"type": "text", "text": "test prompt"},
			},
		},
	}

	reqData, err := json.Marshal(promptReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	// Should get an error because a prompt is already active
	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeInternalError), errObj["code"])
	assert.Contains(t, errObj["message"], "prompt already in progress")
}

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		name        string
		prompt      []acptypes.ContentBlock
		wantCommand string
		wantArgs    string
		wantFound   bool
	}{
		{
			name: "simple command",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/test"},
			},
			wantCommand: "test",
			wantArgs:    "",
			wantFound:   true,
		},
		{
			name: "command with args",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/init fix the bug"},
			},
			wantCommand: "init",
			wantArgs:    "fix the bug",
			wantFound:   true,
		},
		{
			name: "command with whitespace",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "  /commit  "},
			},
			wantCommand: "commit",
			wantArgs:    "",
			wantFound:   true,
		},
		{
			name: "recipe name with slashes",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/github/pr create a pr"},
			},
			wantCommand: "github/pr",
			wantArgs:    "create a pr",
			wantFound:   true,
		},
		{
			name: "not a command",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "hello world"},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "empty prompt",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: ""},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "image block ignored",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeImage, Data: "base64data"},
				{Type: acptypes.ContentTypeText, Text: "/test"},
			},
			wantCommand: "test",
			wantArgs:    "",
			wantFound:   true,
		},
		{
			name: "non-text blocks only",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeImage, Data: "base64data"},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "just slash",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/"},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "slash with space only",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/ "},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, args, found := parseSlashCommand(tt.prompt)
			assert.Equal(t, tt.wantCommand, command)
			assert.Equal(t, tt.wantArgs, args)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestServer_GetAvailableCommands(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	commands := server.getAvailableCommands()
	assert.NotNil(t, commands)
	assert.Greater(t, len(commands), 0)

	for _, cmd := range commands {
		assert.NotEmpty(t, cmd.Name)
		assert.NotEmpty(t, cmd.Description)
		assert.NotNil(t, cmd.Input)
		assert.NotEmpty(t, cmd.Input.Hint)
	}
}

func TestServer_SendAvailableCommands(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionID := acptypes.SessionID("test-session")
	err := server.sendAvailableCommands(sessionID)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var notif map[string]any
	err = json.Unmarshal(scanner.Bytes(), &notif)
	require.NoError(t, err)

	assert.Equal(t, "2.0", notif["jsonrpc"])
	assert.Equal(t, "session/update", notif["method"])
	assert.Nil(t, notif["id"])

	params := notif["params"].(map[string]any)
	assert.Equal(t, "test-session", params["sessionId"])

	update := params["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAvailableCommands, update["sessionUpdate"])
	assert.NotNil(t, update["availableCommands"])

	availableCommands := update["availableCommands"].([]any)
	assert.Greater(t, len(availableCommands), 0)
}

func TestServer_ExtensionCommandsAreAvailableForSession(t *testing.T) {
	workspace := newACPTestExtensionWorkspace(t)
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(io.Discard),
		WithContext(context.Background()),
		WithConfig(&ServerConfig{Provider: "anthropic", Model: "claude-test", NoSkills: true, NoWorkflows: true, DisableSubagent: true}),
	)
	t.Cleanup(func() { server.Shutdown() })
	sess, err := server.sessionManager.NewSession(context.Background(), acptypes.NewSessionRequest{CWD: workspace})
	require.NoError(t, err)

	commands := server.getAvailableCommandsForSession(sess.ID)
	var names []string
	for _, command := range commands {
		names = append(names, command.Name)
	}
	assert.Contains(t, names, "doctor")
}

func TestServer_ExtensionRespondCommandBypassesAgent(t *testing.T) {
	workspace := newACPTestExtensionWorkspace(t)
	output := bytes.NewBuffer(nil)
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(output),
		WithContext(context.Background()),
		WithConfig(&ServerConfig{Provider: "anthropic", Model: "claude-test", NoSkills: true, NoWorkflows: true, DisableSubagent: true}),
	)
	t.Cleanup(func() { server.Shutdown() })
	server.initialized.Store(true)
	storage := &fakeSessionStorage{}
	server.sessionStorage = storage

	sess, err := server.sessionManager.NewSession(context.Background(), acptypes.NewSessionRequest{CWD: workspace})
	require.NoError(t, err)

	req := &acptypes.Request{
		ID: json.RawMessage(`7`),
		Params: mustJSONRawMessage(t, acptypes.PromptRequest{
			SessionID: sess.ID,
			Prompt: []acptypes.ContentBlock{{
				Type: acptypes.ContentTypeText,
				Text: "/doctor verbose=true",
			}},
		}),
	}

	require.NoError(t, server.handleSessionPrompt(req))

	messages := readJSONRPCMessages(t, output)
	require.Len(t, messages, 2)
	updateNotification := messages[0]
	assert.Equal(t, "session/update", updateNotification["method"])
	params := updateNotification["params"].(map[string]any)
	update := params["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAgentMessageChunk, update["sessionUpdate"])
	content := update["content"].(map[string]any)
	assert.Equal(t, fmt.Sprintf("All extensions are healthy for %s.", sess.ID), content["text"])

	response := messages[1]
	assert.Equal(t, float64(7), response["id"])
	result := response["result"].(map[string]any)
	assert.Equal(t, string(acptypes.StopReasonEndTurn), result["stopReason"])
	assert.Contains(t, storage.flushed, sess.ID)
}

func TestServer_ExtensionRunAgentCommandTransformsPrompt(t *testing.T) {
	workspace := newACPTestExtensionWorkspace(t)
	server := NewServer(
		WithInput(bytes.NewBuffer(nil)),
		WithOutput(io.Discard),
		WithContext(context.Background()),
		WithConfig(&ServerConfig{Provider: "anthropic", Model: "claude-test", NoSkills: true, NoWorkflows: true, DisableSubagent: true}),
	)
	t.Cleanup(func() { server.Shutdown() })
	sess, err := server.sessionManager.NewSession(context.Background(), acptypes.NewSessionRequest{CWD: workspace})
	require.NoError(t, err)

	result, handled, err := server.tryExtensionCommand(
		context.Background(),
		sess,
		[]acptypes.ContentBlock{{Type: acptypes.ContentTypeText, Text: "/review target=HEAD"}},
		"review",
		"target=HEAD",
	)
	require.NoError(t, err)
	require.True(t, handled)

	transformed, shouldReturn, err := server.applyExtensionCommandResult(
		json.RawMessage(`1`),
		sess.ID,
		sess,
		[]acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/review target=HEAD"},
			{Type: acptypes.ContentTypeImage, Data: "image-data"},
		},
		result,
	)

	require.NoError(t, err)
	assert.False(t, shouldReturn)
	require.Len(t, transformed, 2)
	assert.Equal(t, "Review HEAD", transformed[0].Text)
	assert.Equal(t, acptypes.ContentTypeImage, transformed[1].Type)
	assert.Equal(t, "review", sess.Thread.GetMetadata()["recipe_name"])
}

func TestParseSlashCommandArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantKVArgs     map[string]string
		wantAdditional string
	}{
		{
			name:           "empty args",
			args:           "",
			wantKVArgs:     map[string]string{},
			wantAdditional: "",
		},
		{
			name:           "only additional text",
			args:           "fix the bug please",
			wantKVArgs:     map[string]string{},
			wantAdditional: "fix the bug please",
		},
		{
			name:           "single key=value",
			args:           "target=main",
			wantKVArgs:     map[string]string{"target": "main"},
			wantAdditional: "",
		},
		{
			name:           "multiple key=value",
			args:           "target=main draft=true",
			wantKVArgs:     map[string]string{"target": "main", "draft": "true"},
			wantAdditional: "",
		},
		{
			name:           "key=value with additional text",
			args:           "target=develop fix the authentication bug",
			wantKVArgs:     map[string]string{"target": "develop"},
			wantAdditional: "fix the authentication bug",
		},
		{
			name:           "quoted value with spaces",
			args:           `title="my feature branch" draft=false`,
			wantKVArgs:     map[string]string{"title": "my feature branch", "draft": "false"},
			wantAdditional: "",
		},
		{
			name:           "mixed order",
			args:           "target=main please review draft=true carefully",
			wantKVArgs:     map[string]string{"target": "main", "draft": "true"},
			wantAdditional: "please review carefully",
		},
		{
			name:           "empty value",
			args:           "key=",
			wantKVArgs:     map[string]string{"key": ""},
			wantAdditional: "",
		},
		{
			name:           "multiple spaces",
			args:           "  target=main   draft=true  ",
			wantKVArgs:     map[string]string{"target": "main", "draft": "true"},
			wantAdditional: "",
		},
		{
			name:           "unclosed quote takes rest of string",
			args:           `title="unclosed value`,
			wantKVArgs:     map[string]string{"title": "unclosed value"},
			wantAdditional: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kvArgs, additional := parseSlashCommandArgs(tt.args)
			assert.Equal(t, tt.wantKVArgs, kvArgs)
			assert.Equal(t, tt.wantAdditional, additional)
		})
	}
}

func TestBuildCommandHint(t *testing.T) {
	tests := []struct {
		name      string
		arguments map[string]fragments.ArgumentMeta
		want      string
	}{
		{
			name:      "no arguments",
			arguments: nil,
			want:      "additional instructions (optional)",
		},
		{
			name:      "empty arguments",
			arguments: map[string]fragments.ArgumentMeta{},
			want:      "additional instructions (optional)",
		},
		{
			name: "single argument with default",
			arguments: map[string]fragments.ArgumentMeta{
				"target": {Default: "main"},
			},
			want: "[target=main] additional instructions",
		},
		{
			name: "single argument without default",
			arguments: map[string]fragments.ArgumentMeta{
				"target": {Description: "Target branch"},
			},
			want: "[target=<value>] additional instructions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCommandHint(tt.arguments)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildCommandHint_MultipleArguments(t *testing.T) {
	t.Run("two keys with defaults", func(t *testing.T) {
		arguments := map[string]fragments.ArgumentMeta{
			"target": {Default: "main"},
			"draft":  {Default: "false"},
		}
		got := buildCommandHint(arguments)
		assert.Equal(t, "[draft=false target=main] additional instructions", got)
	})

	t.Run("mixed with and without defaults", func(t *testing.T) {
		arguments := map[string]fragments.ArgumentMeta{
			"target":    {Default: "main"},
			"issue_url": {Description: "The issue URL"},
		}
		got := buildCommandHint(arguments)
		assert.Equal(t, "[issue_url=<value> target=main] additional instructions", got)
	})

	t.Run("three or more keys - deterministic ordering", func(t *testing.T) {
		arguments := map[string]fragments.ArgumentMeta{
			"zebra":  {Default: "last"},
			"alpha":  {Default: "first"},
			"middle": {Default: "center"},
			"beta":   {Default: "second"},
		}
		got := buildCommandHint(arguments)
		assert.Equal(t, "[alpha=first beta=second middle=center zebra=last] additional instructions", got)
	})
}

func TestServer_TransformSlashCommandPrompt(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	t.Run("transforms valid recipe command", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/init"},
		}

		result, expansion, err := server.transformSlashCommandPrompt("init", "", originalPrompt)
		require.NoError(t, err)
		require.NotNil(t, expansion)
		require.NotEmpty(t, result)
		assert.Equal(t, acptypes.ContentTypeText, result[0].Type)
		assert.NotEmpty(t, result[0].Text)
		assert.Equal(t, "/init", expansion.Display)
	})

	t.Run("includes additional text", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/init please focus on tests"},
		}

		result, expansion, err := server.transformSlashCommandPrompt("init", "please focus on tests", originalPrompt)
		require.NoError(t, err)
		require.NotNil(t, expansion)
		require.NotEmpty(t, result)
		assert.Contains(t, result[0].Text, "Additional instructions:")
		assert.Contains(t, result[0].Text, "please focus on tests")
		assert.Equal(t, "/init please focus on tests", expansion.Display)
	})

	t.Run("preserves non-text blocks", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/init"},
			{Type: acptypes.ContentTypeImage, Data: "base64imagedata", MimeType: "image/png"},
		}

		result, _, err := server.transformSlashCommandPrompt("init", "", originalPrompt)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Equal(t, acptypes.ContentTypeText, result[0].Type)
		assert.Equal(t, acptypes.ContentTypeImage, result[1].Type)
		assert.Equal(t, "base64imagedata", result[1].Data)
	})

	t.Run("transforms goal command", func(t *testing.T) {
		update, handled, err := goals.ParseSlashCommand("goal", "find server cores and ram", time.Now())
		require.NoError(t, err)
		require.True(t, handled)

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/goal find server cores and ram"},
			{Type: acptypes.ContentTypeImage, Data: "base64imagedata", MimeType: "image/png"},
			{Type: acptypes.ContentTypeResource, Resource: &acptypes.EmbeddedResource{
				URI:  "file:///details.txt",
				Text: "resource text",
			}},
			{Type: acptypes.ContentTypeResourceLink, URI: "file:///linked.txt"},
		}

		result := transformGoalCommandPrompt(update, originalPrompt)
		require.Len(t, result, 2)
		assert.Equal(t, acptypes.ContentTypeText, result[0].Type)
		assert.Contains(t, result[0].Text, "<goal_context>")
		assert.Contains(t, result[0].Text, "find server cores and ram")
		assert.Equal(t, acptypes.ContentTypeImage, result[1].Type)

		message, images := bridge.ContentBlocksToMessage(result)
		assert.Equal(t, update.ModelPrompt, message)
		assert.Equal(t, []string{"data:image/png;base64,base64imagedata"}, images)
	})

	t.Run("returns error for unknown recipe with available recipes", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/nonexistent-recipe-xyz"},
		}

		_, _, err := server.transformSlashCommandPrompt("nonexistent-recipe-xyz", "", originalPrompt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown recipe '/nonexistent-recipe-xyz'")
		assert.Contains(t, err.Error(), "Available recipes:")
	})
}

func newACPTestExtensionWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	writeACPTestExtensionExecutable(t, filepath.Join(workspace, ".kodelet", "extensions", "commands", "kodelet-extension-commands"))
	return workspace
}

func writeACPTestExtensionExecutable(t *testing.T, path string) {
	t.Helper()
	restoreViperExtensionsForTest(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_ACP_TEST_EXTENSION_HELPER=1 exec %q -test.run TestACPServerExtensionHelperProcess --\n", executable)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func restoreViperExtensionsForTest(t *testing.T) {
	t.Helper()
	originalSettings := viper.AllSettings()
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
	viper.Reset()
	viper.Set("extensions.enabled", true)
	viper.Set("extensions.local_dir", "./.kodelet/extensions")
	viper.Set("extensions.global_dir", filepath.Join(t.TempDir(), "global-extensions"))
	viper.Set("extensions.timeout", "5s")
	viper.Set("extensions.tool_timeout", "5s")
	viper.Set("extensions.max_output_size", 102400)
}

func TestACPServerExtensionHelperProcess(t *testing.T) {
	if os.Getenv("KODELET_ACP_TEST_EXTENSION_HELPER") != "1" {
		return
	}
	runACPServerExtensionHelperProcess()
	os.Exit(0)
}

func runACPServerExtensionHelperProcess() {
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readACPServerRPCFrame(reader)
		if err != nil {
			return
		}

		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			writeACPServerRPCResponse(request.ID, nil, map[string]any{"code": -32700, "message": err.Error()})
			continue
		}

		switch request.Method {
		case "extension.initialize":
			writeACPServerRPCResponse(request.ID, extensions.InitializeResult{
				Name: "commands",
				Commands: []extensions.CommandRegistration{
					{
						Name:        "doctor",
						Aliases:     []string{"/doctor"},
						Description: "Inspect extension runtime health",
					},
					{
						Name:        "review",
						Aliases:     []string{"/review"},
						Description: "Run extension review",
						Kind:        "recipe",
					},
				},
			}, nil)
		case "extension.command.execute":
			var params struct {
				Name    string                          `json:"name"`
				Input   map[string]any                  `json:"input"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeACPServerRPCResponse(request.ID, nil, map[string]any{"code": -32602, "message": err.Error()})
				continue
			}
			writeACPServerRPCResponse(request.ID, handleACPServerExtensionCommand(params.Name, params.Input, params.Context), nil)
		default:
			writeACPServerRPCResponse(request.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
		}
	}
}

func handleACPServerExtensionCommand(name string, input map[string]any, callContext extensions.ExtensionCallContext) extensions.CommandResult {
	switch name {
	case "doctor":
		return extensions.CommandResult{
			Action:   extensions.CommandActionRespond,
			Response: fmt.Sprintf("All extensions are healthy for %s.", callContext.ConversationID),
		}
	case "review":
		target, _ := input["target"].(string)
		if strings.TrimSpace(target) == "" {
			target = "HEAD"
		}
		return extensions.CommandResult{Action: extensions.CommandActionRunAgent, Prompt: "Review " + target, RecipeName: "review"}
	default:
		return extensions.CommandResult{Action: extensions.CommandActionPass}
	}
}

func readACPServerRPCFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &contentLength)
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	payload := make([]byte, contentLength)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeACPServerRPCResponse(id int64, result any, rpcErr any) {
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	payload, _ := json.Marshal(response)
	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
}
