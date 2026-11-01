package extension_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallContextComesFromInitializeAndCallMetadata(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	var seen extension.CallInfo
	var resolved, relative string
	var escapeErr, storageErr error
	var stored, config map[string]any
	h := toolHarness(t, nil, func(_ context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		seen = tc.CallInfo
		config = tc.Config()
		resolved, _ = tc.ResolveWorkspacePath("sub/file.txt")
		relative = tc.RelativeToWorkspace(filepath.Join(tc.CWD, "sub", "file.txt"))
		_, escapeErr = tc.ResolveWorkspacePath("../escape")
		if err := tc.Storage.WriteJSON("state/value.json", map[string]any{"count": 2}); err != nil {
			return extension.ToolResult{}, err
		}
		if _, err := tc.Storage.ReadJSON("state/value.json", &stored); err != nil {
			return extension.ToolResult{}, err
		}
		_, _, storageErr = tc.Storage.ReadText("../outside")
		tc.Log.Info("inspected", "count", 2)
		return extension.Text("ok"), nil
	})
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Extension.RunnerID = "runner-1"
		params.Extension.CWD = "/not-used"
		params.Extension.DataDir = dataDir
		params.Extension.Config = map[string]any{"mode": "strict"}
	})
	_, err := runTool(t, h, extensiontest.WithCall(extension.CallInfo{
		SessionID: "session-1", ConversationID: "conv-1", UIScopeID: " scope-1 ", CWD: workspace,
		Provider: "openai", Model: "gpt-5", Profile: "default", RecipeName: "review", InvokedBy: "user",
	}))
	require.NoError(t, err)
	assert.Equal(t, extension.CallInfo{
		SessionID: "session-1", RunnerID: "runner-1", ConversationID: "conv-1", UIScopeID: "scope-1", CWD: workspace,
		Provider: "openai", Model: "gpt-5", Profile: "default", RecipeName: "review", InvokedBy: "user",
	}, seen)
	assert.Equal(t, map[string]any{"mode": "strict"}, config)
	assert.Equal(t, filepath.Join(workspace, "sub", "file.txt"), resolved)
	assert.Equal(t, filepath.Join("sub", "file.txt"), relative)
	require.ErrorContains(t, escapeErr, "path escapes workspace")
	assert.Equal(t, map[string]any{"count": float64(2)}, stored)
	require.ErrorContains(t, storageErr, "path escapes extension storage")
	assert.FileExists(t, filepath.Join(dataDir, "state", "value.json"))
}

func TestToolContextRequestsHostUIInteractions(t *testing.T) {
	h := toolHarness(t, replies(map[string]any{
		"kodelet.ui.input":   map[string]any{"status": "submitted", "value": "typed"},
		"kodelet.ui.confirm": map[string]any{"status": "submitted", "confirmed": true},
		"kodelet.ui.select":  map[string]any{"status": "dismissed"},
		"kodelet.ui.notify":  map[string]any{},
	}), func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		value, ok, err := tc.UI.Input(ctx, extension.InputRequest{Title: "Name", Secret: true})
		require.NoError(t, err)
		assert.True(t, ok)
		confirmed, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{Message: "Allow?"})
		require.NoError(t, err)
		_, selected, err := tc.UI.Select(ctx, extension.SelectRequest{Title: "Pick", Options: []string{"a", "b"}})
		require.NoError(t, err)
		require.NoError(t, tc.UI.Notify(ctx, extension.NotifyRequest{Message: "done"}))
		if !confirmed || selected {
			return extension.Text("unexpected"), nil
		}
		return extension.Text(value), nil
	})
	result, err := runTool(t, h)
	require.NoError(t, err)
	assert.Equal(t, "typed", result.Content)
	assert.Equal(t, []string{"kodelet.ui.input", "kodelet.ui.confirm", "kodelet.ui.select", "kodelet.ui.notify"}, methods(h))
	assert.JSONEq(t, `{"title":"Name","secret":true}`, string(firstCall(t, h, "kodelet.ui.input").Params))
	assert.JSONEq(t, `{"message":"Allow?"}`, string(firstCall(t, h, "kodelet.ui.confirm").Params))
	assert.NotEmpty(t, firstCall(t, h, "kodelet.ui.notify").ParentID, "UI requests are scoped to the originating call")
}

