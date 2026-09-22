package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responseParamsBody(t *testing.T, params openairesponses.ResponseNewParams) map[string]any {
	t.Helper()
	raw, err := params.MarshalJSON()
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	return body
}

func codexClientMetadataFromParams(t *testing.T, params openairesponses.ResponseNewParams) map[string]any {
	t.Helper()
	clientMetadata, ok := responseParamsBody(t, params)["client_metadata"].(map[string]any)
	require.True(t, ok)
	return clientMetadata
}

func codexTurnMetadataFromClientMetadata(t *testing.T, clientMetadata map[string]any) map[string]any {
	t.Helper()
	encoded, ok := clientMetadata[auth.CodexTurnMetadataHeader].(string)
	require.True(t, ok)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(encoded), &metadata))
	return metadata
}

func TestNewThread(t *testing.T) {
	for _, tt := range []struct {
		name      string
		apiKey    string
		openAI    llmtypes.OpenAIConfig
		webSocket bool
		wantError string
	}{
		{name: "default websocket", apiKey: "test-key", openAI: llmtypes.OpenAIConfig{Platform: "openai"}, webSocket: true},
		{name: "disabled websocket", apiKey: "test-key", openAI: llmtypes.OpenAIConfig{Platform: "openai", WebSocketMode: new(false)}},
		{name: "custom API key", openAI: llmtypes.OpenAIConfig{Platform: "fireworks", APIKeyEnvVar: "MY_CUSTOM_API_KEY"}, webSocket: true},
		{name: "missing API key", openAI: llmtypes.OpenAIConfig{Platform: "openai"}, wantError: "OPENAI_API_KEY"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", tt.apiKey)
			t.Setenv("MY_CUSTOM_API_KEY", "test-key")
			tt.openAI.APIMode = llmtypes.OpenAIAPIModeResponses
			thread, err := NewThread(llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &tt.openAI})
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, thread.Close()) })
			assert.Equal(t, "openai", thread.Provider())
			assert.NotNil(t, thread.inputItems)
			assert.Empty(t, thread.inputItems)
			assert.Equal(t, tt.webSocket, thread.useWebSocket)
			assert.Equal(t, tt.webSocket && tt.openAI.Platform == "openai", thread.webSocket != nil)
		})
	}
}

func TestNewThreadPlatformModelDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "test-key")
	for _, tt := range []struct {
		name     string
		platform string
		model    string
		want     string
	}{
		{name: "OpenAI", platform: "openai", want: "gpt-6-astra"},
		{name: "Codex", platform: "codex", want: "gpt-6-astra"},
		{name: "normalized Codex", platform: " CODEX ", want: "gpt-6-astra"},
		{name: "explicit model", platform: "codex", model: "gpt-5.5", want: "gpt-5.5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			thread, err := NewThread(llmtypes.Config{
				Model:  tt.model,
				OpenAI: &llmtypes.OpenAIConfig{Platform: tt.platform},
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, thread.Close()) })
			assert.Equal(t, tt.want, thread.GetConfig().Model)
		})
	}
}

func TestSupportsResponsesWebSocket(t *testing.T) {
	tests := []struct {
		name   string
		config llmtypes.Config
		want   bool
	}{
		{
			name:   "default openai platform",
			config: llmtypes.Config{},
			want:   true,
		},
		{
			name: "codex platform",
			config: llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{
				Platform: "codex",
			}},
			want: true,
		},
		{
			name: "custom platform",
			config: llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{
				Platform: "fireworks",
			}},
			want: false,
		},
		{
			name: "custom openai base url",
			config: llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{
				Platform: "openai",
				BaseURL:  "https://example.test/v1",
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, supportsResponsesWebSocket(tt.config))
		})
	}
}

func TestIsReasoningModelDynamic(t *testing.T) {
	thread := &Thread{
		customModels: map[string]string{
			"gpt-5":   "reasoning",
			"gpt-4.1": "non-reasoning",
		},
	}

	tests := []struct {
		model    string
		expected bool
	}{
		{"gpt-5", true},
		{"gpt-4.1", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, thread.isReasoningModelDynamic(tt.model))
		})
	}
}

