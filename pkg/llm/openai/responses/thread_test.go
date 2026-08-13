package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRetryTimer struct {
	delays []time.Duration
}

func (t *recordingRetryTimer) After(delay time.Duration) <-chan time.Time {
	t.delays = append(t.delays, delay)
	ready := make(chan time.Time, 1)
	ready <- time.Now()
	return ready
}

func extractInputItemText(item openairesponses.ResponseInputItemUnionParam) string {
	var text string

	if item.OfMessage != nil {
		if item.OfMessage.Content.OfString.Valid() {
			text += item.OfMessage.Content.OfString.Value
		}
		for _, part := range item.OfMessage.Content.OfInputItemContentList {
			if part.OfInputText != nil {
				text += part.OfInputText.Text
			}
		}
	}

	if item.OfOutputMessage != nil {
		for _, content := range item.OfOutputMessage.Content {
			if txt := content.GetText(); txt != nil {
				text += *txt
			}
		}
	}

	return text
}

func extractInputItemImageURLs(item openairesponses.ResponseInputItemUnionParam) []string {
	if item.OfMessage == nil {
		return nil
	}

	urls := make([]string, 0)
	for _, part := range item.OfMessage.Content.OfInputItemContentList {
		if part.OfInputImage != nil && part.OfInputImage.ImageURL.Valid() {
			urls = append(urls, part.OfInputImage.ImageURL.Value)
		}
	}
	return urls
}

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

func compactOutputItemFromJSON(t *testing.T, raw string) openairesponses.ResponseOutputItemUnion {
	t.Helper()

	var item openairesponses.ResponseOutputItemUnion
	require.NoError(t, json.Unmarshal([]byte(raw), &item))
	return item
}

func TestNewThread(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)
	assert.Equal(t, "openai", thread.Provider())
}

func TestNewThreadWithCustomAPIKey(t *testing.T) {
	os.Setenv("MY_CUSTOM_API_KEY", "test-key")
	defer os.Unsetenv("MY_CUSTOM_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:     "fireworks",
			APIKeyEnvVar: "MY_CUSTOM_API_KEY",
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	require.NotNil(t, thread)
}

func TestNewThreadWithoutAPIKey(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
		},
	}

	_, err := NewThread(config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENAI_API_KEY")
}

func TestThreadSwapContextReplacesHistoryAndClearsState(t *testing.T) {
	state := tools.NewBasicState(context.Background())
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Model: "gpt-4.1"}, "conv-swap"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("old message", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "old message"}},
	}
	thread.SetState(state)
	thread.SetStructuredToolResult("call-1", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})

	require.NoError(t, thread.SwapContext(context.Background(), "summary of prior context"))

	require.Len(t, thread.inputItems, 1)
	assert.Equal(t, "summary of prior context", extractInputItemText(thread.inputItems[0]))
	assert.Equal(t, []StoredInputItem{{Type: "message", Role: "user", Content: "summary of prior context"}}, thread.storedItems)
	assert.Empty(t, thread.GetStructuredToolResults())
	assert.Greater(t, thread.GetUsage().CurrentContextWindow, 0)
}

func TestStoredItemFromCompactOutputAndParseStoredMessageRole(t *testing.T) {
	messageOutput := compactOutputItemFromJSON(t, `{
		"type":"message",
		"role":"assistant",
		"content":[
			{"type":"output_text","text":"hello "},
			{"type":"input_text","text":"world"},
			{"type":"refusal","refusal":"nope"}
		]
	}`)
	message := storedItemFromCompactOutput(messageOutput, `{"type":"message"}`)
	assert.Equal(t, "message", message.Type)
	assert.Equal(t, "assistant", message.Role)
	assert.Equal(t, "hello world", message.Content)
	assert.JSONEq(t, `{"type":"message"}`, string(message.RawItem))

	functionCall := storedItemFromCompactOutput(compactOutputItemFromJSON(t, `{
		"type":"function_call",
		"call_id":"call-1",
		"name":"bash",
		"arguments":"{\"command\":\"true\"}"
	}`), "")
	assert.Equal(t, "call-1", functionCall.CallID)
	assert.Equal(t, "bash", functionCall.Name)
	assert.Equal(t, `{"command":"true"}`, functionCall.Arguments)

	functionOutput := storedItemFromCompactOutput(compactOutputItemFromJSON(t, `{
		"type":"function_call_output",
		"call_id":"call-1",
		"output":"ok"
	}`), "")
	assert.Equal(t, "ok", functionOutput.Output)

	reasoning := storedItemFromCompactOutput(compactOutputItemFromJSON(t, `{
		"type":"reasoning",
		"summary":[{"type":"summary_text","text":"first"},{"type":"summary_text","text":"second"}]
	}`), "")
	assert.Equal(t, "assistant", reasoning.Role)
	assert.Equal(t, "first\nsecond", reasoning.Content)

	compaction := storedItemFromCompactOutput(compactOutputItemFromJSON(t, `{
		"type":"compaction",
		"encrypted_content":"encrypted"
	}`), "")
	assert.Equal(t, "encrypted", compaction.EncryptedContent)

	compactionSummary := storedItemFromCompactOutput(compactOutputItemFromJSON(t, `{
		"type":"compaction_summary",
		"encrypted_content":"summary-encrypted"
	}`), "")
	assert.Equal(t, "summary-encrypted", compactionSummary.EncryptedContent)

	for _, role := range []openairesponses.EasyInputMessageRole{
		openairesponses.EasyInputMessageRoleUser,
		openairesponses.EasyInputMessageRoleAssistant,
		openairesponses.EasyInputMessageRoleSystem,
		openairesponses.EasyInputMessageRoleDeveloper,
	} {
		parsed, ok := parseStoredMessageRole("  " + string(role) + "  ")
		require.True(t, ok)
		assert.Equal(t, role, parsed)
	}
	_, ok := parseStoredMessageRole("tool")
	assert.False(t, ok)
}

func TestStoredItemsAndRawInputHelpers(t *testing.T) {
	items := fromStoredItems([]StoredInputItem{
		{Type: "reasoning", Content: "skip me"},
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "message", Role: "assistant", Content: "hello", RawItem: json.RawMessage(`{"id":"msg-1","status":"in_progress","phase":"commentary"}`)},
		{Type: "function_call", CallID: "call-1", Name: "bash", Arguments: `{"command":"true"}`},
		{Type: "function_call_output", CallID: "call-1", Output: "ok"},
		{Type: "function_call_output", CallID: "call-2", RawOutput: json.RawMessage(`[{"type":"input_text","text":"raw"}]`)},
		{Type: "web_search_call", CallID: "search-1", Status: "completed", Action: "find_in_page", Content: "https://example.com", Arguments: "needle"},
		{Type: "compaction", EncryptedContent: "encrypted"},
		{Type: "unknown"},
	})

	require.Len(t, items, 7)
	assert.NotNil(t, items[0].OfMessage)
	assert.NotNil(t, items[1].OfOutputMessage)
	assert.NotNil(t, items[2].OfFunctionCall)
	assert.NotNil(t, items[3].OfFunctionCallOutput)
	assert.Equal(t, "ok", items[3].OfFunctionCallOutput.Output.OfString.Value)
	assert.NotNil(t, items[4].OfFunctionCallOutput)
	assert.Len(t, items[4].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray, 1)
	assert.NotNil(t, items[5].OfWebSearchCall)
	assert.NotNil(t, items[5].OfWebSearchCall.Action.OfFind)
	assert.NotNil(t, items[6].OfCompaction)
	assert.Equal(t, "encrypted", items[6].OfCompaction.EncryptedContent)

	inputItem, ok := messageInputItemFromRawItem(json.RawMessage(`{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}`))
	require.True(t, ok)
	assert.NotNil(t, inputItem.OfMessage)
	assert.Equal(t, "hello", extractInputItemText(inputItem))
	assert.Equal(t, []string{"data:image/png;base64,abc"}, extractInputItemImageURLs(inputItem))

	assistantItem, ok := messageInputItemFromRawItem(json.RawMessage(`{"role":"assistant","id":"msg-2","status":"incomplete","phase":"final_answer","content":[{"type":"input_text","text":"legacy assistant"}]}`))
	require.True(t, ok)
	assert.NotNil(t, assistantItem.OfOutputMessage)
	assert.Equal(t, "legacy assistant", extractInputItemText(assistantItem))

	_, ok = messageInputItemFromRawItem(json.RawMessage(`{"role":"","content":"no role"}`))
	assert.False(t, ok)
	_, ok = messageInputItemFromRawItem(json.RawMessage(`{"role":"user","content":[{"type":"unsupported"}]}`))
	assert.False(t, ok)

	compactionItem, ok := inputItemFromRawItem(json.RawMessage(`{"type":"compaction_summary","encrypted_content":"compact"}`))
	require.True(t, ok)
	assert.NotNil(t, compactionItem.OfCompaction)
	assert.Equal(t, "compact", compactionItem.OfCompaction.EncryptedContent)

	_, ok = inputItemFromRawItem(json.RawMessage(`{`))
	assert.False(t, ok)
}

func TestNewThreadEnablesWebSocketByDefaultForOpenAIResponses(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	assert.True(t, thread.useWebSocket)
	assert.NotNil(t, thread.webSocket)
}

func TestNewThreadCanDisableWebSocketMode(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	webSocketMode := false

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:      "openai",
			APIMode:       llmtypes.OpenAIAPIModeResponses,
			WebSocketMode: &webSocketMode,
		},
	}

	thread, err := NewThread(config)
	require.NoError(t, err)
	assert.False(t, thread.useWebSocket)
	assert.Nil(t, thread.webSocket)
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

func TestBuildToolsIncludesNativeOpenAISearchWhenEligible(t *testing.T) {
	state := tools.NewBasicState(context.Background(), tools.WithLLMConfig(llmtypes.Config{
		Provider: "openai",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}))

	toolDefs := buildTools(state)
	require.NotEmpty(t, toolDefs)
	assert.NotNil(t, toolDefs[0].OfWebSearch)
	assert.Equal(t, openairesponses.WebSearchToolTypeWebSearch, toolDefs[0].OfWebSearch.Type)
}

func TestResponsesToolConversionPreservesRawJSONSchema(t *testing.T) {
	rawSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"type": []any{"string", "null"}},
		},
		"additionalProperties": false,
		"x-mcp-extension":      true,
	}
	converted := toResponsesAPITools([]tooltypes.Tool{responsesTestTool{name: "raw", rawSchema: rawSchema}})

	require.Len(t, converted, 1)
	require.NotNil(t, converted[0].OfFunction)
	assert.Equal(t, rawSchema, converted[0].OfFunction.Parameters)
}

func TestBuildToolsSkipsNativeOpenAISearchForNonOpenAIPlatforms(t *testing.T) {
	state := tools.NewBasicState(context.Background(), tools.WithLLMConfig(llmtypes.Config{
		Provider: "openai",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "fireworks",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}))

	toolDefs := buildTools(state)
	for _, toolDef := range toolDefs {
		assert.Nil(t, toolDef.OfWebSearch)
	}
}

func TestBuildToolsIncludesNativeOpenAISearchForCodexPlatform(t *testing.T) {
	state := tools.NewBasicState(context.Background(), tools.WithLLMConfig(llmtypes.Config{
		Provider: "openai",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "codex",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}))

	toolDefs := buildTools(state)
	foundWebSearch := false
	for _, toolDef := range toolDefs {
		if toolDef.OfWebSearch != nil && toolDef.OfWebSearch.Type == openairesponses.WebSearchToolTypeWebSearch {
			foundWebSearch = true
			break
		}
	}
	assert.True(t, foundWebSearch)
}

func TestBuildToolsSkipsNativeOpenAISearchWhenAllowlistExcludesIt(t *testing.T) {
	state := tools.NewBasicState(context.Background(), tools.WithLLMConfig(llmtypes.Config{
		Provider:     "openai",
		AllowedTools: []string{"bash"},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}))

	toolDefs := buildTools(state)
	for _, toolDef := range toolDefs {
		assert.Nil(t, toolDef.OfWebSearch)
	}
}

func TestBuildToolsIncludesNativeOpenAISearchWhenAllowlistIncludesIt(t *testing.T) {
	state := tools.NewBasicState(context.Background(), tools.WithLLMConfig(llmtypes.Config{
		Provider:     "openai",
		AllowedTools: []string{"bash", openAISearchToolName},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}))

	toolDefs := buildTools(state)
	foundWebSearch := false
	for _, toolDef := range toolDefs {
		if toolDef.OfWebSearch != nil && toolDef.OfWebSearch.Type == openairesponses.WebSearchToolTypeWebSearch {
			foundWebSearch = true
			break
		}
	}
	assert.True(t, foundWebSearch)
}

func TestBuildToolsForThreadHonorsExtensionAllowedTools(t *testing.T) {
	state := tools.NewBasicState(context.Background(), tools.WithLLMConfig(llmtypes.Config{
		Provider: "openai",
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}))
	thread := &Thread{Thread: base.NewThread(llmtypes.Config{}, "conv-tools")}
	thread.SetMetadataValue("allowed_tools", []string{"file_write"})

	toolDefs := buildToolsForThread(thread, state, false)

	require.Len(t, toolDefs, 1)
	require.NotNil(t, toolDefs[0].OfFunction)
	assert.Equal(t, "file_write", toolDefs[0].OfFunction.Name)
}

func TestIsReasoningModelDynamic(t *testing.T) {
	// Create a thread with default OpenAI platform defaults loaded.
	thread := &Thread{
		customModels: map[string]string{
			"o1":      "reasoning",
			"o1-mini": "reasoning",
			"o3":      "reasoning",
			"o3-mini": "reasoning",
			"o4-mini": "reasoning",
			"gpt-5":   "reasoning",
			"gpt-4.1": "non-reasoning",
			"gpt-4o":  "non-reasoning",
		},
	}

	tests := []struct {
		model    string
		expected bool
	}{
		{"o1", true},
		{"o1-mini", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"gpt-5", true},
		{"gpt-4.1", false},
		{"gpt-4o", false},
		{"claude-3", false}, // Not in loaded defaults, returns false
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, thread.isReasoningModelDynamic(tt.model))
		})
	}
}

