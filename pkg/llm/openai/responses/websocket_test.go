package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseCreateWebSocketRequestMarshalMirrorsResponsesCreateBody(t *testing.T) {
	params := openairesponses.ResponseNewParams{
		Model: "gpt-5.5",
		Input: openairesponses.ResponseNewParamsInputUnion{
			OfInputItemList: openairesponses.ResponseInputParam{
				{
					OfMessage: &openairesponses.EasyInputMessageParam{
						Role: openairesponses.EasyInputMessageRoleUser,
						Content: openairesponses.EasyInputMessageContentUnionParam{
							OfString: param.NewOpt("hello"),
						},
					},
				},
			},
		},
		Instructions:       param.NewOpt("system"),
		Store:              param.NewOpt(false),
		PromptCacheKey:     param.NewOpt("conv-test"),
		Background:         param.NewOpt(false),
		StreamOptions:      openairesponses.ResponseNewParamsStreamOptions{IncludeObfuscation: param.NewOpt(true)},
		PreviousResponseID: param.Opt[string]{},
	}

	data, err := json.Marshal(responseCreateWebSocketRequest{Params: params})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.Equal(t, "response.create", payload["type"])
	assert.Equal(t, "gpt-5.5", payload["model"])
	assert.Equal(t, "system", payload["instructions"])
	assert.Equal(t, false, payload["store"])
	assert.Equal(t, "conv-test", payload["prompt_cache_key"])
	assert.NotContains(t, payload, "background")
	assert.NotContains(t, payload, "stream")
	assert.NotContains(t, payload, "previous_response_id")
	assert.NotContains(t, payload, "stream_options")
}

func TestResponsesWebSocketURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "default",
			baseURL: "",
			want:    "wss://api.openai.com/v1/responses",
		},
		{
			name:    "https base",
			baseURL: "https://api.openai.com/v1/",
			want:    "wss://api.openai.com/v1/responses",
		},
		{
			name:    "http base",
			baseURL: "http://127.0.0.1:8080/v1?ignored=true",
			want:    "ws://127.0.0.1:8080/v1/responses",
		},
		{
			name:    "already websocket",
			baseURL: "wss://chatgpt.com/backend-api/codex",
			want:    "wss://chatgpt.com/backend-api/codex/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := responsesWebSocketURL(tt.baseURL)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResponsesWebSocketURLRejectsUnsupportedScheme(t *testing.T) {
	_, err := responsesWebSocketURL("ftp://api.openai.com/v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported websocket base URL scheme")
}

func TestWebSocketHandshakeErrorIsSafeToFormat(t *testing.T) {
	err := websocketHandshakeError(assert.AnError, &http.Response{StatusCode: http.StatusUpgradeRequired})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 426 Upgrade Required")
}

func TestIsRetryableResponsesWebSocketHandshakeStatusMatchesCodex(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		retryable  bool
	}{
		{name: "bad request does not retry", statusCode: http.StatusBadRequest, retryable: false},
		{name: "forbidden retries as unexpected status", statusCode: http.StatusForbidden, retryable: true},
		{name: "upgrade required retries without fallback", statusCode: http.StatusUpgradeRequired, retryable: true},
		{name: "request timeout retries", statusCode: http.StatusRequestTimeout, retryable: true},
		{name: "conflict retries", statusCode: http.StatusConflict, retryable: true},
		{name: "too many requests does not retry", statusCode: http.StatusTooManyRequests, retryable: false},
		{name: "internal server error retries", statusCode: http.StatusInternalServerError, retryable: true},
		{name: "generic service unavailable retries", statusCode: http.StatusServiceUnavailable, retryable: true},
		{name: "overloaded service unavailable does not retry", statusCode: http.StatusServiceUnavailable, body: `{"error":{"code":"server_is_overloaded"}}`, retryable: false},
		{name: "slow down service unavailable does not retry", statusCode: http.StatusServiceUnavailable, body: `{"error":{"code":"slow_down"}}`, retryable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &websocketHandshakeStatusError{statusCode: tt.statusCode, body: tt.body}
			assert.Equal(t, tt.retryable, isRetryableResponsesStreamError(err))
		})
	}
}

