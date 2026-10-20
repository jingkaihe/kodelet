package controlplane

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/chat"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func progressTestCall(t *testing.T, s *Server, session *testRealtimeSession) (*realtimeCall, chan realtimeDelivery) {
	t.Helper()
	deliveries := make(chan realtimeDelivery, 256)
	session.updateFunc = func(_ context.Context, id string, update llmtypes.RealtimeUpdate) error {
		deliveries <- realtimeDelivery{handoffID: id, update: update}
		return nil
	}
	session.completeFunc = func(_ context.Context, id, text string) error {
		deliveries <- realtimeDelivery{handoffID: id, result: text, done: make(chan struct{})}
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	call := &realtimeCall{
		server: s, id: "voice", chat: chat.ChatRequest{ConversationID: "saved"},
		ctx: ctx, cancel: cancel, session: session, events: make(chan realtimeEvent, 32), handoffs: make(map[string]struct{}),
	}
	s.realtimeMu.Lock()
	s.realtimeCalls = map[string]*realtimeCall{call.id: call}
	s.realtimeMu.Unlock()
	t.Cleanup(func() { call.close(); call.work.Wait() })
	return call, deliveries
}

func receiveProgress(t *testing.T, deliveries <-chan realtimeDelivery) realtimeDelivery {
	t.Helper()
	select {
	case delivery := <-deliveries:
		return delivery
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for voice progress")
		return realtimeDelivery{}
	}
}

func TestRealtimeProgressStreamsTextBeforeCompletion(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	p := call.progressBridge()
	p.bind("turn", "handoff", false)
	for _, kind := range []string{"thinking", "thinking-delta", "tool-use", "tool-update", "tool-result", "ui-notify", "user-message"} {
		s.broadcastRealtimeProgress("saved", "turn", chat.ChatEvent{Kind: kind, Role: "assistant", Delta: "private", Content: "private"})
	}
	s.broadcastRealtimeProgress("other", "turn", chat.ChatEvent{Kind: "text", Role: "assistant", Content: "wrong conversation"})
	s.broadcastRealtimeProgress("saved", "other", chat.ChatEvent{Kind: "text", Role: "assistant", Content: "wrong turn"})
	s.broadcastRealtimeProgress("saved", "turn", chat.ChatEvent{Kind: "text-delta", Role: "assistant", Delta: "Checking "})
	first := receiveProgress(t, deliveries)
	assert.Equal(t, "handoff", first.handoffID)
	assert.Equal(t, llmtypes.RealtimeUpdate{Text: "Checking "}, first.update)
	assert.Nil(t, first.done, "partial text is not a completed handoff")
	s.broadcastRealtimeProgress("saved", "turn", chat.ChatEvent{Kind: "text-delta", Role: "assistant", Delta: "callers."})
	s.broadcastRealtimeProgress("saved", "turn", chat.ChatEvent{Kind: "content-end", Role: "assistant"})
	assert.Equal(t, llmtypes.RealtimeUpdate{Text: "callers.", BlockEnd: true}, receiveProgress(t, deliveries).update)
	p.complete("handoff", "Done.", "turn")
	assert.Equal(t, "Done.", receiveProgress(t, deliveries).result)
	s.broadcastRealtimeProgress("saved", "turn", chat.ChatEvent{Kind: "text", Role: "assistant", Content: "stale"})
	assert.Empty(t, deliveries)
}

func TestRealtimeProgressMultipleHandoffsShareOneStream(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	p := call.progressBridge()
	p.bind("turn", "first", false)
	p.bind("turn", "follow-up", true)
	p.observe("turn", chat.ChatEvent{Kind: "text", Content: "All done."})
	assert.Equal(t, "first", receiveProgress(t, deliveries).handoffID)
	// The follow-up's completion goroutine may win. Do not resolve the first
	// handoff early, or repeat its already forwarded final text.
	p.complete("follow-up", "All done.", "turn")
	completion := receiveProgress(t, deliveries)
	assert.Equal(t, "follow-up", completion.handoffID)
	assert.Contains(t, completion.result, "already provided")
	p.complete("first", "All done.", "turn")
	completion = receiveProgress(t, deliveries)
	assert.Equal(t, "first", completion.handoffID)
	assert.Empty(t, completion.result, "secondary completion is acknowledged quietly")
	assert.Empty(t, deliveries)
}

