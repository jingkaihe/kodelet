package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeWebSearchTools(t *testing.T) {
	require.NoError(t, tools.ValidateTools([]string{anthropicSearchToolName}))
	for _, tc := range []struct {
		name         string
		config       llmtypes.Config
		implicit     bool
		patch        any
		noTools      bool
		subscription bool
		copilot      bool
		want         bool
		wantError    bool
	}{
		{name: "disabled by default", implicit: true},
		{name: "explicit profile", config: llmtypes.Config{AllowedTools: []string{anthropicSearchToolName}}, want: true},
		{name: "message disables tools", noTools: true},
		{name: "request disables tools", config: llmtypes.Config{ExecutionOptions: &llmtypes.ExecutionOptions{NoTools: new(true)}}},
		{name: "request empty allowlist", config: llmtypes.Config{ExecutionOptions: &llmtypes.ExecutionOptions{AllowedTools: new([]string{})}}},
		{name: "request narrows allowlist", config: llmtypes.Config{ExecutionOptions: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"bash"})}}},
		{name: "extension empty allowlist", patch: []string{}},
		{name: "extension narrows allowlist", patch: []any{"bash"}},
		{name: "extension preserves search", patch: []any{anthropicSearchToolName}, want: true},
		{name: "subscription supported", subscription: true, want: true},
		{name: "copilot unsupported", copilot: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config
			if !tc.implicit {
				config.AllowedTools = []string{anthropicSearchToolName}
			}
			thread := &Thread{Thread: base.NewThread(config, "search"), useSubscription: tc.subscription, useCopilot: tc.copilot}
			if tc.patch != nil {
				thread.SetMetadataValue("allowed_tools", tc.patch)
			}
			definitions, err := thread.requestTools(llmtypes.MessageOpt{NoToolUse: tc.noTools})
			if tc.wantError {
				require.ErrorContains(t, err, "Copilot")
				return
			}
			require.NoError(t, err)
			if !tc.want {
				assert.Empty(t, definitions)
				return
			}
			raw, err := json.Marshal(definitions)
			require.NoError(t, err)
			assert.JSONEq(t, `[{"type":"web_search_20250305","name":"web_search","max_uses":5}]`, string(raw))
		})
	}
}

