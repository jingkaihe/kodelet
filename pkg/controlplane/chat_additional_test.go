package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type plainResponseWriter struct {
	header     http.Header
	body       strings.Builder
	statusCode int
}

func newPlainResponseWriter() *plainResponseWriter {
	return &plainResponseWriter{header: make(http.Header)}
}

func (w *plainResponseWriter) Header() http.Header {
	return w.header
}

func (w *plainResponseWriter) Write(payload []byte) (int, error) {
	return w.body.Write(payload)
}

func (w *plainResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

type failingChatSink struct {
	err   error
	calls int
}

func (s *failingChatSink) Send(ChatEvent) error {
	s.calls++
	return s.err
}

func TestNDJSONEventSinkRequiresFlusherAndWritesLines(t *testing.T) {
	_, err := newNDJSONEventSink(newPlainResponseWriter())
	require.ErrorContains(t, err, "streaming is not supported")

	recorder := httptest.NewRecorder()
	sink, err := newNDJSONEventSink(recorder)
	require.NoError(t, err)
	require.NoError(t, sink.Send(ChatEvent{Kind: "text", Content: "hello", Role: "assistant"}))
	require.NoError(t, sink.KeepAlive())

	assert.True(t, recorder.Flushed)
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Len(t, lines, 1)
	var event ChatEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &event))
	assert.Equal(t, "text", event.Kind)
	assert.Equal(t, "hello", event.Content)
	assert.Equal(t, "assistant", event.Role)
	body := recorder.Body.String()
	sink.Close()
	sink.Close()
	assert.ErrorIs(t, sink.Send(ChatEvent{Kind: "late"}), io.ErrClosedPipe)
	assert.ErrorIs(t, sink.KeepAlive(), io.ErrClosedPipe)
	assert.Equal(t, body, recorder.Body.String())
}

func TestNDJSONEventSinkReportsMarshalErrors(t *testing.T) {
	sink, err := newNDJSONEventSink(httptest.NewRecorder())
	require.NoError(t, err)

	err = sink.Send(ChatEvent{Kind: "bad", Content: func() {}})
	require.ErrorContains(t, err, "failed to marshal chat event")
}

type blockingFlushResponseWriter struct {
	*httptest.ResponseRecorder
	started   chan struct{}
	release   chan struct{}
	deadlines []time.Time
}

func (w *blockingFlushResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func (w *blockingFlushResponseWriter) Flush() {
	close(w.started)
	<-w.release
	w.ResponseRecorder.Flush()
}

func TestNDJSONEventSinkCloseWaitsForFlush(t *testing.T) {
	writer := &blockingFlushResponseWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-writer.release:
		default:
			close(writer.release)
		}
	})
	sink, err := newNDJSONEventSink(&responseWriter{ResponseWriter: writer})
	require.NoError(t, err)
	sent := make(chan error, 1)
	go func() { sent <- sink.Send(ChatEvent{Kind: "text"}) }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		require.FailNow(t, "write did not reach Flush")
	}
	closed := make(chan struct{})
	go func() { sink.Close(); close(closed) }()
	select {
	case <-closed:
		require.FailNow(t, "Close returned before the in-flight Flush finished")
	case <-time.After(10 * time.Millisecond):
	}
	close(writer.release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		require.FailNow(t, "Close did not finish after Flush")
	}
	require.NoError(t, <-sent)
	assert.ErrorIs(t, sink.Send(ChatEvent{Kind: "late"}), io.ErrClosedPipe)
	require.Len(t, writer.deadlines, 2, "the HTTP middleware must forward write deadlines so draining is bounded")
	assert.False(t, writer.deadlines[0].IsZero())
	assert.True(t, writer.deadlines[1].IsZero())
}

type delayedChatSink struct {
	ChatEventSink
	release <-chan struct{}
	result  chan error
}

func (s *delayedChatSink) Send(event ChatEvent) error {
	<-s.release
	err := s.ChatEventSink.Send(event)
	s.result <- err
	return err
}

func TestChatHandlerRejectsNativeCleanupAfterReturning(t *testing.T) {
	for _, runErr := range []error{nil, context.Canceled, errors.New("run failed")} {
		t.Run(fmt.Sprint(runErr), func(t *testing.T) {
			release := make(chan struct{})
			result := make(chan error, 1)
			server := &Server{}
			server.chatRunner = &mockChatRunner{runFunc: func(_ context.Context, req ChatRequest, _ ChatEventSink) (string, error) {
				broker := server.uiInputBrokerForRun(req.ConversationID)
				broker.mu.Lock()
				broker.native = &nativeUIState{epoch: "old-ui"}
				broker.owner.sink = &delayedChatSink{ChatEventSink: broker.owner.sink, release: release, result: result}
				broker.mu.Unlock()
				return req.ConversationID, runErr
			}}
			request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"hello","clientCapabilities":{"interactiveUI":true,"persistentSurfaces":true}}`))
			request.Header.Set(chat.ClientIDHeader, "native-client")
			writer := httptest.NewRecorder()
			server.handleChat(writer, request)
			body := writer.Body.String()
			close(release)
			select {
			case err := <-result:
				assert.ErrorIs(t, err, io.ErrClosedPipe)
			case <-time.After(time.Second):
				require.FailNow(t, "native cleanup did not finish")
			}
			assert.Equal(t, body, writer.Body.String(), "cleanup must not access the ResponseWriter after the handler returns")
		})
	}
}

func TestSubscriberEventSinkBufferFullAndCloseIdempotent(t *testing.T) {
	sink := newSubscriberEventSink()
	for i := 0; i < cap(sink.ch); i++ {
		require.NoError(t, sink.Send(ChatEvent{Kind: "text"}))
	}

	require.ErrorContains(t, sink.Send(ChatEvent{Kind: "overflow"}), "subscriber buffer full")
	sink.Close()
	sink.Close()

	for i := 0; i < cap(sink.ch); i++ {
		<-sink.ch
	}
	_, ok := <-sink.ch
	assert.False(t, ok)
}

func TestBroadcastingEventSinkBroadcastsOnSuccessAndFailure(t *testing.T) {
	primary := &recordingChatSink{}
	var broadcasted []ChatEvent
	sink := &broadcastingEventSink{
		primary:        primary,
		conversationID: "conv-123",
		broadcast: func(conversationID string, event ChatEvent) {
			assert.Equal(t, "conv-123", conversationID)
			broadcasted = append(broadcasted, event)
		},
	}
	event := ChatEvent{Kind: "text", ConversationID: "conv-123", Role: "assistant", Content: "hi"}

	require.NoError(t, sink.Send(event))
	assert.Equal(t, []ChatEvent{event}, primary.Events())
	assert.Equal(t, []ChatEvent{event}, broadcasted)

	wantErr := errors.New("write failed")
	failingPrimary := &failingChatSink{err: wantErr}
	failing := &broadcastingEventSink{
		primary:        failingPrimary,
		conversationID: "conv-123",
		broadcast: func(_ string, event ChatEvent) {
			broadcasted = append(broadcasted, event)
		},
	}

	secondEvent := ChatEvent{Kind: "text", ConversationID: "conv-123", Role: "assistant", Content: "again"}
	require.NoError(t, failing.Send(event))
	require.NoError(t, failing.Send(secondEvent))
	assert.Equal(t, 1, failingPrimary.calls)
	assert.Equal(t, []ChatEvent{event, event, secondEvent}, broadcasted)
}
