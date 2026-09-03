package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type extensionUINotification struct {
	method string
	params any
}

type fakeExtensionUISource struct {
	owner extensions.UIExtensionOwner
	err   error

	mu            sync.Mutex
	notifications []extensionUINotification
	lifecycleCh   chan string
}

func (s *fakeExtensionUISource) ExtensionUIOwner() extensions.UIExtensionOwner {
	return s.owner
}

func (s *fakeExtensionUISource) NotifyExtensionUI(_ context.Context, method string, params any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications = append(s.notifications, extensionUINotification{method: method, params: params})
	return s.err
}

func (s *fakeExtensionUISource) PrepareUISurfaceEventLifecycle(_ string, id string, _ uint64) {
	s.mu.Lock()
	lifecycleCh := s.lifecycleCh
	s.mu.Unlock()
	if lifecycleCh != nil {
		lifecycleCh <- id
	}
}

func (s *fakeExtensionUISource) recordedNotifications() []extensionUINotification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]extensionUINotification(nil), s.notifications...)
}

func TestTUIExtensionUIHostPreparesSurfaceEventsBeforePublishingOpen(t *testing.T) {
	ch := make(chan tea.Msg)
	lifecycleCh := make(chan string)
	host := newTUIExtensionUIHost(ch, nil)
	source := &fakeExtensionUISource{
		owner:       extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1},
		lifecycleCh: lifecycleCh,
	}
	type openResult struct {
		response extensions.UIFrameResponse
		err      error
	}
	resultCh := make(chan openResult, 1)
	go func() {
		response, err := host.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
			ID:    "surface",
			Frame: extensionTestFrame(1, "frame"),
		})
		resultCh <- openResult{response: response, err: err}
	}()

	select {
	case id := <-lifecycleCh:
		assert.Equal(t, "surface", id)
	case <-time.After(time.Second):
		t.Fatal("surface event lifecycle was not prepared before publication")
	}
	select {
	case message := <-ch:
		_, ok := message.(extensionUIFlushMsg)
		assert.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("surface open was not published")
	}
	select {
	case result := <-resultCh:
		require.NoError(t, result.err)
		assert.True(t, result.response.Accepted)
	case <-time.After(time.Second):
		t.Fatal("surface open did not complete")
	}
}

func TestTUIExtensionUIHostAppendsSanitizedTranscriptEntry(t *testing.T) {
	ch := make(chan tea.Msg, 1)
	host := newTUIExtensionUIHost(ch, nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "drawing", Generation: 1}}

	response, err := host.AppendTranscript(context.Background(), source, extensions.UITranscriptAppendRequest{
		ScopeID: "conversation-drawing",
		Title:   "Saved\x1b[31m",
		Message: "./drawing.png\ncopy\tthis",
	})

	require.NoError(t, err)
	assert.True(t, response.Accepted)
	msg, ok := (<-ch).(extensionUITranscriptMsg)
	require.True(t, ok)
	assert.Equal(t, source.owner, msg.owner)
	assert.Equal(t, "conversation-drawing", msg.conversationKey)
	assert.Equal(t, "Saved", msg.title)
	assert.Equal(t, "./drawing.png\ncopy this", msg.message)
}

