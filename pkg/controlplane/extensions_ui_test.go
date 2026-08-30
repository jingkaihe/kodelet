package controlplane

import (
	"context"
	"fmt"
	"sync"
	"testing"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWebExtensionUISource struct {
	owner extensions.UIExtensionOwner
}

func (s testWebExtensionUISource) ExtensionUIOwner() extensions.UIExtensionOwner {
	return s.owner
}

func (testWebExtensionUISource) NotifyExtensionUI(context.Context, string, any) error {
	return nil
}

func TestWebExtensionUIHostStoresAndEmitsConversationWidgets(t *testing.T) {
	var mu sync.Mutex
	events := make([]chat.ChatEvent, 0)
	host := newWebExtensionUIHost(func(_ string, event chat.ChatEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})
	capabilities := host.ExtensionUIHostCapabilities(t.Context())
	assert.True(t, capabilities.Widgets)
	assert.False(t, capabilities.Surfaces)

	source := testWebExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 1}}
	ctx := extensions.ContextWithExtensionUIScope(t.Context(), "conversation-1")
	response, err := host.SetWidget(ctx, source, extensions.UIWidgetSetRequest{
		ID:        "background-agents",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame: extensions.UIFrame{
			Sequence: 1,
			Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "1 active"}}}},
		},
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)

	snapshotRevision, snapshot := host.Snapshot("conversation-1")
	require.Len(t, snapshot, 1)
	assert.Regexp(t, `^\d+:1$`, snapshotRevision)
	assert.Equal(t, "subagent", snapshot[0].ExtensionID)
	assert.Equal(t, "0:1", snapshot[0].Generation)
	assert.Equal(t, uint64(1), snapshot[0].Frame.Sequence)

	response, err = host.UpdateWidget(ctx, source, extensions.UIWidgetFrameRequest{
		ID: "background-agents",
		Frame: extensions.UIFrame{
			Sequence: 2,
			Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: "1 completed"}}}},
		},
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	updatedRevision, updatedSnapshot := host.Snapshot("conversation-1")
	require.Len(t, updatedSnapshot, 1)
	assert.Regexp(t, `^\d+:2$`, updatedRevision)
	assert.Equal(t, uint64(2), updatedSnapshot[0].Frame.Sequence)

	response, err = host.RemoveWidget(ctx, source, extensions.UIWidgetRemoveRequest{
		ID:       "background-agents",
		Sequence: 3,
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	removedRevision, removedSnapshot := host.Snapshot("conversation-1")
	assert.Regexp(t, `^\d+:3$`, removedRevision)
	assert.Empty(t, removedSnapshot)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 3)
	assert.Equal(t, []bool{false, false, true}, []bool{
		events[0].UIWidget.Removed,
		events[1].UIWidget.Removed,
		events[2].UIWidget.Removed,
	})
	assert.Equal(t, snapshotRevision, events[0].UIWidgetRevision)
	assert.Equal(t, updatedRevision, events[1].UIWidgetRevision)
	assert.Equal(t, removedRevision, events[2].UIWidgetRevision)
}

func TestServerExtensionUIEventsReachActiveChatStream(t *testing.T) {
	primary := &recordingChatSink{}
	server := &Server{
		activeChats: map[string]*activeChatRun{
			"conversation-1": {eventSink: primary},
		},
		chatSubscribers: make(map[string]map[*subscriberEventSink]struct{}),
	}
	host := newWebExtensionUIHost(server.emitExtensionUIEvent)
	source := testWebExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 1}}
	_, err := host.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{
		ScopeID:   "conversation-1",
		ID:        "background-agents",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensions.UIFrame{Sequence: 1},
	})
	require.NoError(t, err)

	events := primary.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "ui-widget", events[0].Kind)
	require.NotNil(t, events[0].UIWidget)
	assert.Equal(t, "0:1", events[0].UIWidget.Generation)
	assert.Regexp(t, `^\d+:1$`, events[0].UIWidgetRevision)
}

func TestWebExtensionUIHostBoundsAndNormalizesFrames(t *testing.T) {
	host := newWebExtensionUIHost(nil)
	source := testWebExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "subagent", Generation: 1}}
	response, err := host.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{
		ScopeID:   "conversation-1",
		ID:        "empty",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensions.UIFrame{Sequence: 1},
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	_, snapshot := host.Snapshot("conversation-1")
	require.Len(t, snapshot, 1)
	assert.NotNil(t, snapshot[0].Frame.Lines)

	_, err = host.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{
		ScopeID:   "conversation-1",
		ID:        "too-many-lines",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame: extensions.UIFrame{
			Sequence: 1,
			Lines:    make([]extensions.UIFrameLine, maxWebUIWidgetLines+1),
		},
	})
	require.ErrorContains(t, err, "exceeds 64 lines")

	for index := 0; index < maxWebUIWidgetsPerScope-1; index++ {
		response, err = host.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{
			ScopeID:   "conversation-1",
			ID:        fmt.Sprintf("widget-%d", index),
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame:     extensions.UIFrame{Sequence: 1},
		})
		require.NoError(t, err)
		assert.True(t, response.Accepted)
	}
	response, err = host.SetWidget(t.Context(), source, extensions.UIWidgetSetRequest{
		ScopeID:   "conversation-1",
		ID:        "one-too-many",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensions.UIFrame{Sequence: 1},
	})
	require.NoError(t, err)
	assert.False(t, response.Accepted)
	assert.Contains(t, response.Reason, "at most 32")
}
