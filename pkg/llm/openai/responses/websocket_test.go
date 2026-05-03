package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseCreateWebSocketRequestMarshalMirrorsResponsesCreateBody(t *testing.T) {
	params := openairesponses.ResponseNewParams{
		Model: "gpt-4.1",
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
	assert.Equal(t, "gpt-4.1", payload["model"])
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

func TestCreateResponsesStreamUsesConfiguredWebSocketTransport(t *testing.T) {
	thread := &Thread{useWebSocket: true}
	params := openairesponses.ResponseNewParams{
		Model: "gpt-4.1",
		Store: param.NewOpt(false),
	}
	expectedStream := ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil)

	var captured openairesponses.ResponseNewParams
	thread.webSocket = &fakeResponsesWebSocketStreamer{
		streamFunc: func(_ context.Context, params openairesponses.ResponseNewParams, _ []string, _ auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			captured = params
			return expectedStream, nil
		},
	}

	stream, usedWebSocket, err := thread.createResponsesStream(context.Background(), params, nil)
	require.NoError(t, err)
	assert.True(t, usedWebSocket)
	assert.Same(t, expectedStream, stream)
	require.True(t, captured.Store.Valid())
	assert.False(t, captured.Store.Value)
}

func TestCreateResponsesStreamDisablesWebSocketOnTransportError(t *testing.T) {
	thread := &Thread{useWebSocket: true}
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			return nil, assert.AnError
		},
	}
	thread.webSocket = fakeStreamer

	stream, usedWebSocket, err := thread.createResponsesStream(context.Background(), openairesponses.ResponseNewParams{}, nil)
	require.Error(t, err)
	assert.False(t, usedWebSocket)
	assert.Nil(t, stream)
	assert.True(t, thread.disableWebSocket.Load())
	assert.False(t, fakeStreamer.closed)
}

func TestProcessMessageExchangeClosesWebSocketAfterStreamError(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
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
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		t.Fatal("HTTP streaming fallback should not run after websocket stream creation succeeds")
		return nil
	}
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{}, assert.AnError
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.Error(t, err)
	assert.True(t, thread.disableWebSocket.Load())
	assert.True(t, fakeStreamer.closed)
}