func TestExtractMessages(t *testing.T) {
	// Create sample input items in JSON format
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "Hello, world!"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "Hi there!"
		}
	]`

	messages, err := ExtractMessages([]byte(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, messages, 2)

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Hello, world!", messages[0].Content)
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "Hi there!", messages[1].Content)
}

func TestExtractMessagesWithToolResults(t *testing.T) {
	// Create sample input items with function call and result
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "What files are in the directory?"
		},
		{
			"type": "function_call",
			"call_id": "call_123",
			"name": "list_files",
			"arguments": "{\"path\": \"/tmp\"}"
		},
		{
			"type": "function_call_output",
			"call_id": "call_123",
			"output": "file1.txt\nfile2.txt"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "The directory contains file1.txt and file2.txt."
		}
	]`

	// Add tool results map
	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_123": {
			ToolName: "list_files",
			Success:  true,
		},
	}

	messages, err := ExtractMessages([]byte(inputItems), toolResults)
	require.NoError(t, err)
	require.Len(t, messages, 4)

	assert.Equal(t, "user", messages[0].Role)
	assert.Contains(t, messages[1].Content, "list_files")
	assert.Contains(t, messages[2].Content, "Tool result")
	assert.Equal(t, "assistant", messages[3].Role)
}

func remoteCompactionV2Stream(t *testing.T, encryptedContent string, extraOutputItems ...map[string]any) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
	t.Helper()
	events := make([]map[string]any, 0, len(extraOutputItems)+2)
	for _, item := range extraOutputItems {
		events = append(events, map[string]any{
			"type": "response.output_item.done",
			"item": item,
		})
	}
	events = append(events,
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":              "compaction",
				"encrypted_content": encryptedContent,
			},
		},
		map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     "resp_compact",
				"status": "completed",
				"usage": map[string]any{
					"input_tokens":  100,
					"output_tokens": 10,
					"total_tokens":  110,
					"input_tokens_details": map[string]any{
						"cached_tokens": 20,
					},
					"output_tokens_details": map[string]any{
						"reasoning_tokens": 0,
					},
				},
			},
		},
	)
	return responseStreamFromMaps(t, events)
}

func remoteCompactionV2TerminalEvent(
	eventType string,
	serviceTier string,
	inputTokens int,
	outputTokens int,
	cachedTokens int,
) map[string]any {
	status := strings.TrimPrefix(eventType, "response.")
	response := map[string]any{
		"id":     "resp_compact_" + status,
		"status": status,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + outputTokens,
			"input_tokens_details": map[string]any{
				"cached_tokens": cachedTokens,
			},
			"output_tokens_details": map[string]any{
				"reasoning_tokens": 0,
			},
		},
	}
	if serviceTier != "" {
		response["service_tier"] = serviceTier
	}
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	return map[string]any{
		"type":     eventType,
		"response": response,
	}
}

func TestCompactContextUsesRemoteCompactionV2(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	var captured openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return remoteCompactionV2Stream(t, "encrypted-summary")
	}
	thread.compactWithSummaryFunc = func(context.Context) error {
		t.Fatal("summary fallback should not run when remote compaction v2 succeeds")
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))

	require.True(t, captured.Instructions.Valid())
	assert.NotEmpty(t, captured.Instructions.Value)
	require.Len(t, captured.Input.OfInputItemList, 2)
	assert.NotNil(t, captured.Input.OfInputItemList[1].OfCompactionTrigger)
	require.True(t, captured.ParallelToolCalls.Valid())
	assert.Equal(t, len(captured.Tools) > 0, captured.ParallelToolCalls.Value)
	require.True(t, captured.PromptCacheKey.Valid())
	assert.Equal(t, "conv-test", captured.PromptCacheKey.Value)

	require.Len(t, thread.storedItems, 2)
	assert.Equal(t, "message", thread.storedItems[0].Type)
	assert.Equal(t, "compaction", thread.storedItems[1].Type)
	assert.Equal(t, "encrypted-summary", thread.storedItems[1].EncryptedContent)
	require.Len(t, thread.inputItems, 2)
	require.NotNil(t, thread.inputItems[1].OfCompaction)
	assert.Equal(t, "encrypted-summary", thread.inputItems[1].OfCompaction.EncryptedContent)
	assert.Equal(t, 100, thread.Usage.InputTokens)
	assert.Equal(t, 20, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
}

func TestCompactContextRemoteV2SummaryFallbackAdvancesCodexWindowOnce(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	fakeWebSocket := &fakeResponsesWebSocketStreamer{}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-summary-fallback"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 4,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
		webSocket:   fakeWebSocket,
	}
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return responseStreamFromMaps(t, []map[string]any{{
			"type": "response.completed",
			"response": map[string]any{
				"id":     "resp_without_compaction",
				"status": "completed",
				"usage": map[string]any{
					"input_tokens":  1,
					"output_tokens": 1,
					"total_tokens":  2,
				},
			},
		}})
	}
	thread.compactWithSummaryFunc = func(ctx context.Context) error {
		return thread.SwapContext(ctx, "summary fallback")
	}

	require.NoError(t, thread.CompactContext(context.Background()))

	assert.Equal(t, uint64(5), thread.codexWindowGenerationSnapshot())
	assert.Equal(t, 1, fakeWebSocket.resets)
	require.Len(t, thread.inputItemsSnapshot(), 1)
	assert.Equal(t, "summary fallback", extractInputItemText(thread.inputItemsSnapshot()[0]))
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

func TestSendMessageAutoCompactionExcludesIncomingUserUntilReplacementInstalled(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	thread := &Thread{
		Thread:              base.NewThread(config, "conv-auto-order"),
		isCodex:             true,
		codexInstallationID: "00000000-0000-4000-8000-000000000001",
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("existing user", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "existing user"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.Usage.CurrentContextWindow = 90
	thread.Usage.MaxContextWindow = 100

	var compactParams openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		compactParams = params
		return remoteCompactionV2Stream(t, "auto-encrypted")
	}
	var postCompactHistory []openairesponses.ResponseInputItemUnionParam
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		postCompactHistory = thread.inputItemsSnapshot()
		return "done", false, true, nil
	}

	_, err := thread.SendMessage(
		context.Background(),
		"incoming user",
		&llmtypes.StringCollectorHandler{Silent: true},
		llmtypes.MessageOpt{
			Images:    []string{"data:image/png;base64,aGVsbG8="},
			NoToolUse: true,
			MaxTurns:  1,
		},
	)
	require.NoError(t, err)

	for _, item := range compactParams.Input.OfInputItemList {
		assert.NotContains(t, extractInputItemText(item), "incoming user")
		assert.Empty(t, extractInputItemImageURLs(item))
	}
	clientMetadata := codexClientMetadataFromParams(t, compactParams)
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "compaction", turnMetadata["request_kind"])
	compaction, ok := turnMetadata["compaction"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "auto", compaction["trigger"])
	assert.Equal(t, "context_limit", compaction["reason"])
	assert.Equal(t, "pre_turn", compaction["phase"])

	require.Len(t, postCompactHistory, 3)
	assert.Equal(t, "existing user", extractInputItemText(postCompactHistory[0]))
	require.NotNil(t, postCompactHistory[1].OfCompaction)
	assert.Equal(t, "incoming user", extractInputItemText(postCompactHistory[2]))
	assert.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, extractInputItemImageURLs(postCompactHistory[2]))
	assert.Equal(t, uint64(1), thread.codexWindowGenerationSnapshot())
}

func TestSendMessageNoSaveRestoresCodexWindowAndWebSocketIdentity(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	fakeWebSocket := &fakeResponsesWebSocketStreamer{}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-no-save-window"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 7,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("existing", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "existing"}},
		webSocket:   fakeWebSocket,
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.SetStructuredToolResult("old-call", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})
	thread.Usage.InputTokens = 5
	thread.Usage.OutputTokens = 2
	thread.Usage.CacheReadInputTokens = 3
	thread.Usage.CurrentContextWindow = 90
	thread.Usage.MaxContextWindow = 100
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "temporary-encrypted-summary")
	}
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		assert.Equal(t, uint64(8), thread.codexWindowGenerationSnapshot())
		history := thread.inputItemsSnapshot()
		require.Len(t, history, 3)
		require.NotNil(t, history[1].OfCompaction)
		assert.Equal(t, "incoming", extractInputItemText(history[2]))
		return "done", false, true, nil
	}

	_, err := thread.SendMessage(
		context.Background(),
		"incoming",
		&llmtypes.StringCollectorHandler{Silent: true},
		llmtypes.MessageOpt{NoSaveConversation: true, NoToolUse: true, MaxTurns: 1},
	)
	require.NoError(t, err)

	assert.Equal(t, uint64(7), thread.codexWindowGenerationSnapshot())
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 1)
	assert.Equal(t, "existing", extractInputItemText(history[0]))
	assert.Equal(t, 105, thread.Usage.InputTokens, "billable no-save usage must remain recorded")
	assert.Equal(t, 12, thread.Usage.OutputTokens)
	assert.Equal(t, 23, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 90, thread.Usage.CurrentContextWindow)
	assert.Equal(t, 100, thread.Usage.MaxContextWindow)
	assert.Contains(t, thread.GetStructuredToolResults(), "old-call")
	assert.Equal(t, 2, fakeWebSocket.resets, "compaction install and no-save rollback must each reset websocket identity")
}

func TestSendMessageNoSaveRestoresStateAfterExchangeError(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-no-save-error"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 2,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("existing", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "existing"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.SetStructuredToolResult("old-call", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})
	thread.Usage.CurrentContextWindow = 90
	thread.Usage.MaxContextWindow = 100
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "temporary-encrypted-summary")
	}
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		return "", false, false, errors.New("exchange failed")
	}

	_, err := thread.SendMessage(
		context.Background(),
		"incoming",
		&llmtypes.StringCollectorHandler{Silent: true},
		llmtypes.MessageOpt{NoSaveConversation: true, NoToolUse: true, MaxTurns: 1},
	)
	require.EqualError(t, err, "exchange failed")

	assert.Equal(t, uint64(2), thread.codexWindowGenerationSnapshot())
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 1)
	assert.Equal(t, "existing", extractInputItemText(history[0]))
	assert.Equal(t, 90, thread.Usage.CurrentContextWindow)
	assert.Equal(t, 100, thread.Usage.MaxContextWindow)
	assert.Contains(t, thread.GetStructuredToolResults(), "old-call")
}

func TestSendMessageNoSavePreservesConcurrentUserAppend(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-no-save-concurrent-append"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("existing", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "existing"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	exchangeStarted := make(chan struct{})
	releaseExchange := make(chan struct{})
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		close(exchangeStarted)
		<-releaseExchange
		return "done", false, true, nil
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := thread.SendMessage(
			context.Background(),
			"transient incoming",
			&llmtypes.StringCollectorHandler{Silent: true},
			llmtypes.MessageOpt{NoSaveConversation: true, NoToolUse: true, MaxTurns: 1},
		)
		errCh <- err
	}()

	<-exchangeStarted
	thread.AddUserMessage(context.Background(), "concurrent append")
	close(releaseExchange)
	require.NoError(t, <-errCh)

	history := thread.inputItemsSnapshot()
	require.Len(t, history, 2)
	assert.Equal(t, "existing", extractInputItemText(history[0]))
	assert.Equal(t, "concurrent append", extractInputItemText(history[1]))
	stored := thread.snapshotHistory().storedItems
	require.Len(t, stored, 2)
	assert.Equal(t, "existing", stored[0].Content)
	assert.Equal(t, "concurrent append", stored[1].Content)
}

func TestCompactContextOpenAICompatiblePlatformsUseSummaryCompaction(t *testing.T) {
	for _, platform := range []string{"copilot", "fireworks"} {
		t.Run(platform, func(t *testing.T) {
			thread := &Thread{
				Thread: base.NewThread(
					llmtypes.Config{Provider: "openai", Model: "gpt-5", OpenAI: &llmtypes.OpenAIConfig{Platform: platform}},
					"conv-test",
				),
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
			}

			summaryCalled := false
			thread.compactWithSummaryFunc = func(_ context.Context) error {
				summaryCalled = true
				return nil
			}

			require.NoError(t, thread.CompactContext(context.Background()))
			assert.True(t, summaryCalled)
		})
	}
}

func TestSummaryContextReplacementRejectsConcurrentHistoryChange(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(
			llmtypes.Config{Provider: "openai", Model: "gpt-5", OpenAI: &llmtypes.OpenAIConfig{Platform: "fireworks"}},
			"conv-summary-stale",
		),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("original", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "original"}},
	}
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	thread.compactWithSummaryFunc = func(context.Context) error {
		snapshot := thread.snapshotHistory()
		close(requestStarted)
		<-releaseRequest
		return thread.swapContextAtRevision(snapshot.revision, "stale summary")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- thread.CompactContext(context.Background())
	}()
	<-requestStarted
	thread.AddUserMessage(context.Background(), "arrived while summarizing")
	close(releaseRequest)

	err := <-errCh
	require.ErrorIs(t, err, errRemoteCompactionHistoryChanged)
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 2)
	assert.Equal(t, "original", extractInputItemText(history[0]))
	assert.Equal(t, "arrived while summarizing", extractInputItemText(history[1]))
}

func TestSupportsRemoteCompactionV2(t *testing.T) {
	tests := []struct {
		name     string
		config   llmtypes.Config
		expected bool
	}{
		{
			name:     "default platform is openai",
			config:   llmtypes.Config{},
			expected: true,
		},
		{
			name:     "openai",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}},
			expected: true,
		},
		{
			name:     "codex",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}},
			expected: true,
		},
		{
			name:     "copilot",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot"}},
			expected: false,
		},
		{
			name:     "custom compatible platform",
			config:   llmtypes.Config{OpenAI: &llmtypes.OpenAIConfig{Platform: "fireworks"}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, supportsRemoteCompactionV2(tt.config))
		})
	}
}

