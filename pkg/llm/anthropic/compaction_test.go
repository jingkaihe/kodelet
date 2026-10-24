package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nativeCompactionTestBlock = `{
	"type": "compaction",
	"content": "  Preserve the user's pending request.\nNext: finish the implementation.  ",
	"signature": "signed-checkpoint",
	"encrypted_content": "opaque-checkpoint",
	"tool_changes": [],
	"future_metadata": {"revision": 7, "value": null}
}`

const nativeCompactionTestUsage = `{
	"input_tokens": 0,
	"output_tokens": 0,
	"iterations": [{
		"type": "compaction",
		"input_tokens": 120000,
		"output_tokens": 256,
		"cache_read_input_tokens": 40,
		"cache_creation_input_tokens": 30,
		"cache_creation": {"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 20}
	}]
}`

const nativeCompactionTestCapabilities = `{
	"id": "claude-sonnet-4-6",
	"capabilities": {"compaction": {"supported": true, "summarize": {"supported": true}}}
}`

// Signed compaction arrives whole at content_block_start, without a delta. Text
// replies use deltas so the same fixture also exercises the summary fallback.
func compactionTestStream(block, stop, usage string) string {
	var stream strings.Builder
	writeEvent := func(kind string, data map[string]any) {
		data["type"] = kind
		raw, _ := json.Marshal(data)
		fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", kind, raw)
	}
	writeEvent("message_start", map[string]any{
		"message": json.RawMessage(`{
			"id": "msg-compact",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-6",
			"content": [],
			"usage": {"input_tokens": 0, "output_tokens": 0}
		}`),
	})
	var text struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(block), &text)
	if text.Type == "text" {
		block = `{"type":"text","text":""}`
	}
	writeEvent("content_block_start", map[string]any{
		"index":         0,
		"content_block": json.RawMessage(block),
	})
	if text.Type == "text" {
		writeEvent("content_block_delta", map[string]any{
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": text.Text},
		})
	}
	writeEvent("content_block_stop", map[string]any{"index": 0})
	writeEvent("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": stop},
		"usage": json.RawMessage(usage),
	})
	writeEvent("message_stop", map[string]any{})
	return stream.String()
}

type compactionTestRequest struct {
	path   string
	header http.Header
	params map[string]json.RawMessage
}

// Replies follow request order; an empty reply simulates unavailable discovery.
func newNativeCompactionTestThread(t *testing.T, replies ...string) (*Thread, <-chan compactionTestRequest) {
	t.Helper()
	requests := make(chan compactionTestRequest, len(replies))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(calls.Add(1)) - 1
		if !assert.Less(t, i, len(replies), "unexpected request: %s %s", r.Method, r.URL.Path) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request := compactionTestRequest{path: r.URL.Path, header: r.Header.Clone()}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request.params))
			w.Header().Set("Content-Type", "text/event-stream")
		}
		requests <- request
		if replies[i] == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, replies[i])
	}))
	t.Cleanup(func() {
		server.Close()
		assert.EqualValues(t, len(replies), calls.Load(), "request count")
	})
	t.Setenv("HOME", t.TempDir())
	// TestMain provides a disposable, migrated KODELET_BASE_PATH for steering.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	thread := createTestThread()
	thread.Config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAPIKey
	thread.client = anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey("test-key"),
		option.WithBaseURL(server.URL),
		option.WithHeader("anthropic-beta", "existing-beta"),
		option.WithMaxRetries(0),
	)
	thread.AddUserMessage(t.Context(), "original question")
	thread.AddAssistantMessage(t.Context(), "original answer")
	thread.AddUserMessage(t.Context(), "pending request")
	thread.Usage.CurrentContextWindow = 180000
	thread.Usage.MaxContextWindow = 200000
	return thread, requests
}

type nativeCompactionTestEnvironment struct {
	agentenv.UtilityEnvironment
}

func (*nativeCompactionTestEnvironment) Manifest() agentenv.Manifest {
	tool := testTool{
		name: "test_tool",
		rawSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []string{"path"},
		},
	}
	return agentenv.Manifest{
		Tools: []agentenv.ToolDefinition{{Name: "test_tool", Tool: tool}},
	}
}

func (*nativeCompactionTestEnvironment) ProcessAgentInit(
	ctx context.Context, _ string, _ []string,
) (agentenv.AgentInitDecision, error) {
	return agentenv.AgentInitDecision{SystemPrompt: "Keep this system instruction."}, ctx.Err()
}

