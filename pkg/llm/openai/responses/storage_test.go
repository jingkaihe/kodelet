package responses

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestStreamMessagesWebSearchUsesRawItemDetails(t *testing.T) {
	for _, tt := range []struct {
		action string
		raw    string
		want   string
	}{
		{
			action: "open_page",
			raw:    `{"type":"open_page","url":"https://example.com/story"}`,
			want:   `{"status":"completed","type":"open_page","url":"https://example.com/story"}`,
		},
		{
			action: "find_in_page",
			raw:    `{"type":"find_in_page","url":"https://example.com/docs","pattern":"installation"}`,
			want:   `{"status":"completed","type":"find_in_page","url":"https://example.com/docs","pattern":"installation"}`,
		},
	} {
		t.Run(tt.action, func(t *testing.T) {
			items := []StoredInputItem{{
				Type: "web_search_call", CallID: "ws-call", Status: "completed", Action: tt.action,
				RawItem: json.RawMessage(`{"id":"ws-call","type":"web_search_call","status":"completed","action":` + tt.raw + `}`),
			}}
			streamable, err := StreamMessages(mustJSON(t, items), nil)
			require.NoError(t, err)
			require.Len(t, streamable, 2)
			assert.Equal(t, "tool-use", streamable[0].Kind)
			assert.JSONEq(t, tt.want, streamable[0].Input)
		})
	}
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

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestConversationSnapshotRemovesTrailingFunctionCallFromStorage(t *testing.T) {
	thread := &Thread{
		Thread: base.NewThread(llmtypes.Config{Model: "gpt-4.1"}, "conv-orphan"),
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

	snapshot := thread.snapshotConversationState(true)

	require.Len(t, thread.inputItems, 1)
	require.Len(t, thread.storedItems, 1)
	assert.Equal(t, "message", thread.storedItems[0].Type)
	assert.Equal(t, thread.storedItems, snapshot.storedItems)
	assert.Equal(t, uint64(1), thread.historyRevision)
}

type mockResponsesConversationStore struct {
	savedRecords []convtypes.ConversationRecord
	loadedRecord *convtypes.ConversationRecord
	saveFunc     func(context.Context, convtypes.ConversationRecord) error
}

func (m *mockResponsesConversationStore) Save(ctx context.Context, record convtypes.ConversationRecord) error {
	m.savedRecords = append(m.savedRecords, record)
	if m.saveFunc != nil {
		return m.saveFunc(ctx, record)
	}
	return nil
}

func (m *mockResponsesConversationStore) Load(_ context.Context, _ string) (convtypes.ConversationRecord, error) {
	if m.loadedRecord != nil {
		return *m.loadedRecord, nil
	}
	return convtypes.ConversationRecord{}, convtypes.ErrConversationNotFound
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
	require.NotNil(t, store.savedRecords[0].CompactionHistory)
	assert.Equal(t, thread.CompactionHistory, store.savedRecords[0].CompactionHistory)
	assert.NotSame(t, thread.CompactionHistory, store.savedRecords[0].CompactionHistory)

	loadStore := &mockResponsesConversationStore{loadedRecord: &store.savedRecords[0]}
	restored := &Thread{Thread: base.NewThread(config, "conv-compact-lifecycle")}
	restored.SetState(tools.NewBasicState(context.Background()))
	restored.Store = loadStore
	restored.LoadConversation = restored.loadConversation
	require.NoError(t, restored.EnablePersistence(context.Background(), true))

	loadedSnapshot := restored.snapshotHistory()
	require.Len(t, loadedSnapshot.inputItems, 2)
	require.NotNil(t, loadedSnapshot.inputItems[1].OfCompaction)
	assert.Equal(t, "persisted-encrypted-summary", loadedSnapshot.inputItems[1].OfCompaction.EncryptedContent)
	assert.Equal(t, thread.CompactionHistory, restored.CompactionHistory)
	assert.NotSame(t, store.savedRecords[0].CompactionHistory, restored.CompactionHistory)

	restored.AddUserMessage(context.Background(), "follow up")
	expectedInferenceInput := restored.inputItemsSnapshot()
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
	assert.JSONEq(t, string(mustJSON(t, expectedInferenceInput)), string(mustJSON(t, captured.Input.OfInputItemList)))
	require.Len(t, captured.Input.OfInputItemList, 3)
	require.NotNil(t, captured.Input.OfInputItemList[1].OfCompaction)
	assert.Equal(t, "persisted-encrypted-summary", captured.Input.OfInputItemList[1].OfCompaction.EncryptedContent)
	assert.Equal(t, "follow up", extractInputItemText(captured.Input.OfInputItemList[2]))

	// A compaction after resume must archive only the follow-up, not its seed.
	restored.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "second-encrypted-summary")
	}
	require.NoError(t, restored.CompactContext(t.Context()))
	require.NoError(t, restored.SaveConversation(t.Context()))
	require.Len(t, restored.CompactionHistory.Segments, 2)
	assert.Equal(t, 3, restored.CompactionHistory.ActiveDisplayStart)
	var archivedFollowUp []StoredInputItem
	require.NoError(t, json.Unmarshal(restored.CompactionHistory.Segments[1].RawMessages, &archivedFollowUp))
	require.Len(t, archivedFollowUp, 1)
	assert.Equal(t, "follow up", archivedFollowUp[0].Content)
	assert.Equal(t, restored.CompactionHistory, loadStore.savedRecords[len(loadStore.savedRecords)-1].CompactionHistory)
	assert.Len(t, store.savedRecords[0].CompactionHistory.Segments, 1, "later compaction must not mutate the loaded record")
}