func TestDefaultHarnessReportsInteractiveUIUnavailable(t *testing.T) {
	h := toolHarness(t, nil, func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		if _, ok, err := tc.UI.Input(ctx, extension.InputRequest{Title: "Name"}); err != nil || ok {
			return extension.ToolResult{}, errors.New("expected unavailable input")
		}
		if confirmed, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{Title: "Ok?"}); err != nil || confirmed {
			return extension.ToolResult{}, errors.New("expected unavailable confirm")
		}
		return extension.Text("ok"), tc.Update(ctx, "progress", nil)
	})
	result, err := runTool(t, h)
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Content)
}

func TestToolUpdatesRespectHostCapabilities(t *testing.T) {
	h := toolHarness(t, replies(map[string]any{"kodelet.tool.update": map[string]any{}}), func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		return extension.Text("done"), tc.Update(ctx, "halfway", map[string]any{"presentation": extension.Presentation{Summary: "Half"}})
	})
	initWith(t, h, func(params *extension.InitializeParams) { params.Capabilities = map[string]any{} })
	_, err := runTool(t, h)
	require.NoError(t, err)
	assert.Empty(t, methods(h), "updates are ignored without host support")

	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities = map[string]any{"tools": map[string]any{"updates": true}}
	})
	_, err = runTool(t, h)
	require.NoError(t, err)
	assert.JSONEq(t, `{"content":"halfway","data":{"presentation":{"summary":"Half"}}}`, string(firstCall(t, h, "kodelet.tool.update").Params))
}

func TestForkConversation(t *testing.T) {
	var hostErr atomic.Pointer[extension.RPCError]
	var options extension.ForkOptions
	var forkErr error
	h := toolHarness(t, replies(map[string]any{
		"kodelet.conversation.fork": func(extensiontest.HostCall) (any, error) {
			if err := hostErr.Load(); err != nil {
				return nil, err
			}
			return map[string]any{"conversationId": " conv-2 "}, nil
		},
	}), func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		var id string
		id, forkErr = tc.ForkConversation(ctx, options)
		return extension.Text(id), nil
	})
	capabilities := func(conversations map[string]any) {
		initWith(t, h, func(params *extension.InitializeParams) { params.Capabilities["conversations"] = conversations })
	}

	_, err := runTool(t, h)
	require.NoError(t, err)
	require.ErrorIs(t, forkErr, extension.ErrConversationForkUnavailable)
	assert.Empty(t, methods(h), "unsupported hosts are not asked")

	capabilities(map[string]any{"fork": true, "hierarchy": true})
	options = extension.ForkOptions{Name: "reviewer", AsChild: true}
	result, err := runTool(t, h)
	require.NoError(t, err)
	require.NoError(t, forkErr)
	assert.Equal(t, "conv-2", result.Content)
	assert.JSONEq(t, `{"name":"reviewer","asChild":true}`, string(firstCall(t, h, "kodelet.conversation.fork").Params))

	options = extension.ForkOptions{Name: "  "}
	_, err = runTool(t, h)
	require.NoError(t, err)
	calls := h.HostCalls()
	assert.Empty(t, calls[len(calls)-1].Params, "ordinary forks send no parameters")

	hostErr.Store(&extension.RPCError{Code: -32004, Message: "fork unavailable while streaming"})
	_, err = runTool(t, h)
	require.NoError(t, err)
	require.ErrorIs(t, forkErr, extension.ErrConversationForkUnavailable)
	assert.EqualError(t, forkErr, "fork unavailable while streaming")

	capabilities(map[string]any{"fork": true})
	options = extension.ForkOptions{AsChild: true}
	_, err = runTool(t, h)
	require.NoError(t, err)
	require.ErrorContains(t, forkErr, "child conversation forks require conversation hierarchy support")
}

