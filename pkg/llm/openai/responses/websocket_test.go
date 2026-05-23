package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
	closed     bool
}

func (f *fakeResponsesWebSocketStreamer) Stream(
	ctx context.Context,
	params openairesponses.ResponseNewParams,
	requestHeaders []string,
	authorizer auth.HTTPAuthorizer,
) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
	return f.streamFunc(ctx, params, requestHeaders, authorizer)
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

func TestWebSocketStreamDecoderClosesTransportAfterTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		require.NoError(t, err)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_test","status":"completed","usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`)))
	}))
	defer server.Close()

	transport := newResponsesWebSocketTransport("http" + strings.TrimPrefix(server.URL, "http") + "/v1")
	stream, err := transport.Stream(context.Background(), openairesponses.ResponseNewParams{Model: "gpt-5.5"}, nil, nil)
	require.NoError(t, err)
	require.True(t, stream.Next())
	assert.Equal(t, "response.completed", stream.Current().Type)
	require.NoError(t, stream.Err())

	transport.mu.Lock()
	defer transport.mu.Unlock()
	assert.Nil(t, transport.conn)
}

func TestProcessMessageExchangeClosesWebSocketAfterStreamError(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-5.5", Retry: llmtypes.RetryConfig{Attempts: 1}, OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
	assert.True(t, fakeStreamer.closed)
}

func TestProcessMessageExchangeFailsWhenWebSocketCreationFails(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-5.5", Retry: llmtypes.RetryConfig{Attempts: 1}, OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
	assert.True(t, fakeStreamer.closed)
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
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
		Thread:       base.NewThread(config, "conv-test", hooks.Trigger{}),
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