func TestUtilityThreadConfigPreservesStickyHTTPSFallback(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{
			Provider: "openai",
			Model:    "gpt-5.5",
			OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
		}, "conv-utility-transport"),
		useWebSocket: false,
	}

	config := thread.utilityThreadConfig()

	require.NotNil(t, config.OpenAI)
	require.NotNil(t, config.OpenAI.WebSocketMode)
	assert.False(t, *config.OpenAI.WebSocketMode)
	assert.Nil(t, thread.Config.OpenAI.WebSocketMode, "utility transport override must not mutate the parent config")
}

func TestProcessMessageExchangeUsesStableCodexWebSocketHeaders(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-test"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 2,
		useWebSocket:          true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	var capturedHeaders []string
	var capturedParams openairesponses.ResponseNewParams
	thread.webSocket = &fakeResponsesWebSocketStreamer{
		streamFunc: func(_ context.Context, params openairesponses.ResponseNewParams, headers []string, _ auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			capturedParams = params
			capturedHeaders = append([]string(nil), headers...)
			return ssestream.NewStream[openairesponses.ResponseStreamEventUnion](emptyResponsesStreamDecoder{}, nil), nil
		},
	}
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	_, _, _, err := thread.processMessageExchange(
		context.WithValue(context.Background(), codexTurnIDContextKey{}, "turn-normal"),
		&llmtypes.StringCollectorHandler{Silent: true},
		"gpt-5.5",
		256,
		"system",
		llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	assert.Contains(t, capturedHeaders, auth.CodexBetaFeaturesHeader+": "+auth.CodexBetaFeatures)
	assert.Contains(t, capturedHeaders, "session-id: conv-test")
	assert.Contains(t, capturedHeaders, "thread-id: conv-test")
	assert.Contains(t, capturedHeaders, auth.CodexInstallationIDHeader+": "+thread.codexInstallationID)
	assert.Contains(t, capturedHeaders, auth.CodexWindowIDHeader+": conv-test:2")
	clientMetadata := codexClientMetadataFromParams(t, capturedParams)
	assert.Equal(t, "turn-normal", clientMetadata["turn_id"])
	assert.Equal(t, "conv-test:2", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "turn", turnMetadata["request_kind"])
	assert.Equal(t, "turn-normal", turnMetadata["turn_id"])
	assert.NotContains(t, turnMetadata, "compaction")
}

func TestProcessMessageExchangeCodexHTTPSProjectsRequestMetadata(t *testing.T) {
	var requestBody map[string]any
	var requestHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		requestHeaders = r.Header.Clone()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_normal\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1,\"output_tokens_details\":{\"reasoning_tokens\":0},\"total_tokens\":2}}}\n\n"))
		require.NoError(t, err)
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex", BaseURL: server.URL},
	}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-normal-https"),
		client:                &client,
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 6,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.newStreamingFunc = client.Responses.NewStreaming

	_, _, completed, err := thread.processMessageExchange(
		context.WithValue(context.Background(), codexTurnIDContextKey{}, "turn-normal-https"),
		&llmtypes.StringCollectorHandler{Silent: true},
		"gpt-5.5",
		256,
		"system",
		llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, auth.CodexBetaFeatures, requestHeaders.Get(auth.CodexBetaFeaturesHeader))
	assert.Equal(t, "conv-normal-https", requestHeaders.Get("session-id"))
	assert.Equal(t, "conv-normal-https", requestHeaders.Get("thread-id"))
	assert.Equal(t, thread.codexInstallationID, requestHeaders.Get(auth.CodexInstallationIDHeader))
	assert.Equal(t, "conv-normal-https:6", requestHeaders.Get(auth.CodexWindowIDHeader))

	clientMetadata, ok := requestBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "turn-normal-https", clientMetadata["turn_id"])
	assert.Equal(t, "conv-normal-https:6", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "turn", turnMetadata["request_kind"])
	assert.Equal(t, "turn-normal-https", turnMetadata["turn_id"])
	assert.NotContains(t, turnMetadata, "compaction")
	assert.JSONEq(t, clientMetadata[auth.CodexTurnMetadataHeader].(string), requestHeaders.Get(auth.CodexTurnMetadataHeader))
}