func TestLoadConversationRejectsInvalidCompactionBoundary(t *testing.T) {
	for _, tt := range []struct {
		name  string
		start int
		items []StoredInputItem
	}{
		{name: "negative", start: -1, items: []StoredInputItem{{Type: "message", Role: "user", Content: "seed"}}},
		{name: "past end", start: 2, items: []StoredInputItem{{Type: "message", Role: "user", Content: "seed"}}},
		{name: "inside orphaned call", start: 2, items: []StoredInputItem{
			{Type: "message", Role: "user", Content: "seed"},
			{Type: "function_call", CallID: "orphan", Name: "bash", Arguments: `{}`},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			thread := &Thread{Thread: base.NewThread(llmtypes.Config{Model: "gpt-4.1"}, "invalid-load")}
			thread.AddUserMessage(t.Context(), "live history")
			original := thread.snapshotHistory()
			record := convtypes.ConversationRecord{
				Provider:    "openai",
				Metadata:    map[string]any{"api_mode": "responses"},
				RawMessages: mustJSON(t, tt.items),
				CompactionHistory: &convtypes.CompactionHistory{
					ActiveDisplayStart: tt.start,
					Segments: []convtypes.CompactedSegment{{
						RawMessages: json.RawMessage(`[]`),
						Marker:      llmtypes.CompactionMarker{ID: "compact-1", Method: "api", CreatedAt: time.Now()},
					}},
				},
			}
			store := &mockResponsesConversationStore{loadedRecord: &record}
			thread.Store = store
			thread.LoadConversation = thread.loadConversation
			require.ErrorContains(t, thread.EnablePersistence(t.Context(), true), "compaction display boundary")
			assert.False(t, thread.IsPersisted())
			assert.Equal(t, original, thread.snapshotHistory())
			assert.Nil(t, thread.CompactionHistory)
		})
	}
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
	assert.Equal(t, uint64(1), thread.snapshotHistory().codexWindowGeneration)

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
	require.NoError(t, restored.EnablePersistence(context.Background(), true))
	assert.Equal(t, uint64(1), restored.snapshotHistory().codexWindowGeneration)

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
	assert.Nil(t, store.savedRecords[0].CompactionHistory)

	liveSnapshot := thread.snapshotHistory()
	require.Len(t, liveSnapshot.storedItems, 1)
	assert.Equal(t, "compaction", liveSnapshot.storedItems[0].Type)
	assert.Equal(t, 5, thread.GetUsage().CurrentContextWindow)
	assert.Contains(t, thread.GetStructuredToolResults(), "call-old")
	require.NoError(t, thread.SaveConversation(t.Context()))
	require.Len(t, store.savedRecords, 2)
	compacted := store.savedRecords[1]
	require.NotNil(t, compacted.CompactionHistory)
	require.Len(t, compacted.CompactionHistory.Segments, 1)
	assert.Equal(t, 1, compacted.CompactionHistory.ActiveDisplayStart)
	assert.Contains(t, string(compacted.CompactionHistory.Segments[0].RawMessages), "old history")
	assert.Equal(t, 5, compacted.Usage.CurrentContextWindow)
	assert.Contains(t, compacted.ToolResults, "call-old")
}

func TestForkConversationPreservesCompactionArchive(t *testing.T) {
	thread := &Thread{Thread: base.NewThread(llmtypes.Config{Model: "gpt-4.1"}, "compacted-parent")}
	thread.AddUserMessage(t.Context(), "original request")
	thread.SetStructuredToolResult("old-tool", tooltypes.StructuredToolResult{ToolName: "bash", Success: true})
	require.NoError(t, thread.SwapContext(t.Context(), "summary"))
	thread.AddUserMessage(t.Context(), "follow up")
	originalArchive := thread.CompactionHistory.Clone()
	originalRaw := mustJSON(t, thread.snapshotHistory().storedItems)
	store := &mockResponsesConversationStore{}
	thread.Store, thread.Persisted = store, true

	_, err := thread.ForkConversation(t.Context())
	require.NoError(t, err)
	require.Len(t, store.savedRecords, 1)
	fork := store.savedRecords[0]
	assert.Equal(t, originalArchive, fork.CompactionHistory)
	assert.NotSame(t, thread.CompactionHistory, fork.CompactionHistory)
	assert.JSONEq(t, string(originalRaw), string(fork.RawMessages))
	assert.Contains(t, fork.ToolResults, "old-tool")
	require.NoError(t, fork.CompactionHistory.Validate(fork.RawMessages))

	// Snapshots and the parent must not share mutable archive bytes.
	fork.CompactionHistory.Segments[0].RawMessages[0] = 'x'
	assert.Equal(t, originalArchive, thread.CompactionHistory)
	require.NoError(t, thread.SwapContext(t.Context(), "new summary"))
	assert.Len(t, fork.CompactionHistory.Segments, 1)
	assert.Len(t, thread.CompactionHistory.Segments, 2)
}