func TestParseResponsesWebSocketEventErrorSupportsStandardAndWrappedFields(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		statusCode int
		code       string
		message    string
		retryable  bool
	}{
		{
			name:      "standard top-level fields",
			payload:   `{"type":"error","code":"invalid_prompt","message":"bad prompt","param":null,"sequence_number":1}`,
			code:      "invalid_prompt",
			message:   "bad prompt",
			retryable: false,
		},
		{
			name:       "transport-wrapped fields",
			payload:    `{"type":"error","status":400,"error":{"code":"websocket_connection_limit_reached","message":"connection reached 60 minute limit"}}`,
			statusCode: http.StatusBadRequest,
			code:       "websocket_connection_limit_reached",
			message:    "connection reached 60 minute limit",
			retryable:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseResponsesWebSocketEventError([]byte(tt.payload))
			require.Error(t, err)

			var eventErr *responsesWebSocketEventError
			require.ErrorAs(t, err, &eventErr)
			assert.Equal(t, tt.statusCode, eventErr.statusCode)
			assert.Equal(t, tt.code, eventErr.code)
			assert.Equal(t, tt.message, eventErr.message)
			assert.Equal(t, tt.message, eventErr.Error())
			assert.Equal(t, tt.retryable, isRetryableResponsesStreamError(err))
		})
	}
}

func TestResponsesWebSocketTransportSetsBetaHeader(t *testing.T) {
	tests := []struct {
		name       string
		authorizer auth.HTTPAuthorizer
	}{
		{name: "without authorizer"},
		{
			name: "after authorizer override",
			authorizer: auth.AuthorizerFunc(func(req *http.Request) error {
				req.Header.Set("OpenAI-Beta", "responses=experimental")
				return nil
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headerSeen := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headerSeen = r.Header.Get("OpenAI-Beta")
				http.Error(w, "stop after handshake headers", http.StatusUpgradeRequired)
			}))
			defer server.Close()

			transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
			_, err := transport.connectionLocked(context.Background(), nil, tt.authorizer)
			require.Error(t, err)
			assert.Equal(t, responsesWebSocketBetaHeaderValue, headerSeen)
		})
	}
}

type fakeResponsesWebSocketStreamer struct {
	streamFunc func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error)
	generation uint64
	closed     bool
}

func (f *fakeResponsesWebSocketStreamer) Stream(
	ctx context.Context,
	paramsFactory responsesWebSocketParamsFactory,
	requestHeaders []string,
	authorizer auth.HTTPAuthorizer,
) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], uint64, error) {
	generation := f.generation
	if generation == 0 {
		generation = 1
	}
	stream, err := f.streamFunc(ctx, paramsFactory(generation), requestHeaders, authorizer)
	return stream, generation, err
}

func (f *fakeResponsesWebSocketStreamer) Close() error {
	f.closed = true
	return nil
}

type emptyResponsesStreamDecoder struct{}

func (emptyResponsesStreamDecoder) Event() ssestream.Event { return ssestream.Event{} }
func (emptyResponsesStreamDecoder) Next() bool             { return false }
func (emptyResponsesStreamDecoder) Close() error           { return nil }
func (emptyResponsesStreamDecoder) Err() error             { return nil }