func TestNativeCompactionLifecycle(t *testing.T) {
	const weakModel = "claude-haiku-5-5"
	secondBlock := strings.ReplaceAll(nativeCompactionTestBlock, "signed-checkpoint", "second-signature")
	thread, requests := newNativeCompactionTestThread(t,
		nativeCompactionTestCapabilities,
		compactionTestStream(nativeCompactionTestBlock, "compaction", nativeCompactionTestUsage),
		strings.ReplaceAll(nativeCompactionTestCapabilities, "claude-sonnet-4-6", weakModel),
		compactionTestStream(secondBlock, "compaction", nativeCompactionTestUsage),
		compactionTestStream(
			`{"type":"text","text":"continued answer"}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`,
		),
	)
	thread.Config.ReasoningEffort = "high"
	environment := &nativeCompactionTestEnvironment{}
	_, err := environment.Open(t.Context(), agentenv.RunSpec{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Close(context.Background())) })
	thread.SetEnvironment(environment)
	original, err := json.Marshal(thread.messages)
	require.NoError(t, err)

	require.NoError(t, thread.CompactContext(t.Context()))
	require.Len(t, requests, 2)
	discovery := <-requests
	assert.Equal(t, "/v1/models/claude-sonnet-4-6", discovery.path)
	compact := <-requests
	assert.Equal(t, "/v1/messages", compact.path)
	for _, request := range []compactionTestRequest{discovery, compact} {
		betas := strings.Join(request.header.Values("anthropic-beta"), ",")
		assert.Contains(t, betas, "compact-2026-09-04")
		assert.Contains(t, betas, "existing-beta")
		assert.Equal(t, "test-key", request.header.Get("x-api-key"))
	}
	request := compact.params
	assert.JSONEq(t, `{"type":"summarize"}`, string(request["compaction"]))
	assert.NotContains(t, request, "context_management")
	assert.NotContains(t, request, "tool_choice")
	assert.Equal(t, "true", string(request["stream"]))
	assert.Equal(t, "8192", string(request["max_tokens"]))
	assert.JSONEq(t, `[{"type":"text","text":"Keep this system instruction."}]`, string(request["system"]))
	assert.JSONEq(t, `[{
		"name":"test_tool", "description":"test",
		"input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
	}]`, string(request["tools"]))
	assert.JSONEq(t, `{"type":"adaptive","display":"summarized"}`, string(request["thinking"]))
	assert.JSONEq(t, `{"effort":"high"}`, string(request["output_config"]))
	assert.Contains(t, string(request["messages"]), "pending request")
	assert.NotContains(t, string(request["messages"]), prompts.CompactPrompt)

	require.Len(t, thread.messages, 1)
	assert.Equal(t, anthropic.MessageParamRoleAssistant, thread.messages[0].Role)
	require.Len(t, thread.messages[0].Content, 1)
	block, err := json.Marshal(thread.messages[0].Content[0])
	require.NoError(t, err)
	assert.JSONEq(t, nativeCompactionTestBlock, string(block), "retain the complete signed block")
	assert.True(t, hasCompactionBlock(thread.messages))
	history := thread.GetCompactionHistory()
	require.Len(t, history.Segments, 1)
	assert.JSONEq(t, string(original), string(history.Segments[0].RawMessages))
	assert.Equal(t, "api", history.Segments[0].Marker.Method)
	assert.Equal(t, 1, history.ActiveDisplayStart)
	usage := thread.GetUsage()
	assert.Equal(t, 120000, usage.InputTokens)
	assert.Equal(t, 256, usage.OutputTokens)
	assert.Equal(t, 40, usage.CacheReadInputTokens)
	assert.Equal(t, 30, usage.CacheCreationInputTokens)
	pricing := getModelPricing(thread.Config.Model)
	assert.InDelta(t, 120000*pricing.Input, usage.InputCost, 1e-10)
	assert.InDelta(t, 256*pricing.Output, usage.OutputCost, 1e-10)
	assert.InDelta(t, 40*pricing.PromptCachingRead, usage.CacheReadCost, 1e-10)
	assert.InDelta(t,
		10*pricing.PromptCachingWrite5m+20*pricing.PromptCachingWrite1h, usage.CacheCreationCost, 1e-10,
	)
	assert.Equal(t, 100, usage.CurrentContextWindow, "estimate context from the summary, not billed usage")
	assert.Equal(t, 200000, usage.MaxContextWindow)

	// Save/load must retain the signed block for automatic recompaction.
	thread.AddUserMessage(t.Context(), "continue after compaction")
	store := &MockConversationStore{}
	thread.Store = store
	thread.Persisted = true
	require.NoError(t, thread.SaveConversation(t.Context()))
	require.Len(t, store.SavedRecords, 1)

	resumed := createTestThread()
	resumed.client = thread.client
	resumed.Store = store
	resumed.Persisted = true
	require.NoError(t, resumed.loadConversation(t.Context()))
	assert.Equal(t, thread.GetCompactionHistory(), resumed.GetCompactionHistory())
	resumed.Persisted = false // Also retain coverage of memory-only automatic compaction.

	// Automatic compaction uses the active turn's model, tools, thinking and cache policy.
	resumed.Usage.CurrentContextWindow = 180000
	resumed.Config.WeakModel = weakModel
	resumed.Config.WeakModelMaxTokens = 16384
	resumed.Config.ReasoningEffort = "high"
	resumed.SetEnvironment(environment)
	handler := &llmtypes.StringCollectorHandler{Silent: true}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output, err := resumed.SendMessage(ctx, "new request", handler, llmtypes.MessageOpt{
		UseWeakModel:    true,
		NoToolUse:       true,
		PromptCache:     true,
		CompactRatio:    0.8,
		DisableUsageLog: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "continued answer", output)
	require.Len(t, requests, 3)
	discovery = <-requests
	assert.Equal(t, "/v1/models/"+weakModel, discovery.path)
	recompact := <-requests
	normal := <-requests
	assert.Equal(t, `"`+weakModel+`"`, string(recompact.params["model"]))
	assert.Equal(t, "16384", string(recompact.params["max_tokens"]))
	var tools []json.RawMessage
	if raw, ok := recompact.params["tools"]; ok {
		require.NoError(t, json.Unmarshal(raw, &tools))
	}
	assert.Empty(t, tools, "NoToolUse also applies to compaction")
	assert.Contains(t, recompact.params, "compaction")
	assert.NotContains(t, normal.params, "compaction")
	assert.Contains(t, string(recompact.params["system"]), "cache_control")
	for _, key := range []string{"model", "max_tokens", "tools", "system", "thinking", "output_config"} {
		assert.Equal(t, normal.params[key], recompact.params[key], "preserve the active turn's %s", key)
	}
	assert.Contains(t, string(recompact.params["messages"]), "new request")
	assert.Contains(t, string(recompact.params["messages"]), "continue after compaction")
	assert.NotContains(t, string(normal.params["messages"]), "original question")
	wantBlocks := []string{nativeCompactionTestBlock, secondBlock}
	for i, request := range []compactionTestRequest{recompact, normal} {
		assert.Contains(t, strings.Join(request.header.Values("anthropic-beta"), ","), "compact-2026-09-04")
		var messages []json.RawMessage
		require.NoError(t, json.Unmarshal(request.params["messages"], &messages))
		require.NotEmpty(t, messages)
		wantMessage := `{"role":"assistant","content":[` + wantBlocks[i] + `]}`
		assert.JSONEq(t, wantMessage, string(messages[0]), "replay the complete signed block unchanged")
	}
	assert.Len(t, resumed.GetCompactionHistory().Segments, 2)
}