func TestCompactContextAccountsCacheWriteUsage(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.6-sol",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "openai",
			ServiceTier: llmtypes.OpenAIServiceTierDefault,
		},
	}
	_, customPricing := loadCustomConfiguration(config)
	thread := &Thread{
		Thread:        base.NewThread(config, "conv-test"),
		customPricing: customPricing,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	var captured openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return responseStreamFromMaps(t, []map[string]any{
			{
				"type": "response.output_item.done",
				"item": map[string]any{"type": "compaction", "encrypted_content": "enc"},
			},
			{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_compact",
					"status": "completed",
					"usage": map[string]any{
						"input_tokens":  100,
						"output_tokens": 10,
						"total_tokens":  140,
						"input_tokens_details": map[string]any{
							"cached_tokens":      20,
							"cache_write_tokens": 30,
						},
						"output_tokens_details": map[string]any{"reasoning_tokens": 0},
					},
				},
			},
		})
	}

	require.NoError(t, thread.CompactContext(context.Background()))

	assert.Equal(t, openairesponses.ResponseNewParamsServiceTierDefault, captured.ServiceTier)
	assert.Equal(t, 70, thread.Usage.InputTokens)
	assert.Equal(t, 20, thread.Usage.CacheReadInputTokens)
	assert.Equal(t, 30, thread.Usage.CacheCreationInputTokens)
	assert.Equal(t, 10, thread.Usage.OutputTokens)
	assert.Equal(t, 130, thread.Usage.TotalTokens())
	assert.InDelta(t, 50*0.000005, thread.Usage.InputCost, 1e-12)
	assert.InDelta(t, 20*0.0000005, thread.Usage.CacheReadCost, 1e-12)
	assert.InDelta(t, 30*0.00000625, thread.Usage.CacheCreationCost, 1e-12)
	assert.InDelta(t, 10*0.00003, thread.Usage.OutputCost, 1e-12)
	assert.Equal(t,
		estimateRemoteCompactionV2ContextTokens(captured.Instructions.Value, thread.inputItemsSnapshot()),
		thread.Usage.CurrentContextWindow,
	)
	assert.Equal(t, 1_050_000, thread.Usage.MaxContextWindow)
}

func TestCompactContextRemoteV2UsesReturnedServiceTierForPricing(t *testing.T) {
	tests := []struct {
		name                  string
		configuredTier        llmtypes.OpenAIServiceTier
		returnedTier          string
		expectedInputCost     float64
		expectedCacheReadCost float64
		expectedOutputCost    float64
	}{
		{
			name:                  "auto served as priority",
			configuredTier:        llmtypes.OpenAIServiceTierAuto,
			returnedTier:          "priority",
			expectedInputCost:     80 * 0.00001,
			expectedCacheReadCost: 20 * 0.000001,
			expectedOutputCost:    10 * 0.00006,
		},
		{
			name:                  "priority served as default",
			configuredTier:        llmtypes.OpenAIServiceTierPriority,
			returnedTier:          "default",
			expectedInputCost:     80 * 0.000005,
			expectedCacheReadCost: 20 * 0.0000005,
			expectedOutputCost:    10 * 0.00003,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.6-sol",
				Retry:    llmtypes.RetryConfig{Attempts: 1},
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:    "openai",
					ServiceTier: tt.configuredTier,
				},
			}
			_, customPricing := loadCustomConfiguration(config)
			thread := &Thread{
				Thread:        base.NewThread(config, "conv-returned-tier"),
				customPricing: customPricing,
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}

			thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				return responseStreamFromMaps(t, []map[string]any{
					{
						"type": "response.output_item.done",
						"item": map[string]any{"type": "compaction", "encrypted_content": "enc"},
					},
					remoteCompactionV2TerminalEvent("response.completed", tt.returnedTier, 100, 10, 20),
				})
			}

			require.NoError(t, thread.CompactContext(context.Background()))
			assert.InDelta(t, tt.expectedInputCost, thread.Usage.InputCost, 1e-12)
			assert.InDelta(t, tt.expectedCacheReadCost, thread.Usage.CacheReadCost, 1e-12)
			assert.InDelta(t, tt.expectedOutputCost, thread.Usage.OutputCost, 1e-12)
		})
	}
}

func TestCompactContextFallsBackOnRemoteCompactionV2Error(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return responseStreamFromMaps(t, []map[string]any{
			{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_without_compaction",
					"status": "completed",
					"usage": map[string]any{
						"input_tokens":          1,
						"output_tokens":         1,
						"total_tokens":          2,
						"input_tokens_details":  map[string]any{"cached_tokens": 0},
						"output_tokens_details": map[string]any{"reasoning_tokens": 0},
					},
				},
			},
		})
	}

	fallbackCalled := false
	thread.compactWithSummaryFunc = func(_ context.Context) error {
		fallbackCalled = true
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.True(t, fallbackCalled)
	assert.Equal(t, 1, thread.Usage.InputTokens)
	assert.Equal(t, 1, thread.Usage.OutputTokens)
	assert.Zero(t, thread.Usage.CurrentContextWindow, "rejected output usage must not replace active-context metrics")
}

func TestCollectRemoteCompactionV2StreamIgnoresOtherOutputItems(t *testing.T) {
	stream := remoteCompactionV2Stream(t, "encrypted-summary", map[string]any{
		"type":      "function_call",
		"call_id":   "call-1",
		"name":      "bash",
		"arguments": `{"command":"false"}`,
	})

	result, err := collectRemoteCompactionV2Stream(context.Background(), stream)
	require.NoError(t, err)
	assert.Equal(t, "compaction", result.output.Type)
	assert.Equal(t, "encrypted-summary", result.output.AsCompaction().EncryptedContent)
}

func TestCollectRemoteCompactionV2StreamPreservesIncompleteUsage(t *testing.T) {
	result, err := collectRemoteCompactionV2Stream(
		context.Background(),
		responseStreamFromMaps(t, []map[string]any{
			remoteCompactionV2TerminalEvent("response.incomplete", "priority", 12, 3, 4),
		}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response incomplete")
	require.Len(t, result.usageRecords, 1)
	assert.Equal(t, int64(12), result.usageRecords[0].usage.InputTokens)
	assert.Equal(t, int64(3), result.usageRecords[0].usage.OutputTokens)
	assert.Equal(t, int64(4), result.usageRecords[0].usage.InputTokensDetails.CachedTokens)
	assert.Equal(t, llmtypes.OpenAIServiceTierPriority, result.usageRecords[0].serviceTier)
}

func TestCollectRemoteCompactionV2StreamValidation(t *testing.T) {
	completed := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_compact",
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":          1,
				"output_tokens":         1,
				"total_tokens":          2,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 0},
			},
		},
	}
	compaction := func(encrypted string) map[string]any {
		return map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{"type": "compaction", "encrypted_content": encrypted},
		}
	}

	tests := []struct {
		name        string
		events      []map[string]any
		message     string
		recoverable bool
	}{
		{name: "missing compaction", events: []map[string]any{completed}, message: "exactly one compaction"},
		{name: "multiple compactions", events: []map[string]any{compaction("one"), compaction("two"), completed}, message: "got 2"},
		{name: "missing completion", events: []map[string]any{compaction("one")}, message: "before response.completed", recoverable: true},
		{
			name: "incomplete response",
			events: []map[string]any{{
				"type": "response.incomplete",
				"response": map[string]any{
					"id":                 "resp_incomplete",
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
				},
			}},
			message:     "response incomplete",
			recoverable: true,
		},
		{name: "empty encrypted content", events: []map[string]any{compaction(""), completed}, message: "empty encrypted content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := collectRemoteCompactionV2Stream(context.Background(), responseStreamFromMaps(t, tt.events))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
			assert.Equal(t, tt.recoverable, retry.IsRecoverable(err))
		})
	}
}