func TestNativeWebSearchExchange(t *testing.T) {
	const searchCall = `{"type":"server_tool_use","id":"search-1","name":"web_search","input":{"query":"evidence"}}`
	const searchResult = `{"type":"web_search_tool_result","tool_use_id":"search-1","content":[{"type":"web_search_result","url":"https://example.com","title":"Source","encrypted_content":"private-results"}]}`
	const answer = `{"type":"text","text":"Finding.","citations":[{"type":"web_search_result_location","url":"https://example.com/a(b)","title":null,"encrypted_index":"private-index","cited_text":"Evidence"}]}`
	const pausedAnswer = `{"type":"text","text":"Earlier finding. ","citations":[{"type":"web_search_result_location","url":"https://example.org/earlier","title":"Earlier source","encrypted_index":"earlier-index","cited_text":"Earlier evidence"}]}`
	const searchError = `{"type":"web_search_tool_result","tool_use_id":"search-1","content":{"type":"web_search_tool_result_error","error_code":"unavailable"}}`
	for _, tc := range []struct {
		name      string
		blocks    [][]string
		stops     []string
		nonstream bool
		wantError string
	}{
		{name: "complete search", blocks: [][]string{{searchCall, searchResult, `{"type":"text","text":"Research: "}`, answer}}, stops: []string{"end_turn"}},
		{
			name: "multiple searches in one turn",
			blocks: [][]string{{
				searchCall, searchResult,
				strings.NewReplacer("search-1", "search-2", "evidence", "more evidence").Replace(searchCall),
				strings.ReplaceAll(searchResult, "search-1", "search-2"),
				strings.NewReplacer("search-1", "search-3", "evidence", "verify evidence").Replace(searchCall),
				strings.ReplaceAll(searchResult, "search-1", "search-3"),
				`{"type":"text","text":"Research: "}`, answer,
			}},
			stops: []string{"end_turn"},
		},
		{name: "paused search", blocks: [][]string{{searchCall, searchResult, pausedAnswer}, {`{"type":"text","text":"Research: "}`, answer}}, stops: []string{"pause_turn", "end_turn"}},
		{name: "paused non-streaming search", nonstream: true, blocks: [][]string{{searchCall, searchResult, pausedAnswer}, {`{"type":"text","text":"Research: "}`, answer}}, stops: []string{"pause_turn", "end_turn"}},
		{name: "bounded pauses", blocks: [][]string{{searchCall, searchResult}}, stops: []string{"pause_turn"}, wantError: "within 3 requests"},
		{name: "embedded search error", blocks: [][]string{{searchCall, searchError}}, stops: []string{"end_turn"}, wantError: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []anthropic.MessageNewParams
			reported := make(chan string, 1)
			var expectedProgress []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request anthropic.MessageNewParams
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				requests = append(requests, request)
				turn := min(len(requests)-1, len(tc.blocks)-1)
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(kind string, payload any) {
					data, err := json.Marshal(payload)
					require.NoError(t, err)
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
				}
				emit("message_start", json.RawMessage(`{"type":"message_start","message":{"id":"msg-search","type":"message","role":"assistant","content":[],"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":10,"output_tokens":0}}}`))
				for i, raw := range tc.blocks[turn] {
					var block map[string]any
					require.NoError(t, json.Unmarshal([]byte(raw), &block))
					text, citations, input := block["text"], block["citations"], block["input"]
					if text != nil {
						block["text"] = ""
						delete(block, "citations")
					}
					if input != nil {
						block["input"] = map[string]any{}
					}
					emit("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": block})
					if text != nil {
						emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": text}})
					}
					if input != nil {
						data, err := json.Marshal(input)
						require.NoError(t, err)
						emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(data)}})
					}
					if citations != nil {
						for _, citation := range citations.([]any) {
							emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "citations_delta", "citation": citation}})
						}
					}
					emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
					if block["type"] == "server_tool_use" || block["type"] == "web_search_tool_result" {
						var expected string
						if block["type"] == "server_tool_use" {
							inputJSON, err := json.Marshal(input)
							require.NoError(t, err)
							expected = fmt.Sprintf("call:%s:web_search:%s", block["id"], inputJSON)
						} else if tc.wantError == "unavailable" {
							expected = "result:search-1:web_search:Anthropic web search failed: unavailable"
						} else {
							expected = fmt.Sprintf("result:%s:web_search:Found 1 search results", block["tool_use_id"])
						}
						expectedProgress = append(expectedProgress, expected)
						w.(http.Flusher).Flush()
						select {
						case progress := <-reported:
							assert.Equal(t, expected, progress)
						case <-time.After(2 * time.Second):
							t.Error("native search progress was not emitted before the next block")
							return
						}
					}
				}
				emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": tc.stops[turn]}, "usage": map[string]any{"output_tokens": 3}})
				emit("message_stop", map[string]any{"type": "message_stop"})
			}))
			defer server.Close()
			thread := &Thread{
				Thread: base.NewThread(llmtypes.Config{Provider: "anthropic", AllowedTools: []string{anthropicSearchToolName}}, "search"),
				client: anthropic.NewClient(option.WithAPIKey("server-key"), option.WithBaseURL(server.URL)),
				messages: []anthropic.MessageParam{
					anthropic.NewUserMessage(anthropic.NewTextBlock("Research this")),
				},
			}
			handler := &searchProgressHandler{
				StringCollectorHandler: llmtypes.StringCollectorHandler{Silent: true},
				reported:               reported,
			}
			var delivery llmtypes.MessageHandler = handler
			if tc.nonstream {
				delivery = struct{ llmtypes.MessageHandler }{handler}
			}
			output, more, err := thread.processMessageExchange(
				t.Context(), delivery, "claude-haiku-4-5-20251001", 4096, "Research",
				llmtypes.MessageOpt{DisableUsageLog: true, PromptCache: true},
			)
			server.Close()
			assert.Equal(t, expectedProgress, handler.progress)
			for _, result := range handler.results {
				assert.Equal(t, tc.wantError == "unavailable", result.IsError())
				assert.NotContains(t, result.AssistantFacing(), "private-results")
				assert.NotContains(t, result.AssistantFacing(), "https://example.com")
			}
			assert.False(t, more, "server tools must not create an extra client-tool turn")
			assert.EqualValues(t, 10*len(requests), thread.GetUsage().InputTokens)
			assert.EqualValues(t, 3*len(requests), thread.GetUsage().OutputTokens)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.LessOrEqual(t, len(requests), 3)
				return
			}
			require.NoError(t, err)
			wantOutput := "Research: Finding. [source](<https://example.com/a(b)>)"
			if len(tc.blocks) > 1 {
				wantOutput = "Earlier finding.  [source](<https://example.org/earlier>)" + wantOutput
				assert.Contains(t, handler.CollectedText(), "[source](<https://example.org/earlier>)")
			}
			assert.Equal(t, wantOutput, output)
			assert.Contains(t, handler.CollectedText(), "Finding. [source](<https://example.com/a(b)>)")
			if !tc.nonstream {
				wantBlocks := `{"text":"Research: ","citations":[]},{"text":"Finding.","citations":[{"url":"https://example.com/a(b)","cited_text":"Evidence"}]}`
				if len(tc.blocks) > 1 {
					wantBlocks = `{"text":"Earlier finding. ","citations":[{"url":"https://example.org/earlier","title":"Earlier source","cited_text":"Earlier evidence"}]},` + wantBlocks
				}
				data, err := json.Marshal(handler.textData)
				require.NoError(t, err)
				assert.JSONEq(t, "["+wantBlocks+"]", string(data))
				assert.Equal(t, wantOutput, strings.Join(handler.texts, ""), "emit complete blocks without duplicate text deltas")
			}
			require.Len(t, requests, len(tc.blocks))
			require.Len(t, requests[0].Tools, 1)
			require.NotNil(t, requests[0].Tools[0].OfWebSearchTool20250305)
			if len(requests) == 2 {
				require.Len(t, requests[1].Messages, 2)
				replayed, err := json.Marshal(requests[1].Messages[1])
				require.NoError(t, err)
				assert.JSONEq(t, `{"role":"assistant","content":[`+strings.Join(tc.blocks[0], ",")+`]}`, string(replayed))
			}
			raw, err := json.Marshal(thread.messages)
			require.NoError(t, err)
			replayed, err := StreamMessages(raw, nil)
			require.NoError(t, err)
			assert.Contains(t, replayed[len(replayed)-1].Content, "[source](<https://example.com/a(b)>)")
			extracted, err := ExtractMessages(raw, nil)
			require.NoError(t, err)
			assert.Contains(t, extracted[len(extracted)-1].Content, "[source](<https://example.com/a(b)>)")
		})
	}
}