func TestLoadCustomConfiguration(t *testing.T) {
	config := llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{
			Models: &llmtypes.CustomModels{
				Reasoning:    []string{"custom-o1", "custom-o3"},
				NonReasoning: []string{"custom-gpt"},
			},
			Pricing: map[string]llmtypes.ModelPricing{
				"custom-model": {
					Input:         0.001,
					Output:        0.002,
					ContextWindow: 128000,
				},
			},
		},
	}

	customModels, customPricing := loadCustomConfiguration(config)

	assert.Equal(t, "reasoning", customModels["custom-o1"])
	assert.Equal(t, "reasoning", customModels["custom-o3"])
	assert.Equal(t, "non-reasoning", customModels["custom-gpt"])

	pricing, ok := customPricing["custom-model"]
	require.True(t, ok)
	assert.Equal(t, 0.001, pricing.Input)
	assert.Equal(t, 0.002, pricing.Output)
	assert.Equal(t, 128000, pricing.ContextWindow)
}

func TestLoadCustomConfigurationDefaultPlatform(t *testing.T) {
	// When no config is provided, default "openai" platform defaults should be loaded.
	config := llmtypes.Config{}

	customModels, customPricing := loadCustomConfiguration(config)

	// Should load default OpenAI platform defaults.
	assert.NotEmpty(t, customModels)
	assert.NotEmpty(t, customPricing)

	// Verify some known OpenAI models are present
	assert.Equal(t, "reasoning", customModels["o1"])
	assert.Equal(t, "reasoning", customModels["o3"])
	assert.Equal(t, "non-reasoning", customModels["gpt-4o"])

	// Verify pricing is loaded
	_, hasGPT4o := customPricing["gpt-4o"]
	assert.True(t, hasGPT4o, "gpt-4o pricing should be present")
}

func TestLoadCustomConfigurationCodexPlatformUsesConfiguredServiceTierPricing(t *testing.T) {
	standardModels, standardPricing := loadCustomConfiguration(llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"},
	})
	fastModels, fastPricing := loadCustomConfiguration(llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "codex",
			ServiceTier: llmtypes.OpenAIServiceTierFast,
		},
	})

	assert.Equal(t, "reasoning", standardModels["gpt-5.3-codex"])
	assert.Equal(t, standardModels, fastModels)

	standardCodex := standardPricing["gpt-5.3-codex"]
	assert.Equal(t, 0.00000175, standardCodex.Input)
	assert.Equal(t, 0.000000175, standardCodex.CachedInput)
	assert.Equal(t, 0.000014, standardCodex.Output)
	assert.Equal(t, 0, standardCodex.LongContextThreshold)

	fastCodex := fastPricing["gpt-5.3-codex"]
	assert.Equal(t, 0.0000035, fastCodex.Input)
	assert.Equal(t, 0.00000035, fastCodex.CachedInput)
	assert.Equal(t, 0.000028, fastCodex.Output)
	assert.Equal(t, 272_000, fastCodex.ContextWindow)

	fastGPT55 := fastPricing["gpt-5.5"]
	assert.Equal(t, 0.0000125, fastGPT55.Input)
	assert.Equal(t, 0.00000125, fastGPT55.CachedInput)
	assert.Equal(t, 0.000075, fastGPT55.Output)
	assert.Equal(t, 0, fastGPT55.LongContextThreshold)
}

func TestLoadCustomConfigurationOpenAIPlatformUsesConfiguredServiceTierPricing(t *testing.T) {
	standardModels, standardPricing := loadCustomConfiguration(llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"},
	})
	priorityModels, priorityPricing := loadCustomConfiguration(llmtypes.Config{
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "openai",
			ServiceTier: llmtypes.OpenAIServiceTierPriority,
		},
	})

	assert.Equal(t, "reasoning", standardModels["gpt-5.6-sol"])
	assert.Equal(t, "reasoning", standardModels["gpt-6-astra"])
	assert.Equal(t, standardModels, priorityModels)

	standardGPT6Astra := standardPricing["gpt-6-astra"]
	assert.Equal(t, 0.00001, standardGPT6Astra.Input)
	assert.Equal(t, 0.00005, standardGPT6Astra.Output)

	priorityGPT6Astra := priorityPricing["gpt-6-astra"]
	assert.Equal(t, 0.00002, priorityGPT6Astra.Input)
	assert.Equal(t, 0.0001, priorityGPT6Astra.Output)

	standardGPT56Sol := standardPricing["gpt-5.6-sol"]
	assert.Equal(t, 0.000005, standardGPT56Sol.Input)
	assert.Equal(t, 0.00000625, standardGPT56Sol.CacheWriteInput)
	assert.Equal(t, 0.0000125, standardGPT56Sol.LongContextCacheWriteInput)

	priorityGPT56Sol := priorityPricing["gpt-5.6-sol"]
	assert.Equal(t, 0.00001, priorityGPT56Sol.Input)
	assert.Equal(t, 0.0000125, priorityGPT56Sol.CacheWriteInput)
	assert.Equal(t, 0, priorityGPT56Sol.LongContextThreshold)

	assert.Equal(t, standardPricing["gpt-4.1"], priorityPricing["gpt-4.1"])
}