func TestTUIExtensionUIHostCoalescesLatestWidgetAndSurfaceFrames(t *testing.T) {
	ch := make(chan tea.Msg, 8)
	host := newTUIExtensionUIHost(ch, nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1}}

	response, err := host.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
		ID:        "status",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensionTestFrame(1, "first"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	response, err = host.UpdateWidget(context.Background(), source, extensions.UIWidgetFrameRequest{
		ID:    "status",
		Frame: extensionTestFrame(3, "latest"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	response, err = host.UpdateWidget(context.Background(), source, extensions.UIWidgetFrameRequest{
		ID:    "status",
		Frame: extensionTestFrame(2, "stale"),
	})
	require.NoError(t, err)
	assert.False(t, response.Accepted)
	assert.Equal(t, uint64(3), response.LatestSequence)

	_, err = host.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
		ID:    "doom",
		Frame: extensionTestFrame(1, "loading"),
	})
	require.NoError(t, err)
	_, err = host.UpdateSurface(context.Background(), source, extensions.UISurfaceFrameRequest{
		ID:    "doom",
		Frame: extensionTestFrame(2, "frame 2"),
	})
	require.NoError(t, err)
	_, err = host.UpdateSurface(context.Background(), source, extensions.UISurfaceFrameRequest{
		ID:    "doom",
		Frame: extensionTestFrame(4, "frame 4"),
	})
	require.NoError(t, err)

	assert.Len(t, ch, 1, "one queued Bubble Tea flush should cover every pending frame")
	msg := <-ch
	_, ok := msg.(extensionUIFlushMsg)
	assert.True(t, ok)
	batch := host.drain()
	require.Len(t, batch.widgets, 1)
	assert.Equal(t, uint64(3), batch.widgets[0].widget.frame.Sequence)
	assert.Equal(t, "latest", batch.widgets[0].widget.frame.Lines[0].Spans[0].Text)
	require.Len(t, batch.surfaces, 1)
	assert.True(t, batch.surfaces[0].opened)
	assert.Equal(t, uint64(4), batch.surfaces[0].surface.frame.Sequence)
	assert.Equal(t, "frame 4", batch.surfaces[0].surface.frame.Lines[0].Spans[0].Text)
}

func TestTUIExtensionUIHostScopesSameIDWidgetsByConversation(t *testing.T) {
	ch := make(chan tea.Msg, 4)
	host := newTUIExtensionUIHost(ch, nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "todo", Generation: 1}}
	firstCtx := contextWithTUIConversation(context.Background(), "conversation-first")
	secondCtx := contextWithTUIConversation(context.Background(), "conversation-second")

	for _, request := range []struct {
		ctx     context.Context
		scopeID string
		text    string
	}{
		{ctx: firstCtx, scopeID: "conversation-first", text: "first todos"},
		{ctx: secondCtx, scopeID: "conversation-second", text: "second todos"},
	} {
		response, err := host.SetWidget(request.ctx, source, extensions.UIWidgetSetRequest{
			ScopeID:   request.scopeID,
			ID:        "todo-progress",
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame:     extensionTestFrame(1, request.text),
		})
		require.NoError(t, err)
		assert.True(t, response.Accepted)
	}

	<-ch
	batch := host.drain()
	require.Len(t, batch.widgets, 2)
	assert.Equal(t, "conversation-first", batch.widgets[0].widget.key.conversationKey)
	assert.Equal(t, "conversation-second", batch.widgets[1].widget.key.conversationKey)
	assert.Equal(t, uint64(1), host.widgetSeq[extensionUIKey{owner: source.owner, conversationKey: "conversation-first", id: "todo-progress"}])
	assert.Equal(t, uint64(1), host.widgetSeq[extensionUIKey{owner: source.owner, conversationKey: "conversation-second", id: "todo-progress"}])

	response, err := host.UpdateWidget(firstCtx, source, extensions.UIWidgetFrameRequest{
		ScopeID: "conversation-first",
		ID:      "todo-progress",
		Frame:   extensionTestFrame(2, "updated first todos"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	assert.Equal(t, "second todos", host.widgets[extensionUIKey{owner: source.owner, conversationKey: "conversation-second", id: "todo-progress"}].frame.Lines[0].Spans[0].Text)

	_, err = host.UpdateWidget(extensions.ContextWithExtensionUIImplicitScope(context.Background()), source, extensions.UIWidgetFrameRequest{
		ID:    "todo-progress",
		Frame: extensionTestFrame(3, "ambiguous"),
	})
	require.ErrorContains(t, err, "scope is ambiguous")
}

func TestTUIExtensionUIExplicitScopeOverridesRequestContext(t *testing.T) {
	ch := make(chan tea.Msg, 2)
	host := newTUIExtensionUIHost(ch, nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "scoped", Generation: 1}}
	wrongCtx := contextWithTUIConversation(context.Background(), "conversation-wrong")

	response, err := host.SetWidget(wrongCtx, source, extensions.UIWidgetSetRequest{
		ScopeID:   "conversation-right",
		ID:        "status",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensionTestFrame(1, "right"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	transcript, err := host.AppendTranscript(wrongCtx, source, extensions.UITranscriptAppendRequest{
		ScopeID: "conversation-right",
		Message: "scoped transcript",
	})
	require.NoError(t, err)
	assert.True(t, transcript.Accepted)

	assert.Contains(t, host.widgets, extensionUIKey{owner: source.owner, conversationKey: "conversation-right", id: "status"})
	assert.NotContains(t, host.widgets, extensionUIKey{owner: source.owner, conversationKey: "conversation-wrong", id: "status"})
	var transcriptMsg extensionUITranscriptMsg
	for range 2 {
		switch msg := (<-ch).(type) {
		case extensionUITranscriptMsg:
			transcriptMsg = msg
		}
	}
	assert.Equal(t, "conversation-right", transcriptMsg.conversationKey)
}

func TestTUIExtensionUIExplicitEmptyScopeOverridesRequestContext(t *testing.T) {
	host := newTUIExtensionUIHost(make(chan tea.Msg, 2), nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "global", Generation: 1}}
	conversationCtx := contextWithTUIConversation(context.Background(), "conversation-one")

	response, err := host.SetWidget(conversationCtx, source, extensions.UIWidgetSetRequest{
		ScopeID: "",
		ID:      "status", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: extensionTestFrame(1, "global"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)

	legacyCtx := extensions.ContextWithExtensionUIImplicitScope(conversationCtx)
	response, err = host.SetWidget(legacyCtx, source, extensions.UIWidgetSetRequest{
		ID: "legacy-status", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: extensionTestFrame(1, "scoped"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)

	assert.Contains(t, host.widgets, extensionUIKey{owner: source.owner, id: "status"})
	assert.Contains(t, host.widgets, extensionUIKey{owner: source.owner, conversationKey: "conversation-one", id: "legacy-status"})
}

func TestTUIExtensionUIFallbackIgnoresClosedSequenceTombstones(t *testing.T) {
	host := newTUIExtensionUIHost(make(chan tea.Msg, 4), nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "legacy", Generation: 1}}
	scopedCtx := contextWithTUIConversation(context.Background(), "conversation-old")

	_, err := host.SetWidget(scopedCtx, source, extensions.UIWidgetSetRequest{
		ScopeID: "conversation-old", ID: "status", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: extensionTestFrame(1, "old"),
	})
	require.NoError(t, err)
	_, err = host.RemoveWidget(scopedCtx, source, extensions.UIWidgetRemoveRequest{ScopeID: "conversation-old", ID: "status", Sequence: 2})
	require.NoError(t, err)
	response, err := host.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
		ID: "status", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: extensionTestFrame(1, "global"),
	})
	require.NoError(t, err)
	assert.True(t, response.Accepted)
	assert.Contains(t, host.widgets, extensionUIKey{owner: source.owner, id: "status"})
	assert.NotContains(t, host.widgets, extensionUIKey{owner: source.owner, conversationKey: "conversation-old", id: "status"})
}

func TestTUIExtensionUIHostPersistentOperationsResolveEstablishedConversationScope(t *testing.T) {
	ch := make(chan tea.Msg, 1)
	host := newTUIExtensionUIHost(ch, nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "persistent", Generation: 1}}
	conversationCtx := extensions.ContextWithExtensionUIImplicitScope(contextWithTUIConversation(context.Background(), "conversation-first"))

	widgetResponse, err := host.SetWidget(conversationCtx, source, extensions.UIWidgetSetRequest{
		ID:        "status",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensionTestFrame(1, "starting"),
	})
	require.NoError(t, err)
	assert.True(t, widgetResponse.Accepted)
	surfaceResponse, err := host.OpenSurface(conversationCtx, source, extensions.UISurfaceOpenRequest{
		ID:    "game",
		Frame: extensionTestFrame(1, "opening"),
	})
	require.NoError(t, err)
	assert.True(t, surfaceResponse.Accepted)
	legacyCtx := extensions.ContextWithExtensionUIImplicitScope(context.Background())

	widgetResponse, err = host.UpdateWidget(legacyCtx, source, extensions.UIWidgetFrameRequest{
		ID:    "status",
		Frame: extensionTestFrame(2, "running"),
	})
	require.NoError(t, err)
	assert.True(t, widgetResponse.Accepted)
	surfaceResponse, err = host.UpdateSurface(legacyCtx, source, extensions.UISurfaceFrameRequest{
		ID:    "game",
		Frame: extensionTestFrame(2, "ready"),
	})
	require.NoError(t, err)
	assert.True(t, surfaceResponse.Accepted)

	widgetResponse, err = host.RemoveWidget(legacyCtx, source, extensions.UIWidgetRemoveRequest{ID: "status", Sequence: 3})
	require.NoError(t, err)
	assert.True(t, widgetResponse.Accepted)
	surfaceResponse, err = host.CloseSurface(legacyCtx, source, extensions.UISurfaceCloseRequest{ID: "game", Sequence: 3})
	require.NoError(t, err)
	assert.True(t, surfaceResponse.Accepted)

	<-ch
	batch := host.drain()
	require.Len(t, batch.widgets, 1)
	assert.True(t, batch.widgets[0].remove)
	assert.Equal(t, "conversation-first", batch.widgets[0].widget.key.conversationKey)
	require.Len(t, batch.surfaces, 1)
	assert.True(t, batch.surfaces[0].remove)
	assert.Equal(t, "conversation-first", batch.surfaces[0].surface.key.conversationKey)
	assert.Empty(t, host.widgets)
	assert.Empty(t, host.surfaces)
}

func TestTUIExtensionUIHostRejectsAmbiguousContextFreeSurfaceOperations(t *testing.T) {
	host := newTUIExtensionUIHost(make(chan tea.Msg, 1), nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "surfaces", Generation: 1}}

	for _, conversationKey := range []string{"conversation-first", "conversation-second"} {
		response, err := host.OpenSurface(
			contextWithTUIConversation(context.Background(), conversationKey),
			source,
			extensions.UISurfaceOpenRequest{ScopeID: conversationKey, ID: "game", Frame: extensionTestFrame(1, conversationKey)},
		)
		require.NoError(t, err)
		assert.True(t, response.Accepted)
	}

	legacyCtx := extensions.ContextWithExtensionUIImplicitScope(context.Background())
	_, err := host.UpdateSurface(legacyCtx, source, extensions.UISurfaceFrameRequest{
		ID:    "game",
		Frame: extensionTestFrame(2, "ambiguous update"),
	})
	require.ErrorContains(t, err, "scope is ambiguous")
	_, err = host.CloseSurface(legacyCtx, source, extensions.UISurfaceCloseRequest{ID: "game", Sequence: 2})
	require.ErrorContains(t, err, "scope is ambiguous")
}

func TestTUIExtensionUIHostGlobalAndScopedObjectsWithSameIDCoexist(t *testing.T) {
	host := newTUIExtensionUIHost(make(chan tea.Msg, 4), nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "coexist", Generation: 1}}
	scopedCtx := contextWithTUIConversation(context.Background(), "conversation-one")

	widgetResponse, err := host.SetWidget(scopedCtx, source, extensions.UIWidgetSetRequest{
		ScopeID: "conversation-one", ID: "status", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: extensionTestFrame(1, "scoped"),
	})
	require.NoError(t, err)
	assert.True(t, widgetResponse.Accepted)
	widgetResponse, err = host.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
		ID: "status", Placement: extensions.UIWidgetPlacementAboveComposer, Frame: extensionTestFrame(1, "global"),
	})
	require.NoError(t, err)
	assert.True(t, widgetResponse.Accepted)

	surfaceResponse, err := host.OpenSurface(scopedCtx, source, extensions.UISurfaceOpenRequest{
		ScopeID: "conversation-one", ID: "panel", Frame: extensionTestFrame(1, "scoped"),
	})
	require.NoError(t, err)
	assert.True(t, surfaceResponse.Accepted)
	surfaceResponse, err = host.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
		ID: "panel", Frame: extensionTestFrame(1, "global"),
	})
	require.NoError(t, err)
	assert.True(t, surfaceResponse.Accepted)

	assert.Contains(t, host.widgets, extensionUIKey{owner: source.owner, conversationKey: "conversation-one", id: "status"})
	assert.Contains(t, host.widgets, extensionUIKey{owner: source.owner, id: "status"})
	assert.Contains(t, host.surfaces, extensionUIKey{owner: source.owner, conversationKey: "conversation-one", id: "panel"})
	assert.Contains(t, host.surfaces, extensionUIKey{owner: source.owner, id: "panel"})

	legacyCtx := extensions.ContextWithExtensionUIImplicitScope(context.Background())
	_, err = host.UpdateWidget(legacyCtx, source, extensions.UIWidgetFrameRequest{
		ID: "status", Frame: extensionTestFrame(2, "ambiguous"),
	})
	require.ErrorContains(t, err, "scope is ambiguous")
	_, err = host.CloseSurface(legacyCtx, source, extensions.UISurfaceCloseRequest{ID: "panel", Sequence: 2})
	require.ErrorContains(t, err, "scope is ambiguous")
}

