package responses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/avast/retry-go/v4"
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
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// processStream eagerly completes streams for direct stream tests. Production
// completion belongs to processMessageExchangeWithStreamRetries.
func (t *Thread) processStream(
	ctx context.Context,
	stream *ssestream.Stream[openairesponses.ResponseStreamEventUnion],
	handler llmtypes.MessageHandler,
	model string,
	opt llmtypes.MessageOpt,
) (processStreamResult, error) {
	result, err := t.readStream(ctx, stream, handler, model, opt)
	if result.complete != nil {
		var completionErr error
		result, completionErr = result.complete(ctx)
		if completionErr != nil {
			return result, completionErr
		}
	}
	return result, err
}

type recordingRetryTimer struct {
	delays []time.Duration
}

type compactionCaptureHandler struct {
	llmtypes.StringCollectorHandler
	onCompaction func(llmtypes.CompactionMarker, bool)
}

func (h *compactionCaptureHandler) HandleCompaction(marker llmtypes.CompactionMarker, beforeCurrentUser bool) {
	h.onCompaction(marker, beforeCurrentUser)
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

func TestBuildToolsForThreadNativeSearch(t *testing.T) {
	for _, tt := range []struct {
		name         string
		platform     string
		allowedTools []string
		wantSearch   bool
	}{
		{name: "OpenAI", platform: "openai", wantSearch: true},
		{name: "Codex", platform: "codex", wantSearch: true},
		{name: "compatible platform", platform: "fireworks"},
		{name: "excluded by allowlist", platform: "openai", allowedTools: []string{"bash"}},
		{name: "included by allowlist", platform: "openai", allowedTools: []string{"bash", openAISearchToolName}, wantSearch: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := llmtypes.Config{
				Provider:     "openai",
				AllowedTools: tt.allowedTools,
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: tt.platform,
					APIMode:  llmtypes.OpenAIAPIModeResponses,
				},
			}
			thread := &Thread{Thread: base.NewThread(config, "conv-tools")}
			state := tools.NewBasicState(t.Context(), tools.WithLLMConfig(config))
			toolDefs := buildToolsForThread(thread, state, false)
			if tt.wantSearch {
				require.NotEmpty(t, toolDefs)
				require.NotNil(t, toolDefs[0].OfWebSearch)
				assert.Equal(t, openairesponses.WebSearchToolTypeWebSearch, toolDefs[0].OfWebSearch.Type)
			} else {
				for _, toolDef := range toolDefs {
					assert.Nil(t, toolDef.OfWebSearch)
				}
			}
		})
	}
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
	store := &mockResponsesConversationStore{}
	thread.Store, thread.Persisted = store, true
	require.NoError(t, thread.SavePendingUserMessage(t.Context(), "incoming user", "data:image/png;base64,aGVsbG8="))
	require.Len(t, store.savedRecords, 1)
	assert.Nil(t, store.savedRecords[0].CompactionHistory)
	published := 0
	handler := &compactionCaptureHandler{
		StringCollectorHandler: llmtypes.StringCollectorHandler{Silent: true},
		onCompaction: func(marker llmtypes.CompactionMarker, beforeCurrentUser bool) {
			published++
			assert.True(t, beforeCurrentUser)
			require.Len(t, store.savedRecords, 2, "the compacted checkpoint must be saved before notification")
			checkpoint := store.savedRecords[1]
			require.NotNil(t, checkpoint.CompactionHistory)
			assert.Equal(t, marker, checkpoint.CompactionHistory.Segments[0].Marker)
			assert.Contains(t, string(checkpoint.RawMessages), "incoming user")
			assert.Contains(t, string(checkpoint.RawMessages), "data:image/png;base64,aGVsbG8=")
			assert.NotContains(t, string(checkpoint.CompactionHistory.Segments[0].RawMessages), "incoming user")
		},
	}

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
		assert.Equal(t, 1, published, "notify before starting the next inference request")
		return "done", false, true, nil
	}

	_, err := thread.SendMessage(
		context.Background(),
		"incoming user",
		handler,
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
	assert.Equal(t, uint64(1), thread.snapshotHistory().codexWindowGeneration)
	assert.Equal(t, 1, published)
}

func TestSendMessagePublishesMidTurnCompactionAfterCurrentUser(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{Thread: base.NewThread(config, "mid-turn-compact")}
	thread.SetState(tools.NewBasicState(t.Context()))
	store := &mockResponsesConversationStore{}
	thread.Store, thread.Persisted = store, true
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "mid-turn-context")
	}
	published := 0
	handler := &compactionCaptureHandler{
		StringCollectorHandler: llmtypes.StringCollectorHandler{Silent: true},
		onCompaction: func(marker llmtypes.CompactionMarker, beforeCurrentUser bool) {
			published++
			assert.False(t, beforeCurrentUser)
			require.Len(t, store.savedRecords, 1)
			assert.Equal(t, marker, store.savedRecords[0].CompactionHistory.Segments[0].Marker)
			assert.Contains(t, string(store.savedRecords[0].CompactionHistory.Segments[0].RawMessages), "incoming user")
		},
	}
	exchanges := 0
	thread.processMessageExchangeFunc = func(context.Context, llmtypes.MessageHandler, string, int, string, llmtypes.MessageOpt) (string, bool, bool, error) {
		exchanges++
		if exchanges == 1 {
			assert.Zero(t, published)
			thread.Usage.CurrentContextWindow, thread.Usage.MaxContextWindow = 90, 100
			return "", true, true, nil
		}
		assert.Equal(t, 1, published)
		history := thread.inputItemsSnapshot()
		require.Len(t, history, 2, "mid-turn compaction must not append the incoming user again")
		assert.Equal(t, "incoming user", extractInputItemText(history[0]))
		assert.NotNil(t, history[1].OfCompaction)
		return "done", false, true, nil
	}
	_, err := thread.SendMessage(t.Context(), "incoming user", handler, llmtypes.MessageOpt{NoToolUse: true, MaxTurns: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, exchanges)
	assert.Equal(t, 1, published)
}

