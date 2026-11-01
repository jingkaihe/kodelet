package extension_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingHost answers host requests from a method table and records calls.
type recordingHost struct {
	mu        sync.Mutex
	calls     []extensiontest.HostCall
	responses map[string]func(extensiontest.HostCall) (any, error)
}

func newRecordingHost(responses map[string]func(extensiontest.HostCall) (any, error)) *recordingHost {
	return &recordingHost{responses: responses}
}

func (h *recordingHost) serve(_ context.Context, call extensiontest.HostCall) (any, error) {
	h.mu.Lock()
	h.calls = append(h.calls, call)
	respond := h.responses[call.Method]
	h.mu.Unlock()
	if respond == nil {
		return nil, &extension.RPCError{Code: -32601, Message: "method not found: " + call.Method}
	}
	return respond(call)
}

func (h *recordingHost) methods() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	methods := make([]string, 0, len(h.calls))
	for _, call := range h.calls {
		methods = append(methods, call.Method)
	}
	return methods
}

func (h *recordingHost) call(method string) extensiontest.HostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, call := range h.calls {
		if call.Method == method {
			return call
		}
	}
	return extensiontest.HostCall{}
}

func fixed(value any) func(extensiontest.HostCall) (any, error) {
	return func(extensiontest.HostCall) (any, error) { return value, nil }
}

func initWith(t *testing.T, h *extensiontest.Harness, configure func(*extension.InitializeParams)) {
	t.Helper()
	params := extensiontest.DefaultInitializeParams()
	configure(&params)
	_, err := h.Initialize(t.Context(), &params)
	require.NoError(t, err)
}

func TestCallContextComesFromInitializeAndCallMetadata(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	ext := extension.New(extension.Metadata{})
	var seen extension.CallInfo
	var resolved, relative string
	var escapeErr, storageErr error
	var stored map[string]any
	var config map[string]any
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "inspect", Execute: func(_ context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
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
	}})
	h := newHarness(t, ext, nil)
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Extension.RunnerID = "runner-1"
		params.Extension.CWD = "/not-used"
		params.Extension.DataDir = dataDir
		params.Extension.Config = map[string]any{"mode": "strict"}
	})
	_, err := h.ExecuteTool(t.Context(), "inspect", map[string]any{}, extensiontest.WithCall(extension.CallInfo{
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
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){
		"kodelet.ui.input":   fixed(map[string]any{"status": "submitted", "value": "typed"}),
		"kodelet.ui.confirm": fixed(map[string]any{"status": "submitted", "confirmed": true}),
		"kodelet.ui.select":  fixed(map[string]any{"status": "dismissed"}),
		"kodelet.ui.notify":  fixed(map[string]any{}),
	})
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "ask", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
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
	}})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	result, err := h.ExecuteTool(t.Context(), "ask", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "typed", result.Content)
	assert.Equal(t, []string{"kodelet.ui.input", "kodelet.ui.confirm", "kodelet.ui.select", "kodelet.ui.notify"}, host.methods())
	assert.JSONEq(t, `{"title":"Name","secret":true}`, string(host.call("kodelet.ui.input").Params))
	assert.JSONEq(t, `{"message":"Allow?"}`, string(host.call("kodelet.ui.confirm").Params))
	assert.NotEmpty(t, host.call("kodelet.ui.notify").ParentID, "UI requests are scoped to the originating call")
}

func TestDefaultHarnessReportsInteractiveUIUnavailable(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "ask", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		_, ok, err := tc.UI.Input(ctx, extension.InputRequest{Title: "Name"})
		if err != nil || ok {
			return extension.ToolResult{}, errors.New("expected unavailable input")
		}
		confirmed, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{Title: "Ok?"})
		if err != nil || confirmed {
			return extension.ToolResult{}, errors.New("expected unavailable confirm")
		}
		return extension.Text("ok"), tc.Update(ctx, "progress", nil)
	}})
	h := newHarness(t, ext, nil)
	result, err := h.ExecuteTool(t.Context(), "ask", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Content)
}

