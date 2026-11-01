package extension_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
	"github.com/jingkaihe/kodelet/sdk/go/extension/extensiontest"
	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type uiAction struct {
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
}

type uiFixture struct {
	h       *extensiontest.Harness
	surface chan *extension.Surface
}

// newUIFixture serves a "ui" tool that performs one persistent UI action per call.
func newUIFixture(t *testing.T, ui map[string]any, hostReplies map[string]any) *uiFixture {
	t.Helper()
	fixture := &uiFixture{surface: make(chan *extension.Surface, 4)}
	ext := extension.New(extension.Metadata{})
	extension.RegisterTool(ext, extension.Tool[uiAction]{Name: "ui", Execute: func(ctx context.Context, in uiAction, tc *extension.ToolContext) (extension.ToolResult, error) {
		switch in.Action {
		case "widget":
			if err := tc.UI.SetWidget(ctx, "status", []extension.FrameLine{extension.Line("a"), extension.Styled(extension.StyledSpan{Text: "b", Style: &extension.Style{Bold: true}})}, ""); err != nil {
				return extension.ToolResult{}, err
			}
			if err := tc.UI.SetWidget(ctx, "status", nil, extension.WidgetBelowComposer); err != nil {
				return extension.ToolResult{}, err
			}
			return extension.Text("ok"), tc.UI.RemoveWidget(ctx, "status")
		case "transcript":
			return extension.Text("ok"), tc.UI.AppendTranscript(ctx, extension.TranscriptRequest{Message: "note"})
		case "surface":
			surface, err := tc.UI.OpenSurface(ctx, extension.SurfaceOptions{
				ID: in.ID, InitialLines: []extension.FrameLine{extension.Line("loading")},
				Width: extension.Percent(50), Height: extension.Cells(10), Anchor: extension.AnchorCenter, NonCapturing: true,
			})
			if err != nil {
				return extension.ToolResult{}, err
			}
			fixture.surface <- surface
			return extension.Text("opened"), nil
		case "badid":
			return extension.ToolResult{}, tc.UI.SetWidget(ctx, " padded ", nil, "")
		}
		return extension.ToolResult{}, errors.New("unknown action")
	}})
	fixture.h = newHarness(t, ext, &extensiontest.Options{Host: replies(hostReplies)})
	initWith(t, fixture.h, func(params *extension.InitializeParams) {
		if ui != nil {
			params.Capabilities["ui"] = ui
		}
	})
	return fixture
}

func (f *uiFixture) run(t *testing.T, action, id, scope string) (extension.ToolResult, error) {
	t.Helper()
	return f.h.ExecuteTool(t.Context(), "ui", map[string]any{"action": action, "id": id}, extensiontest.WithCall(extension.CallInfo{UIScopeID: scope}))
}

func (f *uiFixture) openSurface(t *testing.T, id, scope string) *extension.Surface {
	t.Helper()
	_, err := f.run(t, "surface", id, scope)
	require.NoError(t, err)
	return <-f.surface
}

// acceptOnce rejects the first request and accepts the rest.
func acceptOnce(rejection map[string]any) reply {
	var rejected atomic.Bool
	return func(extensiontest.HostCall) (any, error) {
		if !rejected.Swap(true) {
			return rejection, nil
		}
		return map[string]any{"accepted": true}, nil
	}
}

func TestPersistentUIIsCapabilityGated(t *testing.T) {
	fixture := newUIFixture(t, nil, map[string]any{
		"kodelet.ui.widget.set": map[string]any{}, "kodelet.ui.widget.remove": map[string]any{},
		"kodelet.ui.transcript.append": map[string]any{},
	})
	_, err := fixture.run(t, "widget", "", "scope")
	require.NoError(t, err)
	_, err = fixture.run(t, "transcript", "", "scope")
	require.NoError(t, err)
	_, err = fixture.run(t, "surface", "panel", "scope")
	require.ErrorContains(t, err, "interactive extension surfaces are not available")
	assert.Empty(t, methods(fixture.h), "unsupported UI is never sent to the host")

	require.NoError(t, fixture.h.Notify("kodelet.ui.capabilities", map[string]any{"widgets": true, "transcript": true}))
	_, err = fixture.run(t, "transcript", "", "scope")
	require.NoError(t, err)
	assert.JSONEq(t, `{"message":"note","scopeId":"scope"}`, string(firstCall(t, fixture.h, "kodelet.ui.transcript.append").Params))
	_, err = fixture.run(t, "badid", "", "scope")
	require.ErrorContains(t, err, "leading or trailing whitespace")
}