func TestCompactContextRemoteV2RetriesRetryableStreamTermination(t *testing.T) {
	tests := []struct {
		name        string
		firstEvents []map[string]any
	}{
		{
			name: "clean close before completion",
			firstEvents: []map[string]any{{
				"type": "response.output_item.done",
				"item": map[string]any{"type": "compaction", "encrypted_content": "partial"},
			}},
		},
		{
			name: "incomplete response",
			firstEvents: []map[string]any{{
				"type": "response.incomplete",
				"response": map[string]any{
					"id":                 "resp_incomplete",
					"status":             "incomplete",
					"incomplete_details": map[string]any{"reason": "max_output_tokens"},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			thread := &Thread{
				Thread: base.NewThread(config, "conv-test"),
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}

			attempts := 0
			thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				attempts++
				if attempts == 1 {
					return responseStreamFromMaps(t, tt.firstEvents)
				}
				return remoteCompactionV2Stream(t, "retried-summary")
			}
			thread.compactWithSummaryFunc = func(context.Context) error {
				t.Fatal("summary fallback should not run after a successful retry")
				return nil
			}

			require.NoError(t, thread.CompactContext(context.Background()))
			assert.Equal(t, 2, attempts)
			require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
			assert.Equal(t, "retried-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
		})
	}
}

func TestCompactContextRemoteV2AccumulatesIncompleteRetryUsage(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.6-sol",
		Retry: llmtypes.RetryConfig{
			Attempts:     2,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform:    "openai",
			ServiceTier: llmtypes.OpenAIServiceTierDefault,
		},
	}
	_, customPricing := loadCustomConfiguration(config)
	thread := &Thread{
		Thread:        base.NewThread(config, "conv-incomplete-retries"),
		customPricing: customPricing,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.Usage.CurrentContextWindow = 55
	thread.Usage.MaxContextWindow = 100

	attempts := 0
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		return responseStreamFromMaps(t, []map[string]any{
			remoteCompactionV2TerminalEvent("response.incomplete", "default", 10, 1, 2),
		})
	}

	err := thread.compactContextRemoteV2(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response incomplete")
	assert.Equal(t, 2, attempts)
	usage := thread.GetUsage()
	assert.Equal(t, 20, usage.InputTokens)
	assert.Equal(t, 4, usage.CacheReadInputTokens)
	assert.Equal(t, 2, usage.OutputTokens)
	assert.InDelta(t, 16*0.000005, usage.InputCost, 1e-12)
	assert.InDelta(t, 4*0.0000005, usage.CacheReadCost, 1e-12)
	assert.InDelta(t, 2*0.00003, usage.OutputCost, 1e-12)
	assert.Equal(t, 55, usage.CurrentContextWindow)
	assert.Equal(t, 100, usage.MaxContextWindow)
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 1)
	assert.Nil(t, history[0].OfCompaction)
}

func TestRetainedStoredItemsForRemoteCompactionV2(t *testing.T) {
	items := []StoredInputItem{
		{Type: "message", Role: "user", Content: "first user"},
		{Type: "message", Role: "assistant", Content: "final answer", RawItem: json.RawMessage(`{"phase":"final_answer"}`)},
		{Type: "message", Role: "assistant", Content: "commentary", RawItem: json.RawMessage(`{"phase":"commentary","content":[{"type":"output_text","text":"commentary"}]}`)},
		{Type: "function_call", CallID: "call-1", Name: "bash", Arguments: `{}`},
		{Type: "message", Role: "developer", Content: "stale instructions"},
		{Type: "message", Role: "user", Content: goals.RenderContext(goals.New("keep working", time.Now()))},
		{Type: "message", Role: "user", Content: "latest user"},
		{Type: "compaction", EncryptedContent: "old-compaction"},
	}

	retained := retainedStoredItemsForRemoteCompactionV2(items)
	require.Len(t, retained, 2)
	assert.Equal(t, "first user", retained[0].Content)
	assert.Equal(t, "latest user", retained[1].Content)
}

func TestRetainedStoredItemsForRemoteCompactionV2AppliesBudget(t *testing.T) {
	oldContent := strings.Repeat("a", (remoteCompactionV2RetainedMessageTokenBudget+100)*4)
	retained := retainedStoredItemsForRemoteCompactionV2([]StoredInputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: oldContent,
			RawItem: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"old"},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}`),
		},
	})

	require.Len(t, retained, 1)
	assert.LessOrEqual(t, approximateStoredMessageTokens(retained[0]), remoteCompactionV2RetainedMessageTokenBudget)
	assert.Contains(t, retained[0].Content, "...[truncated]...")
	assert.Contains(t, string(retained[0].RawItem), "input_image")
}

func TestApproximateStoredMessageTokensUsesUTF8Bytes(t *testing.T) {
	assert.Equal(t, 2, approximateStoredMessageTokens(StoredInputItem{
		Type:    "message",
		Role:    "user",
		Content: "éééé",
	}))
}

func TestApproximateResponseInputItemTokensDiscountsInlineImagePayload(t *testing.T) {
	userImage := func(payloadLength int) openairesponses.ResponseInputItemUnionParam {
		return openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role: openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{
					OfInputItemContentList: openairesponses.ResponseInputMessageContentListParam{{
						OfInputImage: &openairesponses.ResponseInputImageParam{
							ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", payloadLength)),
						},
					}},
				},
			},
		}
	}
	functionOutputImage := func(payloadLength int) openairesponses.ResponseInputItemUnionParam {
		return openairesponses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
				CallID: "call-image",
				Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
					OfResponseFunctionCallOutputItemArray: openairesponses.ResponseFunctionCallOutputItemListParam{{
						OfInputImage: &openairesponses.ResponseInputImageContentParam{
							ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", payloadLength)),
						},
					}},
				},
			},
		}
	}

	for name, build := range map[string]func(int) openairesponses.ResponseInputItemUnionParam{
		"user message":    userImage,
		"function output": functionOutputImage,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t,
				approximateResponseInputItemTokens(build(100)),
				approximateResponseInputItemTokens(build(100_000)),
			)
		})
	}
}

func TestTrimRemoteCompactionV2InputDoesNotRewriteForInlineImageEncodingSize(t *testing.T) {
	input := []openairesponses.ResponseInputItemUnionParam{{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: "call-image",
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: openairesponses.ResponseFunctionCallOutputItemListParam{{
					OfInputImage: &openairesponses.ResponseInputImageContentParam{
						ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", 100_000)),
					},
				}},
			},
		},
	}}

	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", 2_500)

	assert.Zero(t, rewritten)
	assert.Empty(t, trimmed[0].OfFunctionCallOutput.Output.OfString)
	require.Len(t, trimmed[0].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray, 1)
}

func TestTrimRemoteCompactionV2InputAccountsForOriginalImagePatches(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewGray(image.Rect(0, 0, 2048, 2048))))
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	input := []openairesponses.ResponseInputItemUnionParam{{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: "call-original-image",
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: openairesponses.ResponseFunctionCallOutputItemListParam{{
					OfInputImage: &openairesponses.ResponseInputImageContentParam{
						Detail:   openairesponses.ResponseInputImageContentDetailOriginal,
						ImageURL: param.NewOpt(imageURL),
					},
				}},
			},
		},
	}}

	assert.Greater(t, approximateResponseInputItemTokens(input[0]), 4_000)
	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", 3_000)

	assert.Equal(t, 1, rewritten)
	require.True(t, trimmed[0].OfFunctionCallOutput.Output.OfString.Valid())
	assert.Equal(t, remoteCompactionV2TruncatedOutputMessage, trimmed[0].OfFunctionCallOutput.Output.OfString.Value)
}

func TestEstimateRemoteCompactionV2ContextTokensCountsRetainedImages(t *testing.T) {
	withoutImage := fromStoredItems([]StoredInputItem{{
		Type:    "message",
		Role:    "user",
		Content: "hello",
		RawItem: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`),
	}, {Type: "compaction", EncryptedContent: "encrypted"}})
	withImage := fromStoredItems([]StoredInputItem{{
		Type:    "message",
		Role:    "user",
		Content: "hello",
		RawItem: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}`),
	}, {Type: "compaction", EncryptedContent: "encrypted"}})

	delta := estimateRemoteCompactionV2ContextTokens("system", withImage) -
		estimateRemoteCompactionV2ContextTokens("system", withoutImage)
	assert.Greater(t, delta, 1_800)
	assert.Less(t, delta, 1_900)
}

func TestTrimRemoteCompactionV2InputRewritesNewestFunctionOutputsFirst(t *testing.T) {
	oldOutput := openairesponses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: "call-old",
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfString: param.NewOpt(strings.Repeat("o", 4_000)),
			},
		},
	}
	newOutput := openairesponses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: "call-new",
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfString: param.NewOpt(strings.Repeat("n", 4_000)),
			},
		},
	}
	input := []openairesponses.ResponseInputItemUnionParam{oldOutput, newOutput}
	totalTokens := approximateResponseInputItemTokens(oldOutput) + approximateResponseInputItemTokens(newOutput)
	rewrittenNew, ok := rewriteResponseInputFunctionOutputForContextWindow(newOutput)
	require.True(t, ok)
	contextWindow := totalTokens - approximateResponseInputItemTokens(newOutput) + approximateResponseInputItemTokens(rewrittenNew)

	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", contextWindow)

	assert.Equal(t, 1, rewritten)
	assert.Equal(t, strings.Repeat("o", 4_000), trimmed[0].OfFunctionCallOutput.Output.OfString.Value)
	assert.Equal(t, remoteCompactionV2TruncatedOutputMessage, trimmed[1].OfFunctionCallOutput.Output.OfString.Value)
	assert.Equal(t, strings.Repeat("n", 4_000), input[1].OfFunctionCallOutput.Output.OfString.Value, "request fitting must not mutate the history snapshot")
}

func TestTrimRemoteCompactionV2InputRewritesMultimodalFunctionOutput(t *testing.T) {
	outputItems := openairesponses.ResponseFunctionCallOutputItemListParam{
		openairesponses.ResponseFunctionCallOutputItemParamOfInputText(strings.Repeat("large", 2_000)),
		{
			OfInputImage: &openairesponses.ResponseInputImageContentParam{
				ImageURL: param.NewOpt("data:image/png;base64," + strings.Repeat("a", 4_000)),
			},
		},
	}
	input := []openairesponses.ResponseInputItemUnionParam{{
		OfFunctionCallOutput: &openairesponses.ResponseInputItemFunctionCallOutputParam{
			CallID: "call-multimodal",
			Output: openairesponses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfResponseFunctionCallOutputItemArray: outputItems,
			},
		},
	}}

	trimmed, rewritten := trimRemoteCompactionV2InputToContextWindow(input, "", 1)

	assert.Equal(t, 1, rewritten)
	require.True(t, trimmed[0].OfFunctionCallOutput.Output.OfString.Valid())
	assert.Equal(t, remoteCompactionV2TruncatedOutputMessage, trimmed[0].OfFunctionCallOutput.Output.OfString.Value)
	assert.Empty(t, trimmed[0].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray)
	assert.Len(t, input[0].OfFunctionCallOutput.Output.OfResponseFunctionCallOutputItemArray, 2, "original multimodal output must remain intact")
}

func TestCompactContextRemoteV2UpdatesContextWindowEstimate(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.Usage.CurrentContextWindow = 200000
	thread.Usage.MaxContextWindow = 1047576
	thread.ToolResults = map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "bash", Success: true},
	}

	var captured openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return remoteCompactionV2Stream(t, "enc_value")
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.Equal(t,
		estimateRemoteCompactionV2ContextTokens(captured.Instructions.Value, thread.inputItemsSnapshot()),
		thread.Usage.CurrentContextWindow,
	)
	assert.Equal(t, 1047576, thread.Usage.MaxContextWindow)
	assert.Empty(t, thread.ToolResults)
}

func TestCompactContextRemoteV2UsesWebSocket(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.1-codex",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	thread := &Thread{
		Thread:                base.NewThread(config, "conv-test"),
		isCodex:               true,
		codexInstallationID:   "00000000-0000-4000-8000-000000000001",
		codexWindowGeneration: 3,
		useWebSocket:          true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
		webSocketContinuation: responsesWebSocketContinuation{
			connectionGeneration: 1,
			responseID:           "resp_before_compact",
		},
	}

	var captured openairesponses.ResponseNewParams
	var capturedHeaders []string
	fakeWebSocket := &fakeResponsesWebSocketStreamer{
		streamFunc: func(_ context.Context, params openairesponses.ResponseNewParams, headers []string, _ auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			captured = params
			capturedHeaders = append([]string(nil), headers...)
			return remoteCompactionV2Stream(t, "ws-encrypted-summary"), nil
		},
	}
	thread.webSocket = fakeWebSocket
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		t.Fatal("HTTPS fallback should not run when websocket compaction succeeds")
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	require.NotEmpty(t, captured.Input.OfInputItemList)
	assert.NotNil(t, captured.Input.OfInputItemList[len(captured.Input.OfInputItemList)-1].OfCompactionTrigger)
	assert.Contains(t, capturedHeaders, auth.CodexBetaFeaturesHeader+": "+auth.CodexBetaFeatures)
	assert.Contains(t, capturedHeaders, "session-id: conv-test")
	assert.Contains(t, capturedHeaders, "thread-id: conv-test")
	assert.Contains(t, capturedHeaders, auth.CodexInstallationIDHeader+": "+thread.codexInstallationID)
	assert.Contains(t, capturedHeaders, auth.CodexWindowIDHeader+": conv-test:3")
	clientMetadata := codexClientMetadataFromParams(t, captured)
	assert.Equal(t, "conv-test:3", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "compaction", turnMetadata["request_kind"])
	compaction, ok := turnMetadata["compaction"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "manual", compaction["trigger"])
	assert.Equal(t, "standalone_turn", compaction["phase"])
	assert.Empty(t, thread.webSocketContinuation.responseID)
	assert.Equal(t, 1, fakeWebSocket.resets)
	assert.Equal(t, uint64(4), thread.codexWindowGenerationSnapshot())
	require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
	assert.Equal(t, "ws-encrypted-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
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

func TestCompactContextRemoteV2FallsBackFromWebSocketToHTTPS(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     1,
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
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	webSocketAttempts := 0
	thread.webSocket = &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			webSocketAttempts++
			return responseStreamFromMaps(t, []map[string]any{{
				"type": "response.output_item.done",
				"item": map[string]any{"type": "compaction", "encrypted_content": "partial"},
			}}), nil
		},
	}
	httpsAttempts := 0
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		httpsAttempts++
		return remoteCompactionV2Stream(t, "https-summary")
	}
	thread.compactWithSummaryFunc = func(context.Context) error {
		t.Fatal("summary fallback should not run when HTTPS fallback succeeds")
		return nil
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.Equal(t, 1, webSocketAttempts)
	assert.Equal(t, 1, httpsAttempts)
	assert.False(t, thread.useWebSocket)

	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		httpsAttempts++
		return nil
	}
	thread.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}
	_, _, _, err := thread.processMessageExchange(
		context.Background(),
		&llmtypes.StringCollectorHandler{Silent: true},
		"gpt-5.5",
		256,
		"system",
		llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, webSocketAttempts, "sticky fallback must keep later turns off websocket")
	assert.Equal(t, 2, httpsAttempts)
	require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
	assert.Equal(t, "https-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
}

func TestCompactContextRemoteV2PreservesWebSocketUsageAcrossHTTPSFallback(t *testing.T) {
	for _, httpsSucceeds := range []bool{true, false} {
		name := "https failure"
		if httpsSucceeds {
			name = "https success"
		}
		t.Run(name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.6-sol",
				Retry: llmtypes.RetryConfig{
					Attempts:     1,
					InitialDelay: 1,
					MaxDelay:     1,
					BackoffType:  "fixed",
				},
				OpenAI: &llmtypes.OpenAIConfig{
					Platform:    "openai",
					ServiceTier: llmtypes.OpenAIServiceTierAuto,
				},
			}
			_, customPricing := loadCustomConfiguration(config)
			thread := &Thread{
				Thread:        base.NewThread(config, "conv-websocket-fallback-usage"),
				customPricing: customPricing,
				useWebSocket:  true,
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}
			thread.Usage.CurrentContextWindow = 55
			thread.Usage.MaxContextWindow = 100
			thread.webSocket = &fakeResponsesWebSocketStreamer{
				streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
					return responseStreamFromMaps(t, []map[string]any{
						remoteCompactionV2TerminalEvent("response.incomplete", "priority", 10, 1, 2),
					}), nil
				},
			}
			thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
				events := []map[string]any{
					remoteCompactionV2TerminalEvent("response.incomplete", "default", 20, 2, 4),
				}
				if httpsSucceeds {
					events = []map[string]any{
						{
							"type": "response.output_item.done",
							"item": map[string]any{"type": "compaction", "encrypted_content": "https-summary"},
						},
						remoteCompactionV2TerminalEvent("response.completed", "default", 20, 2, 4),
					}
				}
				return responseStreamFromMaps(t, events)
			}

			err := thread.compactContextRemoteV2(context.Background())
			if httpsSucceeds {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "response incomplete")
			}
			assert.False(t, thread.useWebSocket)
			usage := thread.GetUsage()
			assert.Equal(t, 30, usage.InputTokens)
			assert.Equal(t, 6, usage.CacheReadInputTokens)
			assert.Equal(t, 3, usage.OutputTokens)
			assert.InDelta(t, 8*0.00001+16*0.000005, usage.InputCost, 1e-12)
			assert.InDelta(t, 2*0.000001+4*0.0000005, usage.CacheReadCost, 1e-12)
			assert.InDelta(t, 1*0.00006+2*0.00003, usage.OutputCost, 1e-12)
			if httpsSucceeds {
				assert.NotEqual(t, 55, usage.CurrentContextWindow)
				require.NotNil(t, thread.inputItemsSnapshot()[1].OfCompaction)
			} else {
				assert.Equal(t, 55, usage.CurrentContextWindow)
				assert.Equal(t, 100, usage.MaxContextWindow)
				history := thread.inputItemsSnapshot()
				require.Len(t, history, 1)
				assert.Nil(t, history[0].OfCompaction)
			}
		})
	}
}

func TestCompactContextRemoteV2NonRetryableWebSocketErrorDoesNotDisableWebSocket(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 2},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread:       base.NewThread(config, "conv-test"),
		useWebSocket: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}

	webSocketAttempts := 0
	fakeWebSocket := &fakeResponsesWebSocketStreamer{
		streamFunc: func(context.Context, openairesponses.ResponseNewParams, []string, auth.HTTPAuthorizer) (*ssestream.Stream[openairesponses.ResponseStreamEventUnion], error) {
			webSocketAttempts++
			return responseStreamFromMaps(t, []map[string]any{{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_without_compaction",
					"status": "completed",
				},
			}}), nil
		},
	}
	thread.webSocket = fakeWebSocket
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		t.Fatal("non-retryable websocket validation errors must not fall back to HTTPS")
		return nil
	}

	err := thread.compactContextRemoteV2(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected exactly one compaction output item")
	assert.Equal(t, 1, webSocketAttempts)
	assert.True(t, thread.useWebSocket)
	assert.Equal(t, 0, fakeWebSocket.resets)
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

func TestCompactContextRemoteV2UsesSDKResponsesEndpoint(t *testing.T) {
	var requestBody map[string]any
	var requestHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		requestHeaders = r.Header.Clone()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))

		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"sdk-encrypted-summary\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_sdk_compact\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1,\"output_tokens_details\":{\"reasoning_tokens\":0},\"total_tokens\":2}}}\n\n"))
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
		Model:    "gpt-5.1-codex",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex", BaseURL: server.URL},
	}
	thread := &Thread{
		Thread:              base.NewThread(config, "conv-sdk"),
		client:              &client,
		isCodex:             true,
		codexInstallationID: "00000000-0000-4000-8000-000000000001",
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.newStreamingFunc = client.Responses.NewStreaming

	require.NoError(t, thread.CompactContext(context.Background()))

	assert.Equal(t, auth.CodexBetaFeatures, requestHeaders.Get(auth.CodexBetaFeaturesHeader))
	assert.Equal(t, "conv-sdk", requestHeaders.Get("session-id"))
	assert.Equal(t, "conv-sdk", requestHeaders.Get("thread-id"))
	assert.Equal(t, thread.codexInstallationID, requestHeaders.Get(auth.CodexInstallationIDHeader))
	assert.Equal(t, "conv-sdk:0", requestHeaders.Get(auth.CodexWindowIDHeader))
	input, ok := requestBody["input"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, input)
	trigger, ok := input[len(input)-1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "compaction_trigger", trigger["type"])
	assert.Equal(t, "gpt-5.1-codex", requestBody["model"])
	assert.Equal(t, false, requestBody["store"])
	clientMetadata, ok := requestBody["client_metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, thread.codexInstallationID, clientMetadata[auth.CodexInstallationIDHeader])
	assert.Equal(t, "conv-sdk:0", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "compaction", turnMetadata["request_kind"])
	compaction, ok := turnMetadata["compaction"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "manual", compaction["trigger"])
	assert.Equal(t, "user_requested", compaction["reason"])
	assert.Equal(t, "responses_compaction_v2", compaction["implementation"])
	assert.Equal(t, "standalone_turn", compaction["phase"])
	assert.Equal(t, "memento", compaction["strategy"])
	assert.Equal(t, uint64(1), thread.codexWindowGenerationSnapshot())
	require.NotNil(t, thread.inputItems[len(thread.inputItems)-1].OfCompaction)
	assert.Equal(t, "sdk-encrypted-summary", thread.inputItems[len(thread.inputItems)-1].OfCompaction.EncryptedContent)
}

func TestCompactContextRemoteV2OwnsHTTPRetryBudget(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"server_error","message":"try again","type":"server_error"}}`))
	}))
	defer server.Close()

	client := openai.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIKey("test-key"),
	)
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai", BaseURL: server.URL},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		client: &client,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.newStreamingFunc = client.Responses.NewStreaming

	err := thread.compactContextRemoteV2(context.Background())
	require.Error(t, err)
	assert.Equal(t, int32(3), requests.Load(), "outer V2 retry loop should be the only retry owner")
}