func TestTUIExtensionUIHostSynchronizesClosedOwnerSequenceReads(t *testing.T) {
	const attempts = 2000
	tests := []struct {
		name    string
		request func(*tuiExtensionUIHost, *fakeExtensionUISource, uint64) (extensions.UIFrameResponse, error)
	}{
		{
			name: "widget",
			request: func(host *tuiExtensionUIHost, source *fakeExtensionUISource, sequence uint64) (extensions.UIFrameResponse, error) {
				return host.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
					ID:        "object",
					Placement: extensions.UIWidgetPlacementAboveComposer,
					Frame:     extensionTestFrame(sequence, "frame"),
				})
			},
		},
		{
			name: "surface",
			request: func(host *tuiExtensionUIHost, source *fakeExtensionUISource, sequence uint64) (extensions.UIFrameResponse, error) {
				return host.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
					ID:    "object",
					Frame: extensionTestFrame(sequence, "frame"),
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newTUIExtensionUIHost(make(chan tea.Msg, 1), nil)
			closedSource := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "closed", Generation: 1}}
			liveSource := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "live", Generation: 1}}
			response, err := test.request(host, closedSource, 1)
			require.NoError(t, err)
			require.True(t, response.Accepted)
			host.CleanupExtensionUI(closedSource.owner)

			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				for range attempts {
					response, err := test.request(host, closedSource, 2)
					if err != nil {
						errs <- err
						return
					}
					if response.LatestSequence != 1 {
						errs <- errors.Errorf("closed response latest sequence = %d, want 1", response.LatestSequence)
						return
					}
				}
			}()
			go func() {
				defer wg.Done()
				<-start
				for sequence := uint64(1); sequence <= attempts; sequence++ {
					if _, err := test.request(host, liveSource, sequence); err != nil {
						errs <- err
						return
					}
				}
			}()
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
		})
	}
}

func TestTUIExtensionUIHostRejectsNoncanonicalIDs(t *testing.T) {
	host := newTUIExtensionUIHost(make(chan tea.Msg, 1), nil)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}}

	_, err := host.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
		ID:        " status ",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame:     extensionTestFrame(1, "frame"),
	})
	require.ErrorContains(t, err, "leading or trailing whitespace")
	_, err = host.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
		ID:    " surface ",
		Frame: extensionTestFrame(1, "frame"),
	})
	require.ErrorContains(t, err, "leading or trailing whitespace")

	assert.Empty(t, host.widgetSeq)
	assert.Empty(t, host.surfaceSeq)
}

