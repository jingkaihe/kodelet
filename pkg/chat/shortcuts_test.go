package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceShortcutOptionsValidateBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "token", "")
	require.NoError(t, err)
	valid := WorkspaceShortcutRequest{Target: WorkspaceTarget{RunnerID: "runner", CWD: "/runner/only"}, Digest: "sha256:discovery", Shortcut: protocol.ShortcutDescriptor{Key: "ctrl+r", ExtensionID: "review", Generation: 4}}
	for _, options := range []*llmtypes.ExecutionOptions{
		{NoExtensions: new(true)}, {Model: new("gpt-4.1")}, {MaxTurns: new(0)},
	} {
		request := valid
		request.Target.Options = options
		_, err := client.ExecuteWorkspaceShortcut(t.Context(), request)
		require.Error(t, err)
	}
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	for _, raw := range []string{"null", `{"noSkills":null}`, `{"unknown":true}`, `{"model":"gpt-4.1"}`} {
		payload := strings.Replace(string(data), `"target":{`, `"target":{"options":`+raw+`,`, 1)
		var request WorkspaceShortcutRequest
		require.Error(t, json.Unmarshal([]byte(payload), &request), raw)
	}
	for _, raw := range []string{`{}`, `{"noExtensions":false,"noSkills":false,"allowedTools":[]}`} {
		payload := strings.Replace(string(data), `"target":{`, `"target":{"options":`+raw+`,`, 1)
		var request WorkspaceShortcutRequest
		require.NoError(t, json.Unmarshal([]byte(payload), &request), raw)
		if request.Target.Options.AllowedTools != nil {
			assert.Empty(t, *request.Target.Options.AllowedTools)
		}
	}
	assert.Zero(t, calls.Load())
}

func TestControlPlaneShortcutStreamsUIAndResultWithoutProviderTurn(t *testing.T) {
	replied := make(chan struct{})
	var clientID atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/chat/shortcuts":
			clientID.Store(r.Header.Get(ClientIDHeader))
			assert.NotEmpty(t, clientID.Load())
			var request WorkspaceShortcutRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, "/runner/only", request.Target.CWD)
			assert.NotNil(t, request.Target.Options.AllowedTools)
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"kind":"ui-confirm","conversation_id":"temporary-scope","ui_confirm":{"id":"prompt","title":"Confirm shortcut"}}`)
			w.(http.Flusher).Flush()
			select {
			case <-replied:
			case <-r.Context().Done():
				return
			}
			fmt.Fprintln(w, `{"kind":"shortcut-result","content":{"matched":true,"result":{"action":"submit","message":"/review"}}}`)
			fmt.Fprintln(w, `{"kind":"done"}`)
		case "/api/conversations/temporary-scope/ui-input/prompt":
			assert.Equal(t, clientID.Load(), r.Header.Get(ClientIDHeader))
			close(replied)
		default:
			assert.Fail(t, "unexpected endpoint", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "token", "")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ctx = extensions.ContextWithUIInputBroker(ctx, &staticControlPlaneUIBroker{})
	result, err := client.ExecuteWorkspaceShortcut(ctx, WorkspaceShortcutRequest{
		Target: WorkspaceTarget{RunnerID: "runner", CWD: "/runner/only", Options: &llmtypes.ExecutionOptions{AllowedTools: &[]string{}}},
		Digest: "sha256:discovery", Shortcut: protocol.ShortcutDescriptor{Key: "ctrl+r", ExtensionID: "review", Generation: 2},
	})
	require.NoError(t, err)
	assert.True(t, result.Matched)
	require.NotNil(t, result.Result)
	assert.Equal(t, "/review", result.Result.Message)
}