func TestNativeCompactionDeserializeStringContent(t *testing.T) {
	raw := `[{"role":"assistant","content":[` + nativeCompactionTestBlock + `]},
		{"role":"user","content":"continue"}]`
	messages, err := DeserializeMessages([]byte(raw))
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.True(t, hasCompactionBlock(messages))
	require.Len(t, messages[1].Content, 1)
	require.NotNil(t, messages[1].Content[0].OfText)
	assert.Equal(t, "continue", messages[1].Content[0].OfText.Text)
}

func TestNativeCompactionDeserializeWithoutCompaction(t *testing.T) {
	raw := `[{"role":"user","content":[{"type":"text","text":"question"}]},
		{"role":"assistant","content":[{"type":"text","text":"answer"}]}]`
	messages, err := DeserializeMessages([]byte(raw))
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.False(t, hasCompactionBlock(messages))
	require.Len(t, messages[1].Content, 1)
	require.NotNil(t, messages[1].Content[0].OfText)
	assert.Equal(t, "answer", messages[1].Content[0].OfText.Text)
}

func TestNativeCompactionFailureFallsBackToSummary(t *testing.T) {
	valid := compactionTestStream(nativeCompactionTestBlock, "compaction", nativeCompactionTestUsage)
	for _, tc := range []struct {
		name   string
		stream string
	}{
		{
			name:   "truncated",
			stream: strings.Split(valid, "event: message_stop")[0],
		},
		{
			name:   "unsigned",
			stream: strings.ReplaceAll(valid, `"signature":"signed-checkpoint"`, `"signature":""`),
		},
		{
			name:   "null content",
			stream: strings.ReplaceAll(valid, `"content":"  Preserve`, `"content":null,"was":"  Preserve`),
		},
		{
			name:   "wrong stop reason",
			stream: strings.ReplaceAll(valid, `"stop_reason":"compaction"`, `"stop_reason":"max_tokens"`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thread, requests := newNativeCompactionTestThread(t,
				nativeCompactionTestCapabilities,
				tc.stream,
				compactionTestStream(
					`{"type":"text","text":"fallback summary"}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`,
				),
				compactionTestStream(
					`{"type":"text","text":"continued answer"}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`,
				),
			)
			thread.SetEnvironment(&agentenv.UtilityEnvironment{})
			handler := &llmtypes.StringCollectorHandler{Silent: true}
			output, err := thread.SendMessage(t.Context(), "new request", handler, llmtypes.MessageOpt{
				NoToolUse:       true,
				CompactRatio:    0.8,
				DisableUsageLog: true,
			})
			require.NoError(t, err)
			assert.Equal(t, "continued answer", output)
			require.Len(t, requests, 4)
			assert.Equal(t, "/v1/models/claude-sonnet-4-6", (<-requests).path)
			assert.Contains(t, (<-requests).params, "compaction")
			summary := <-requests
			assert.NotContains(t, summary.params, "compaction")
			assert.Contains(t, string(summary.params["messages"]), "new request")
			normal := <-requests
			assert.NotContains(t, string(normal.params["messages"]), "original question")
			assert.Contains(t, string(normal.params["messages"]), "fallback summary")
			history := thread.GetCompactionHistory()
			require.NotNil(t, history)
			require.Len(t, history.Segments, 1)
			assert.Equal(t, "summary", history.Segments[0].Marker.Method)
			assert.False(t, hasCompactionBlock(thread.messages))
			assert.GreaterOrEqual(t, thread.GetUsage().InputTokens, 120000, "failed compaction is still billed")
		})
	}
}