func TestWebSocketStreamDecoderKeepsTransportAfterCompletedEvent(t *testing.T) {
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		require.NoError(t, err)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_test","status":"completed","usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`)))
		<-releaseServer
	}))
	defer server.Close()

	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	defer func() { require.NoError(t, transport.Close()) }()
	stream, _, err := transport.Stream(
		context.Background(),
		func(uint64) openairesponses.ResponseNewParams {
			return openairesponses.ResponseNewParams{Model: "gpt-5.5"}
		},
		nil,
		nil,
	)
	require.NoError(t, err)
	require.True(t, stream.Next())
	assert.Equal(t, "response.completed", stream.Current().Type)
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())

	transport.mu.Lock()
	assert.NotNil(t, transport.conn)
	transport.mu.Unlock()
	close(releaseServer)
}

func TestResponsesWebSocketTransportReusesConnectionAndHandlesIdlePing(t *testing.T) {
	var handshakes atomic.Int32
	requests := make(chan map[string]any, 2)
	pongSeen := make(chan struct{})
	var pongOnce sync.Once
	releaseServer := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		handshakes.Add(1)
		conn.SetPongHandler(func(string) error {
			pongOnce.Do(func() { close(pongSeen) })
			return nil
		})

		for i := 1; i <= 2; i++ {
			_, data, err := conn.ReadMessage()
			if !assert.NoError(t, err) {
				return
			}
			var payload map[string]any
			if !assert.NoError(t, json.Unmarshal(data, &payload)) {
				return
			}
			requests <- payload
			if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, completedWebSocketEvent(fmt.Sprintf("resp_%d", i)))) {
				return
			}
			if i == 1 {
				if !assert.NoError(t, conn.WriteControl(websocket.PingMessage, []byte("idle"), time.Now().Add(time.Second))) {
					return
				}
			}
		}
		<-releaseServer
	}))
	defer server.Close()

	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	defer func() { require.NoError(t, transport.Close()) }()
	paramsFactory := func(uint64) openairesponses.ResponseNewParams {
		return openairesponses.ResponseNewParams{Model: "gpt-5.5"}
	}

	first, firstGeneration, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
	require.NoError(t, err)
	require.True(t, first.Next())
	assert.Equal(t, "response.completed", first.Current().Type)
	assert.False(t, first.Next())
	require.NoError(t, first.Err())
	require.NoError(t, first.Close())

	select {
	case <-pongSeen:
	case <-time.After(time.Second):
		t.Fatal("persistent websocket did not answer an idle ping")
	}

	second, secondGeneration, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
	require.NoError(t, err)
	require.True(t, second.Next())
	assert.Equal(t, "response.completed", second.Current().Type)
	assert.False(t, second.Next())
	require.NoError(t, second.Err())
	require.NoError(t, second.Close())

	assert.Equal(t, firstGeneration, secondGeneration)
	assert.Equal(t, int32(1), handshakes.Load())
	firstRequest := <-requests
	secondRequest := <-requests
	assert.Equal(t, "response.create", firstRequest["type"])
	assert.Equal(t, "response.create", secondRequest["type"])
	close(releaseServer)
}

func TestResponsesThreadReusesConnectionWithIncrementalToolOutput(t *testing.T) {
	var handshakes atomic.Int32
	requests := make(chan map[string]any, 2)
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		handshakes.Add(1)

		for requestIndex := 0; requestIndex < 2; requestIndex++ {
			_, data, err := conn.ReadMessage()
			if !assert.NoError(t, err) {
				return
			}
			var payload map[string]any
			if !assert.NoError(t, json.Unmarshal(data, &payload)) {
				return
			}
			requests <- payload

			if requestIndex == 0 {
				functionCall := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"ok_tool","arguments":"{}"}}`)
				if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, functionCall)) {
					return
				}
			}
			if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, completedWebSocketEvent(fmt.Sprintf("resp_%d", requestIndex+1)))) {
				return
			}
		}
		<-releaseServer
	}))
	defer server.Close()

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		webSocket:    transport,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			responseMessageItem(openairesponses.EasyInputMessageRoleUser, "run a command"),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "run a command"}},
	}
	defer func() { require.NoError(t, thread.Close()) }()
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithExtensionTools([]tooltypes.Tool{responsesTestTool{name: "ok_tool"}})))
	handler := &llmtypes.StringCollectorHandler{Silent: true}

	_, toolsUsed, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.True(t, toolsUsed)
	assert.True(t, completed)
	_, _, completed, err = thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.True(t, completed)

	firstRequest := <-requests
	secondRequest := <-requests
	assert.NotContains(t, firstRequest, "previous_response_id")
	assert.Equal(t, "resp_1", secondRequest["previous_response_id"])
	secondInput, ok := secondRequest["input"].([]any)
	require.True(t, ok)
	require.Len(t, secondInput, 1)
	toolOutput, ok := secondInput[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "function_call_output", toolOutput["type"])
	assert.Equal(t, "call_1", toolOutput["call_id"])
	assert.Equal(t, int32(1), handshakes.Load())
	close(releaseServer)
}

