package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	err error
}

func (s failingChatSink) Send(ChatEvent) error {
	return s.err
}

func TestNDJSONEventSinkRequiresFlusherAndWritesLines(t *testing.T) {
	_, err := newNDJSONEventSink(newPlainResponseWriter())
	require.ErrorContains(t, err, "streaming is not supported")

	recorder := httptest.NewRecorder()
	sink, err := newNDJSONEventSink(recorder)
	require.NoError(t, err)
	require.NoError(t, sink.Send(ChatEvent{Kind: "text", Content: "hello", Role: "assistant"}))

	assert.True(t, recorder.Flushed)
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Len(t, lines, 1)
	var event ChatEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &event))
	assert.Equal(t, "text", event.Kind)
	assert.Equal(t, "hello", event.Content)
	assert.Equal(t, "assistant", event.Role)
}

func TestNDJSONEventSinkReportsMarshalErrors(t *testing.T) {
	sink, err := newNDJSONEventSink(httptest.NewRecorder())
	require.NoError(t, err)

	err = sink.Send(ChatEvent{Kind: "bad", Content: func() {}})
	require.ErrorContains(t, err, "failed to marshal chat event")
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
	assert.Equal(t, []ChatEvent{event}, primary.events)
	assert.Equal(t, []ChatEvent{event}, broadcasted)

	wantErr := errors.New("write failed")
	failing := &broadcastingEventSink{
		primary:        failingChatSink{err: wantErr},
		conversationID: "conv-123",
		broadcast: func(_ string, event ChatEvent) {
			broadcasted = append(broadcasted, event)
		},
	}

	require.ErrorIs(t, failing.Send(event), wantErr)
	assert.Equal(t, []ChatEvent{event, event}, broadcasted)
}