func TestProcessMessageExchangeMirrorsCodexPromptCachingRequestShape(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
	}
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello 1")},
			},
		},
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleAssistant,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hi")},
			},
		},
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello 2")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{
		{Type: "message", Role: "user", Content: "hello 1"},
		{Type: "message", Role: "assistant", Content: "hi"},
		{Type: "message", Role: "user", Content: "hello 2"},
	}

	var capturedParams openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		capturedParams = params
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)

	require.Len(t, capturedParams.Input.OfInputItemList, len(thread.inputItems), "full input history should be replayed")
	assert.Equal(t, "hello 1", extractInputItemText(capturedParams.Input.OfInputItemList[0]))
	assert.Equal(t, "hi", extractInputItemText(capturedParams.Input.OfInputItemList[1]))
	assert.Equal(t, "hello 2", extractInputItemText(capturedParams.Input.OfInputItemList[2]))
	assert.True(t, capturedParams.PromptCacheKey.Valid())
	assert.Equal(t, "conv-test", capturedParams.PromptCacheKey.Value)
	assert.False(t, capturedParams.PreviousResponseID.Valid(), "should not use previous_response_id")
	assert.True(t, capturedParams.Store.Valid())
	assert.False(t, capturedParams.Store.Value, "should mirror Codex by disabling stored conversation state")
}

func TestApplyPromptCacheOptions(t *testing.T) {
	tests := []struct {
		name         string
		config       llmtypes.Config
		model        string
		expectOption bool
	}{
		{
			name:         "OpenAI GPT-6 Astra",
			config:       llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}},
			model:        "gpt-6-astra",
			expectOption: true,
		},
		{
			name:         "OpenAI GPT-5.6 alias",
			config:       llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}},
			model:        "gpt-5.6",
			expectOption: true,
		},
		{
			name:         "OpenAI GPT-5.6 variant",
			config:       llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}},
			model:        "gpt-5.6-sol",
			expectOption: true,
		},
		{
			name:   "Codex GPT-5.6 variant",
			config: llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			model:  "gpt-5.6-luna",
		},
		{
			name:   "Copilot GPT-5.6 variant",
			config: llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot"}},
			model:  "gpt-5.6-luna",
		},
		{
			name:   "OpenAI earlier model",
			config: llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}},
			model:  "gpt-5.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := openairesponses.ResponseNewParams{}
			applyPromptCacheOptions(&params, tt.config, tt.model)

			if tt.expectOption {
				assert.Equal(t, "implicit", params.PromptCacheOptions.Mode)
				assert.Equal(t, "30m", params.PromptCacheOptions.Ttl)
				return
			}

			assert.Empty(t, params.PromptCacheOptions.Mode)
			assert.Empty(t, params.PromptCacheOptions.Ttl)
		})
	}
}

func TestOpenAIReasoningEffortForRequest(t *testing.T) {
	assert.Equal(t, shared.ReasoningEffortMax, openAIReasoningEffortForRequest("gpt-6-astra", shared.ReasoningEffortMax))
	assert.Equal(t, shared.ReasoningEffortXhigh, openAIReasoningEffortForRequest("gpt-6-astra", shared.ReasoningEffort("XHIGH")))
	assert.Equal(t, shared.ReasoningEffortLow, openAIReasoningEffortForRequest("gpt-6-astra", shared.ReasoningEffortNone))
	assert.Equal(t, shared.ReasoningEffortLow, openAIReasoningEffortForRequest(" GPT-6-ASTRA ", shared.ReasoningEffortMinimal))
	assert.Equal(t, shared.ReasoningEffortNone, openAIReasoningEffortForRequest("gpt-5.6-sol", shared.ReasoningEffortNone))
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		assert.Equal(t, shared.ReasoningEffortNone, openAIReasoningEffortForRequest(model, shared.ReasoningEffortNone))
		assert.Equal(t, shared.ReasoningEffortMax, openAIReasoningEffortForRequest(model, shared.ReasoningEffort(" MAX ")))
	}
}

