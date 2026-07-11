package responses

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketContinuationUsesIncrementalClientItems(t *testing.T) {
	user := responseMessageItem(openairesponses.EasyInputMessageRoleUser, "run the tools")
	firstCall := responseFunctionCallItem("call_1", "first")
	firstOutput := responseFunctionCallOutputItem("call_1", "first result")
	secondCall := responseFunctionCallItem("call_2", "second")
	secondOutput := responseFunctionCallOutputItem("call_2", "second result")

	initialParams := responseContinuationTestParams([]openairesponses.ResponseInputItemUnionParam{user})
	continuation := responsesWebSocketContinuation{}
	continuation.commit(7, initialParams, processStreamResult{
		responseCompleted: true,
		responseID:        "resp_1",
		serverKnownItems:  []openairesponses.ResponseInputItemUnionParam{firstCall, secondCall},
	})

	currentParams := responseContinuationTestParams([]openairesponses.ResponseInputItemUnionParam{
		user,
		firstCall,
		firstOutput,
		secondCall,
		secondOutput,
	})
	prepared := continuation.prepare(currentParams, 7)

	require.True(t, prepared.PreviousResponseID.Valid())
	assert.Equal(t, "resp_1", prepared.PreviousResponseID.Value)
	require.Len(t, prepared.Input.OfInputItemList, 2)
	assert.True(t, responsesInputItemEqual(firstOutput, prepared.Input.OfInputItemList[0]))
	assert.True(t, responsesInputItemEqual(secondOutput, prepared.Input.OfInputItemList[1]))
}

func TestResponsesWebSocketContinuationFallsBackToFullInput(t *testing.T) {
	user := responseMessageItem(openairesponses.EasyInputMessageRoleUser, "hello")
	assistant := responseMessageItem(openairesponses.EasyInputMessageRoleAssistant, "hi")
	nextUser := responseMessageItem(openairesponses.EasyInputMessageRoleUser, "again")
	initialParams := responseContinuationTestParams([]openairesponses.ResponseInputItemUnionParam{user})

	continuation := responsesWebSocketContinuation{}
	continuation.commit(3, initialParams, processStreamResult{
		responseCompleted: true,
		responseID:        "resp_1",
		serverKnownItems:  []openairesponses.ResponseInputItemUnionParam{assistant},
	})
	fullParams := responseContinuationTestParams([]openairesponses.ResponseInputItemUnionParam{user, assistant, nextUser})

	t.Run("new connection generation", func(t *testing.T) {
		prepared := continuation.prepare(fullParams, 4)
		assert.False(t, prepared.PreviousResponseID.Valid())
		assert.Equal(t, fullParams.Input.OfInputItemList, prepared.Input.OfInputItemList)
	})

	t.Run("request properties changed", func(t *testing.T) {
		changed := fullParams
		changed.Model = "gpt-5.6"
		prepared := continuation.prepare(changed, 3)
		assert.False(t, prepared.PreviousResponseID.Valid())
		assert.Equal(t, changed.Input.OfInputItemList, prepared.Input.OfInputItemList)
	})

	t.Run("local history is incompatible", func(t *testing.T) {
		incompatible := responseContinuationTestParams([]openairesponses.ResponseInputItemUnionParam{nextUser})
		prepared := continuation.prepare(incompatible, 3)
		assert.False(t, prepared.PreviousResponseID.Valid())
		assert.Equal(t, incompatible.Input.OfInputItemList, prepared.Input.OfInputItemList)
	})
}