type searchProgressHandler struct {
	llmtypes.StringCollectorHandler
	reported chan string
	progress []string
	results  []tooltypes.ToolResult
	texts    []string
	textData []any
}

func (h *searchProgressHandler) HandleStructuredText(text string, data any) {
	h.texts = append(h.texts, text)
	h.textData = append(h.textData, data)
	h.HandleText(text)
}

func (h *searchProgressHandler) HandleToolUse(id, name, input string) {
	event := fmt.Sprintf("call:%s:%s:%s", id, name, input)
	h.progress = append(h.progress, event)
	h.reported <- event
}

func (h *searchProgressHandler) HandleToolResult(id, name string, result tooltypes.ToolResult) {
	output := result.GetResult()
	if result.IsError() {
		output = result.GetError()
	}
	event := fmt.Sprintf("result:%s:%s:%s", id, name, output)
	h.progress = append(h.progress, event)
	h.results = append(h.results, result)
	h.reported <- event
}

func TestWebSearchCitationLinks(t *testing.T) {
	var citations []anthropic.TextCitationParamUnion
	for _, raw := range []string{"https://example.com/a(b)?q=<x>", "javascript:alert(1)", "https://example.com/a(b)?q=<x>", "https://"} {
		citations = append(citations, anthropic.TextCitationParamUnion{
			OfWebSearchResultLocation: &anthropic.CitationWebSearchResultLocationParam{URL: raw},
		})
	}
	assert.Equal(t, " [source](<https://example.com/a(b)?q=%3Cx%3E>)", webSearchCitationLinks(citations))
}