func TestResponsesWebSocketTransportSerializesInFlightResponses(t *testing.T) {
	firstRequestSeen := make(chan struct{})
	allowFirstCompletion := make(chan struct{})
	secondRequestSeen := make(chan struct{})
	releaseServer := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); !assert.NoError(t, err) {
			return
		}
		close(firstRequestSeen)
		<-allowFirstCompletion
		if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, completedWebSocketEvent("resp_1"))) {
			return
		}

		if _, _, err := conn.ReadMessage(); !assert.NoError(t, err) {
			return
		}
		close(secondRequestSeen)
		if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, completedWebSocketEvent("resp_2"))) {
			return
		}
		<-releaseServer
	}))
	defer server.Close()

	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	defer func() { require.NoError(t, transport.Close()) }()
	paramsFactory := func(uint64) openairesponses.ResponseNewParams {
		return openairesponses.ResponseNewParams{Model: "gpt-5.5"}
	}

	first, _, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
	require.NoError(t, err)
	<-firstRequestSeen

	secondResult := make(chan *ssestream.Stream[openairesponses.ResponseStreamEventUnion], 1)
	secondErr := make(chan error, 1)
	go func() {
		stream, _, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
		secondResult <- stream
		secondErr <- err
	}()

	select {
	case <-secondRequestSeen:
		t.Fatal("second response.create was sent while the first response was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(allowFirstCompletion)
	require.True(t, first.Next())
	assert.False(t, first.Next())
	require.NoError(t, first.Err())
	require.NoError(t, first.Close())

	second := <-secondResult
	require.NoError(t, <-secondErr)
	select {
	case <-secondRequestSeen:
	case <-time.After(time.Second):
		t.Fatal("second response.create was not sent after the first response completed")
	}
	require.True(t, second.Next())
	assert.False(t, second.Next())
	require.NoError(t, second.Err())
	require.NoError(t, second.Close())
	close(releaseServer)
}

func TestResponsesWebSocketTransportReconnectsAfterConnectionLimit(t *testing.T) {
	var handshakes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		connection := handshakes.Add(1)
		if _, _, err := conn.ReadMessage(); !assert.NoError(t, err) {
			return
		}
		if connection == 1 {
			err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":400,"error":{"code":"websocket_connection_limit_reached","message":"connection reached 60 minute limit"}}`))
		} else {
			err = conn.WriteMessage(websocket.TextMessage, completedWebSocketEvent("resp_reconnected"))
		}
		assert.NoError(t, err)
		if connection > 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
	}))
	defer server.Close()

	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	defer func() { require.NoError(t, transport.Close()) }()
	paramsFactory := func(uint64) openairesponses.ResponseNewParams {
		return openairesponses.ResponseNewParams{Model: "gpt-5.5"}
	}

	first, firstGeneration, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
	require.NoError(t, err)
	assert.False(t, first.Next())
	var eventErr *responsesWebSocketEventError
	require.ErrorAs(t, first.Err(), &eventErr)
	assert.Equal(t, "websocket_connection_limit_reached", eventErr.code)
	assert.True(t, isRetryableResponsesStreamError(first.Err()))
	require.NoError(t, first.Close())

	second, secondGeneration, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
	require.NoError(t, err)
	require.True(t, second.Next())
	assert.False(t, second.Next())
	require.NoError(t, second.Err())
	require.NoError(t, second.Close())

	assert.NotEqual(t, firstGeneration, secondGeneration)
	assert.Equal(t, int32(2), handshakes.Load())
}

