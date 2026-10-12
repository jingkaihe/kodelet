package client

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceNativeSurfaceInputLifecycleAndRunCapabilities(t *testing.T) {
	runtime := extensions.EmptyRuntime()
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })
	service, err := NewService(t.Context(), t.TempDir(), ServiceOptions{
		RuntimeProvider: staticRuntimeProvider{runtime: runtime},
		ConfigLoader:    func(string) (llmtypes.Config, error) { return llmtypes.Config{}, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	peer := &recordingPeer{}
	service.Attach(peer)
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner", Generation: 1}))
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "unattended", ConversationID: "conversation"})
	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "native", Generation: 4}}
	request := extensions.UISurfaceOpenRequest{ID: "canvas", Frame: extensions.UIFrame{Sequence: 1}}
	response, err := service.OpenSurface(t.Context(), source, request)
	require.NoError(t, err)
	assert.False(t, response.Accepted, "unattended runs have no native UI")
	assert.Empty(t, service.ExtensionUIHostCapabilities(t.Context()))
	callService[any](t, service, protocol.MethodRunClose, protocol.RunCloseParams{RunID: "unattended"})
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "run", ConversationID: "conversation", ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: true, PersistentWidgets: true, PersistentSurfaces: true}})
	assert.Equal(t, extensions.ExtensionUIHostCapabilities{Widgets: true, Surfaces: true, Transcript: true}, service.ExtensionUIHostCapabilities(t.Context()))
	service.mu.Lock()
	service.runs["run"].opening = true // session.start can await surface input before run.open returns.
	service.mu.Unlock()
	response, err = service.OpenSurface(t.Context(), source, request)
	require.NoError(t, err)
	require.True(t, response.Accepted)
	key := runnerSurfaceKey{owner: source.owner, scopeID: "conversation", id: "canvas"}
	service.mu.Lock()
	first := service.uiSurfaces[key]
	service.mu.Unlock()
	require.NotZero(t, first.lifecycle)
	input := runnerpayload.UISurfaceInputParams{RunID: "run", Owner: runnerpayload.ExtensionOwner{ExtensionID: "native", Generation: 4}, Lifecycle: first.lifecycle, Request: extensions.UISurfaceInputNotification{ScopeID: "conversation", ID: "canvas", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"}}
	callService[any](t, service, protocol.MethodUISurfaceInput, input)
	callService[any](t, service, protocol.MethodUISurfaceResize, runnerpayload.UISurfaceResizeParams{RunID: input.RunID, Owner: input.Owner, Lifecycle: input.Lifecycle, Request: extensions.UISurfaceResizeNotification{ScopeID: "conversation", ID: "canvas", Sequence: 2, Width: 80, Height: 24}})
	assert.Equal(t, []string{extensions.UISurfaceInputMethod, extensions.UISurfaceResizeMethod}, source.notifications)
	response, err = service.OpenSurface(t.Context(), source, request)
	require.NoError(t, err)
	assert.False(t, response.Accepted, "stale reopen must not replace input authority")
	require.NoError(t, service.notifySurfaceInput(t.Context(), input))
	request.Frame.Sequence++
	response, err = service.OpenSurface(t.Context(), source, request)
	require.NoError(t, err)
	require.True(t, response.Accepted)
	require.ErrorContains(t, service.notifySurfaceInput(t.Context(), input), "closed or its client disconnected")
	service.mu.Lock()
	input.Lifecycle = service.uiSurfaces[key].lifecycle
	service.mu.Unlock()
	wrong := input
	wrong.Owner.Generation++
	require.Error(t, service.notifySurfaceInput(t.Context(), wrong))
	wrong = input
	wrong.Request.ScopeID = "other"
	require.Error(t, service.notifySurfaceInput(t.Context(), wrong))
	require.NoError(t, service.notifySurfaceInput(t.Context(), input))
	service.mu.Lock()
	service.runs["run"].opening = false
	service.mu.Unlock()
	require.NoError(t, service.cancelRun(t.Context(), "run"))
	require.Error(t, service.notifySurfaceInput(t.Context(), input))
	service.mu.Lock()
	assert.Empty(t, service.uiSurfaces)
	service.mu.Unlock()
}

func TestServiceNativeSurfaceCleanupDoesNotRetainSource(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "native", Generation: 1}}
	key := runnerSurfaceKey{owner: source.owner, scopeID: "conversation", id: "canvas"}
	service := &Service{runs: map[string]*activeRun{"run": {id: "run", ctx: ctx, cancel: cancel}}, uiSurfaces: map[runnerSurfaceKey]runnerSurfaceSource{key: {source: source, runID: "run", lifecycle: 1}}}
	defer cancel()
	service.CleanupExtensionUI(source.owner)
	assert.Empty(t, service.uiSurfaces)
	require.Error(t, service.notifySurfaceInput(t.Context(), runnerpayload.UISurfaceInputParams{RunID: "run", Owner: runnerpayload.ExtensionOwner{ExtensionID: "native", Generation: 1}, Lifecycle: 1, Request: extensions.UISurfaceInputNotification{ScopeID: "conversation", ID: "canvas", Sequence: 1}}))
}