func TestProcessMessageExchangeUsesIncrementalWebSocketContinuation(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			responseMessageItem(openairesponses.EasyInputMessageRoleUser, "run a command"),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "run a command"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	var requests []openairesponses.ResponseNewParams
	fakeStreamer := &fakeResponsesWebSocketStreamer{
		generation: 11,
		streamFunc: func(_ context.Context, params openairesponses.ResponseNewParams, _ []string, _ auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			requests = append(requests, params)
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.webSocket = fakeStreamer

	functionCall := responseFunctionCallItem("call_1", "shell")
	functionOutput := responseFunctionCallOutputItem("call_1", "ok")
	attempt := 0
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		attempt++
		if attempt == 1 {
			thread.inputItems = append(thread.inputItems, functionCall, functionOutput)
			return processStreamResult{
				responseCompleted: true,
				responseID:        "resp_1",
				serverKnownItems:  []openairesponses.ResponseInputItemUnionParam{functionCall},
			}, nil
		}
		return processStreamResult{responseCompleted: true, responseID: "resp_2"}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	_, _, _, err = thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)

	require.Len(t, requests, 2)
	assert.False(t, requests[0].PreviousResponseID.Valid())
	require.True(t, requests[1].PreviousResponseID.Valid())
	assert.Equal(t, "resp_1", requests[1].PreviousResponseID.Value)
	require.Len(t, requests[1].Input.OfInputItemList, 1)
	assert.True(t, responsesInputItemEqual(functionOutput, requests[1].Input.OfInputItemList[0]))
}

func TestProcessMessageExchangeRetriesMissingContinuationWithFullInput(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     2,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	firstUser := responseMessageItem(openairesponses.EasyInputMessageRoleUser, "hello")
	assistant := responseMessageItem(openairesponses.EasyInputMessageRoleAssistant, "hi")
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems:   []openairesponses.ResponseInputItemUnionParam{firstUser},
		storedItems:  []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	var requests []openairesponses.ResponseNewParams
	thread.webSocket = &fakeResponsesWebSocketStreamer{
		generation: 5,
		streamFunc: func(_ context.Context, params openairesponses.ResponseNewParams, _ []string, _ auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			requests = append(requests, params)
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}

	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		thread.inputItems = append(thread.inputItems, assistant)
		return processStreamResult{
			responseCompleted: true,
			responseID:        "resp_1",
			serverKnownItems:  []openairesponses.ResponseInputItemUnionParam{assistant},
		}, nil
	}
	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)

	thread.AddUserMessage(context.Background(), "again")
	secondAttempt := 0
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		secondAttempt++
		if secondAttempt == 1 {
			return processStreamResult{}, &responsesWebSocketEventError{
				statusCode: 400,
				code:       "previous_response_not_found",
				message:    "previous response is no longer available",
			}
		}
		return processStreamResult{responseCompleted: true, responseID: "resp_2"}, nil
	}
	_, _, _, err = thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)

	require.Len(t, requests, 3)
	require.True(t, requests[1].PreviousResponseID.Valid())
	assert.Equal(t, "resp_1", requests[1].PreviousResponseID.Value)
	require.Len(t, requests[1].Input.OfInputItemList, 1)
	assert.Equal(t, "again", extractInputItemText(requests[1].Input.OfInputItemList[0]))
	assert.False(t, requests[2].PreviousResponseID.Valid())
	require.Len(t, requests[2].Input.OfInputItemList, 3)
}

func responseContinuationTestParams(input []openairesponses.ResponseInputItemUnionParam) openairesponses.ResponseNewParams {
	return openairesponses.ResponseNewParams{
		Model:        "gpt-5.5",
		Instructions: param.NewOpt("system"),
		Store:        param.NewOpt(false),
		Input: openairesponses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
	}
}

func responseMessageItem(role openairesponses.EasyInputMessageRole, text string) openairesponses.ResponseInputItemUnionParam {
	return openairesponses.ResponseInputItemUnionParam{
		OfMessage: &openairesponses.EasyInputMessageParam{
			Role:    role,
			Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(text)},
		},
	}
}

func responseFunctionCallItem(callID, name string) openairesponses.ResponseInputItemUnionParam {
	return openairesponses.ResponseInputItemUnionParam{
		OfFunctionCall: &openairesponses.ResponseFunctionToolCallParam{
			CallID:    callID,
			Name:      name,
			Arguments: `{}`,
		},
	}
}

func responseFunctionCallOutputItem(callID, output string) openairesponses.ResponseInputItemUnionParam {
	return openairesponses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: callID,
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfString: param.NewOpt(output),
			},
		},
	}
}