func TestResponsesWebSocketTransportReconnectsAfterCancellation(t *testing.T) {
	var handshakes atomic.Int32
	firstRequestSeen := make(chan struct{})
	releaseSecond := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		connection := handshakes.Add(1)
		if _, _, err := conn.ReadMessage(); !assert.NoError(t, err) {
			return
		}
		if connection == 1 {
			close(firstRequestSeen)
			_, _, _ = conn.ReadMessage()
			return
		}
		if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, completedWebSocketEvent("resp_after_cancel"))) {
			return
		}
		<-releaseSecond
	}))
	defer server.Close()

	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	defer func() { require.NoError(t, transport.Close()) }()
	paramsFactory := func(uint64) openairesponses.ResponseNewParams {
		return openairesponses.ResponseNewParams{Model: "gpt-5.5"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	first, firstGeneration, err := transport.Stream(ctx, paramsFactory, nil, nil)
	require.NoError(t, err)
	<-firstRequestSeen
	cancel()
	assert.False(t, first.Next())
	assert.ErrorIs(t, first.Err(), context.Canceled)
	require.NoError(t, first.Close())

	second, secondGeneration, err := transport.Stream(context.Background(), paramsFactory, nil, nil)
	require.NoError(t, err)
	require.True(t, second.Next())
	assert.False(t, second.Next())
	require.NoError(t, second.Err())
	require.NoError(t, second.Close())
	assert.NotEqual(t, firstGeneration, secondGeneration)
	assert.Equal(t, int32(2), handshakes.Load())
	close(releaseSecond)
}

func TestResponsesThreadCloseClosesPersistentWebSocket(t *testing.T) {
	fakeStreamer := &fakeResponsesWebSocketStreamer{}
	thread := &Thread{
		webSocket: fakeStreamer,
		webSocketContinuation: responsesWebSocketContinuation{
			connectionGeneration: 1,
			responseID:           "resp_1",
		},
	}

	require.NoError(t, thread.Close())
	assert.True(t, fakeStreamer.closed)
	assert.Empty(t, thread.webSocketContinuation.responseID)
}

func completedWebSocketEvent(responseID string) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"response.completed","response":{"id":%q,"status":"completed","usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`,
		responseID,
	))
}

func TestProcessMessageExchangeDoesNotCloseWebSocketTransportAfterStreamError(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-5.5", Retry: llmtypes.RetryConfig{Attempts: 1}, OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{}, assert.AnError
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.Error(t, err)
	assert.False(t, fakeStreamer.closed)
}

func TestProcessMessageExchangeFailsWhenWebSocketCreationFails(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-5.5", Retry: llmtypes.RetryConfig{Attempts: 1}, OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			return nil, assert.AnError
		},
	}
	thread.webSocket = fakeStreamer

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create Responses API websocket stream")
	assert.False(t, fakeStreamer.closed)
}

func TestProcessMessageExchangeRetriesWebSocketCreationFailure(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	attempts := 0
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			attempts++
			if attempts < 3 {
				return nil, assert.AnError
			}
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, 3, attempts)
}

func TestProcessMessageExchangeRetriesWebSocketForbiddenHandshakeFailure(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	attempts := 0
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			attempts++
			if attempts < 3 {
				return nil, &websocketHandshakeStatusError{message: "forbidden", statusCode: http.StatusForbidden}
			}
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, 3, attempts)
}

func TestProcessMessageExchangeRetriesWebSocketStreamAfterPresentationOnlyOutput(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	attempts := 0
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			attempts++
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		if attempts == 1 {
			thread.pendingReasoning.WriteString("partial reasoning")
			return processStreamResult{}, assert.AnError
		}
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, 2, attempts)
	assert.Empty(t, thread.pendingReasoning.String())
}

func TestProcessMessageExchangeKeepsWebSocketStreamDurableStateBeforeRetry(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	attempts := 0
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			attempts++
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		if attempts == 1 {
			thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role:    openairesponses.EasyInputMessageRoleAssistant,
					Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("first attempt")},
				},
			})
			thread.storedItems = append(thread.storedItems, StoredInputItem{Type: "message", Role: "assistant", Content: "first attempt"})
			return processStreamResult{}, assert.AnError
		}
		thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleAssistant,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("second attempt")},
			},
		})
		thread.storedItems = append(thread.storedItems, StoredInputItem{Type: "message", Role: "assistant", Content: "second attempt"})
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, "second attempt", output)
	assert.Equal(t, 2, attempts)
	require.Len(t, thread.storedItems, 3)
	assert.Equal(t, "hello", thread.storedItems[0].Content)
	assert.Equal(t, "first attempt", thread.storedItems[1].Content)
	assert.Equal(t, "second attempt", thread.storedItems[2].Content)
}

func TestProcessMessageExchangeRetriesWebSocketStreamAfterPendingToolCallOnly(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{
						OfString: param.NewOpt("hello"),
					},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	attempts := 0
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			attempts++
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		if attempts == 1 {
			return processStreamResult{toolsUsed: true}, assert.AnError
		}
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, 2, attempts)
}