func TestForkConversationSnapshotsLiveContextWithoutMutatingParent(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-4.1",
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "codex"},
	}
	thread := &Thread{
		Thread:  base.NewThread(config, "parent-conversation"),
		isCodex: true,
		inputItems: []openairesponses.ResponseInputItemUnionParam{
			openairesponses.ResponseInputItemParamOfMessage("implement context inheritance", openairesponses.EasyInputMessageRoleUser),
			{OfFunctionCall: &openairesponses.ResponseFunctionToolCallParam{CallID: "call-subagent", Name: "subagent", Arguments: `{"task":"inspect"}`}},
		},
		storedItems: []StoredInputItem{
			{Type: "message", Role: "user", Content: "implement context inheritance"},
			{Type: "function_call", CallID: "call-subagent", Name: "subagent", Arguments: `{"task":"inspect"}`},
		},
		codexWindowGeneration: 3,
	}
	thread.Usage.InputTokens = 10
	thread.Usage.OutputTokens = 5
	thread.Usage.CurrentContextWindow = 123
	thread.Usage.MaxContextWindow = 456
	thread.SetMetadataValue("custom_key", "parent value")
	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true

	originalHistory := thread.snapshotHistory()
	originalMetadata, originalUsage := thread.GetMetadata(), thread.GetUsage()
	initiator := convtypes.ConversationForkInitiator{
		Type:        convtypes.ConversationForkInitiatorTypeExtensionTool,
		ExtensionID: "subagent",
		ToolName:    "subagent",
	}
	forkContext := convtypes.ContextWithConversationForkInitiator(t.Context(), initiator)
	snapshot, err := thread.SnapshotConversationFork(forkContext)
	require.NoError(t, err)
	assert.Equal(t, thread.ConversationID, snapshot.ID)
	assert.Empty(t, store.savedRecords, "capturing a child seed must not publish a temporary conversation")
	assert.NotContains(t, string(snapshot.RawMessages), "call-subagent")
	forkedID, err := thread.ForkConversation(forkContext)

	require.NoError(t, err)
	require.Len(t, store.savedRecords, 1)
	assert.NotEqual(t, thread.ConversationID, forkedID)
	assert.Equal(t, forkedID, store.savedRecords[0].ID)
	assert.Len(t, thread.inputItems, 2)
	assert.Len(t, thread.storedItems, 2)
	assert.Equal(t, originalHistory, thread.snapshotHistory())
	assert.Equal(t, originalMetadata, thread.GetMetadata())
	assert.Equal(t, originalUsage, thread.GetUsage())
	assert.Equal(t, "parent-conversation", thread.ConversationID)
	var savedItems []StoredInputItem
	require.NoError(t, json.Unmarshal(store.savedRecords[0].RawMessages, &savedItems))
	require.Len(t, savedItems, 1)
	assert.Equal(t, "implement context inheritance", savedItems[0].Content)
	assert.Zero(t, store.savedRecords[0].Usage.InputTokens)
	assert.Zero(t, store.savedRecords[0].Usage.OutputTokens)
	assert.Equal(t, 123, store.savedRecords[0].Usage.CurrentContextWindow)
	assert.Equal(t, 456, store.savedRecords[0].Usage.MaxContextWindow)
	assert.NotContains(t, store.savedRecords[0].Metadata, convtypes.CodexResponsesWindowGenerationMetadataKey)
	assert.Equal(t, "parent value", store.savedRecords[0].Metadata["custom_key"])
	assert.Equal(t, llmtypes.OpenAIAPIMode(""), thread.Config.OpenAI.APIMode)
	forkMetadata, ok := store.savedRecords[0].Metadata[convtypes.ConversationForkMetadataKey].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, thread.ConversationID, forkMetadata["source_conversation_id"])
	assert.Equal(t, thread.ConversationID, forkMetadata["root_conversation_id"])
	assert.Equal(t, 1, forkMetadata["depth"])
	assert.Equal(t, string(convtypes.ConversationForkModeLiveSnapshot), forkMetadata["mode"])
	savedInitiator, ok := convtypes.ConversationForkInitiatorFromMetadata(store.savedRecords[0].Metadata)
	require.True(t, ok)
	assert.Equal(t, initiator, savedInitiator)
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

func TestRecordUsesResponsesAPI_MetadataDetection(t *testing.T) {
	assert.True(t, recordUsesResponsesAPI(map[string]any{"api_mode": "responses"}))
	assert.False(t, recordUsesResponsesAPI(map[string]any{"api_mode": "chat_completions"}))
	assert.False(t, recordUsesResponsesAPI(nil))
}