func TestNativeCompactionHistoryChangeSkipsFallback(t *testing.T) {
	thread, requests := newNativeCompactionTestThread(t,
		nativeCompactionTestCapabilities,
		compactionTestStream(nativeCompactionTestBlock, "compaction", nativeCompactionTestUsage),
	)
	params, err := thread.messageParams(
		thread.Config.Model, thread.Config.MaxTokens, "system", llmtypes.MessageOpt{NoToolUse: true},
	)
	require.NoError(t, err)
	thread.AddUserMessage(t.Context(), "arrived during compaction")
	before, err := json.Marshal(thread.messages)
	require.NoError(t, err)

	err = thread.compactContext(t.Context(), params)
	require.ErrorIs(t, err, errCompactionHistoryChanged)
	require.Len(t, requests, 2, "discovery and native compaction only, without a summary fallback")
	after, err := json.Marshal(thread.messages)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
	assert.Nil(t, thread.GetCompactionHistory())
}

func TestAutoCompactionFailureContinuesTurn(t *testing.T) {
	// Native compaction and its summary fallback both fail; the turn still answers
	// from the uncompacted history instead of failing every later turn.
	thread, requests := newNativeCompactionTestThread(t,
		nativeCompactionTestCapabilities,
		compactionTestStream(nativeCompactionTestBlock, "max_tokens", nativeCompactionTestUsage),
		compactionTestStream(`{"type":"text","text":"   "}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`),
		compactionTestStream(
			`{"type":"text","text":"continued answer"}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`,
		),
	)
	thread.SetEnvironment(&agentenv.UtilityEnvironment{})
	original, err := json.Marshal(thread.messages)
	require.NoError(t, err)
	handler := &llmtypes.StringCollectorHandler{Silent: true}
	output, err := thread.SendMessage(t.Context(), "new request", handler, llmtypes.MessageOpt{
		NoToolUse:       true,
		CompactRatio:    0.8,
		DisableUsageLog: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "continued answer", output)
	require.Len(t, requests, 4)
	for range 3 {
		<-requests
	}
	normal := <-requests
	assert.NotContains(t, normal.params, "compaction")
	assert.Contains(t, string(normal.params["messages"]), "original question")
	require.Len(t, thread.messages, 5, "history, the admitted user message and the answer")
	before, err := json.Marshal(thread.messages[:3])
	require.NoError(t, err)
	assert.JSONEq(t, string(original), string(before))
	assert.Nil(t, thread.GetCompactionHistory())
}

func TestNativeCompactionCapabilityCached(t *testing.T) {
	summary := compactionTestStream(
		`{"type":"text","text":"fallback summary"}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`,
	)
	native := compactionTestStream(nativeCompactionTestBlock, "compaction", nativeCompactionTestUsage)
	for _, tc := range []struct {
		name    string
		replies []string
		paths   []string
	}{
		{
			name:    "supported",
			replies: []string{nativeCompactionTestCapabilities, native, native},
			paths:   []string{"/v1/models/claude-sonnet-4-6", "/v1/messages", "/v1/messages"},
		},
		{
			name:    "discovery not found",
			replies: []string{"", summary, summary},
			paths:   []string{"/v1/models/claude-sonnet-4-6", "/v1/messages", "/v1/messages"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thread, requests := newNativeCompactionTestThread(t, tc.replies...)
			require.NoError(t, thread.CompactContext(t.Context()))
			thread.AddUserMessage(t.Context(), "more work")
			require.NoError(t, thread.CompactContext(t.Context()))
			require.Len(t, requests, len(tc.paths))
			for _, path := range tc.paths {
				assert.Equal(t, path, (<-requests).path)
			}
			assert.Len(t, thread.GetCompactionHistory().Segments, 2)
		})
	}
}

func TestNativeCompactionDiscoveryFailureCaching(t *testing.T) {
	for status, cached := range map[int]bool{
		http.StatusNotFound:            true,
		http.StatusBadRequest:          true,
		http.StatusTooManyRequests:     false,
		http.StatusUnauthorized:        false,
		http.StatusInternalServerError: false,
	} {
		assert.Equal(t, cached, isDefinitiveDiscoveryFailure(status), "status %d", status)
	}
}

func TestNativeCompactionSummaryFallback(t *testing.T) {
	for _, mode := range []string{
		"unsupported capability", "unsupported summarize", "discovery failure", "copilot",
	} {
		t.Run(mode, func(t *testing.T) {
			capabilities := strings.Replace(
				nativeCompactionTestCapabilities, `"supported": true`, `"supported": false`, 1,
			)
			switch mode {
			case "unsupported summarize":
				capabilities = strings.Replace(nativeCompactionTestCapabilities,
					`"summarize": {"supported": true}`, `"summarize": {"supported": false}`, 1,
				)
			case "discovery failure":
				capabilities = ""
			}
			replies := []string{
				capabilities,
				compactionTestStream(
					`{"type":"text","text":"fallback summary"}`, "end_turn", `{"input_tokens":15,"output_tokens":3}`,
				),
			}
			if mode == "copilot" {
				replies = replies[1:]
			}
			thread, requests := newNativeCompactionTestThread(t, replies...)
			thread.useCopilot = mode == "copilot"
			require.NoError(t, thread.CompactContext(t.Context()))
			require.Len(t, requests, len(replies))
			if mode != "copilot" {
				discovery := <-requests
				assert.Equal(t, "/v1/models/claude-sonnet-4-6", discovery.path)
			}
			request := <-requests
			assert.Equal(t, "/v1/messages", request.path)
			assert.NotContains(t, request.params, "compaction")
			assert.NotContains(t, request.params, "context_management")
			assert.NotContains(t, strings.Join(request.header.Values("anthropic-beta"), ","), "compact-2026-09-04")
			var messages []anthropic.MessageParam
			require.NoError(t, json.Unmarshal(request.params["messages"], &messages))
			require.NotEmpty(t, messages)
			last := messages[len(messages)-1]
			require.NotEmpty(t, last.Content)
			require.NotNil(t, last.Content[0].OfText)
			assert.Equal(t, prompts.CompactPrompt, last.Content[0].OfText.Text)
			require.Len(t, thread.messages, 1)
			assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[0].Role)
			require.Len(t, thread.messages[0].Content, 1)
			require.NotNil(t, thread.messages[0].Content[0].OfText)
			assert.Equal(t, "fallback summary", strings.TrimSpace(thread.messages[0].Content[0].OfText.Text))
			assert.False(t, hasCompactionBlock(thread.messages))
			history := thread.GetCompactionHistory()
			require.Len(t, history.Segments, 1)
			assert.Equal(t, "summary", history.Segments[0].Marker.Method)
		})
	}
}