func TestCompactContextRemoteV2DoesNotRetryGenericHTTP429(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer server.Close()

	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test-key"))
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry: llmtypes.RetryConfig{
			Attempts:     3,
			InitialDelay: 1,
			MaxDelay:     1,
			BackoffType:  "fixed",
		},
		OpenAI: &llmtypes.OpenAIConfig{Platform: "openai", BaseURL: server.URL},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		client: &client,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("hello", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.newStreamingFunc = client.Responses.NewStreaming

	err := thread.compactContextRemoteV2(context.Background())
	require.Error(t, err)
	assert.Equal(t, int32(1), requests.Load())
}

func TestCompactContextRemoteV2RejectsStaleHistorySnapshot(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("original", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "original"}},
	}
	thread.Usage.InputTokens = 7
	thread.Usage.CurrentContextWindow = 55
	thread.Usage.MaxContextWindow = 100

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	successStream := remoteCompactionV2Stream(t, "stale-summary")
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		close(requestStarted)
		<-releaseRequest
		return successStream
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- thread.compactContextRemoteV2(context.Background())
	}()
	<-requestStarted
	thread.AddUserMessage(context.Background(), "arrived while compacting")
	close(releaseRequest)

	err := <-errCh
	require.ErrorIs(t, err, errRemoteCompactionHistoryChanged)
	snapshot := thread.snapshotHistory()
	require.Len(t, snapshot.storedItems, 2)
	assert.Equal(t, "original", snapshot.storedItems[0].Content)
	assert.Equal(t, "arrived while compacting", snapshot.storedItems[1].Content)
	for _, item := range snapshot.storedItems {
		assert.NotEqual(t, "compaction", item.Type)
	}
	usage := thread.GetUsage()
	assert.Equal(t, 107, usage.InputTokens, "billable compaction input must still be recorded")
	assert.Equal(t, 10, usage.OutputTokens)
	assert.Equal(t, 55, usage.CurrentContextWindow, "stale compaction must not replace live context accounting")
	assert.Equal(t, 100, usage.MaxContextWindow)
}

func TestIsRetryableResponsesStreamErrorClassifiesSDKStreamErrors(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		retryable bool
	}{
		{name: "invalid prompt", payload: `{"error":{"code":"invalid_prompt","message":"bad prompt"}}`},
		{name: "quota", payload: `{"error":{"code":"insufficient_quota","message":"quota exceeded"}}`},
		{name: "overload", payload: `{"error":{"code":"server_is_overloaded","message":"busy"}}`, retryable: true},
		{name: "unknown", payload: `{"error":{"code":"transient_error","message":"retry"}}`, retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ssestream.StreamError{Event: ssestream.Event{Data: []byte(tt.payload)}}
			assert.Equal(t, tt.retryable, isRetryableResponsesStreamError(err))
		})
	}
}

func TestFromStoredItemsWithRawCompactionSummary(t *testing.T) {
	stored := []StoredInputItem{
		{
			Type: "compaction_summary",
			RawItem: json.RawMessage(`{
				"id": "cmp_1",
				"type": "compaction_summary",
				"encrypted_content": "enc_value"
			}`),
		},
	}

	restored := fromStoredItems(stored)
	require.Len(t, restored, 1)
	require.NotNil(t, restored[0].OfCompaction)
	assert.Equal(t, "enc_value", restored[0].OfCompaction.EncryptedContent)
}

func TestFromStoredItemsWithCompactedAssistantRawMessage(t *testing.T) {
	stored := []StoredInputItem{
		{
			Type:    "message",
			Role:    "assistant",
			Content: "Compacted assistant context",
			RawItem: json.RawMessage(`{
				"id": "msg_1",
				"type": "message",
				"role": "assistant",
				"status": "completed",
				"content": [{"type": "output_text", "text": "Compacted assistant context"}]
			}`),
		},
	}

	restored := fromStoredItems(stored)
	require.Len(t, restored, 1)
	require.NotNil(t, restored[0].OfOutputMessage)
	b, err := json.Marshal(restored[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"role":"assistant"`)
	assert.Contains(t, string(b), `"type":"message"`)
	assert.Contains(t, string(b), `"type":"output_text"`)
	assert.Equal(t, "Compacted assistant context", extractInputItemText(restored[0]))
}

func TestStreamMessages(t *testing.T) {
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "Hello"
		},
		{
			"type": "function_call",
			"call_id": "call_123",
			"name": "test_tool",
			"arguments": "{}"
		},
		{
			"type": "function_call_output",
			"call_id": "call_123",
			"output": "result"
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 3)

	assert.Equal(t, "text", streamable[0].Kind)
	assert.Equal(t, "user", streamable[0].Role)

	assert.Equal(t, "tool-use", streamable[1].Kind)
	assert.Equal(t, "test_tool", streamable[1].ToolName)

	assert.Equal(t, "tool-result", streamable[2].Kind)
}

func TestStreamMessagesWebSearchOpenPageUsesRawItemURL(t *testing.T) {
	inputItems := `[
		{
			"type": "web_search_call",
			"call_id": "ws_open",
			"status": "completed",
			"action": "open_page",
			"raw_item": {
				"id": "ws_open",
				"type": "web_search_call",
				"status": "completed",
				"action": {
					"type": "open_page",
					"url": "https://example.com/story"
				}
			}
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 2)

	assert.Equal(t, "tool-use", streamable[0].Kind)
	assert.JSONEq(t, `{"status":"completed","type":"open_page","url":"https://example.com/story"}`, streamable[0].Input)
}

func TestStreamMessagesWebSearchFindInPageUsesRawItemDetails(t *testing.T) {
	inputItems := `[
		{
			"type": "web_search_call",
			"call_id": "ws_find",
			"status": "completed",
			"action": "find_in_page",
			"raw_item": {
				"id": "ws_find",
				"type": "web_search_call",
				"status": "completed",
				"action": {
					"type": "find_in_page",
					"url": "https://example.com/docs",
					"pattern": "installation"
				}
			}
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 2)

	assert.Equal(t, "tool-use", streamable[0].Kind)
	assert.JSONEq(t, `{"status":"completed","type":"find_in_page","url":"https://example.com/docs","pattern":"installation"}`, streamable[0].Input)
}

func TestExtractMessagesWithReasoning(t *testing.T) {
	// Create sample input items with reasoning as a separate item
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "What is 2+2?"
		},
		{
			"type": "reasoning",
			"role": "assistant",
			"content": "I need to add 2 and 2 together. 2+2=4."
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "The answer is 4."
		}
	]`

	messages, err := ExtractMessages([]byte(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, messages, 3) // user + thinking + assistant

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "What is 2+2?", messages[0].Content)

	// Thinking message should come before the assistant message
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Contains(t, messages[1].Content, "Thinking")
	assert.Contains(t, messages[1].Content, "I need to add 2 and 2 together")

	assert.Equal(t, "assistant", messages[2].Role)
	assert.Equal(t, "The answer is 4.", messages[2].Content)
}

func TestStreamMessagesWithReasoning(t *testing.T) {
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "Hello"
		},
		{
			"type": "reasoning",
			"role": "assistant",
			"content": "The user greeted me, I should respond politely."
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "Hi there!"
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 3) // user + thinking + text

	assert.Equal(t, "text", streamable[0].Kind)
	assert.Equal(t, "user", streamable[0].Role)

	assert.Equal(t, "thinking", streamable[1].Kind)
	assert.Equal(t, "assistant", streamable[1].Role)
	assert.Equal(t, "The user greeted me, I should respond politely.", streamable[1].Content)

	assert.Equal(t, "text", streamable[2].Kind)
	assert.Equal(t, "assistant", streamable[2].Role)
	assert.Equal(t, "Hi there!", streamable[2].Content)
}

func TestExtractMessagesAfterCompaction(t *testing.T) {
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "old user"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "old assistant"
		},
		{
			"type": "compaction",
			"encrypted_content": "enc"
		},
		{
			"type": "message",
			"role": "user",
			"content": "new user"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "new assistant"
		}
	]`

	messages, err := ExtractMessages([]byte(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, messages, 3)

	assert.Equal(t, "assistant", messages[0].Role)
	assert.Equal(t, compactedHistoryNotice, messages[0].Content)
	assert.Equal(t, "user", messages[1].Role)
	assert.Equal(t, "new user", messages[1].Content)
	assert.Equal(t, "assistant", messages[2].Role)
	assert.Equal(t, "new assistant", messages[2].Content)
}

func TestStreamMessagesAfterCompaction(t *testing.T) {
	inputItems := `[
		{
			"type": "message",
			"role": "user",
			"content": "old user"
		},
		{
			"type": "compaction",
			"encrypted_content": "enc"
		},
		{
			"type": "reasoning",
			"role": "assistant",
			"content": "thinking"
		},
		{
			"type": "message",
			"role": "assistant",
			"content": "new assistant"
		}
	]`

	streamable, err := StreamMessages(json.RawMessage(inputItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 3)

	assert.Equal(t, "text", streamable[0].Kind)
	assert.Equal(t, "assistant", streamable[0].Role)
	assert.Equal(t, compactedHistoryNotice, streamable[0].Content)
	assert.Equal(t, "thinking", streamable[1].Kind)
	assert.Equal(t, "text", streamable[2].Kind)
	assert.Equal(t, "assistant", streamable[2].Role)
	assert.Equal(t, "new assistant", streamable[2].Content)
}

func TestStorageRoundTripWithReasoning(t *testing.T) {
	// Create stored items directly (simulating what happens during streaming)
	// Items are stored in order: user message -> reasoning -> assistant message
	storedItems := []StoredInputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: "What is 2+2?",
		},
		{
			Type:    "reasoning",
			Role:    "assistant",
			Content: "I need to add 2 and 2 together. 2+2=4.",
		},
		{
			Type:    "message",
			Role:    "assistant",
			Content: "The answer is 4.",
		},
	}

	// Verify stored format has reasoning as a separate item
	require.Len(t, storedItems, 3)
	assert.Equal(t, "message", storedItems[0].Type)
	assert.Equal(t, "reasoning", storedItems[1].Type)
	assert.Equal(t, "I need to add 2 and 2 together. 2+2=4.", storedItems[1].Content)
	assert.Equal(t, "message", storedItems[2].Type)

	// Convert to SDK format - reasoning is skipped (only for display)
	restoredItems := fromStoredItems(storedItems)

	// Verify restored items (2 SDK items, reasoning is skipped for API calls)
	require.Len(t, restoredItems, 2)
	assert.NotNil(t, restoredItems[0].OfMessage)
	assert.Equal(t, openairesponses.EasyInputMessageRoleUser, restoredItems[0].OfMessage.Role)
	assert.NotNil(t, restoredItems[1].OfOutputMessage)
	b, err := json.Marshal(restoredItems[1])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"role":"assistant"`)
	assert.Contains(t, string(b), `"type":"message"`)
	assert.Contains(t, string(b), `"type":"output_text"`)
	assert.Contains(t, string(b), `"text":"The answer is 4."`)

	// Verify JSON round-trip preserves reasoning for display
	jsonData, err := json.Marshal(storedItems)
	require.NoError(t, err)

	var parsedItems []StoredInputItem
	err = json.Unmarshal(jsonData, &parsedItems)
	require.NoError(t, err)

	require.Len(t, parsedItems, 3)
	assert.Equal(t, "reasoning", parsedItems[1].Type)
	assert.Equal(t, "I need to add 2 and 2 together. 2+2=4.", parsedItems[1].Content)
}

func TestFromStoredItemsAssistantStoredMessageUsesOutputMessage(t *testing.T) {
	storedItems := []StoredInputItem{{
		Type:    "message",
		Role:    "assistant",
		Content: "The answer is 4.",
	}}

	restoredItems := fromStoredItems(storedItems)
	require.Len(t, restoredItems, 1)
	require.NotNil(t, restoredItems[0].OfOutputMessage)
	b, err := json.Marshal(restoredItems[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"role":"assistant"`)
	assert.Contains(t, string(b), `"type":"message"`)
	assert.Contains(t, string(b), `"type":"output_text"`)
	assert.Contains(t, string(b), `"text":"The answer is 4."`)
}

func TestFromStoredItemsAssistantRawInputTextMessageNormalizesToOutputMessage(t *testing.T) {
	storedItems := []StoredInputItem{{
		Type:    "message",
		Role:    "assistant",
		Content: "legacy assistant text",
		RawItem: json.RawMessage(`{
			"id": "msg_legacy",
			"type": "message",
			"role": "assistant",
			"status": "completed",
			"phase": "final_answer",
			"content": [{"type": "input_text", "text": "legacy assistant text"}]
		}`),
	}}

	restoredItems := fromStoredItems(storedItems)
	require.Len(t, restoredItems, 1)
	require.NotNil(t, restoredItems[0].OfOutputMessage)
	b, err := json.Marshal(restoredItems[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"id":"msg_legacy"`)
	assert.Contains(t, string(b), `"role":"assistant"`)
	assert.Contains(t, string(b), `"status":"completed"`)
	assert.Contains(t, string(b), `"phase":"final_answer"`)
	assert.Contains(t, string(b), `"type":"output_text"`)
	assert.Contains(t, string(b), `"text":"legacy assistant text"`)
}

func TestAddUserMessageWithImagesPersistsRawItem(t *testing.T) {
	thread := &Thread{
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}

	thread.AddUserMessage(context.Background(), "what is in the image?", "data:image/png;base64,aGVsbG8=")

	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "message", thread.storedItems[0].Type)
	assert.Equal(t, "user", thread.storedItems[0].Role)
	assert.Equal(t, "what is in the image?", thread.storedItems[0].Content)
	require.NotEmpty(t, thread.storedItems[0].RawItem)
	assert.Contains(t, string(thread.storedItems[0].RawItem), `"type":"input_image"`)
	assert.Contains(t, string(thread.storedItems[0].RawItem), `"image_url":"data:image/png;base64,aGVsbG8="`)

	restoredItems := fromStoredItems(thread.storedItems)
	require.Len(t, restoredItems, 1)
	require.NotNil(t, restoredItems[0].OfMessage)
	require.Len(t, restoredItems[0].OfMessage.Content.OfInputItemContentList, 2)
	assert.Equal(t, "data:image/png;base64,aGVsbG8=", restoredItems[0].OfMessage.Content.OfInputItemContentList[0].OfInputImage.ImageURL.Value)
	assert.Equal(t, "what is in the image?", restoredItems[0].OfMessage.Content.OfInputItemContentList[1].OfInputText.Text)

	streamable, err := StreamMessages(mustJSON(t, thread.storedItems), nil)
	require.NoError(t, err)
	require.Len(t, streamable, 1)
	assert.Equal(t, "text", streamable[0].Kind)
	assert.Equal(t, "user", streamable[0].Role)
	assert.Equal(t, "what is in the image?", streamable[0].Content)
	assert.Contains(t, string(streamable[0].RawItem), `"type":"input_image"`)
}