func TestTUIExtensionWidgetsRenderAboveAndBelowComposer(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	m.resize()
	baseHeight := m.viewport.Height()
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}}

	_, err := m.extensionUI.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
		ID:        "above",
		Placement: extensions.UIWidgetPlacementAboveComposer,
		Frame: extensions.UIFrame{Sequence: 1, Lines: []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{
			{Text: "state: "},
			{Text: "ready", Style: extensions.UIStyle{Foreground: "#00ff00", Bold: true}},
		}}}},
	})
	require.NoError(t, err)
	_, err = m.extensionUI.SetWidget(context.Background(), source, extensions.UIWidgetSetRequest{
		ID:        "below",
		Placement: extensions.UIWidgetPlacementBelowComposer,
		Frame:     extensionTestFrame(1, "below composer"),
	})
	require.NoError(t, err)

	applyPendingExtensionUI(t, &m)
	assert.Equal(t, baseHeight-2, m.viewport.Height())
	view := xansi.Strip(m.View().Content)
	assert.Contains(t, view, "state: ready")
	assert.Contains(t, view, "below composer")
	aboveIndex := indexLineContaining(view, "state: ready")
	composerIndex := indexLineContaining(view, "Ask kodelet")
	belowIndex := indexLineContaining(view, "below composer")
	assert.Less(t, aboveIndex, composerIndex)
	assert.Greater(t, belowIndex, composerIndex)
}

func TestTUIExtensionWidgetsRenderOnlyForActiveConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	first := m.conversationState
	second := newConversationState("conversation-second", "conversation-second", true, m.conversationDefaults)
	m.conversations[second.key] = second
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "todo", Generation: 1}}

	for _, request := range []struct {
		ctx     context.Context
		scopeID string
		id      string
		text    string
	}{
		{ctx: contextWithTUIConversation(context.Background(), first.key), scopeID: first.key, id: "todo-progress", text: "first conversation widget"},
		{ctx: contextWithTUIConversation(context.Background(), second.key), scopeID: second.key, id: "todo-progress", text: "second conversation widget"},
		{ctx: context.Background(), id: "global-status", text: "global widget"},
	} {
		_, err := m.extensionUI.SetWidget(request.ctx, source, extensions.UIWidgetSetRequest{
			ScopeID:   request.scopeID,
			ID:        request.id,
			Placement: extensions.UIWidgetPlacementAboveComposer,
			Frame:     extensionTestFrame(1, request.text),
		})
		require.NoError(t, err)
	}
	applyPendingExtensionUI(t, &m)

	firstView := xansi.Strip(m.View().Content)
	assert.Contains(t, firstView, "first conversation widget")
	assert.Contains(t, firstView, "global widget")
	assert.NotContains(t, firstView, "second conversation widget")
	m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)] = 3

	requireConversationActivation(t, &m, second.key)
	secondView := xansi.Strip(m.View().Content)
	assert.Contains(t, secondView, "second conversation widget")
	assert.Contains(t, secondView, "global widget")
	assert.NotContains(t, secondView, "first conversation widget")
	assert.Zero(t, m.extensionWidgetScrollOffset(extensions.UIWidgetPlacementAboveComposer))

	requireConversationActivation(t, &m, first.key)
	assert.Equal(t, 3, m.extensionWidgetScrollOffset(extensions.UIWidgetPlacementAboveComposer))
}

func TestTUIExtensionWidgetsAboveComposerOffsetSettingsHitTargets(t *testing.T) {
	m := newModel(context.Background(), Config{
		Profile:                "work",
		ProfileOptions:         []string{"default", "work"},
		ReasoningEffort:        "medium",
		ReasoningEffortOptions: []string{"low", "medium", "high"},
	})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 100
	m.height = 30
	key := extensionUIKey{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}, id: "above"}
	m.extensionWidgets[key] = tuiExtensionWidget{
		key:       key,
		placement: extensions.UIWidgetPlacementAboveComposer,
		frame:     extensionTestFrame(1, "status"),
	}
	m.rebuildExtensionWidgetOrder()
	m.resize()

	composerTop := m.viewport.Height() + m.extensionWidgetsHeight(extensions.UIWidgetPlacementAboveComposer)
	profileStart, _, ok := m.profileLabelBoundsInBlock()
	require.True(t, ok)
	reasoningStart, _, ok := m.reasoningEffortLabelBoundsInBlock()
	require.True(t, ok)

	assert.False(t, m.profileComposerRegionContains(tuiLeftMargin+profileStart, m.viewport.Height()))
	assert.True(t, m.profileComposerRegionContains(tuiLeftMargin+profileStart, composerTop))
	assert.False(t, m.reasoningComposerRegionContains(tuiLeftMargin+reasoningStart, m.viewport.Height()))
	assert.True(t, m.reasoningComposerRegionContains(tuiLeftMargin+reasoningStart, composerTop))
}

func TestTUIExtensionWidgetsFoldFromFirstLine(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 24
	key := extensionUIKey{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}, id: "status"}
	m.extensionWidgets[key] = tuiExtensionWidget{
		key:       key,
		placement: extensions.UIWidgetPlacementAboveComposer,
		frame: extensions.UIFrame{Sequence: 1, Lines: []extensions.UIFrameLine{
			{Spans: []extensions.UIStyledSpan{{Text: "Build status"}}},
			{Spans: []extensions.UIStyledSpan{{Text: "tests passing"}}},
			{Spans: []extensions.UIStyledSpan{{Text: "lint clean"}}},
		}},
	}
	m.rebuildExtensionWidgetOrder()
	m.resize()

	expandedViewportHeight := m.viewport.Height()
	rendered := xansi.Strip(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer))
	assert.Contains(t, rendered, "Build status ▾")
	assert.Contains(t, rendered, "tests passing")
	assert.Contains(t, rendered, "lint clean")

	headerY := m.viewport.Height()
	handled := m.routeExtensionWidgetMouse(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin,
		Y:      headerY,
	})
	require.True(t, handled)
	assert.True(t, m.collapsedWidgets[key])
	assert.Equal(t, expandedViewportHeight+2, m.viewport.Height())
	rendered = xansi.Strip(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer))
	assert.Contains(t, rendered, "Build status ▸")
	assert.NotContains(t, rendered, "tests passing")
	assert.NotContains(t, rendered, "lint clean")

	headerY = m.viewport.Height()
	handled = m.routeExtensionWidgetMouse(tea.MouseClickMsg{
		Button: tea.MouseLeft,
		X:      tuiLeftMargin,
		Y:      headerY,
	})
	require.True(t, handled)
	assert.NotContains(t, m.collapsedWidgets, key)
	assert.Equal(t, expandedViewportHeight, m.viewport.Height())
	rendered = xansi.Strip(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer))
	assert.Contains(t, rendered, "Build status ▾")
	assert.Contains(t, rendered, "tests passing")
}

