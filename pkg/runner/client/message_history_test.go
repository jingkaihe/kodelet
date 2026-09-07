package client

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceMessageHistoryUsesExistingStore(t *testing.T) {
	for _, location := range []string{"default-home", "base-override"} {
		t.Run(location, func(t *testing.T) {
			home, workspace, other := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("KODELET_BASE_PATH", "")
			base := filepath.Join(home, ".kodelet")
			if location == "base-override" {
				base = t.TempDir()
				t.Setenv("KODELET_BASE_PATH", base)
			}
			// The old TUI writes this same store before the runner exists. No
			// conversion or migration should be needed to recall those messages.
			store := messagehistory.NewStoreWithBasePath(base)
			require.NoError(t, store.Append(t.Context(), messagehistory.Entry{ScopeCWD: workspace, Text: "previous TUI session", Source: "tui"}))
			runtime := &recordingRuntimeProvider{}
			service, err := NewService(t.Context(), workspace, ServiceOptions{
				RuntimeProvider: runtime,
				ConfigLoader: func(string) (llmtypes.Config, error) {
					t.Fatal("composer history must not initialize model or extension configuration")
					return llmtypes.Config{}, nil
				},
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			list := callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{})
			assert.Equal(t, workspace, list.CWD)
			assert.Equal(t, workspace, list.ScopeCWD)
			assert.Equal(t, []string{"previous TUI session"}, list.Messages)

			entry := messagehistory.Entry{
				ScopeCWD: other, Source: "spoofed-source", ConversationID: "conversation-new", Profile: "work",
				CreatedAt: time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC),
				Text:      "  /goal preserve raw text\n  second line: 你好\n  ",
			}
			appended, err := service.workspaceMessageHistory(t.Context(), protocol.WorkspaceMessageHistoryParams{Entry: &entry})
			require.NoError(t, err)
			assert.Equal(t, protocol.WorkspaceMessageHistoryResult{CWD: workspace, ScopeCWD: workspace}, appended)
			assert.Equal(t, other, entry.ScopeCWD, "do not mutate the caller's entry")
			stored, err := store.List(t.Context(), workspace, messagehistory.MaxEntriesPerScope)
			require.NoError(t, err)
			require.Len(t, stored, 2)
			assert.Equal(t, messagehistory.Entry{
				Version: 1, ScopeCWD: workspace, Source: "tui", ConversationID: entry.ConversationID,
				Profile: entry.Profile, CreatedAt: entry.CreatedAt, Text: strings.TrimSpace(entry.Text),
			}, stored[1])
			spoofed, err := store.List(t.Context(), other, messagehistory.MaxEntriesPerScope)
			require.NoError(t, err)
			assert.Empty(t, spoofed, "the supplied entry scope must never select the history file")
			callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{Entry: &entry})
			callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{Entry: &messagehistory.Entry{Text: " \n "}})
			assert.Zero(t, runtime.discoveryCalls)
			assert.Zero(t, runtime.activeCalls)
			assert.Empty(t, service.runs)
			require.NoError(t, service.Close())

			reopened, err := NewService(t.Context(), workspace, ServiceOptions{})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			list = callService[protocol.WorkspaceMessageHistoryResult](t, reopened, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{})
			assert.Equal(t, []string{"previous TUI session", strings.TrimSpace(entry.Text)}, list.Messages, "recall survives service recreation, with store deduplication and blank handling intact")
		})
	}
}

func TestWorkspaceMessageHistorySharesGitRootOnRunner(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	t.Setenv("KODELET_BASE_PATH", base)
	startup, repository := filepath.Join(root, "startup"), filepath.Join(root, "repository")
	first, second := filepath.Join(repository, "pkg", "one"), filepath.Join(repository, "pkg", "two")
	for _, directory := range []string{startup, first, second} {
		require.NoError(t, os.MkdirAll(directory, 0o700))
	}
	output, err := exec.Command("git", "-C", repository, "init").CombinedOutput()
	require.NoError(t, err, string(output))
	store := messagehistory.NewStoreWithBasePath(base)
	require.NoError(t, store.Append(t.Context(), messagehistory.Entry{ScopeCWD: repository, Text: "legacy project message"}))
	require.NoError(t, store.Append(t.Context(), messagehistory.Entry{ScopeCWD: startup, Text: "startup-only message"}))
	service, err := NewService(t.Context(), startup, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	appended := callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{
		CWD: "../repository/pkg/one", Entry: &messagehistory.Entry{Text: "submitted from a subdirectory"},
	})
	assert.Equal(t, protocol.WorkspaceMessageHistoryResult{CWD: first, ScopeCWD: repository}, appended)
	list := callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{CWD: second})
	assert.Equal(t, second, list.CWD)
	assert.Equal(t, repository, list.ScopeCWD)
	assert.Equal(t, []string{"legacy project message", "submitted from a subdirectory"}, list.Messages)
	list = callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{})
	assert.Equal(t, []string{"startup-only message"}, list.Messages)
	list = callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{CWD: t.TempDir()})
	assert.Empty(t, list.Messages, "unrelated directories must not share history")
}