func TestServiceNativeSurfaceRetainedProcessReceivesInputAndClosure(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "run", ConversationID: "conversation", ClientCapabilities: protocol.ClientCapabilities{PersistentSurfaces: true, PersistentWidgets: true}})
	state := backgroundState(t, workspace, "conversation")
	require.NotEmpty(t, state.LeaseID)
	result, err := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "run", ToolCallID: "surface", Name: "lifetime", Input: json.RawMessage(`{"Operation":"surface"}`)})
	require.NoError(t, err)
	assert.Contains(t, result.Result.AssistantFacing, `"accepted":true`)
	service.mu.Lock()
	var key runnerSurfaceKey
	var surface runnerSurfaceSource
	for candidate, value := range service.uiSurfaces {
		key, surface = candidate, value
	}
	service.mu.Unlock()
	require.NotNil(t, surface.source, "surface must survive the creating tool request")
	owner := runnerpayload.ExtensionOwner{ExtensionID: key.owner.ExtensionID, Generation: key.owner.Generation}
	require.NoError(t, service.notifySurfaceInput(t.Context(), runnerpayload.UISurfaceInputParams{RunID: "run", Owner: owner, Lifecycle: surface.lifecycle, Request: extensions.UISurfaceInputNotification{ScopeID: "conversation", ID: "canvas", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"}}))
	require.NoError(t, service.closeRun(t.Context(), "run"))
	assertBackgroundNotification := func(method string) {
		t.Helper()
		require.Eventually(t, func() bool {
			data, _ := os.ReadFile(filepath.Join(workspace, "ui-notifications.log"))
			return strings.Contains(string(data), method)
		}, time.Second, time.Millisecond)
	}
	assertBackgroundNotification(extensions.UISurfaceInputMethod)
	assertBackgroundNotification("extension.ui.surface.closed")
	data, err := os.ReadFile(filepath.Join(workspace, "ui-notifications.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"openSequence":1`)
	service.mu.Lock()
	assert.Empty(t, service.uiSurfaces)
	assert.Len(t, service.backgroundLeases, 1, "surface cleanup must not revoke an explicit worker lease")
	service.mu.Unlock()
	ctx := context.WithValue(t.Context(), runnerRunIDContextKey{}, "run")
	response, err := service.OpenSurface(ctx, surface.source, extensions.UISurfaceOpenRequest{ScopeID: "conversation", ID: "canvas", Frame: extensions.UIFrame{Sequence: 2}})
	require.NoError(t, err)
	assert.False(t, response.Accepted)
	assert.Contains(t, response.Reason, "active execution")
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{RunID: "next", ConversationID: "conversation"})
	assertBackgroundNotification(`"surfaces":false`)
	initializations, err := os.ReadFile(filepath.Join(workspace, "initializations.log"))
	require.NoError(t, err)
	assert.Len(t, strings.Fields(string(initializations)), 1, "UI capability refresh must not restart a retained process")
}

// blockingSurfaceSource holds the first surface input until released.
type blockingSurfaceSource struct {
	owner   extensions.UIExtensionOwner
	inputs  atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *blockingSurfaceSource) ExtensionUIOwner() extensions.UIExtensionOwner { return s.owner }

func (s *blockingSurfaceSource) NotifyExtensionUI(_ context.Context, method string, _ any) error {
	if method == extensions.UISurfaceInputMethod && s.inputs.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return nil
}

func TestServiceReloadDrainsAdmittedSurfaceEventsAndFencesNewOnes(t *testing.T) {
	service := newRegisteredTestService(t, t.TempDir(), ServiceOptions{
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{Skills: &llmtypes.SkillsConfig{Enabled: false}, ExtensionSettings: map[string]any{"enabled": false}}, nil
		},
	})
	callService[runnerpayload.Manifest](t, service, protocol.MethodRunOpen, protocol.RunOpenParams{
		RunID: "run", ConversationID: "conversation", EnvironmentReload: true,
	})
	source := &blockingSurfaceSource{
		owner:   extensions.UIExtensionOwner{ExtensionID: "native", Generation: 1},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	key := runnerSurfaceKey{owner: source.owner, scopeID: "conversation", id: "canvas"}
	service.mu.Lock()
	run := service.runs["run"]
	old := run.environment
	service.uiSurfaces = map[runnerSurfaceKey]runnerSurfaceSource{key: {source: source, runID: "run", lifecycle: 1}}
	service.mu.Unlock()
	input := runnerpayload.UISurfaceInputParams{
		RunID: "run", Owner: runnerpayload.ExtensionOwner{ExtensionID: "native", Generation: 1}, Lifecycle: 1,
		Request: extensions.UISurfaceInputNotification{ScopeID: "conversation", ID: "canvas", Sequence: 1},
	}
	delivered := make(chan error, 1)
	go func() { delivered <- service.notifySurfaceInput(t.Context(), input) }()
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("surface input was not delivered")
	}

	reloaded := make(chan error, 1)
	go func() {
		_, err := service.reloadRun(t.Context(), protocol.RunReloadParams{RunID: "run", ExpectedEnvironmentRevision: 1})
		reloaded <- err
	}()
	require.Eventually(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return run.reloading
	}, time.Second, time.Millisecond)
	// Reload owns surface teardown: neither new input nor invalidation reaches
	// the extension being replaced.
	require.ErrorContains(t, service.notifySurfaceInput(t.Context(), input), "closed")
	service.invalidateUISurface(runnerpayload.UISurfaceInvalidateParams{
		RunID: "run", Owner: input.Owner, ScopeID: "conversation", ID: "canvas", Lifecycle: 1,
	})
	service.mu.Lock()
	assert.Contains(t, service.uiSurfaces, key)
	service.mu.Unlock()
	assert.Never(t, func() bool { return !old.IsOpen() }, 50*time.Millisecond, time.Millisecond,
		"the admitted surface event still owns the old environment")

	close(source.release)
	require.NoError(t, <-delivered)
	select {
	case err := <-reloaded:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("reload did not finish")
	}
	assert.False(t, old.IsOpen())
	assert.Equal(t, int32(1), source.inputs.Load(), "fenced input must not reach the extension")
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Equal(t, int64(2), run.manifest.EnvironmentRevision)
	assert.NotContains(t, service.uiSurfaces, key, "reload closes surfaces owned by replaced extensions")
}