func TestRealtimeProgressLateSteerRebindsWithoutCompleting(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	p := call.progressBridge()
	p.bind("old", "late", false)
	p.observe("old", chat.ChatEvent{Kind: "text-delta", Delta: "Old task context."})
	p.unbind("old", "late")
	assert.Equal(t, llmtypes.RealtimeUpdate{Text: "Old task context.", BlockEnd: true}, receiveProgress(t, deliveries).update)
	p.bind("new", "late", false)
	p.observe("old", chat.ChatEvent{Kind: "text", Content: "stale"})
	p.observe("new", chat.ChatEvent{Kind: "text", Content: "Now doing the follow-up."})
	assert.Equal(t, "Now doing the follow-up.", receiveProgress(t, deliveries).update.Text)
	p.complete("late", "Follow-up done.", "new")
	assert.Equal(t, "Follow-up done.", receiveProgress(t, deliveries).result)
}

func TestRealtimeProgressBackpressurePreservesCompletion(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	session.updateFunc = func(ctx context.Context, id string, update llmtypes.RealtimeUpdate) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		deliveries <- realtimeDelivery{handoffID: id, update: update}
		return nil
	}
	p := call.progressBridge()
	p.bind("turn", "handoff", false)
	p.observe("turn", chat.ChatEvent{Kind: "text", Content: "First."})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	// None of these broadcasts may wait on the blocked voice writer.
	for range realtimeProgressMaxQueue + 20 {
		s.broadcastRealtimeProgress("saved", "turn", chat.ChatEvent{Kind: "text", Role: "assistant", Content: strings.Repeat("é", 100)})
	}
	p.mu.Lock()
	assert.LessOrEqual(t, len(p.queue), realtimeProgressMaxQueue+1)
	assert.LessOrEqual(t, p.queuedBytes, realtimeProgressMaxBytes+len(realtimeProgressOmitted))
	assert.True(t, p.turns["turn"].omitted)
	p.mu.Unlock()
	completed := make(chan struct{})
	go func() { p.complete("handoff", "Final result.", "turn"); close(completed) }()
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.turns["turn"].completed
	}, time.Second, time.Millisecond)
	close(gate)
	omitted := false
	updates := 0
	for {
		delivery := receiveProgress(t, deliveries)
		if delivery.done != nil {
			assert.Equal(t, "Final result.", delivery.result)
			break
		}
		assert.True(t, utf8.ValidString(delivery.update.Text))
		updates++
		omitted = omitted || strings.Contains(delivery.update.Text, "was omitted")
	}
	assert.True(t, omitted)
	assert.LessOrEqual(t, updates, 2, "completion skips the obsolete backlog")
	<-completed
	assert.Empty(t, deliveries)
}

func TestRealtimeProgressHangupCancelsBlockedDelivery(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, _ := progressTestCall(t, s, session)
	entered, exited := make(chan struct{}), make(chan struct{})
	session.updateFunc = func(ctx context.Context, _ string, _ llmtypes.RealtimeUpdate) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}
	p := call.progressBridge()
	p.bind("turn", "handoff", false)
	p.observe("turn", chat.ChatEvent{Kind: "text", Content: "Working."})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	call.close()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("hangup left delivery blocked")
	}
	assert.True(t, session.closed.Load())
}

func TestRealtimeProgressResumesAfterOversizedBlock(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	p := call.progressBridge()
	p.bind("turn", "handoff", false)
	p.observe("turn", chat.ChatEvent{Kind: "text-delta", Delta: strings.Repeat("é", realtimeProgressMaxText)})
	omitted := receiveProgress(t, deliveries)
	assert.Equal(t, realtimeProgressOmitted, omitted.update.Text)
	assert.True(t, omitted.update.BlockEnd)
	p.observe("turn", chat.ChatEvent{Kind: "text-delta", Delta: "discard the rest"})
	p.observe("turn", chat.ChatEvent{Kind: "content-end"})
	p.observe("turn", chat.ChatEvent{Kind: "text", Content: "The regression test failed; investigating."})
	assert.Equal(t, "The regression test failed; investigating.", receiveProgress(t, deliveries).update.Text)
	p.complete("handoff", "Done.", "turn")
	assert.Equal(t, "Done.", receiveProgress(t, deliveries).result)
}