func TestTUIExtensionWidgetFoldStateSurvivesUpdatesAndClearsOnRemoval(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 24
	m.resize()
	key := extensionUIKey{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}, id: "status"}
	widget := tuiExtensionWidget{
		key:       key,
		placement: extensions.UIWidgetPlacementAboveComposer,
		frame: extensions.UIFrame{Sequence: 1, Lines: []extensions.UIFrameLine{
			{Spans: []extensions.UIStyledSpan{{Text: "Status"}}},
			{Spans: []extensions.UIStyledSpan{{Text: "first"}}},
		}},
	}

	m.applyExtensionUIBatch(extensionUIBatch{widgets: []pendingExtensionWidget{{widget: widget}}})
	m.collapsedWidgets[key] = true
	widget.frame = extensions.UIFrame{Sequence: 2, Lines: []extensions.UIFrameLine{
		{Spans: []extensions.UIStyledSpan{{Text: "Status updated"}}},
		{Spans: []extensions.UIStyledSpan{{Text: "second"}}},
	}}
	m.applyExtensionUIBatch(extensionUIBatch{widgets: []pendingExtensionWidget{{widget: widget}}})

	assert.True(t, m.collapsedWidgets[key])
	rendered := xansi.Strip(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer))
	assert.Contains(t, rendered, "Status updated ▸")
	assert.NotContains(t, rendered, "second")

	m.applyExtensionUIBatch(extensionUIBatch{widgets: []pendingExtensionWidget{{widget: tuiExtensionWidget{key: key}, remove: true}}})
	assert.NotContains(t, m.collapsedWidgets, key)
}

func TestToggleAllDetailsOnlyChangesVisibleConversationWidgets(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	first := m.conversationState
	second := newConversationState("conversation-second", "conversation-second", true, m.conversationDefaults)
	m.conversations[second.key] = second
	owner := extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}
	firstKey := extensionUIKey{owner: owner, conversationKey: first.key, id: "status"}
	secondKey := extensionUIKey{owner: owner, conversationKey: second.key, id: "status"}
	for _, key := range []extensionUIKey{firstKey, secondKey} {
		m.extensionWidgets[key] = tuiExtensionWidget{
			key:       key,
			placement: extensions.UIWidgetPlacementAboveComposer,
			frame: extensions.UIFrame{Sequence: 1, Lines: []extensions.UIFrameLine{
				{Spans: []extensions.UIStyledSpan{{Text: "Status"}}},
				{Spans: []extensions.UIStyledSpan{{Text: "Details"}}},
			}},
		}
		m.collapsedWidgets[key] = true
	}

	m.toggleAllDetails()
	assert.False(t, m.collapsedWidgets[firstKey])
	assert.True(t, m.collapsedWidgets[secondKey])

	requireConversationActivation(t, &m, second.key)
	m.toggleAllDetails()
	assert.False(t, m.collapsedWidgets[secondKey])
}

func TestTUIExtensionWidgetsContainScrollingWithinTenLines(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "work"}})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 40
	m.profilePickerOpen = true
	owner := extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}
	for placementIndex, placement := range []string{extensions.UIWidgetPlacementAboveComposer, extensions.UIWidgetPlacementBelowComposer} {
		for widgetIndex, id := range []string{"a", "b"} {
			lines := make([]extensions.UIFrameLine, 8)
			for lineIndex := range lines {
				lines[lineIndex] = extensions.UIFrameLine{Spans: []extensions.UIStyledSpan{{Text: fmt.Sprintf("%d-%d-%d", placementIndex, widgetIndex, lineIndex)}}}
			}
			key := extensionUIKey{owner: owner, id: placement + id}
			m.extensionWidgets[key] = tuiExtensionWidget{key: key, placement: placement, frame: extensions.UIFrame{Sequence: 1, Lines: lines}}
		}
	}
	m.rebuildExtensionWidgetOrder()
	m.resize()
	m.viewport.SetContent(strings.Repeat("transcript\n", 100))
	m.viewport.GotoTop()

	assert.Equal(t, maxExtensionWidgetLines, m.extensionWidgetsHeight(extensions.UIWidgetPlacementAboveComposer))
	rendered := strings.Split(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer), "\n")
	require.Len(t, rendered, maxExtensionWidgetLines)
	assert.Contains(t, rendered[0], "0-0-0")
	assert.Contains(t, rendered[9], "0-1-1")

	require.Positive(t, m.profilePickerHeight())
	aboveY := m.viewport.Height() + m.profilePickerHeight()
	m.viewport.SetYOffset(6)
	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: tuiLeftMargin, Y: aboveY})
	m = updated.(model)
	assert.Zero(t, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)])
	assert.Equal(t, 6, m.viewport.YOffset())
	m.viewport.GotoTop()

	updated, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: tuiLeftMargin, Y: aboveY})
	m = updated.(model)
	assert.Equal(t, extensionWidgetScrollStep, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)])
	assert.Zero(t, m.viewport.YOffset())
	rendered = strings.Split(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer), "\n")
	require.Len(t, rendered, maxExtensionWidgetLines)
	assert.Contains(t, rendered[0], "0-0-3")
	assert.Contains(t, rendered[9], "0-1-4")

	belowY := aboveY + maxExtensionWidgetLines + inputHeight + 2
	updated, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: tuiLeftMargin, Y: belowY})
	m = updated.(model)
	assert.Equal(t, extensionWidgetScrollStep, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementBelowComposer)])
	rendered = strings.Split(m.renderExtensionWidgets(extensions.UIWidgetPlacementBelowComposer), "\n")
	assert.Contains(t, rendered[0], "1-0-3")
	assert.Contains(t, rendered[9], "1-1-4")

	updated, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: tuiLeftMargin, Y: aboveY})
	m = updated.(model)
	assert.Equal(t, 6, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)])
	assert.Zero(t, m.viewport.YOffset())

	updated, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: tuiLeftMargin, Y: aboveY})
	m = updated.(model)
	assert.Equal(t, 6, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)])
	assert.Zero(t, m.viewport.YOffset())

	rendered = strings.Split(m.renderExtensionWidgets(extensions.UIWidgetPlacementAboveComposer), "\n")
	assert.Contains(t, rendered[0], "0-0-6")
	assert.Contains(t, rendered[9], "0-1-7")
}

func TestTUIExtensionWidgetScrollOffsetsClampAfterUpdatesAndRemoval(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 24
	m.resize()
	key := extensionUIKey{owner: extensions.UIExtensionOwner{ExtensionID: "widgets", Generation: 1}, id: "status"}
	widget := tuiExtensionWidget{
		key:       key,
		placement: extensions.UIWidgetPlacementAboveComposer,
		frame:     extensions.UIFrame{Sequence: 1, Lines: make([]extensions.UIFrameLine, 16)},
	}
	m.applyExtensionUIBatch(extensionUIBatch{widgets: []pendingExtensionWidget{{widget: widget}}})
	m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)] = 6

	widget.frame = extensions.UIFrame{Sequence: 2, Lines: make([]extensions.UIFrameLine, 12)}
	m.applyExtensionUIBatch(extensionUIBatch{widgets: []pendingExtensionWidget{{widget: widget}}})
	assert.Equal(t, 2, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)])

	m.applyExtensionUIBatch(extensionUIBatch{widgets: []pendingExtensionWidget{{widget: tuiExtensionWidget{key: key}, remove: true}}})
	assert.Zero(t, m.widgetOffsets[m.extensionWidgetOffsetKey(extensions.UIWidgetPlacementAboveComposer)])
}