func TestBrowserAcquisitionSharesConcurrentReleaseAndRetriesFailures(t *testing.T) {
	var releases atomic.Int32
	gate := make(chan struct{})
	var failNext atomic.Bool
	h := toolHarness(t, replies(map[string]any{
		"kodelet.browser.acquire": map[string]any{"leaseId": "lease-1", "sessionId": "session-1", "cdpUrl": "ws://127.0.0.1/devtools", "pageTargetId": "page-1"},
		"kodelet.browser.release": func(extensiontest.HostCall) (any, error) {
			releases.Add(1)
			<-gate
			if failNext.Swap(false) {
				return nil, errors.New("release failed")
			}
			return map[string]any{}, nil
		},
	}), func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		connection, err := tc.AcquireBrowser(ctx)
		if err != nil {
			return extension.ToolResult{}, err
		}
		failNext.Store(true)
		errs := make(chan error, 2)
		for range 2 {
			go func() { errs <- connection.Release(ctx) }()
		}
		require.Eventually(t, func() bool { return releases.Load() == 1 }, time.Second, time.Millisecond)
		time.Sleep(20 * time.Millisecond)
		gate <- struct{}{}
		require.Error(t, <-errs)
		require.Error(t, <-errs)
		go func() { gate <- struct{}{} }()
		require.NoError(t, connection.Release(ctx))
		require.NoError(t, connection.Release(ctx))
		return extension.Text(connection.LeaseID + " " + connection.SessionID + " " + connection.CDPURL + " " + connection.PageTargetID), nil
	})
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["browser"] = map[string]any{"version": 1}
	})
	result, err := runTool(t, h)
	require.NoError(t, err)
	assert.Equal(t, "lease-1 session-1 ws://127.0.0.1/devtools page-1", result.Content)
	assert.Equal(t, int32(2), releases.Load(), "concurrent releases share a request and success is cached")
	assert.JSONEq(t, `{"leaseId":"lease-1"}`, string(firstCall(t, h, "kodelet.browser.release").Params))
}

func TestBrowserAcquisitionValidatesCapabilityAndResponse(t *testing.T) {
	h := toolHarness(t, replies(map[string]any{
		"kodelet.browser.acquire": map[string]any{"leaseId": "lease-1", "sessionId": " ", "cdpUrl": "ws://x", "pageTargetId": "p"},
	}), func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		_, err := tc.AcquireBrowser(ctx)
		return extension.ToolResult{}, err
	})
	_, err := runTool(t, h)
	require.ErrorContains(t, err, "browser acquisition is not supported")
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["browser"] = map[string]any{"version": 1}
	})
	_, err = runTool(t, h)
	require.ErrorContains(t, err, "invalid browser acquisition response")
}

func TestBackgroundTaskLeases(t *testing.T) {
	var failRelease, acquireSupported atomic.Bool
	failRelease.Store(true)
	var lease *extension.BackgroundTaskLease
	var description string
	h := toolHarness(t, replies(map[string]any{
		"kodelet.runtime.background.acquire": func(extensiontest.HostCall) (any, error) {
			if !acquireSupported.Load() {
				return nil, &extension.RPCError{Code: -32601, Message: "method not found"}
			}
			return map[string]any{"leaseId": " lease-1 "}, nil
		},
		"kodelet.runtime.background.release": func(extensiontest.HostCall) (any, error) {
			if failRelease.Swap(false) {
				return nil, errors.New("try again")
			}
			return map[string]any{}, nil
		},
	}), func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		var err error
		lease, err = tc.AcquireBackgroundTask(ctx, description)
		return extension.Text("started"), err
	})
	_, err := runTool(t, h)
	require.ErrorContains(t, err, "background extension tasks are not available")

	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["runtime"] = map[string]any{"backgroundTasks": true}
	})
	_, err = runTool(t, h)
	require.NoError(t, err, "hosts without the method return a local no-op lease")
	assert.Empty(t, lease.ID)
	require.NoError(t, lease.Close(t.Context()))

	acquireSupported.Store(true)
	description = " indexing "
	_, err = runTool(t, h)
	require.NoError(t, err)
	assert.Equal(t, "lease-1", lease.ID)
	calls := h.HostCalls()
	assert.JSONEq(t, `{"description":"indexing"}`, string(calls[len(calls)-1].Params))

	require.ErrorContains(t, lease.Close(t.Context()), "try again")
	require.NoError(t, lease.Close(t.Context()))
	require.NoError(t, lease.Close(t.Context()))
	release := firstCall(t, h, "kodelet.runtime.background.release")
	assert.JSONEq(t, `{"leaseId":"lease-1"}`, string(release.Params))
	assert.Empty(t, release.ParentID, "releases after the call returns are unscoped")
	assert.Equal(t, []string{
		"kodelet.runtime.background.acquire", "kodelet.runtime.background.acquire",
		"kodelet.runtime.background.release", "kodelet.runtime.background.release",
	}, methods(h))
}