func TestAddUserMessageGoalContextWithImagesSeparatesAttachments(t *testing.T) {
	thread := &Thread{
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}
	goalContext := "<goal_context>\nContinue working.\n</goal_context>"

	thread.AddUserMessage(context.Background(), goalContext, "data:image/png;base64,aGVsbG8=")

	require.Len(t, thread.inputItems, 2)
	require.Len(t, thread.storedItems, 2)
	assert.Empty(t, extractInputItemText(thread.inputItems[0]))
	assert.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, extractInputItemImageURLs(thread.inputItems[0]))
	assert.Equal(t, goalContext, extractInputItemText(thread.inputItems[1]))
	assert.Empty(t, thread.storedItems[0].Content)
	assert.Equal(t, goalContext, thread.storedItems[1].Content)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestCleanupOrphanedItems_RemovesTrailingFunctionCallFromStorage(t *testing.T) {
	thread := &Thread{
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role: openairesponses.EasyInputMessageRoleUser,
				},
			},
			{
				OfFunctionCall: &openairesponses.ResponseFunctionToolCallParam{
					CallID:    "call_orphaned",
					Name:      "bash",
					Arguments: `{"command":"ls"}`,
				},
			},
		},
		storedItems: []StoredInputItem{
			{
				Type:    "message",
				Role:    "user",
				Content: "list files",
			},
			{
				Type:      "function_call",
				CallID:    "call_orphaned",
				Name:      "bash",
				Arguments: `{"command":"ls"}`,
			},
		},
	}

	thread.cleanupOrphanedItems()

	require.Len(t, thread.inputItems, 1)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "message", thread.storedItems[0].Type)
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
	assert.Equal(t, standardModels, priorityModels)

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

func TestAddUserMessageAppendsInputItems(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	thread, err := NewThread(config)
	require.NoError(t, err)

	// Initially, input history should be empty
	assert.Empty(t, thread.inputItems)

	// Add a user message
	ctx := context.Background()
	thread.AddUserMessage(ctx, "Hello, world!")

	// Input history should now have one item
	assert.Len(t, thread.inputItems, 1)

	// Add another message
	thread.AddUserMessage(ctx, "How are you?")

	// Input history should now have two items
	assert.Len(t, thread.inputItems, 2)
}

func TestAddAssistantMessageAppendsInputAndStoredItems(t *testing.T) {
	thread := &Thread{}
	thread.AddAssistantMessage(t.Context(), "Direct response")

	require.Len(t, thread.inputItems, 1)
	require.NotNil(t, thread.inputItems[0].OfMessage)
	assert.Equal(t, openairesponses.EasyInputMessageRoleAssistant, thread.inputItems[0].OfMessage.Role)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "assistant", thread.storedItems[0].Role)
	assert.Equal(t, "Direct response", thread.storedItems[0].Content)
}

func TestThreadInitialization(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
	}

	thread, err := NewThread(config)
	require.NoError(t, err)

	assert.NotNil(t, thread.inputItems)
}

// Integration tests that use the real OpenAI API
// These tests are skipped if OPENAI_API_KEY is not set to a valid key

func skipIfNoAPIKey(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || apiKey == "test-key" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set or is test-key")
	}
}

func TestIntegration_CompactContext(t *testing.T) {
	skipIfNoAPIKey(t)

	ctx := context.Background()

	// Use a cheap model for testing
	config := llmtypes.Config{
		Provider:  "openai",
		Model:     "gpt-4.1-mini",
		WeakModel: "gpt-4.1-mini",
		MaxTokens: 1024,
	}

	thread, err := NewThread(config)
	require.NoError(t, err)

	// Add conversation history to compact
	thread.AddUserMessage(ctx, "Hello, I'm working on a project that involves building a REST API.")
	thread.AddUserMessage(ctx, "The API needs to handle user authentication, data validation, and rate limiting.")
	thread.AddUserMessage(ctx, "I'm using Go with the Gin framework.")
	thread.AddUserMessage(ctx, "Can you suggest a good project structure?")

	// Store the original input items count
	originalItemCount := len(thread.inputItems)
	t.Logf("Original input items count: %d", originalItemCount)

	// Compact the context
	err = thread.CompactContext(ctx)
	require.NoError(t, err)

	t.Logf("Compacted input items count: %d", len(thread.inputItems))

	// Verify the compaction worked
	assert.NotEmpty(t, thread.inputItems, "Compacted items should not be empty")

	// The compacted output should have fewer or equal items
	// (user messages + one compaction item)
	assert.LessOrEqual(t, len(thread.inputItems), originalItemCount+1,
		"Compacted items should be fewer or equal to original")

	// Verify that tool results were cleared
	assert.Empty(t, thread.ToolResults, "ToolResults should be cleared after compaction")

	// Check that we have at least one compaction item
	hasCompactionItem := false
	for _, item := range thread.inputItems {
		if item.OfCompaction != nil {
			hasCompactionItem = true
			assert.NotEmpty(t, item.OfCompaction.EncryptedContent,
				"Compaction item should have encrypted content")
			break
		}
	}
	assert.True(t, hasCompactionItem, "Should have at least one compaction item after compacting")
}

func TestIntegration_SendMessageAndCompact(t *testing.T) {
	skipIfNoAPIKey(t)

	ctx := context.Background()

	// Use a cheap model for testing
	config := llmtypes.Config{
		Provider:  "openai",
		Model:     "gpt-4.1-mini",
		WeakModel: "gpt-4.1-mini",
		MaxTokens: 256,
	}

	thread, err := NewThread(config)
	require.NoError(t, err)

	// Create a simple handler that collects text
	handler := &llmtypes.StringCollectorHandler{Silent: true}

	// Send a message and get a response
	_, err = thread.SendMessage(ctx, "What is 2 + 2? Reply with just the number.", handler, llmtypes.MessageOpt{
		NoToolUse: true,
		MaxTurns:  1,
	})
	require.NoError(t, err)

	response := handler.CollectedText()
	t.Logf("Response: %s", response)
	assert.NotEmpty(t, response)

	// Store the count before compacting
	itemCountBeforeCompact := len(thread.inputItems)
	t.Logf("Input items before compact: %d", itemCountBeforeCompact)

	// Now compact the context
	err = thread.CompactContext(ctx)
	require.NoError(t, err)

	t.Logf("Input items after compact: %d", len(thread.inputItems))

	// Verify we can still continue the conversation after compaction
	handler2 := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = thread.SendMessage(ctx, "What is 3 + 3? Reply with just the number.", handler2, llmtypes.MessageOpt{
		NoToolUse: true,
		MaxTurns:  1,
	})
	require.NoError(t, err)

	response2 := handler2.CollectedText()
	t.Logf("Response after compact: %s", response2)
	assert.NotEmpty(t, response2)
}

type mockResponsesConversationStore struct {
	savedRecords []convtypes.ConversationRecord
	loadedRecord *convtypes.ConversationRecord
}

func (m *mockResponsesConversationStore) Save(_ context.Context, record convtypes.ConversationRecord) error {
	m.savedRecords = append(m.savedRecords, record)
	return nil
}

func (m *mockResponsesConversationStore) Load(_ context.Context, _ string) (convtypes.ConversationRecord, error) {
	if m.loadedRecord != nil {
		return *m.loadedRecord, nil
	}
	return convtypes.ConversationRecord{}, nil
}

func (*mockResponsesConversationStore) Delete(_ context.Context, _ string) error {
	return nil
}

func (*mockResponsesConversationStore) Query(_ context.Context, _ convtypes.QueryOptions) (convtypes.QueryResult, error) {
	return convtypes.QueryResult{}, nil
}

func (*mockResponsesConversationStore) Close() error {
	return nil
}

func TestProcessMessageExchangeSavesConversationPerTurn(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai", ServiceTier: llmtypes.OpenAIServiceTierFlex}}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}

	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleAssistant,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("turn")},
			},
		})
		thread.storedItems = append(thread.storedItems, StoredInputItem{Type: "message", Role: "assistant", Content: "turn"})
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	require.Equal(t, 1, len(store.savedRecords))
	assert.Equal(t, "openai", store.savedRecords[0].Provider)
	assert.Equal(t, "responses", store.savedRecords[0].Metadata["api_mode"])
	assert.Equal(t, "openai", store.savedRecords[0].Metadata["platform"])
	assert.Equal(t, "flex", store.savedRecords[0].Metadata["service_tier"])
	snapshot, ok, err := conversations.ConfigSnapshotFromMetadata(store.savedRecords[0].Metadata)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "openai", snapshot.Provider)
	assert.Equal(t, "gpt-4.1", snapshot.Model)
	require.NotNil(t, snapshot.OpenAI)
	assert.Equal(t, llmtypes.OpenAIAPIModeResponses, snapshot.OpenAI.APIMode)
}

func TestResponsesSaveConversationPreservesProviderNeutralMetadata(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{Thread: base.NewThread(config, "conv-test")}
	thread.SetState(tools.NewBasicState(context.Background()))
	metadata := conversations.AddSlashCommandDisplay(nil, "expanded recipe prompt", "/init focus", "init")
	for key, value := range metadata {
		thread.SetMetadataValue(key, value)
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "expanded recipe prompt"}}
	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true

	err := thread.SaveConversation(context.Background())
	require.NoError(t, err)
	require.Len(t, store.savedRecords, 1)
	assert.Contains(t, store.savedRecords[0].Metadata, conversations.MessageDisplayMetadataKey)
	assert.Equal(t, "responses", store.savedRecords[0].Metadata["api_mode"])
	assert.Equal(t, "/init focus", store.savedRecords[0].Summary)
}

func TestRemoteCompactionV2PersistsLoadsAndReplaysFollowUp(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "openai",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-compact-lifecycle"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("original request", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "original request"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "persisted-encrypted-summary")
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true
	require.NoError(t, thread.SaveConversation(context.Background()))
	require.Len(t, store.savedRecords, 1)

	var persistedItems []StoredInputItem
	require.NoError(t, json.Unmarshal(store.savedRecords[0].RawMessages, &persistedItems))
	require.Len(t, persistedItems, 2)
	assert.Equal(t, "message", persistedItems[0].Type)
	assert.Equal(t, "compaction", persistedItems[1].Type)
	assert.Equal(t, "persisted-encrypted-summary", persistedItems[1].EncryptedContent)

	loadStore := &mockResponsesConversationStore{loadedRecord: &store.savedRecords[0]}
	restored := &Thread{Thread: base.NewThread(config, "conv-compact-lifecycle")}
	restored.SetState(tools.NewBasicState(context.Background()))
	restored.Store = loadStore
	restored.LoadConversation = restored.loadConversation
	restored.EnablePersistence(context.Background(), true)

	loadedSnapshot := restored.snapshotHistory()
	require.Len(t, loadedSnapshot.inputItems, 2)
	require.NotNil(t, loadedSnapshot.inputItems[1].OfCompaction)
	assert.Equal(t, "persisted-encrypted-summary", loadedSnapshot.inputItems[1].OfCompaction.EncryptedContent)

	restored.AddUserMessage(context.Background(), "follow up")
	var captured openairesponses.ResponseNewParams
	restored.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return nil
	}
	restored.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	_, _, _, err := restored.processMessageExchange(
		context.Background(),
		&llmtypes.StringCollectorHandler{Silent: true},
		"gpt-5.5",
		256,
		"system",
		llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	require.Len(t, captured.Input.OfInputItemList, 3)
	require.NotNil(t, captured.Input.OfInputItemList[1].OfCompaction)
	assert.Equal(t, "persisted-encrypted-summary", captured.Input.OfInputItemList[1].OfCompaction.EncryptedContent)
	assert.Equal(t, "follow up", extractInputItemText(captured.Input.OfInputItemList[2]))
}

func TestCodexWindowGenerationPersistsAcrossCompactionAndLoad(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI: &llmtypes.OpenAIConfig{
			Platform: "codex",
			APIMode:  llmtypes.OpenAIAPIModeResponses,
		},
	}
	installationID := "00000000-0000-4000-8000-000000000001"
	thread := &Thread{
		Thread:              base.NewThread(config, "conv-window"),
		isCodex:             true,
		codexInstallationID: installationID,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("original request", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "original request"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "persisted-window-summary")
	}

	require.NoError(t, thread.CompactContext(context.Background()))
	assert.Equal(t, uint64(1), thread.codexWindowGenerationSnapshot())

	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true
	require.NoError(t, thread.SaveConversation(context.Background()))
	require.Len(t, store.savedRecords, 1)
	assert.Equal(t, uint64(1), store.savedRecords[0].Metadata[convtypes.CodexResponsesWindowGenerationMetadataKey])

	restored := &Thread{
		Thread:              base.NewThread(config, "conv-window"),
		isCodex:             true,
		codexInstallationID: installationID,
	}
	restored.SetState(tools.NewBasicState(context.Background()))
	restored.Store = &mockResponsesConversationStore{loadedRecord: &store.savedRecords[0]}
	restored.LoadConversation = restored.loadConversation
	restored.EnablePersistence(context.Background(), true)
	assert.Equal(t, uint64(1), restored.codexWindowGenerationSnapshot())

	restored.AddUserMessage(context.Background(), "follow up")
	var captured openairesponses.ResponseNewParams
	restored.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		captured = params
		return nil
	}
	restored.processStreamFunc = func(context.Context, *ssestream.Stream[openairesponses.ResponseStreamEventUnion], llmtypes.MessageHandler, string, llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}
	_, _, _, err := restored.processMessageExchange(
		context.WithValue(context.Background(), codexTurnIDContextKey{}, "turn-after-load"),
		&llmtypes.StringCollectorHandler{Silent: true},
		"gpt-5.5",
		256,
		"system",
		llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	clientMetadata := codexClientMetadataFromParams(t, captured)
	assert.Equal(t, "conv-window:1", clientMetadata[auth.CodexWindowIDHeader])
	turnMetadata := codexTurnMetadataFromClientMetadata(t, clientMetadata)
	assert.Equal(t, "turn", turnMetadata["request_kind"])
	assert.Equal(t, "turn-after-load", turnMetadata["turn_id"])
}