func TestRealtimeProgressObservesTypedTurnSteeredByVoice(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	started, proceed, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseProceed := sync.OnceFunc(func() { close(proceed) })
	releaseFinish := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(func() { releaseProceed(); releaseFinish() })
	s.chatRunner = &mockChatRunner{runFunc: func(_ context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
		assert.NoError(t, sink.Send(chat.ChatEvent{Kind: "text-delta", Role: "assistant", Delta: "The tests have not "}))
		close(started)
		<-proceed
		_, err := s.turns.db.Exec("DELETE FROM steering_messages WHERE conversation_id='saved'")
		assert.NoError(t, err)
		assert.NoError(t, sink.Send(chat.ChatEvent{Kind: "text-delta", Role: "assistant", Delta: "passed."}))
		assert.NoError(t, sink.Send(chat.ChatEvent{Kind: "content-end", Role: "assistant"}))
		assert.NoError(t, sink.Send(chat.ChatEvent{Kind: "text-delta", Role: "assistant", Delta: "Applying your corrections."}))
		assert.NoError(t, sink.Send(chat.ChatEvent{Kind: "content-end", Role: "assistant"}))
		<-finish
		result := "Corrections applied."
		return req.ConversationID, sink.Send(chat.ChatEvent{Kind: "result", Result: &result})
	}}
	req := chat.ChatRequest{ConversationID: "saved", TurnID: "typed", Message: "Start work"}
	receipt, admitted, err := s.turns.admit(t.Context(), req)
	require.NoError(t, err)
	require.True(t, admitted)
	typedDone := make(chan struct{})
	go func() {
		defer close(typedDone)
		_, err := s.runAdmittedChat(t.Context(), req, newSubscriberEventSink(), "browser", nil, receipt)
		assert.NoError(t, err)
	}()
	<-started
	call.delegate(llmtypes.RealtimeEvent{HandoffID: "first", Text: "Change the colour"})
	call.delegate(llmtypes.RealtimeEvent{HandoffID: "second", Text: "Also update the title"})
	require.Eventually(t, func() bool {
		s.activeChatsMu.Lock()
		defer s.activeChatsMu.Unlock()
		return len(s.activeChats["saved"].voiceSteers) == 2
	}, time.Second, time.Millisecond)
	releaseProceed()
	assert.Equal(t, "Applying your corrections.", receiveProgress(t, deliveries).update.Text)
	assert.Empty(t, deliveries, "two handoffs do not duplicate progress")
	releaseFinish()
	<-typedDone
	call.work.Wait()
	first, second := receiveProgress(t, deliveries), receiveProgress(t, deliveries)
	assert.NotEqual(t, first.handoffID, second.handoffID)
	assert.Equal(t, "Corrections applied.", first.result)
	assert.Empty(t, second.result)
}

func TestRealtimeProgressRebindingPreservesQueuedCompletion(t *testing.T) {
	s, session := realtimeTestServer(t)
	call, deliveries := progressTestCall(t, s, session)
	gate, entered := make(chan struct{}), make(chan struct{}, 1)
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	session.updateFunc = func(ctx context.Context, id string, update llmtypes.RealtimeUpdate) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		deliveries <- realtimeDelivery{handoffID: id, update: update}
		return nil
	}
	p := call.progressBridge()
	p.bind("old", "late", false)
	p.bind("old", "consumed", true)
	p.observe("old", chat.ChatEvent{Kind: "text-delta", Delta: "Old "})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	p.mu.Lock()
	turn := p.turns["old"]
	turn.buffer, turn.block = "task ", "Old task "
	p.flushLocked(turn, false)
	turn.buffer, turn.block = "finished.", "Old task finished."
	p.mu.Unlock()
	completed := make(chan struct{})
	go func() { p.complete("consumed", "Old task finished.", "old"); close(completed) }()
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.turns["old"].completed
	}, time.Second, time.Millisecond)
	p.mu.Lock()
	sealed := append([]realtimeDelivery(nil), p.queue...)
	p.mu.Unlock()
	p.unbind("old", "late")
	p.bind("new", "late", false)
	p.mu.Lock()
	assert.Equal(t, sealed, p.queue, "rebinding cannot alter deliveries sealed by completion")
	p.mu.Unlock()
	p.observe("new", chat.ChatEvent{Kind: "text", Content: "New work."})
	release()
	assert.Equal(t, "Old ", receiveProgress(t, deliveries).update.Text)
	for _, expected := range sealed {
		actual := receiveProgress(t, deliveries)
		assert.Equal(t, expected.handoffID, actual.handoffID)
		assert.Equal(t, expected.update, actual.update)
		assert.Equal(t, expected.result, actual.result)
	}
	assert.Equal(t, "New work.", receiveProgress(t, deliveries).update.Text)
	<-completed
	p.complete("late", "Follow-up result.", "new")
	assert.Equal(t, "Follow-up result.", receiveProgress(t, deliveries).result)
}