func TestTUIExtensionSurfaceLayoutFocusAndInputRouting(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 100
	m.height = 40
	m.resize()
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "doom", Generation: 1}}

	_, err := m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
		ID: "game",
		Options: extensions.UISurfaceOptions{
			Width:     extensions.UISizeValue{Percent: 50, Set: true},
			Height:    extensions.UISizeValue{Cells: 12, Set: true},
			MaxHeight: extensions.UISizeValue{Percent: 95, Set: true},
			Anchor:    extensions.UISurfaceAnchorCenter,
		},
		Frame: extensionTestFrame(1, "DOOM FRAME"),
	})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	key, focused := m.focusedExtensionSurfaceKey()
	require.True(t, focused)
	assert.Equal(t, "game", key.id)
	surface := m.extensionSurfaces[key]
	assert.Equal(t, m.contentWidth()/2, surface.layout.width)
	assert.Equal(t, 12, surface.layout.height)
	assert.Equal(t, (m.contentWidth()-surface.layout.width)/2, surface.layout.x)
	assert.Equal(t, (m.height-surface.layout.height)/2, surface.layout.y)
	assert.Contains(t, xansi.Strip(m.View().Content), "DOOM FRAME")

	cmd, handled := m.routeExtensionSurfaceKey(textKeyPress("q"))
	require.True(t, handled)
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())

	cmd, handled = m.routeExtensionSurfaceMouse(tea.MouseClickMsg{
		X:      tuiLeftMargin + surface.layout.x + 3,
		Y:      surface.layout.y + 2,
		Button: tea.MouseLeft,
	})
	require.True(t, handled)
	assert.Nil(t, cmd())

	notifications := source.recordedNotifications()
	inputNotifications := []extensions.UISurfaceInputNotification{}
	for _, notification := range notifications {
		if notification.method == extensions.UISurfaceInputMethod {
			inputNotifications = append(inputNotifications, notification.params.(extensions.UISurfaceInputNotification))
		}
	}
	require.Len(t, inputNotifications, 2)
	assert.Equal(t, "q", inputNotifications[0].Key)
	assert.Equal(t, "q", inputNotifications[0].Text)
	require.NotNil(t, inputNotifications[1].Mouse)
	assert.Equal(t, 3, inputNotifications[1].Mouse.X)
	assert.Equal(t, 2, inputNotifications[1].Mouse.Y)
	assert.Greater(t, inputNotifications[1].Sequence, inputNotifications[0].Sequence)
}

func TestTUIExtensionSurfaceCapturesBracketedPaste(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	m.resize()
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "editor", Generation: 1}}

	_, err := m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{ID: "surface", Frame: extensionTestFrame(1, "EDITOR")})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	updated, cmd := m.Update(tea.PasteMsg{Content: "first\nsecond"})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())
	assert.Empty(t, m.textarea.Value())

	notifications := source.recordedNotifications()
	require.NotEmpty(t, notifications)
	input, ok := notifications[len(notifications)-1].params.(extensions.UISurfaceInputNotification)
	require.True(t, ok)
	assert.Equal(t, extensions.UISurfaceInputKey, input.Kind)
	assert.Empty(t, input.Key)
	assert.Equal(t, "first\nsecond", input.Text)
}

func TestTUIExtensionSurfacesRenderAndCaptureInputOnlyForActiveConversation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 100
	m.height = 40
	m.resize()
	first := m.conversationState
	second := newConversationState("conversation-second", "conversation-second", true, m.conversationDefaults)
	m.conversations[second.key] = second
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "surfaces", Generation: 1}}
	options := extensions.UISurfaceOptions{
		Width:  extensions.UISizeValue{Cells: 30, Set: true},
		Height: extensions.UISizeValue{Cells: 5, Set: true},
		Anchor: extensions.UISurfaceAnchorCenter,
	}

	_, err := m.extensionUI.OpenSurface(contextWithTUIConversation(context.Background(), first.key), source, extensions.UISurfaceOpenRequest{
		ScopeID: first.key, ID: "first", Options: options, Frame: extensionTestFrame(1, "FIRST SURFACE"),
	})
	require.NoError(t, err)
	_, err = m.extensionUI.OpenSurface(contextWithTUIConversation(context.Background(), second.key), source, extensions.UISurfaceOpenRequest{
		ScopeID: second.key, ID: "second", Options: options, Frame: extensionTestFrame(1, "SECOND SURFACE"),
	})
	require.NoError(t, err)
	_, err = m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
		ID: "global", Options: extensions.UISurfaceOptions{Width: options.Width, Height: options.Height, Anchor: extensions.UISurfaceAnchorTop, NonCapturing: true}, Frame: extensionTestFrame(1, "GLOBAL SURFACE"),
	})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	firstKey := extensionUIKey{owner: source.owner, conversationKey: first.key, id: "first"}
	secondKey := extensionUIKey{owner: source.owner, conversationKey: second.key, id: "second"}
	focused, ok := m.focusedExtensionSurfaceKey()
	require.True(t, ok)
	assert.Equal(t, firstKey, focused)
	assert.Positive(t, m.extensionSurfaces[firstKey].layout.width)
	assert.Zero(t, m.extensionSurfaces[secondKey].layout.width)
	firstView := xansi.Strip(m.View().Content)
	assert.Contains(t, firstView, "FIRST SURFACE")
	assert.Contains(t, firstView, "GLOBAL SURFACE")
	assert.NotContains(t, firstView, "SECOND SURFACE")
	cmd, handled := m.routeExtensionSurfaceKey(textKeyPress("a"))
	require.True(t, handled)
	assert.Nil(t, cmd())

	requireConversationActivation(t, &m, second.key)
	focused, ok = m.focusedExtensionSurfaceKey()
	require.True(t, ok)
	assert.Equal(t, secondKey, focused)
	assert.Positive(t, m.extensionSurfaces[secondKey].layout.width)
	secondView := xansi.Strip(m.View().Content)
	assert.Contains(t, secondView, "SECOND SURFACE")
	assert.Contains(t, secondView, "GLOBAL SURFACE")
	assert.NotContains(t, secondView, "FIRST SURFACE")
	cmd, handled = m.routeExtensionSurfaceKey(textKeyPress("b"))
	require.True(t, handled)
	assert.Nil(t, cmd())

	inputs := []extensions.UISurfaceInputNotification{}
	for _, notification := range source.recordedNotifications() {
		if notification.method == extensions.UISurfaceInputMethod {
			inputs = append(inputs, notification.params.(extensions.UISurfaceInputNotification))
		}
	}
	require.Len(t, inputs, 2)
	assert.Equal(t, "first", inputs[0].ID)
	assert.Equal(t, "second", inputs[1].ID)
}