func TestSendMessageDoesNotPublishCompactionWhenCheckpointFails(t *testing.T) {
	config := llmtypes.Config{
		Provider: "openai",
		Model:    "gpt-5.5",
		Retry:    llmtypes.RetryConfig{Attempts: 1},
		OpenAI:   &llmtypes.OpenAIConfig{Platform: "openai"},
	}
	thread := &Thread{Thread: base.NewThread(config, "failed-compact-checkpoint")}
	thread.SetState(tools.NewBasicState(t.Context()))
	thread.AddUserMessage(t.Context(), "original")
	thread.Usage.CurrentContextWindow, thread.Usage.MaxContextWindow = 90, 100
	saveErr := errors.New("save failed")
	store := &mockResponsesConversationStore{saveFunc: func(_ context.Context, record convtypes.ConversationRecord) error {
		assert.Contains(t, string(record.RawMessages), "incoming user", "even a failed save must include the admitted input")
		return saveErr
	}}
	thread.Store, thread.Persisted = store, true
	thread.newStreamingFunc = func(context.Context, openairesponses.ResponseNewParams, ...option.RequestOption) *ssestream.Stream[openairesponses.ResponseStreamEventUnion] {
		return remoteCompactionV2Stream(t, "compacted-context")
	}
	thread.processMessageExchangeFunc = func(context.Context, llmtypes.MessageHandler, string, int, string, llmtypes.MessageOpt) (string, bool, bool, error) {
		t.Fatal("must not continue inference after failing to persist compaction")
		return "", false, false, nil
	}
	handler := &compactionCaptureHandler{
		StringCollectorHandler: llmtypes.StringCollectorHandler{Silent: true},
		onCompaction: func(llmtypes.CompactionMarker, bool) {
			t.Fatal("must not publish a checkpoint that failed to persist")
		},
	}
	_, err := thread.SendMessage(t.Context(), "incoming user", handler, llmtypes.MessageOpt{NoToolUse: true, MaxTurns: 1})
	require.ErrorIs(t, err, saveErr)
	require.Len(t, store.savedRecords, 1)
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
	store := &mockResponsesConversationStore{}
	thread.Store = store
	thread.Persisted = true
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
		ctx context.Context,
		_ llmtypes.MessageHandler,
		_ string,
		_ int,
		_ string,
		_ llmtypes.MessageOpt,
	) (string, bool, bool, error) {
		forkedID, forkErr := thread.ForkConversation(ctx)
		require.ErrorIs(t, forkErr, llmtypes.ErrConversationForkUnavailable)
		assert.Empty(t, forkedID)
		assert.Empty(t, store.savedRecords)
		assert.Equal(t, uint64(8), thread.snapshotHistory().codexWindowGeneration)
		history := thread.inputItemsSnapshot()
		require.Len(t, history, 3)
		require.NotNil(t, history[1].OfCompaction)
		assert.Equal(t, "incoming", extractInputItemText(history[2]))
		require.NotNil(t, thread.CompactionHistory)
		assert.Len(t, thread.CompactionHistory.Segments, 1)
		return "done", false, true, nil
	}

	handler := &compactionCaptureHandler{
		StringCollectorHandler: llmtypes.StringCollectorHandler{Silent: true},
		onCompaction: func(llmtypes.CompactionMarker, bool) {
			t.Fatal("no-save compaction must not publish temporary history")
		},
	}
	_, err := thread.SendMessage(
		context.Background(),
		"incoming",
		handler,
		llmtypes.MessageOpt{NoSaveConversation: true, NoToolUse: true, MaxTurns: 1},
	)
	require.NoError(t, err)

	assert.Equal(t, uint64(7), thread.snapshotHistory().codexWindowGeneration)
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
	assert.Empty(t, store.savedRecords)
	assert.False(t, thread.ConversationForkBlocked())
	assert.Nil(t, thread.CompactionHistory)
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
	require.NoError(t, thread.SwapContext(t.Context(), "previous summary"))
	originalArchive := thread.CompactionHistory.Clone()
	originalHistory := thread.snapshotHistory()
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
		require.Len(t, thread.CompactionHistory.Segments, 2)
		thread.AddUserMessage(t.Context(), "external append")
		return "", false, false, errors.New("exchange failed")
	}

	_, err := thread.SendMessage(
		context.Background(),
		"incoming",
		&llmtypes.StringCollectorHandler{Silent: true},
		llmtypes.MessageOpt{NoSaveConversation: true, NoToolUse: true, MaxTurns: 1},
	)
	require.EqualError(t, err, "exchange failed")

	assert.Equal(t, originalHistory.codexWindowGeneration, thread.snapshotHistory().codexWindowGeneration)
	history := thread.inputItemsSnapshot()
	require.Len(t, history, 2)
	assert.Equal(t, "previous summary", extractInputItemText(history[0]))
	assert.Equal(t, "external append", extractInputItemText(history[1]))
	assert.Equal(t, originalArchive, thread.CompactionHistory)
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