func TestPersistedCodexWindowGenerationRejectsMalformedValues(t *testing.T) {
	assert.Equal(t, uint64(2), persistedCodexWindowGeneration(map[string]any{
		convtypes.CodexResponsesWindowGenerationMetadataKey: float64(2),
	}))
	for _, value := range []any{-1, 1.5, "2", "invalid"} {
		assert.Zero(t, persistedCodexWindowGeneration(map[string]any{
			convtypes.CodexResponsesWindowGenerationMetadataKey: value,
		}))
	}
}

func TestSaveConversationSnapshotsCompactionStateCoherently(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-coherent-save"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("old history", openairesponses.EasyInputMessageRoleUser),
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "old history"}},
	}
	thread.Usage.CurrentContextWindow = 50
	thread.Usage.MaxContextWindow = 100
	thread.ToolResults = map[string]tooltypes.StructuredToolResult{
		"call-old": {ToolName: "bash", Success: true},
	}
	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true

	thread.Mu.Lock()
	saveErr := make(chan error, 1)
	go func() {
		saveErr <- thread.SaveConversation(context.Background())
	}()
	require.Eventually(t, func() bool {
		if thread.historyMu.TryLock() {
			thread.historyMu.Unlock()
			return false
		}
		return true
	}, time.Second, time.Millisecond)

	newStoredItems := []StoredInputItem{{Type: "compaction", EncryptedContent: "new-history"}}
	replaceErr := make(chan error, 1)
	go func() {
		replaceErr <- thread.replaceCompactedHistory(
			thread.historyRevision,
			fromStoredItems(newStoredItems),
			newStoredItems,
			5,
		)
	}()
	thread.Mu.Unlock()

	require.NoError(t, <-saveErr)
	require.NoError(t, <-replaceErr)
	require.Len(t, store.savedRecords, 1)
	var persisted []StoredInputItem
	require.NoError(t, json.Unmarshal(store.savedRecords[0].RawMessages, &persisted))
	require.Len(t, persisted, 1)
	assert.Equal(t, "old history", persisted[0].Content)
	assert.Equal(t, 50, store.savedRecords[0].Usage.CurrentContextWindow)
	assert.Contains(t, store.savedRecords[0].ToolResults, "call-old")

	liveSnapshot := thread.snapshotHistory()
	require.Len(t, liveSnapshot.storedItems, 1)
	assert.Equal(t, "compaction", liveSnapshot.storedItems[0].Type)
	assert.Equal(t, 5, thread.GetUsage().CurrentContextWindow)
	assert.Empty(t, thread.GetStructuredToolResults())
}

func TestResponsesSaveConversationKeepsInitialNameAndPreservesExplicitRenames(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{Thread: base.NewThread(config, "conv-name")}
	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "first user request"}}

	require.NoError(t, thread.SaveConversation(context.Background()))
	require.Len(t, store.savedRecords, 1)
	assert.Equal(t, "first user request", store.savedRecords[0].Summary)
	assert.Equal(t, "first user request", conversations.AutomaticConversationName(store.savedRecords[0].Metadata))

	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "compacted history"}}
	require.NoError(t, thread.SaveConversation(context.Background()))
	require.Len(t, store.savedRecords, 2)
	assert.Equal(t, "first user request", store.savedRecords[1].Summary)

	externallyRenamed := store.savedRecords[1]
	externallyRenamed.Metadata = conversations.SetConversationName(externallyRenamed.Metadata, "external rename")
	externallyRenamed.Summary = "external rename"
	store.loadedRecord = &externallyRenamed

	require.NoError(t, thread.SaveConversation(context.Background()))
	require.Len(t, store.savedRecords, 3)
	assert.Equal(t, "external rename", store.savedRecords[2].Summary)
	assert.Equal(t, "external rename", conversations.ExplicitConversationName(thread.GetMetadata()))

	name, err := conversations.RenameThread(context.Background(), thread, "active rename")
	require.NoError(t, err)
	assert.Equal(t, "active rename", name)
	require.Len(t, store.savedRecords, 4)
	assert.Equal(t, "active rename", store.savedRecords[3].Summary)

	externallyRenamedAgain := store.savedRecords[3]
	externallyRenamedAgain.Metadata = conversations.SetConversationName(externallyRenamedAgain.Metadata, "newer external rename")
	externallyRenamedAgain.Summary = "newer external rename"
	store.loadedRecord = &externallyRenamedAgain
	require.NoError(t, thread.SaveConversation(context.Background()))
	require.Len(t, store.savedRecords, 5)
	assert.Equal(t, "newer external rename", store.savedRecords[4].Summary)
}

func TestProcessMessageExchangeInjectsPendingSteer(t *testing.T) {
	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	_, err = steerStore.Enqueue(context.Background(), "conv-test", "Please focus on error handling", nil)
	require.NoError(t, err)

	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
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

	var capturedParams openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		capturedParams = params
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err = thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)

	require.Len(t, capturedParams.Input.OfInputItemList, 2)
	assert.Equal(t, "hello", extractInputItemText(capturedParams.Input.OfInputItemList[0]))
	assert.Equal(t, "Please focus on error handling", extractInputItemText(capturedParams.Input.OfInputItemList[1]))
	assert.Contains(t, handler.CollectedText(), "🗣️ User steering: Please focus on error handling")

	require.Len(t, thread.inputItems, 2)
	require.Len(t, thread.storedItems, 2)
	assert.Equal(t, "Please focus on error handling", thread.storedItems[1].Content)
	hasPending, err := steerStore.HasPending(context.Background(), "conv-test")
	require.NoError(t, err)
	assert.False(t, hasPending)
}

func TestProcessMessageExchangeInjectsPendingSteerWithImages(t *testing.T) {
	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()
	_, err = steerStore.Enqueue(context.Background(), "conv-test", "Use this image", []string{"data:image/png;base64,aGVsbG8="})
	require.NoError(t, err)

	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
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
	_, _, _, err = thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)

	require.Len(t, capturedParams.Input.OfInputItemList, 1)
	assert.Equal(t, "Use this image", extractInputItemText(capturedParams.Input.OfInputItemList[0]))
	assert.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, extractInputItemImageURLs(capturedParams.Input.OfInputItemList[0]))
	assert.Contains(t, handler.CollectedText(), "🗣️ User steering: Use this image (1 image)")
	hasPending, err := steerStore.HasPending(context.Background(), "conv-test")
	require.NoError(t, err)
	assert.False(t, hasPending)
}

func TestProcessMessageExchangeRegistersNativeOpenAISearchToolInRequest(t *testing.T) {
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
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithLLMConfig(config)))
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}

	var capturedParams openairesponses.ResponseNewParams
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		capturedParams = params
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{})
	require.NoError(t, err)

	foundWebSearch := false
	for _, toolDef := range capturedParams.Tools {
		if toolDef.OfWebSearch != nil && toolDef.OfWebSearch.Type == openairesponses.WebSearchToolTypeWebSearch {
			foundWebSearch = true
			break
		}
	}
	assert.True(t, foundWebSearch, "expected native OpenAI web_search tool in request schema")
	require.True(t, capturedParams.ToolChoice.OfToolChoiceMode.Valid())
	assert.Equal(t, openairesponses.ToolChoiceOptionsAuto, capturedParams.ToolChoice.OfToolChoiceMode.Value)
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

func TestApplyGPT56PromptCacheOptions(t *testing.T) {
	tests := []struct {
		name         string
		config       llmtypes.Config
		model        string
		expectOption bool
	}{
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
			applyGPT56PromptCacheOptions(&params, tt.config, tt.model)

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
	assert.Equal(t, shared.ReasoningEffortMax, openAIReasoningEffortForRequest(shared.ReasoningEffortMax))
	assert.Equal(t, shared.ReasoningEffortXhigh, openAIReasoningEffortForRequest(shared.ReasoningEffort("XHIGH")))
}

func TestProcessMessageExchangeDoesNotInjectGoalContextFromMetadata(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
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
	thread.SetMetadataValue(goals.MetadataKey, goals.New("find server cores and ram", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)))

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

	require.Len(t, capturedParams.Input.OfInputItemList, 1)
	assert.Equal(t, "hello", extractInputItemText(capturedParams.Input.OfInputItemList[0]))
	assert.NotContains(t, extractInputItemText(capturedParams.Input.OfInputItemList[0]), "<goal_context>")
	require.Len(t, thread.inputItems, 1)
	require.Len(t, thread.storedItems, 1)
}

func TestProcessMessageExchangeDoesNotDuplicatePersistedGoalContext(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
	}
	goal := goals.New("find server cores and ram", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	goalContext := goals.RenderContext(goal)
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(goalContext)},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: goalContext}}
	thread.SetMetadataValue(goals.MetadataKey, goal)

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

	require.Len(t, capturedParams.Input.OfInputItemList, 1)
	assert.Equal(t, goalContext, extractInputItemText(capturedParams.Input.OfInputItemList[0]))
}

func TestProcessMessageExchangeDoesNotDuplicateExistingMiddleGoalContext(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
	}
	goal := goals.New("find server cores and ram", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	goalContext := goals.RenderContext(goal)
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(goalContext)},
			},
		},
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("follow up")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}, {Type: "message", Role: "user", Content: goalContext}, {Type: "message", Role: "user", Content: "follow up"}}
	thread.SetMetadataValue(goals.MetadataKey, goal)

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

	require.Len(t, capturedParams.Input.OfInputItemList, 3)
	assert.Equal(t, "hello", extractInputItemText(capturedParams.Input.OfInputItemList[0]))
	assert.Equal(t, goalContext, extractInputItemText(capturedParams.Input.OfInputItemList[1]))
	assert.Equal(t, "follow up", extractInputItemText(capturedParams.Input.OfInputItemList[2]))
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

func TestProcessMessageExchangeSavesConversationOnError(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1", Retry: llmtypes.RetryConfig{Attempts: 1}, OpenAI: &llmtypes.OpenAIConfig{Platform: "openai"}}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}

	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{}, errors.New("exchange failed")
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-4.1", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.Error(t, err)
	require.Equal(t, 1, len(store.savedRecords))
	assert.Equal(t, "openai", store.savedRecords[0].Provider)
	assert.Equal(t, "responses", store.savedRecords[0].Metadata["api_mode"])
}

func TestProcessMessageExchangeRetriesHTTPSStreamError(t *testing.T) {
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
		Thread: base.NewThread(config, "conv-test"),
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}

	attempts := 0
	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		if attempts < 3 {
			return processStreamResult{}, errors.New("stream disconnected")
		}
		thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleAssistant,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("done")},
			},
		})
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, "done", output)
	assert.Equal(t, 3, attempts)
}

func TestProcessMessageExchangeRetriesServerOverload(t *testing.T) {
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
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role:    openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	attempts := 0
	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		if attempts == 1 {
			return processStreamResult{}, &responseStreamEventError{code: "server_is_overloaded", message: "server overloaded"}
		}
		thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleAssistant,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("done")},
			},
		})
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, "done", output)
	assert.Equal(t, 2, attempts)
}

func TestProcessMessageExchangeHTTPRetryOwnership(t *testing.T) {
	tests := []struct {
		name             string
		statusCode       int
		body             string
		expectedRequests int32
	}{
		{
			name:             "structured overload uses outer three-attempt budget",
			statusCode:       http.StatusServiceUnavailable,
			body:             `{"error":{"code":"server_is_overloaded","message":"server overloaded","param":"","type":"server_error"}}`,
			expectedRequests: 3,
		},
		{
			name:             "ordinary rate limit keeps SDK retry budget",
			statusCode:       http.StatusTooManyRequests,
			body:             `{"error":{"code":"rate_limit_exceeded","message":"rate limited","param":"","type":"rate_limit_error"}}`,
			expectedRequests: 3,
		},
		{
			name:             "top-level slow down keeps outer overload classification",
			statusCode:       http.StatusTooManyRequests,
			body:             `{"code":"slow_down","message":"slow down","param":"","type":"server_error"}`,
			expectedRequests: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			client := openai.NewClient(
				option.WithAPIKey("test-api-key"),
				option.WithBaseURL(server.URL+"/v1"),
			)
			config := llmtypes.Config{
				Provider: "openai",
				Model:    "gpt-5.5",
				Retry:    llmtypes.RetryConfig{Attempts: 3},
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "openai",
					BaseURL:  server.URL + "/v1",
				},
			}
			thread := &Thread{
				Thread: base.NewThread(config, "conv-test"),
				client: &client,
				inputItems: []openairesponses.ResponseInputItemUnionParam{
					{
						OfMessage: &openairesponses.EasyInputMessageParam{
							Role:    openairesponses.EasyInputMessageRoleUser,
							Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
						},
					},
				},
				storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
			}
			thread.SetState(tools.NewBasicState(context.Background()))
			thread.newStreamingFunc = thread.client.Responses.NewStreaming
			thread.processStreamFunc = thread.processStream

			handler := &llmtypes.StringCollectorHandler{Silent: true}
			_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
			require.Error(t, err)
			assert.Equal(t, tt.expectedRequests, requests.Load())
		})
	}
}

func TestProcessMessageExchangeServerOverloadBackoffHonorsCancellation(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 3},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role:    openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		cancel()
		return processStreamResult{}, &responseStreamEventError{code: "server_is_overloaded", message: "server overloaded"}
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(ctx, handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, attempts)
}

func TestResponsesServerOverloadRetryDelayMatchesCodex(t *testing.T) {
	tests := []struct {
		name      string
		attempt   uint
		baseDelay time.Duration
	}{
		{name: "first retry", attempt: 1, baseDelay: 250 * time.Millisecond},
		{name: "second retry", attempt: 2, baseDelay: 500 * time.Millisecond},
		{name: "delay caps before jitter", attempt: 8, baseDelay: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lowerBound := time.Duration(float64(tt.baseDelay) * (1 - responsesServerOverloadJitterRatio))
			upperBound := time.Duration(float64(tt.baseDelay) * (1 + responsesServerOverloadJitterRatio))
			for range 20 {
				delay := responsesServerOverloadRetryDelay(tt.attempt)
				assert.Truef(t, delay >= lowerBound && delay <= upperBound, "delay %s outside [%s, %s]", delay, lowerBound, upperBound)
			}
		})
	}
}