func TestWorkspaceMessageHistoryErrors(t *testing.T) {
	var nilService *Service
	_, err := nilService.workspaceMessageHistory(t.Context(), protocol.WorkspaceMessageHistoryParams{})
	require.ErrorContains(t, err, "runner service is required")
	workspace, base := t.TempDir(), t.TempDir()
	t.Setenv("KODELET_BASE_PATH", base)
	service, err := NewService(t.Context(), workspace, ServiceOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	file := filepath.Join(workspace, "not-a-directory")
	require.NoError(t, os.WriteFile(file, []byte("unchanged"), 0o600))
	for _, cwd := range []string{filepath.Join(workspace, "missing"), file} {
		for _, entry := range []*messagehistory.Entry{nil, {Text: "must not be stored"}} {
			_, rpcErr := service.HandleRequest(t.Context(), protocol.MethodWorkspaceMessageHistory, mustJSON(t, protocol.WorkspaceMessageHistoryParams{CWD: cwd, Entry: entry}))
			require.NotNil(t, rpcErr)
			assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, rpcErr := service.HandleRequest(ctx, protocol.MethodWorkspaceMessageHistory, mustJSON(t, protocol.WorkspaceMessageHistoryParams{}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	assert.NoDirExists(t, filepath.Join(base, "message-history"))
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceMessageHistory, json.RawMessage(`{"entry":"not an entry"}`))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeInvalidParams, rpcErr.Code)

	t.Setenv("KODELET_BASE_PATH", file)
	for _, entry := range []*messagehistory.Entry{nil, {Text: "cannot write"}} {
		_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceMessageHistory, mustJSON(t, protocol.WorkspaceMessageHistoryParams{Entry: entry}))
		require.NotNil(t, rpcErr)
		assert.Equal(t, protocol.ErrorCodeInternal, rpcErr.Code)
	}
	require.NoError(t, service.Close())
	_, rpcErr = service.HandleRequest(t.Context(), protocol.MethodWorkspaceMessageHistory, mustJSON(t, protocol.WorkspaceMessageHistoryParams{}))
	require.NotNil(t, rpcErr)
	assert.Equal(t, protocol.ErrorCodeUnavailable, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "closed")
}

func TestWorkspaceMessageHistoryBoundsEncodedResponses(t *testing.T) {
	for _, large := range []struct{ name, text string }{
		{"plain", strings.Repeat("x", workspaceMessageHistoryLimit/2+100)},
		{"escaped", strings.Repeat("<\x00", workspaceMessageHistoryLimit/24+100)},
	} {
		t.Run(large.name, func(t *testing.T) {
			base, workspace := t.TempDir(), t.TempDir()
			t.Setenv("KODELET_BASE_PATH", base)
			store := messagehistory.NewStoreWithBasePath(base)
			texts := []string{"small oldest", "older " + large.text, strings.Repeat("y", workspaceMessageHistoryLimit+1), "newer " + large.text, "small newest"}
			for _, text := range texts {
				require.NoError(t, store.Append(t.Context(), messagehistory.Entry{ScopeCWD: workspace, Text: text}))
			}
			service, err := NewService(t.Context(), workspace, ServiceOptions{})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			list := callService[protocol.WorkspaceMessageHistoryResult](t, service, protocol.MethodWorkspaceMessageHistory, protocol.WorkspaceMessageHistoryParams{})
			assert.Equal(t, []string{texts[0], texts[3], texts[4]}, list.Messages, "prefer newer complete messages without truncating text")
			encoded, err := json.Marshal(list)
			require.NoError(t, err)
			assert.LessOrEqual(t, len(encoded), workspaceMessageHistoryLimit)
			requestID := "rpc:history"
			frame, err := json.Marshal(protocol.Message{JSONRPC: protocol.JSONRPCVersion, ID: &requestID, Result: encoded})
			require.NoError(t, err)
			assert.Less(t, len(frame), 4*1024*1024, "result plus RPC envelope fits the default peer limit")
			stored, err := store.List(t.Context(), workspace, messagehistory.MaxEntriesPerScope)
			require.NoError(t, err)
			require.Len(t, stored, len(texts))
			for i, entry := range stored {
				assert.Equal(t, texts[i], entry.Text, "the response budget must not prune persisted history")
			}
		})
	}
}