func TestTUIExtensionSurfaceEventsIncludeConversationScopeForSameID(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 40
	m.resize()
	first := m.conversationState
	second := newConversationState("conversation-second", "conversation-second", true, m.conversationDefaults)
	m.conversations[second.key] = second
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "shared", Generation: 1}}
	options := extensions.UISurfaceOptions{NonCapturing: false}

	for _, scopeID := range []string{first.key, second.key} {
		_, err := m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{
			ScopeID: scopeID,
			ID:      "shared",
			Options: options,
			Frame:   extensionTestFrame(1, scopeID),
		})
		require.NoError(t, err)
	}
	applyPendingExtensionUI(t, &m)

	cmd, handled := m.routeExtensionSurfaceKey(textKeyPress("a"))
	require.True(t, handled)
	assert.Nil(t, cmd())
	requireConversationActivation(t, &m, second.key)
	cmd, handled = m.routeExtensionSurfaceKey(textKeyPress("b"))
	require.True(t, handled)
	assert.Nil(t, cmd())

	var inputs []extensions.UISurfaceInputNotification
	for _, notification := range source.recordedNotifications() {
		if notification.method == extensions.UISurfaceInputMethod {
			input := notification.params.(extensions.UISurfaceInputNotification)
			if input.Kind == extensions.UISurfaceInputKey {
				inputs = append(inputs, input)
			}
		}
	}
	require.Len(t, inputs, 2)
	assert.Equal(t, first.key, inputs[0].ScopeID)
	assert.Equal(t, second.key, inputs[1].ScopeID)
	assert.Equal(t, "shared", inputs[0].ID)
	assert.Equal(t, "shared", inputs[1].ID)
}

func TestTUIExtensionSurfaceFocusIsSuspendedByModalUI(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "focus", Generation: 1}}
	key := extensionUIKey{
		owner:           source.owner,
		conversationKey: m.activeConversationKey,
		id:              "surface",
	}
	surface := tuiExtensionSurface{key: key, source: source, openOrdinal: 1}
	m.extensionSurfaces[key] = surface
	m.extensionSurfaceOrder = []extensionUIKey{key}
	m.extensionUI = nil

	focused, ok := m.focusedExtensionSurfaceKey()
	require.True(t, ok)
	assert.Equal(t, key, focused)
	m.conversationPicker = &conversationPickerState{}
	_, ok = m.focusedExtensionSurfaceKey()
	assert.False(t, ok)
	for _, cmd := range m.extensionSurfaceFocusTransitionCommands(key, true, surface) {
		assert.Nil(t, cmd())
	}
	m.conversationPicker = nil
	for _, cmd := range m.extensionSurfaceFocusTransitionCommands(extensionUIKey{}, false, tuiExtensionSurface{}) {
		assert.Nil(t, cmd())
	}
	m.shortcutsOpen = true
	_, ok = m.focusedExtensionSurfaceKey()
	assert.False(t, ok)
	m.shortcutsOpen = false
	m.activeUIPrompt = &uiPromptState{mode: uiPromptConfirm}
	_, ok = m.focusedExtensionSurfaceKey()
	assert.False(t, ok)

	var focusKinds []string
	for _, notification := range source.recordedNotifications() {
		if notification.method == extensions.UISurfaceInputMethod {
			focusKinds = append(focusKinds, notification.params.(extensions.UISurfaceInputNotification).Kind)
		}
	}
	assert.Equal(t, []string{extensions.UISurfaceInputBlur, extensions.UISurfaceInputFocus}, focusKinds)
}

func TestTUIExtensionSurfaceKeyRoutingPreservesCombinedModifiers(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		alt   bool
		shift bool
		ctrl  bool
	}{
		{
			name:  "ctrl shift",
			key:   keyPressWithMod(tea.KeyUp, tea.ModCtrl|tea.ModShift),
			shift: true,
			ctrl:  true,
		},
		{
			name: "alt ctrl",
			key:  keyPressWithMod(tea.KeyUp, tea.ModCtrl|tea.ModAlt),
			alt:  true,
			ctrl: true,
		},
		{
			name:  "alt ctrl shift",
			key:   keyPressWithMod(tea.KeyUp, tea.ModCtrl|tea.ModShift|tea.ModAlt),
			alt:   true,
			shift: true,
			ctrl:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner := extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1}
			key := extensionUIKey{owner: owner, id: "surface"}
			source := &fakeExtensionUISource{owner: owner}
			surface := tuiExtensionSurface{key: key, source: source}
			m := model{
				extensionSurfaces:     map[extensionUIKey]tuiExtensionSurface{key: surface},
				extensionSurfaceOrder: []extensionUIKey{key},
			}

			cmd, handled := m.routeExtensionSurfaceKey(test.key)

			require.True(t, handled)
			require.NotNil(t, cmd)
			assert.Nil(t, cmd())
			notifications := source.recordedNotifications()
			require.Len(t, notifications, 1)
			input, ok := notifications[0].params.(extensions.UISurfaceInputNotification)
			require.True(t, ok)
			assert.Equal(t, test.key.String(), input.Key)
			assert.Equal(t, test.alt, input.Alt)
			assert.Equal(t, test.shift, input.Shift)
			assert.Equal(t, test.ctrl, input.Ctrl)
		})
	}
}

func TestTUIExtensionSurfaceFocusStackAndFailureCleanup(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	m.resize()
	first := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "first", Generation: 1}}
	second := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "second", Generation: 1}}

	_, err := m.extensionUI.OpenSurface(context.Background(), first, extensions.UISurfaceOpenRequest{ID: "one", Frame: extensionTestFrame(1, "one")})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)
	_, err = m.extensionUI.OpenSurface(context.Background(), second, extensions.UISurfaceOpenRequest{ID: "two", Frame: extensionTestFrame(1, "two")})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	key, focused := m.focusedExtensionSurfaceKey()
	require.True(t, focused)
	assert.Equal(t, "two", key.id)

	m.extensionUI.CleanupExtensionUI(second.owner)
	applyPendingExtensionUI(t, &m)
	key, focused = m.focusedExtensionSurfaceKey()
	require.True(t, focused)
	assert.Equal(t, "one", key.id)
	assert.NotContains(t, m.extensionSurfaces, extensionUIKey{owner: second.owner, id: "two"})

	first.err = errors.New("process disconnected")
	cmd, handled := m.routeExtensionSurfaceKey(textKeyPress("x"))
	require.True(t, handled)
	transportError, ok := cmd().(extensionUITransportErrorMsg)
	require.True(t, ok)
	assert.Equal(t, first.owner, transportError.owner)
	updated, next := m.Update(transportError)
	m = updated.(model)
	require.Nil(t, next, "the waiter scheduled by the originating flush must remain the sole run-channel consumer")
	flush := <-m.runCh
	updated, _ = m.Update(flush)
	m = updated.(model)
	assert.Empty(t, m.extensionSurfaces)
}