func TestProcessMessageExchangeGPT6SolLuna(t *testing.T) {
	for _, platform := range []string{"openai", "codex"} {
		for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
			t.Run(platform+"/"+model, func(t *testing.T) {
				config := llmtypes.Config{
					Provider: "openai",
					Model:    model,
					OpenAI:   &llmtypes.OpenAIConfig{Platform: platform},
				}
				models, pricing := loadCustomConfiguration(config)
				thread := &Thread{
					Thread:          base.NewThread(config, "conv-test"),
					customModels:    models,
					customPricing:   pricing,
					reasoningEffort: shared.ReasoningEffortMax,
					isCodex:         platform == "codex",
				}
				var captured openairesponses.ResponseNewParams
				thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
					captured = params
					return nil
				}
				thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
					return processStreamResult{responseCompleted: true}, nil
				}

				_, _, _, err := thread.processMessageExchange(context.Background(), &llmtypes.StringCollectorHandler{Silent: true}, model, 256, "system", llmtypes.MessageOpt{NoToolUse: true})
				require.NoError(t, err)
				body := responseParamsBody(t, captured)
				assert.Equal(t, model, body["model"])
				assert.Equal(t, map[string]any{"effort": "max", "summary": "auto"}, body["reasoning"])
				assert.Equal(t, false, body["store"])
				if platform == "openai" {
					assert.Equal(t, map[string]any{"mode": "implicit", "ttl": "30m"}, body["prompt_cache_options"])
					assert.Equal(t, float64(256), body["max_output_tokens"])
				} else {
					assert.NotContains(t, body, "prompt_cache_options")
					assert.NotContains(t, body, "max_output_tokens")
				}
			})
		}
	}
}

func TestProcessMessageExchangeSetsConfiguredServiceTier(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "codex",
			APIMode:     llmtypes.OpenAIAPIModeResponses,
			ServiceTier: llmtypes.OpenAIServiceTierFast,
		},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
	}
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}
	thread.isCodex = true

	var capturedParams openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		capturedParams = params
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.Equal(t, openairesponses.ResponseNewParamsServiceTierPriority, capturedParams.ServiceTier)
}

func TestProcessMessageExchangeSetsTextVerbosity(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		configured llmtypes.OpenAITextVerbosity
		expected   openairesponses.ResponseTextConfigVerbosity
		present    bool
	}{
		{name: "GPT-5 omits unset verbosity", model: "gpt-5.5"},
		{name: "GPT-5 uses configured value", model: "gpt-5.5", configured: llmtypes.OpenAITextVerbosityHigh, expected: openairesponses.ResponseTextConfigVerbosityHigh, present: true},
		{name: "older model omits verbosity", model: "gpt-4.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    tt.model,
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:      "openai",
					APIMode:       llmtypes.OpenAIAPIModeResponses,
					TextVerbosity: tt.configured,
				},
			}
			thread := &Thread{Thread: base.NewThread(config, "conv-test")}
			thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
				{
					OfMessage: &openairesponses.EasyInputMessageParam{
						Role:    openairesponses.EasyInputMessageRoleUser,
						Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
					},
				},
			}

			var capturedParams openairesponses.ResponseNewParams
			thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				capturedParams = params
				return nil
			}
			thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
				return processStreamResult{responseCompleted: true}, nil
			}

			_, _, _, err := thread.processMessageExchange(context.Background(), &llmtypes.StringCollectorHandler{Silent: true}, tt.model, 256, "system", llmtypes.MessageOpt{NoToolUse: true})
			require.NoError(t, err)
			assert.Equal(t, tt.expected, capturedParams.Text.Verbosity)

			body, err := capturedParams.MarshalJSON()
			require.NoError(t, err)
			if tt.present {
				assert.Contains(t, string(body), `"text":{"verbosity":"high"}`)
			} else {
				assert.NotContains(t, string(body), `"text"`)
			}
		})
	}
}