func TestResponsesStreamRetryDelayTypeUsesCodexOverloadSequence(t *testing.T) {
	retryConfig := llmtypes.RetryConfig{
		Attempts:     3,
		InitialDelay: 1,
		MaxDelay:     1,
		BackoffType:  "fixed",
	}
	timer := &recordingRetryTimer{}
	attempts := 0

	err := retry.Do(
		func() error {
			attempts++
			return &responseStreamEventError{code: "server_is_overloaded", message: "server overloaded"}
		},
		retry.Attempts(uint(retryConfig.Attempts)),
		retry.Delay(time.Duration(retryConfig.InitialDelay)*time.Millisecond),
		retry.DelayType(responsesStreamRetryDelayType(retryConfig)),
		retry.WithTimer(timer),
		retry.LastErrorOnly(true),
	)
	require.Error(t, err)
	assert.Equal(t, 3, attempts)
	require.Len(t, timer.delays, 2)
	assert.InDelta(t, 250*time.Millisecond, timer.delays[0], float64(50*time.Millisecond))
	assert.InDelta(t, 500*time.Millisecond, timer.delays[1], float64(100*time.Millisecond))
}

func TestIsResponsesServerOverloadedError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		overloaded bool
	}{
		{
			name:       "SSE response failed event",
			err:        &responseStreamEventError{code: "server_is_overloaded", message: "overloaded"},
			overloaded: true,
		},
		{
			name:       "wrapped slow down event",
			err:        errors.Wrap(&responseStreamEventError{code: "slow_down", message: "slow down"}, "stream error"),
			overloaded: true,
		},
		{
			name:       "websocket event",
			err:        &responsesWebSocketEventError{code: "server_is_overloaded", message: "overloaded"},
			overloaded: true,
		},
		{
			name:       "HTTP API error",
			err:        &openai.Error{StatusCode: http.StatusServiceUnavailable, Code: "server_is_overloaded"},
			overloaded: true,
		},
		{
			name:       "websocket handshake body",
			err:        &websocketHandshakeStatusError{statusCode: http.StatusServiceUnavailable, body: `{"error":{"code":"slow_down"}}`},
			overloaded: true,
		},
		{
			name:       "generic service unavailable",
			err:        &websocketHandshakeStatusError{statusCode: http.StatusServiceUnavailable, body: `{"error":{"code":"server_error"}}`},
			overloaded: false,
		},
		{
			name:       "generic stream failure",
			err:        &responseStreamEventError{code: "server_error", message: "temporary"},
			overloaded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.overloaded, isResponsesServerOverloadedError(tt.err))
		})
	}
}

func TestProcessMessageExchangeKeepsDurableStateBeforeHTTPSStreamRetry(t *testing.T) {
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
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role:    openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.SetStructuredToolResult("existing", tooltypes.StructuredToolResult{ToolName: "existing", Success: true})

	attempts := 0
	var inputLengths []int
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		inputLengths = append(inputLengths, len(params.Input.OfInputItemList))
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		if attempts == 1 {
			thread.pendingReasoning.WriteString("partial reasoning")
			thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role:    openairesponses.EasyInputMessageRoleAssistant,
					Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("partial")},
				},
			})
			thread.storedItems = append(thread.storedItems, StoredInputItem{Type: "message", Role: "assistant", Content: "partial"})
			thread.SetStructuredToolResult("partial", tooltypes.StructuredToolResult{ToolName: "partial", Success: true})
			return processStreamResult{}, errors.New("stream disconnected")
		}
		thread.inputItems = append(thread.inputItems, openairesponses.ResponseInputItemUnionParam{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleAssistant,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("done")},
			},
		})
		thread.storedItems = append(thread.storedItems, StoredInputItem{Type: "message", Role: "assistant", Content: "done"})
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, _, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, "done", output)
	assert.Equal(t, []int{1, 2}, inputLengths)
	require.Len(t, thread.storedItems, 3)
	assert.Equal(t, "hello", thread.storedItems[0].Content)
	assert.Equal(t, "partial", thread.storedItems[1].Content)
	assert.Equal(t, "done", thread.storedItems[2].Content)
	assert.Empty(t, thread.pendingReasoning.String())
	toolResults := thread.GetStructuredToolResults()
	assert.Contains(t, toolResults, "existing")
	assert.Contains(t, toolResults, "partial")
}

func TestProcessMessageExchangeRetriesFromToolResultAfterLocalToolExecution(t *testing.T) {
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
		Thread: base.NewThread(config, "conv-test"),
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			{
				OfMessage: &openairesponses.EasyInputMessageParam{
					Role:    openairesponses.EasyInputMessageRoleUser,
					Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
				},
			},
		},
		storedItems: []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}},
	}
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithExtensionTools([]tooltypes.Tool{responsesTestTool{name: "ok_tool"}})))

	attempts := 0
	var inputLengths []int
	thread.newStreamingFunc = func(_ context.Context, params openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		inputLengths = append(inputLengths, len(params.Input.OfInputItemList))
		if attempts == 1 {
			return responseStreamFromMaps(t, []map[string]any{
				{
					"type": "response.output_item.done",
					"item": map[string]any{
						"type":      "function_call",
						"call_id":   "call_1",
						"name":      "ok_tool",
						"arguments": `{}`,
					},
				},
			})
		}

		return responseStreamFromMaps(t, []map[string]any{
			{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]any{
						{"type": "output_text", "text": "done"},
					},
				},
			},
			{
				"type": "response.completed",
				"response": map[string]any{
					"id":     "resp_2",
					"status": "completed",
					"usage": map[string]any{
						"input_tokens":  1,
						"output_tokens": 1,
						"input_tokens_details": map[string]any{
							"cached_tokens": 0,
						},
					},
				},
			},
		})
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, toolsUsed, completed, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.False(t, toolsUsed)
	assert.Equal(t, "done", output)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, []int{1, 3}, inputLengths)
	require.Len(t, thread.storedItems, 4)
	assert.Equal(t, "message", thread.storedItems[0].Type)
	assert.Equal(t, "function_call", thread.storedItems[1].Type)
	assert.Equal(t, "function_call_output", thread.storedItems[2].Type)
	assert.Equal(t, "message", thread.storedItems[3].Type)
	assert.Equal(t, "done", thread.storedItems[3].Content)
	assert.Contains(t, thread.GetStructuredToolResults(), "call_1")
}

func TestProcessMessageExchangeDoesNotRetryHTTPSUnrecoverableStreamError(t *testing.T) {
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
		Thread: base.NewThread(config, "conv-test"),
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}

	attempts := 0
	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		attempts++
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{}, retry.Unrecoverable(errors.New("invalid request"))
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.5", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestProcessMessageExchangeCodexAddsOverloadRetryOwnershipMiddleware(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-5.1-codex", OpenAI: &llmtypes.OpenAIConfig{Platform: "codex"}}
	thread := &Thread{
		Thread:  base.NewThread(config, "conv-test"),
		isCodex: true,
	}
	thread.SetState(tools.NewBasicState(context.Background()))
	thread.inputItems = []openairesponses.ResponseInputItemUnionParam{
		{
			OfMessage: &openairesponses.EasyInputMessageParam{
				Role:    openairesponses.EasyInputMessageRoleUser,
				Content: openairesponses.EasyInputMessageContentUnionParam{OfString: param.NewOpt("hello")},
			},
		},
	}
	thread.storedItems = []StoredInputItem{{Type: "message", Role: "user", Content: "hello"}}

	streamingOptsCount := 0
	thread.newStreamingFunc = func(_ context.Context, _ openairesponses.ResponseNewParams, opts ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		streamingOptsCount = len(opts)
		return nil
	}
	thread.processStreamFunc = func(_ context.Context, _ *ssestream.Stream[openairesponses.ResponseStreamEventUnion], _ llmtypes.MessageHandler, _ string, _ llmtypes.MessageOpt) (processStreamResult, error) {
		return processStreamResult{responseCompleted: true}, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, _, _, err := thread.processMessageExchange(context.Background(), handler, "gpt-5.1-codex", 256, "system", llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, streamingOptsCount, 1, "codex streaming should add overload retry ownership middleware")
}

func TestSendMessageRequiresResponseCompletedEvent(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	thread := &Thread{
		Thread:      base.NewThread(config, "conv-test"),
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}

	exchangeCalls := 0
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		exchangeCalls++
		return "partial", false, false, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err := thread.SendMessage(context.Background(), "hello", handler, llmtypes.MessageOpt{NoToolUse: true, MaxTurns: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response.completed")
	assert.Equal(t, 1, exchangeCalls)
}

func TestSendMessageContinuesForSteerQueuedBeforeStop(t *testing.T) {
	steerStore, err := steer.NewSteerStore(context.Background())
	require.NoError(t, err)
	defer steerStore.Close()

	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	thread := &Thread{
		Thread:      base.NewThread(config, "conv-test"),
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}

	exchangeCalls := 0
	thread.processMessageExchangeFunc = func(
		ctx context.Context,
		handler llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		if err := thread.processPendingSteer(ctx, handler); err != nil {
			return "", false, false, err
		}

		exchangeCalls++
		if exchangeCalls == 1 {
			if _, err := steerStore.Enqueue(ctx, "conv-test", "Please include the queued correction", nil); err != nil {
				return "", false, false, err
			}
			return "first response", false, true, nil
		}

		return "corrected response", false, true, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, err := thread.SendMessage(context.Background(), "hello", handler, llmtypes.MessageOpt{NoToolUse: true, MaxTurns: 2})
	require.NoError(t, err)
	assert.Equal(t, "corrected response", output)
	assert.Equal(t, 2, exchangeCalls)
	require.Len(t, thread.inputItems, 2)
	assert.Equal(t, "hello", extractInputItemText(thread.inputItems[0]))
	assert.Equal(t, "Please include the queued correction", extractInputItemText(thread.inputItems[1]))
	assert.Contains(t, handler.CollectedText(), "🗣️ User steering: Please include the queued correction")
	hasPending, err := steerStore.HasPending(context.Background(), "conv-test")
	require.NoError(t, err)
	assert.False(t, hasPending)
}

func TestSendMessageAutoContinuesActiveGoalUntilMaxTurns(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	thread := &Thread{
		Thread:      base.NewThread(config, "conv-test"),
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithLLMConfig(config)))
	thread.SetMetadataValue(goals.MetadataKey, goals.New("ship goal support", time.Now()))

	exchangeCalls := 0
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		exchangeCalls++
		return "progress", false, true, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err := thread.SendMessage(context.Background(), "hello", handler, llmtypes.MessageOpt{MaxTurns: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, exchangeCalls)
	require.Len(t, thread.inputItems, 2)
	assert.Equal(t, "hello", extractInputItemText(thread.inputItems[0]))
	assert.Contains(t, extractInputItemText(thread.inputItems[1]), "<goal_context>")
}

func TestSendMessageAutoContinuationStopsWhenUpdateGoalUnavailable(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	thread := &Thread{
		Thread:      base.NewThread(config, "conv-test"),
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}
	thread.SetMetadataValue(goals.MetadataKey, goals.New("ship goal support", time.Now()))

	exchangeCalls := 0
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		exchangeCalls++
		return "progress", false, true, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err := thread.SendMessage(context.Background(), "hello", handler, llmtypes.MessageOpt{NoToolUse: true})
	require.NoError(t, err)
	assert.Equal(t, 1, exchangeCalls)
	require.Len(t, thread.inputItems, 1)
	assert.Equal(t, "hello", extractInputItemText(thread.inputItems[0]))
}

func TestSendMessageAutoContinuationCanRunUntilGoalCompletes(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	thread := &Thread{
		Thread:      base.NewThread(config, "conv-test"),
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithLLMConfig(config)))
	thread.SetMetadataValue(goals.MetadataKey, goals.New("ship goal support", time.Now()))

	exchangeCalls := 0
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		exchangeCalls++
		if exchangeCalls == 3 {
			goal, metadata, err := goals.UpdateStatus(thread.GetMetadata(), goals.StatusComplete, "done", time.Now())
			require.NoError(t, err)
			assert.Equal(t, goals.StatusComplete, goal.Status)
			for key, value := range metadata {
				thread.SetMetadataValue(key, value)
			}
		}
		return "progress", false, true, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err := thread.SendMessage(context.Background(), "hello", handler, llmtypes.MessageOpt{})
	require.NoError(t, err)
	assert.Equal(t, 3, exchangeCalls)
	require.Len(t, thread.inputItems, 3)
	assert.Equal(t, "hello", extractInputItemText(thread.inputItems[0]))
	assert.Contains(t, extractInputItemText(thread.inputItems[1]), "<goal_context>")
	assert.Contains(t, extractInputItemText(thread.inputItems[2]), "<goal_context>")
}

func TestSendMessageAutoContinuationStopsWhenGoalPaused(t *testing.T) {
	config := llmtypes.Config{Provider: "openai", Model: "gpt-4.1"}
	thread := &Thread{
		Thread:      base.NewThread(config, "conv-test"),
		inputItems:  make([]openairesponses.ResponseInputItemUnionParam, 0),
		storedItems: make([]StoredInputItem, 0),
	}
	thread.SetState(tools.NewBasicState(context.Background(), tools.WithLLMConfig(config)))
	thread.SetMetadataValue(goals.MetadataKey, goals.New("ship goal support", time.Now()))

	exchangeCalls := 0
	thread.processMessageExchangeFunc = func(
		_ context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		exchangeCalls++
		goal, metadata, err := goals.UpdateStatus(thread.GetMetadata(), goals.StatusPaused, "user paused", time.Now())
		require.NoError(t, err)
		assert.Equal(t, goals.StatusPaused, goal.Status)
		for key, value := range metadata {
			thread.SetMetadataValue(key, value)
		}
		return "paused", false, true, nil
	}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err := thread.SendMessage(context.Background(), "hello", handler, llmtypes.MessageOpt{MaxTurns: 2})
	require.NoError(t, err)
	assert.Equal(t, 1, exchangeCalls)
}

func TestRecordUsesResponsesAPI_MetadataDetection(t *testing.T) {
	assert.True(t, recordUsesResponsesAPI(map[string]any{"api_mode": "responses"}))
	assert.False(t, recordUsesResponsesAPI(map[string]any{"api_mode": "chat_completions"}))
	assert.False(t, recordUsesResponsesAPI(nil))
}