func TestWidgetsUseSequencesPlacementAndScope(t *testing.T) {
	fixture := newUIFixture(t, map[string]any{"widgets": true}, map[string]any{
		"kodelet.ui.widget.set": map[string]any{}, "kodelet.ui.widget.remove": map[string]any{},
	})
	_, err := fixture.run(t, "widget", "", "scope-1")
	require.NoError(t, err)
	calls := fixture.h.HostCalls()
	require.Len(t, calls, 3)
	assert.JSONEq(t, `{"id":"status","placement":"aboveComposer","scopeId":"scope-1","frame":{"sequence":1,"lines":["a",{"spans":[{"text":"b","style":{"bold":true}}]}]}}`, string(calls[0].Params))
	assert.JSONEq(t, `{"id":"status","placement":"belowComposer","scopeId":"scope-1","frame":{"sequence":2,"lines":[]}}`, string(calls[1].Params))
	assert.JSONEq(t, `{"id":"status","sequence":3,"scopeId":"scope-1"}`, string(calls[2].Params))
}

func TestSurfacesRouteScopedEventsAndCloseOnce(t *testing.T) {
	fixture := newUIFixture(t, map[string]any{"surfaces": true}, map[string]any{
		"kodelet.ui.surface.open":  map[string]any{"accepted": true},
		"kodelet.ui.surface.close": map[string]any{"accepted": true},
	})
	surface := fixture.openSurface(t, "panel", "scope-1")
	assert.JSONEq(t, `{"id":"panel","scopeId":"scope-1","options":{"width":"50%","height":10,"anchor":"center","nonCapturing":true},"frame":{"sequence":1,"lines":["loading"]}}`,
		string(firstCall(t, fixture.h, "kodelet.ui.surface.open").Params))

	inputs := make(chan extension.SurfaceInputEvent, 4)
	surface.OnInput(func(event extension.SurfaceInputEvent) { inputs <- event })
	resizes := make(chan extension.SurfaceResizeEvent, 4)
	surface.OnResize(func(event extension.SurfaceResizeEvent) { resizes <- event })
	var closed atomic.Int32
	surface.OnClose(func() { closed.Add(1) })

	for _, event := range []map[string]any{
		{"id": "panel", "scopeId": "other", "sequence": 9, "kind": "key", "key": "x"},
		{"id": "panel", "scopeId": "scope-1", "sequence": 5, "kind": "key", "key": "a"},
		{"id": "panel", "scopeId": "scope-1", "sequence": 4, "kind": "key", "key": "stale"},
	} {
		require.NoError(t, fixture.h.Notify("extension.ui.surface.input", event))
	}
	require.NoError(t, fixture.h.Notify("extension.ui.surface.resize", map[string]any{"id": "panel", "scopeId": "scope-1", "sequence": 6, "width": 80, "height": 20}))
	assert.Equal(t, extension.SurfaceInputEvent{Sequence: 5, Kind: "key", Key: "a"}, <-inputs)
	assert.Equal(t, extension.SurfaceResizeEvent{Sequence: 6, Width: 80, Height: 20}, <-resizes)
	select {
	case event := <-inputs:
		t.Fatalf("unexpected input %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
	size, ok := surface.Size()
	require.True(t, ok)
	assert.Equal(t, extension.SurfaceSize{Width: 80, Height: 20}, size)

	surface.Update([]extension.FrameLine{extension.Line("ready")})
	require.Eventually(t, func() bool { return len(fixture.h.Notifications()) == 1 }, time.Second, time.Millisecond)
	frame := fixture.h.Notifications()[0]
	assert.Equal(t, "kodelet.ui.surface.frame", frame.Method)
	assert.JSONEq(t, `{"id":"panel","scopeId":"scope-1","frame":{"sequence":2,"lines":["ready"]}}`, string(frame.Params))

	require.NoError(t, surface.Close(t.Context()))
	require.NoError(t, surface.Close(t.Context()))
	assert.JSONEq(t, `{"id":"panel","scopeId":"scope-1","sequence":3}`, string(firstCall(t, fixture.h, "kodelet.ui.surface.close").Params))
	assert.Equal(t, int32(1), closed.Load())
	surface.OnClose(func() { closed.Add(1) })
	assert.Equal(t, int32(2), closed.Load(), "OnClose after close runs immediately")
}

func TestSurfaceIDsAreScopedAndExclusiveUntilClosed(t *testing.T) {
	fixture := newUIFixture(t, map[string]any{"surfaces": true}, map[string]any{"kodelet.ui.surface.open": map[string]any{"accepted": true}})
	first := fixture.openSurface(t, "panel", "scope-1")
	second := fixture.openSurface(t, "panel", "scope-2")
	_, err := fixture.run(t, "surface", "panel", "scope-1")
	require.ErrorContains(t, err, "already open")

	var closed sync.WaitGroup
	closed.Add(1)
	first.OnClose(closed.Done)
	require.NoError(t, fixture.h.Notify("extension.ui.surface.closed", map[string]any{"id": "panel", "scopeId": "scope-1", "openSequence": 99}))
	require.NoError(t, fixture.h.Notify("extension.ui.surface.closed", map[string]any{"id": "panel", "scopeId": "scope-1", "openSequence": 1}))
	closed.Wait()
	reopened := fixture.openSurface(t, "panel", "scope-1")
	assert.NotSame(t, first, reopened)
	_, err = fixture.run(t, "surface", "panel", "scope-2")
	require.ErrorContains(t, err, "already open", "the other scope is untouched")
	assert.Equal(t, "panel", second.ID())
}

func TestRejectedSurfaceOperationsCanBeRetried(t *testing.T) {
	t.Run("open releases the ID", func(t *testing.T) {
		fixture := newUIFixture(t, map[string]any{"surfaces": true}, map[string]any{
			"kodelet.ui.surface.open": acceptOnce(map[string]any{"accepted": false}),
		})
		_, err := fixture.run(t, "surface", "panel", "")
		require.ErrorContains(t, err, "the host rejected the interactive surface")
		fixture.openSurface(t, "panel", "")
	})
	t.Run("close keeps ownership", func(t *testing.T) {
		fixture := newUIFixture(t, map[string]any{"surfaces": true}, map[string]any{
			"kodelet.ui.surface.open":  map[string]any{"accepted": true},
			"kodelet.ui.surface.close": acceptOnce(map[string]any{"accepted": false, "reason": "busy"}),
		})
		surface := fixture.openSurface(t, "dialog", "")
		require.EqualError(t, surface.Close(t.Context()), "busy")
		_, err := fixture.run(t, "surface", "dialog", "")
		require.ErrorContains(t, err, "already open")
		require.NoError(t, surface.Close(t.Context()))
		fixture.openSurface(t, "dialog", "")
	})
}

func TestSurfaceRetainsEventsUntilListenersAttach(t *testing.T) {
	var fixture *uiFixture
	fixture = newUIFixture(t, map[string]any{"surfaces": true}, map[string]any{
		"kodelet.ui.surface.open": func(call extensiontest.HostCall) (any, error) {
			var params struct {
				ScopeID string `json:"scopeId"`
			}
			_ = json.Unmarshal(call.Params, &params)
			_ = fixture.h.Notify("extension.ui.surface.input", map[string]any{"id": "panel", "scopeId": params.ScopeID, "sequence": 1, "kind": "focus"})
			_ = fixture.h.Notify("extension.ui.surface.resize", map[string]any{"id": "panel", "scopeId": params.ScopeID, "sequence": 2, "width": 40, "height": 10})
			return map[string]any{"accepted": true}, nil
		},
	})
	surface := fixture.openSurface(t, "panel", "s")
	var focus extension.SurfaceInputEvent
	surface.OnInput(func(event extension.SurfaceInputEvent) { focus = event })
	var resize extension.SurfaceResizeEvent
	surface.OnResize(func(event extension.SurfaceResizeEvent) { resize = event })
	assert.Equal(t, extension.SurfaceInputEvent{Sequence: 1, Kind: "focus"}, focus)
	assert.Equal(t, extension.SurfaceResizeEvent{Sequence: 2, Width: 40, Height: 10}, resize)
}

func TestSurfaceFramesCoalesceWhileTheHostIsBlocked(t *testing.T) {
	ext := extension.New(extension.Metadata{})
	surfaces := make(chan *extension.Surface, 1)
	addTool(ext, "open", func(ctx context.Context, tc *extension.ToolContext) (extension.ToolResult, error) {
		surface, err := tc.UI.OpenSurface(ctx, extension.SurfaceOptions{ID: "panel"})
		surfaces <- surface
		return extension.ToolResult{}, err
	})
	gate := make(chan struct{})
	var mu sync.Mutex
	var frames []string
	w := runtimeWire(t, ext, extension.RuntimeOptions{}, func(w *wire, message jsonrpc.Message) bool {
		switch message.Method {
		case "kodelet.ui.surface.open":
			go w.send(`{"jsonrpc":"2.0","id":` + string(message.ID) + `,"result":{"accepted":true}}`)
			return true
		case "kodelet.ui.surface.frame":
			var params struct {
				Frame struct {
					Lines []string `json:"lines"`
				} `json:"frame"`
			}
			_ = json.Unmarshal(message.Params, &params)
			mu.Lock()
			frames = append(frames, strings.Join(params.Frame.Lines, ","))
			mu.Unlock()
			<-gate // the host stops reading
			return true
		}
		return false
	})
	w.send(`{"jsonrpc":"2.0","id":1,"method":"extension.initialize","params":{"extension":{"id":"x"},"capabilities":{"ui":{"surfaces":true}}}}`)
	w.next()
	w.send(`{"jsonrpc":"2.0","id":2,"method":"extension.tool.execute","params":{"name":"open","input":{}}}`)
	surface := receive(t, surfaces)
	require.NotNil(t, surface)
	w.next()

	for i := range 200 {
		surface.Update([]extension.FrameLine{extension.Line(strconv.Itoa(i))})
		time.Sleep(100 * time.Microsecond)
	}
	close(gate)
	frameCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(frames)
	}
	require.Eventually(t, func() bool { return frameCount() == 2 }, time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"0", "199"}, frames, "only the frame in flight and the latest update are sent")
}