func TestTUIExtensionSurfaceBatchUsesOpenOrderAndPreservesUpdatedFrames(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	m.resize()
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1}}

	_, err := m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{ID: "z", Frame: extensionTestFrame(1, "z")})
	require.NoError(t, err)
	_, err = m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{ID: "a", Frame: extensionTestFrame(1, "a")})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	focused, ok := m.focusedExtensionSurfaceKey()
	require.True(t, ok)
	assert.Equal(t, "a", focused.id, "the last opened surface must remain on top regardless of lexical ID order")

	_, err = m.extensionUI.UpdateSurface(context.Background(), source, extensions.UISurfaceFrameRequest{ID: "a", Frame: extensionTestFrame(2, "latest a")})
	require.NoError(t, err)
	_, err = m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{ID: "next", Frame: extensionTestFrame(1, "next")})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	aKey := extensionUIKey{owner: source.owner, id: "a"}
	assert.Equal(t, "latest a", m.extensionSurfaces[aKey].frame.Lines[0].Spans[0].Text)
}

func TestTUIExtensionSurfaceReopenReallocatesAndRefocusesSameID(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	m.resize()
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1}}
	request := extensions.UISurfaceOpenRequest{
		ID:      "surface",
		Options: extensions.UISurfaceOptions{Width: extensions.UISizeValue{Cells: 10, Set: true}},
		Frame:   extensionTestFrame(1, "first"),
	}

	_, err := m.extensionUI.OpenSurface(context.Background(), source, request)
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)
	key := extensionUIKey{owner: source.owner, id: "surface"}
	first := m.extensionSurfaces[key]
	assert.Equal(t, 10, first.layout.width)
	staleCmd := m.nextExtensionSurfaceInputCmd(key, first, extensions.UISurfaceInputKey, "x", "x", false, false, false, nil)

	request.Options.Width = extensions.UISizeValue{Cells: 20, Set: true}
	request.Frame = extensionTestFrame(2, "second")
	_, err = m.extensionUI.OpenSurface(context.Background(), source, request)
	require.NoError(t, err)
	assert.Nil(t, staleCmd())
	assert.Empty(t, source.recordedNotifications(), "a queued command from the previous open must be dropped")
	applyPendingExtensionUI(t, &m)
	second := m.extensionSurfaces[key]

	assert.NotEqual(t, first.openOrdinal, second.openOrdinal)
	assert.Equal(t, 20, second.layout.width)
	assert.Equal(t, uint64(2), first.eventSequence)
	assert.Equal(t, uint64(2), second.eventSequence, "replacement must restart resize and focus sequencing for the new handle")
	assert.Equal(t, "second", second.frame.Lines[0].Spans[0].Text)
}

func TestTUIExtensionSurfaceRejectsQueuedEventsFromPreviousProcessGeneration(t *testing.T) {
	m := model{extensionSurfaces: map[extensionUIKey]tuiExtensionSurface{}}
	oldOwner := extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1}
	source := &fakeExtensionUISource{owner: oldOwner}
	key := extensionUIKey{owner: oldOwner, id: "surface"}
	surface := tuiExtensionSurface{key: key, source: source}
	m.extensionSurfaces[key] = surface
	source.owner.Generation = 2

	cmd := m.nextExtensionSurfaceInputCmd(key, surface, extensions.UISurfaceInputKey, "x", "x", false, false, false, nil)
	message, ok := cmd().(extensionUITransportErrorMsg)

	require.True(t, ok)
	assert.Equal(t, oldOwner, message.owner)
	assert.Empty(t, source.recordedNotifications())
}

func TestTUIExtensionSurfaceClampsOversizedMarginsInsideTerminal(t *testing.T) {
	m := model{width: 10, height: 5}
	layout := m.resolveExtensionSurfaceLayout(tuiExtensionSurface{
		options: extensions.UISurfaceOptions{
			Width:  extensions.UISizeValue{Percent: 50, Set: true},
			Height: extensions.UISizeValue{Percent: 50, Set: true},
			Margin: extensions.UIMargin{Top: 100, Right: 100, Bottom: 100, Left: 100},
			Anchor: extensions.UISurfaceAnchorBottomRight,
		},
		frame: extensionTestFrame(1, "frame"),
	})

	assert.Equal(t, 1, layout.width)
	assert.Equal(t, 1, layout.height)
	assert.GreaterOrEqual(t, layout.x, 0)
	assert.Less(t, layout.x, m.contentWidth())
	assert.GreaterOrEqual(t, layout.y, 0)
	assert.Less(t, layout.y, m.height)
}

func TestTUIExtensionSurfacePreservesGlobalControlC(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
	m.width = 80
	m.height = 30
	m.resize()
	source := &fakeExtensionUISource{owner: extensions.UIExtensionOwner{ExtensionID: "game", Generation: 1}}
	_, err := m.extensionUI.OpenSurface(context.Background(), source, extensions.UISurfaceOpenRequest{ID: "surface", Frame: extensionTestFrame(1, "frame")})
	require.NoError(t, err)
	applyPendingExtensionUI(t, &m)

	updated, cmd := m.Update(keyPressWithMod('c', tea.ModCtrl))

	_, ok := updated.(model)
	require.True(t, ok)
	require.NotNil(t, cmd)
	_, ok = cmd().(tea.QuitMsg)
	assert.True(t, ok)
	assert.Empty(t, source.recordedNotifications())
}

func extensionTestFrame(sequence uint64, text string) extensions.UIFrame {
	return extensions.UIFrame{
		Sequence: sequence,
		Lines:    []extensions.UIFrameLine{{Spans: []extensions.UIStyledSpan{{Text: text}}}},
	}
}

func indexLineContaining(content, target string) int {
	for index, line := range strings.Split(content, "\n") {
		if strings.Contains(line, target) {
			return index
		}
	}
	return -1
}

func applyPendingExtensionUI(t *testing.T, m *model) {
	t.Helper()
	msg := <-m.runCh
	_, ok := msg.(extensionUIFlushMsg)
	require.True(t, ok)
	_ = m.applyExtensionUIBatch(m.extensionUI.drain())
}
