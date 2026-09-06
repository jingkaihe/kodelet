package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type remoteUIIgnoringSink struct{}

func (remoteUIIgnoringSink) Send(chat.ChatEvent) error { return nil }

func TestRemoteNativeUIUsesExistingRendererAndFencesCleanup(t *testing.T) {
	events := make(chan chat.ChatEvent, 32)
	acks := make(chan chat.UIPersistentAck, 16)
	inputs := make(chan chat.UIPersistentInput, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotEmpty(t, r.Header.Get(chat.ClientIDHeader))
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			assert.Contains(t, r.Header.Get(chat.UICapabilitiesHeader), "surfaces")
			assert.Contains(t, r.Header.Get(chat.UICapabilitiesHeader), "widgets")
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.(http.Flusher).Flush()
			for {
				select {
				case event := <-events:
					if err := json.NewEncoder(w).Encode(event); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case strings.HasSuffix(r.URL.Path, "/ack"):
			var ack chat.UIPersistentAck
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&ack))
			acks <- ack
		case strings.HasSuffix(r.URL.Path, "/input"):
			var input chat.UIPersistentInput
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&input))
			inputs <- input
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runner, err := chat.NewControlPlaneChatRunner(server.URL, "", "")
	require.NoError(t, err)
	ch := make(chan tea.Msg, 32)
	host := newTUIExtensionUIHost(ch, nil)
	ctx, cancel := context.WithCancel(extensions.ContextWithExtensionUIHost(t.Context(), host))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.StreamConversation(ctx, "conversation", remoteUIIgnoringSink{}) }()
	owner := runnerpayload.ExtensionOwner{ExtensionID: "drawing", Generation: 1}
	sequence := 0
	exchange := func(method, epoch, route string, request any) extensions.UIFrameResponse {
		t.Helper()
		data, err := json.Marshal(request)
		require.NoError(t, err)
		sequence++
		requestID := fmt.Sprint(sequence)
		events <- chat.ChatEvent{Kind: "ui-persistent", ConversationID: "conversation", UIPersistent: &chat.UIPersistentEvent{RequestID: requestID, Epoch: epoch, RouteID: route, Method: method, RunnerID: "runner", RunnerGeneration: 3, Owner: owner, Request: data}}
		select {
		case ack := <-acks:
			assert.Equal(t, requestID, ack.RequestID)
			return ack.Response
		case <-time.After(time.Second):
			require.FailNow(t, "native renderer did not acknowledge operation")
			return extensions.UIFrameResponse{}
		}
	}
	open := extensions.UISurfaceOpenRequest{ScopeID: "conversation", ID: "canvas", Frame: extensionTestFrame(1, "drawing")}
	require.True(t, exchange(protocol.MethodUISurfaceOpen, "first", "route-1", open).Accepted)
	host.mu.Lock()
	require.Len(t, host.surfaces, 1)
	var first tuiExtensionSurface
	for _, surface := range host.surfaces {
		first = surface
	}
	host.mu.Unlock()
	input := extensions.UISurfaceInputNotification{ScopeID: "conversation", ID: "canvas", Sequence: 1, Kind: extensions.UISurfaceInputKey, Key: "enter"}
	require.NoError(t, extensions.NotifyUISurfaceEvent(ctx, first.source, first.openOrdinal, extensions.UISurfaceInputMethod, input))
	assert.Equal(t, protocol.MethodUISurfaceInput, (<-inputs).Method)
	require.NoError(t, extensions.NotifyUISurfaceEvent(ctx, first.source, first.openOrdinal, extensions.UISurfaceResizeMethod, extensions.UISurfaceResizeNotification{ScopeID: "conversation", ID: "canvas", Sequence: 2, Width: 90, Height: 30}))
	resized := <-inputs
	assert.Equal(t, "route-1", resized.RouteID)
	assert.Equal(t, protocol.MethodUISurfaceResize, resized.Method)
	require.True(t, exchange(protocol.MethodUISurfaceFrame, "first", "route-1", extensions.UISurfaceFrameRequest{ScopeID: "conversation", ID: "canvas", Frame: extensionTestFrame(2, "updated")}).Accepted)
	require.True(t, exchange(protocol.MethodUITranscriptAppend, "first", "", extensions.UITranscriptAppendRequest{ScopeID: "conversation", Message: "saved drawing"}).Accepted)
	var transcript extensionUITranscriptMsg
	for len(ch) > 0 {
		if message, ok := (<-ch).(extensionUITranscriptMsg); ok {
			transcript = message
		}
	}
	assert.Equal(t, "saved drawing", transcript.message)
	// A delayed reset from a previous owner must not close a newly opened surface.
	require.True(t, exchange(protocol.MethodUISurfaceOpen, "second", "route-2", open).Accepted)
	events <- chat.ChatEvent{Kind: "ui-persistent-reset", ConversationID: "conversation", UIPersistent: &chat.UIPersistentEvent{Epoch: "first"}}
	require.True(t, exchange(protocol.MethodUISurfaceFrame, "second", "route-2", extensions.UISurfaceFrameRequest{ScopeID: "conversation", ID: "canvas", Frame: extensionTestFrame(2, "new owner")}).Accepted)
	host.mu.Lock()
	assert.Len(t, host.surfaces, 1)
	host.mu.Unlock()
	require.Error(t, extensions.NotifyUISurfaceEvent(ctx, first.source, first.openOrdinal, extensions.UISurfaceInputMethod, input))
	assert.False(t, exchange(protocol.MethodUISurfaceOpen, "first", "route-1", open).Accepted, "queued output from a revoked epoch cannot recreate UI")
	// Widget removal/recreation in one process generation must not reuse a closed native owner.
	for index, removed := range []bool{false, true, false} {
		events <- chat.ChatEvent{Kind: "ui-widget", ConversationID: "conversation", UIWidgetRevision: fmt.Sprintf("epoch:%d", index+1), UIWidget: &chat.UIWidgetEvent{Key: "widget", ID: "status", Generation: "3:1", Removed: removed, Frame: extensionTestFrame(uint64(index+1), "background")}}
	}
	require.True(t, exchange(protocol.MethodUISurfaceClose, "second", "route-2", extensions.UISurfaceCloseRequest{ScopeID: "conversation", ID: "canvas", Sequence: 3}).Accepted)
	host.mu.Lock()
	assert.Empty(t, host.surfaces)
	assert.Len(t, host.widgets, 1)
	host.mu.Unlock()
	// Normal stream completion cleans interactive sources, not retained background widgets.
	require.True(t, exchange(protocol.MethodUISurfaceOpen, "second", "route-3", open).Accepted)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "stream did not detach promptly")
	}
	host.mu.Lock()
	assert.Empty(t, host.surfaces)
	assert.Len(t, host.widgets, 1)
	host.mu.Unlock()
}