func TestToolUpdatesRespectHostCapabilities(t *testing.T) {
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){"kodelet.tool.update": fixed(map[string]any{})})
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "progress", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		return extension.Text("done"), tc.Update(ctx, "halfway", map[string]any{"presentation": extension.Presentation{Summary: "Half"}})
	}})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	initWith(t, h, func(params *extension.InitializeParams) { params.Capabilities = map[string]any{} })
	_, err := h.ExecuteTool(t.Context(), "progress", map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, host.methods(), "updates are ignored without host support")

	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities = map[string]any{"tools": map[string]any{"updates": true}}
	})
	_, err = h.ExecuteTool(t.Context(), "progress", map[string]any{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"content":"halfway","data":{"presentation":{"summary":"Half"}}}`, string(host.call("kodelet.tool.update").Params))
}

func TestForkConversationPreservesRequestsAndTranslatesUnavailableErrors(t *testing.T) {
	var forkErr atomic.Pointer[extension.RPCError]
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){
		"kodelet.conversation.fork": func(extensiontest.HostCall) (any, error) {
			if err := forkErr.Load(); err != nil {
				return nil, err
			}
			return map[string]any{"conversationId": " conv-2 "}, nil
		},
	})
	ext := extension.New(extension.Metadata{})
	var options extension.ForkOptions
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "fork", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		id, err := tc.ForkConversation(ctx, options)
		return extension.Text(id), err
	}})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["conversations"] = map[string]any{"fork": true, "hierarchy": true}
	})

	options = extension.ForkOptions{Name: "reviewer", AsChild: true}
	result, err := h.ExecuteTool(t.Context(), "fork", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "conv-2", result.Content)
	assert.JSONEq(t, `{"name":"reviewer","asChild":true}`, string(host.call("kodelet.conversation.fork").Params))

	options = extension.ForkOptions{Name: "  "}
	_, err = h.ExecuteTool(t.Context(), "fork", map[string]any{})
	require.NoError(t, err)
	host.mu.Lock()
	assert.Empty(t, host.calls[len(host.calls)-1].Params, "ordinary forks send no parameters")
	host.mu.Unlock()

	forkErr.Store(&extension.RPCError{Code: -32004, Message: "fork unavailable while streaming"})
	_, err = h.ExecuteTool(t.Context(), "fork", map[string]any{})
	require.ErrorContains(t, err, "fork unavailable while streaming")

	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["conversations"] = map[string]any{"fork": true}
	})
	options = extension.ForkOptions{AsChild: true}
	_, err = h.ExecuteTool(t.Context(), "fork", map[string]any{})
	require.ErrorContains(t, err, "child conversation forks require conversation hierarchy support")
}

func TestForkUnavailableErrorsMatchSentinel(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	var forkErr error
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "fork", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		_, forkErr = tc.ForkConversation(ctx, extension.ForkOptions{})
		return extension.Text("ok"), nil
	}})
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){
		"kodelet.conversation.fork": func(extensiontest.HostCall) (any, error) {
			return nil, &extension.RPCError{Code: -32004, Message: "not now"}
		},
	})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	_, err := h.ExecuteTool(t.Context(), "fork", map[string]any{})
	require.NoError(t, err)
	assert.ErrorIs(t, forkErr, extension.ErrConversationForkUnavailable)
	assert.Empty(t, host.methods(), "unsupported hosts are not asked")

	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["conversations"] = map[string]any{"fork": true}
	})
	_, err = h.ExecuteTool(t.Context(), "fork", map[string]any{})
	require.NoError(t, err)
	assert.ErrorIs(t, forkErr, extension.ErrConversationForkUnavailable)
	assert.EqualError(t, forkErr, "not now")
}

func TestBrowserAcquisitionSharesConcurrentReleaseAndRetriesFailures(t *testing.T) {
	var releases atomic.Int32
	gate := make(chan struct{})
	failNext := atomic.Bool{}
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){
		"kodelet.browser.acquire": fixed(map[string]any{"leaseId": "lease-1", "sessionId": "session-1", "cdpUrl": "ws://127.0.0.1/devtools", "pageTargetId": "page-1"}),
		"kodelet.browser.release": func(extensiontest.HostCall) (any, error) {
			releases.Add(1)
			<-gate
			if failNext.Swap(false) {
				return nil, errors.New("release failed")
			}
			return map[string]any{}, nil
		},
	})
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "browse", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
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
	}})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["browser"] = map[string]any{"version": 1}
	})
	result, err := h.ExecuteTool(t.Context(), "browse", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "lease-1 session-1 ws://127.0.0.1/devtools page-1", result.Content)
	assert.Equal(t, int32(2), releases.Load(), "concurrent releases share a request and success is cached")
	assert.JSONEq(t, `{"leaseId":"lease-1"}`, string(host.call("kodelet.browser.release").Params))
}

func TestBrowserAcquisitionValidatesCapabilityAndResponse(t *testing.T) {
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){
		"kodelet.browser.acquire": fixed(map[string]any{"leaseId": "lease-1", "sessionId": " ", "cdpUrl": "ws://x", "pageTargetId": "p"}),
	})
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "browse", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		_, err := tc.AcquireBrowser(ctx)
		return extension.ToolResult{}, err
	}})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	_, err := h.ExecuteTool(t.Context(), "browse", map[string]any{})
	require.ErrorContains(t, err, "browser acquisition is not supported")
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["browser"] = map[string]any{"version": 1}
	})
	_, err = h.ExecuteTool(t.Context(), "browse", map[string]any{})
	require.ErrorContains(t, err, "invalid browser acquisition response")
}

func TestBackgroundTaskLeasesUsePersistentHostRPCAndRetryRelease(t *testing.T) {
	var failRelease atomic.Bool
	failRelease.Store(true)
	host := newRecordingHost(map[string]func(extensiontest.HostCall) (any, error){
		"kodelet.runtime.background.acquire": fixed(map[string]any{"leaseId": " lease-1 "}),
		"kodelet.runtime.background.release": func(extensiontest.HostCall) (any, error) {
			if failRelease.Swap(false) {
				return nil, errors.New("try again")
			}
			return map[string]any{}, nil
		},
	})
	ext := extension.New(extension.Metadata{})
	var lease *extension.BackgroundTaskLease
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "background", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		var err error
		lease, err = tc.AcquireBackgroundTask(ctx, " indexing ")
		return extension.Text("started"), err
	}})
	h := newHarness(t, ext, &extensiontest.Options{Host: host.serve})
	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["runtime"] = map[string]any{"backgroundTasks": true}
	})
	_, err := h.ExecuteTool(t.Context(), "background", map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, lease)
	assert.Equal(t, "lease-1", lease.ID)
	assert.JSONEq(t, `{"description":"indexing"}`, string(host.call("kodelet.runtime.background.acquire").Params))

	require.ErrorContains(t, lease.Close(t.Context()), "try again")
	require.NoError(t, lease.Close(t.Context()))
	require.NoError(t, lease.Close(t.Context()))
	release := host.call("kodelet.runtime.background.release")
	assert.JSONEq(t, `{"leaseId":"lease-1"}`, string(release.Params))
	assert.Empty(t, release.ParentID, "releases after the call returns are unscoped")
	assert.Equal(t, []string{"kodelet.runtime.background.acquire", "kodelet.runtime.background.release", "kodelet.runtime.background.release"}, host.methods())
}

func TestBackgroundTaskCapabilityAndNoOpLeases(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	var lease *extension.BackgroundTaskLease
	extension.RegisterTool(ext, extension.Tool[struct{}]{Name: "background", Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
		var err error
		lease, err = tc.AcquireBackgroundTask(ctx, "")
		return extension.ToolResult{}, err
	}})
	h := newHarness(t, ext, nil)
	_, err := h.ExecuteTool(t.Context(), "background", map[string]any{})
	require.ErrorContains(t, err, "background extension tasks are not available")

	initWith(t, h, func(params *extension.InitializeParams) {
		params.Capabilities["runtime"] = map[string]any{"backgroundTasks": true}
	})
	_, err = h.ExecuteTool(t.Context(), "background", map[string]any{})
	require.NoError(t, err, "hosts without the method return a local no-op lease")
	assert.Empty(t, lease.ID)
	require.NoError(t, lease.Close(t.Context()))
}
