package base

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateContextBreakdownProviderHistory(t *testing.T) {
	for _, test := range []struct {
		name, system, history string
		thinkingTokens        int
	}{
		{
			name:   "Anthropic",
			system: `[{"type":"text","text":"system!!","cache_control":{"type":"ephemeral"}}]`,
			history: `[
				{"role":"user","content":[{"type":"text","text":"user"}]},
				{"role":"assistant","content":[
					{"type":"thinking","thinking":"plan","signature":"opaque"},
					{"type":"text","text":"text"},
					{"type":"tool_use","id":"1","name":"code_execute","input":{"code":"run"}}
				]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":[{"type":"text","text":"done"}]}]},
				{"role":"assistant","content":[{"type":"text","text":"last"}]}
			]`,
		},
		{
			name:           "Chat Completions",
			system:         `null`,
			thinkingTokens: 1,
			history: `[
				{"role":"system","content":"system!!"},
				{"role":"user","content":[{"type":"text","text":"user"}]},
				{"role":"assistant","content":"text","reasoning_content":"plan","tool_calls":[{"id":"1","type":"function","function":{"name":"code_execute","arguments":"{\"code\":\"run\"}"}}]},
				{"role":"tool","tool_call_id":"1","content":"done"},
				{"role":"assistant","content":"last"}
			]`,
		},
		{
			name:   "Responses",
			system: `"syst"`,
			history: `[
				{"role":"developer","content":"em!!"},
				{"role":"user","content":[{"type":"input_text","text":"user"}]},
				{"type":"reasoning","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"opaque"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"text"}]},
				{"type":"function_call","id":"fc_1","call_id":"1","name":"code_execute","arguments":"{\"code\":\"run\"}"},
				{"type":"function_call_output","call_id":"1","output":[{"type":"input_text","text":"done"}]},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"last"}]}
			]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			definitions := json.RawMessage(`[{"name":"code_execute","description":"Tool index: bash","input_schema":{"type":"object"}}]`)
			breakdown := EstimateContextBreakdown(json.RawMessage(test.system), definitions, json.RawMessage(test.history), "only")
			require.NotNil(t, breakdown)
			assert.True(t, breakdown.Estimated)
			assert.Equal(t, "only", breakdown.CodeMode)
			assert.Equal(t, 2, breakdown.SystemPromptTokens)
			assert.Equal(t, (len(definitions)+3)/4, breakdown.ToolDefinitionTokens)
			assert.Equal(t, 1, breakdown.InputTextTokens)
			assert.Equal(t, 2, breakdown.OutputTextTokens)
			if test.thinkingTokens > 0 {
				require.NotNil(t, breakdown.ThinkingTokens)
				assert.Equal(t, test.thinkingTokens, *breakdown.ThinkingTokens)
			} else {
				assert.Nil(t, breakdown.ThinkingTokens, "summary text does not measure generated thinking")
			}
			assert.Zero(t, breakdown.OtherTokens)
			assert.Equal(t, []llmtypes.ToolContextUsage{{Name: "code_execute", InputTokens: 4, OutputTokens: 1, Calls: 1}}, breakdown.Tools)
			assert.Equal(t, 10+breakdown.ToolDefinitionTokens+test.thinkingTokens, breakdown.TotalTokens)
		})
	}
}

func TestContextBreakdownMediaAndOpaqueContentExcluded(t *testing.T) {
	history := json.RawMessage(`[
		{"role":"user","content":[
			{"type":"text","text":"user"},
			{"type":"image","source":{"type":"base64","data":"PAYLOAD"}},
			{"type":"image_url","image_url":{"url":"PAYLOAD"}},
			{"type":"input_image","image_url":"PAYLOAD"},
			{"type":"input_audio","data":"PAYLOAD"}
		]},
		{"role":"assistant","content":[{"type":"redacted_thinking","data":"PAYLOAD"}]},
		{"role":"assistant","content":[{"type":"thinking","thinking":"PAYLOAD","signature":"PAYLOAD"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"PAYLOAD"}],"content":[{"type":"reasoning_text","text":"PAYLOAD"}],"encrypted_content":"PAYLOAD"},
		{"type":"compaction","encrypted_content":"PAYLOAD"}
	]`)
	short := EstimateContextBreakdown(nil, nil, history, "")
	long := EstimateContextBreakdown(nil, nil, json.RawMessage(strings.ReplaceAll(string(history), "PAYLOAD", strings.Repeat("x", 100_000))), "")
	require.NotNil(t, short)
	assert.Equal(t, short, long)
	assert.Equal(t, 1, short.TotalTokens)
	assert.Nil(t, short.ThinkingTokens)
	assert.Equal(t, "off", short.CodeMode)
	assert.NotNil(t, short.Tools)
	assert.Empty(t, short.Tools)
}

func TestContextThinkingReportedUsageReplacesReplayedTextEstimate(t *testing.T) {
	estimate := EstimateContextBreakdown(nil, nil, json.RawMessage(`[
		{"role":"assistant","content":"text","reasoning_content":"thinking"}
	]`), "")
	thread := NewThread(llmtypes.Config{}, "thinking")
	thread.SetContextBreakdown(estimate)
	require.NotNil(t, thread.GetUsage().ContextBreakdown.ThinkingTokens)
	assert.Equal(t, 2, *thread.GetUsage().ContextBreakdown.ThinkingTokens)
	assert.Equal(t, 3, thread.GetUsage().ContextBreakdown.TotalTokens)
	assert.Empty(t, thread.GetUsage().ContextThinkingByResponse, "text estimates are not reported usage")

	for _, reported := range []int{20, 0} {
		thread.RecordContextThinking("response", reported)
		thread.SetContextBreakdown(estimate)
		usage := thread.GetUsage()
		assert.Equal(t, reported, *usage.ContextBreakdown.ThinkingTokens)
		assert.Equal(t, 1+reported, usage.ContextBreakdown.TotalTokens, "reported thinking replaces rather than adds to replayed text")
	}
}

func TestRestoreContextBreakdown(t *testing.T) {
	for _, name := range []string{"existing usage", "nil usage"} {
		t.Run(name, func(t *testing.T) {
			thread := NewThread(llmtypes.Config{}, "restore")
			thread.SetContextBreakdown(&llmtypes.ContextBreakdown{
				TotalTokens: 5, Tools: []llmtypes.ToolContextUsage{{Name: "bash", InputTokens: 5}},
			})
			thread.RecordContextThinking("original", 10)
			snapshot := thread.GetUsage()
			thread.RecordContextThinking("temporary", 20)
			if name == "nil usage" {
				thread.Usage = nil
			} else {
				thread.Usage.OutputTokens = 100
				thread.Usage.OutputCost = 0.5
			}

			thread.RestoreContextBreakdown(snapshot)
			restored := thread.GetUsage()
			assert.Equal(t, snapshot.ContextBreakdown, restored.ContextBreakdown)
			assert.Equal(t, snapshot.ContextThinkingByResponse, restored.ContextThinkingByResponse)
			if name == "existing usage" {
				assert.Equal(t, 100, restored.OutputTokens)
				assert.Equal(t, 0.5, restored.OutputCost)
			}

			snapshot.ContextThinkingByResponse["original"] = 99
			*snapshot.ContextBreakdown.ThinkingTokens = 99
			snapshot.ContextBreakdown.Tools[0].InputTokens = 99
			assert.Equal(t, restored, thread.GetUsage(), "restored state owns its snapshots")

			thread.RestoreContextBreakdown(llmtypes.Usage{})
			assert.Nil(t, thread.GetUsage().ContextBreakdown)
			assert.Nil(t, thread.GetUsage().ContextThinkingByResponse)
			assert.Equal(t, restored.OutputTokens, thread.GetUsage().OutputTokens)
		})
	}
}

func TestContextBreakdownAggregatesToolNamesAndNativeResults(t *testing.T) {
	history := json.RawMessage(`[
		{"role":"assistant","content":[
			{"type":"server_tool_use","id":"1","name":"web_search","input":{"query":"test"}},
			{"type":"web_search_tool_result","tool_use_id":"1","content":[{"type":"web_search_result","title":"test","url":"link","encrypted_content":"opaque"}]}
		]},
		{"type":"function_call","call_id":"2","name":"bash","arguments":"abcd"},
		{"type":"function_call_output","call_id":"2","output":"abcd"},
		{"type":"function_call","call_id":"3","name":"bash","arguments":"efgh"},
		{"type":"function_call_output","call_id":"3","output":"efgh"},
		{"type":"function_call_output","call_id":"orphan","output":"lost"},
		{"role":"assistant","content":[{"type":"compaction","content":"summary!","signature":"opaque"}]}
	]`)
	b := EstimateContextBreakdown(nil, nil, history, "on")
	require.NotNil(t, b)
	assert.Equal(t, []llmtypes.ToolContextUsage{
		{Name: "bash", InputTokens: 2, OutputTokens: 2, Calls: 2},
		{Name: "web_search", InputTokens: 4, OutputTokens: 2, Calls: 1},
	}, b.Tools)
	assert.Equal(t, 3, b.OtherTokens)
	assert.Zero(t, b.InputTextTokens)
	assert.Zero(t, b.OutputTextTokens)
}

func TestContextBreakdownSnapshots(t *testing.T) {
	thread := NewThread(llmtypes.Config{}, "test")
	thinking := 0
	breakdown := &llmtypes.ContextBreakdown{
		SystemPromptTokens: 1, TotalTokens: 4, Estimated: true, CodeMode: "only",
		ThinkingTokens: &thinking,
		Tools:          []llmtypes.ToolContextUsage{{Name: "code_execute", Calls: 1, InputTokens: 1, OutputTokens: 2}},
	}
	thread.SetContextBreakdown(breakdown)
	thinking = 999
	breakdown.Tools[0].Name = "mutated"
	usage := thread.GetUsage()
	require.NotNil(t, usage.ContextBreakdown.ThinkingTokens)
	assert.Zero(t, *usage.ContextBreakdown.ThinkingTokens)
	*usage.ContextBreakdown.ThinkingTokens = 999
	assert.Equal(t, "code_execute", usage.ContextBreakdown.Tools[0].Name)
	usage.ContextBreakdown.Tools[0].InputTokens = 999
	usage.ContextBreakdown.TotalTokens = 999
	assert.Equal(t, 4, thread.GetUsage().ContextBreakdown.TotalTokens)
	assert.Equal(t, 1, thread.GetUsage().ContextBreakdown.Tools[0].InputTokens)
	assert.Zero(t, *thread.GetUsage().ContextBreakdown.ThinkingTokens)
	thread.AggregateSubagentUsage(usage)
	assert.Equal(t, 4, thread.GetUsage().ContextBreakdown.TotalTokens)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				snapshot := thread.GetUsage().ContextBreakdown
				snapshot.Tools[0].Calls++
				*snapshot.ThinkingTokens++
				thread.SetContextBreakdown(snapshot)
			}
		})
	}
	wg.Wait()
	thread.Mu.Lock()
	thread.FinalizeSwapContextLocked("summary")
	thread.Mu.Unlock()
	assert.Nil(t, thread.GetUsage().ContextBreakdown)
}

func TestContextBreakdownInvalidWireContent(t *testing.T) {
	assert.Nil(t, EstimateContextBreakdown(make(chan int), nil, nil, ""))
	assert.Nil(t, EstimateContextBreakdown(nil, make(chan int), nil, ""))
	assert.Nil(t, EstimateContextBreakdown(nil, nil, json.RawMessage(`invalid`), ""))
}

func TestContextThinkingAccumulation(t *testing.T) {
	thread := NewThread(llmtypes.Config{}, "thinking")
	estimate := &llmtypes.ContextBreakdown{InputTextTokens: 10, TotalTokens: 10, Estimated: true}
	thread.SetContextBreakdown(estimate)
	assert.Nil(t, thread.GetUsage().ContextBreakdown.ThinkingTokens)
	thread.RecordContextThinking("", 100)
	thread.RecordContextThinking("invalid", -1)
	assert.Empty(t, thread.GetUsage().ContextThinkingByResponse)
	thread.RecordContextThinking("first", 0)
	require.NotNil(t, thread.GetUsage().ContextBreakdown.ThinkingTokens)
	assert.Zero(t, *thread.GetUsage().ContextBreakdown.ThinkingTokens)

	thread.RecordContextThinking("first", 100)
	before := thread.GetUsage()
	thread.RecordContextThinking("second", 50)
	thread.RecordContextThinking("second", 50)
	thread.RecordContextThinking("third", 0)
	usage := thread.GetUsage()
	assert.Equal(t, 150, *usage.ContextBreakdown.ThinkingTokens)
	assert.Equal(t, 160, usage.ContextBreakdown.TotalTokens)
	assert.Equal(t, 100, *before.ContextBreakdown.ThinkingTokens)
	assert.Equal(t, map[string]int{"first": 100}, before.ContextThinkingByResponse)

	usage.ContextThinkingByResponse["first"] = 999
	*usage.ContextBreakdown.ThinkingTokens = 999
	assert.Equal(t, 100, thread.GetUsage().ContextThinkingByResponse["first"])
	assert.Equal(t, 150, *thread.GetUsage().ContextBreakdown.ThinkingTokens)
	thread.AggregateSubagentUsage(llmtypes.Usage{ContextThinkingByResponse: map[string]int{"child": 999}})
	assert.NotContains(t, thread.GetUsage().ContextThinkingByResponse, "child")
	assert.Zero(t, thread.GetUsage().OutputTokens, "already part of provider output, not added again")

	// Appending messages invalidates the text estimate, not the recorded usage.
	thread.SetContextBreakdown(nil)
	assert.Nil(t, thread.GetUsage().ContextBreakdown)
	thread.SetContextBreakdown(estimate)
	assert.Equal(t, 150, *thread.GetUsage().ContextBreakdown.ThinkingTokens)
	thread.SetContextBreakdown(thread.GetUsage().ContextBreakdown)
	assert.Equal(t, 160, thread.GetUsage().ContextBreakdown.TotalTokens, "refresh must not double count")

	// Persistence/forks carry counts independently, including when no estimate exists.
	thread.SetContextBreakdown(nil)
	raw, err := json.Marshal(thread.GetUsage())
	require.NoError(t, err)
	resumed := NewThread(llmtypes.Config{}, "resumed")
	require.NoError(t, json.Unmarshal(raw, resumed.Usage))
	resumed.RecordContextThinking("second", 50)
	resumed.RecordContextThinking("fourth", 20)
	resumed.SetContextBreakdown(estimate)
	assert.Equal(t, 170, *resumed.GetUsage().ContextBreakdown.ThinkingTokens)
	assert.Equal(t, 180, resumed.GetUsage().ContextBreakdown.TotalTokens)
	resumed.Mu.Lock()
	resumed.FinalizeSwapContextLocked("summary")
	resumed.Mu.Unlock()
	assert.Empty(t, resumed.GetUsage().ContextThinkingByResponse)
	resumed.SetContextBreakdown(estimate)
	assert.Nil(t, resumed.GetUsage().ContextBreakdown.ThinkingTokens)
	resumed.RecordContextThinking("after-compaction", 5)
	assert.Equal(t, 5, *resumed.GetUsage().ContextBreakdown.ThinkingTokens)
}

func TestContextThinkingConcurrentSnapshots(t *testing.T) {
	thread := NewThread(llmtypes.Config{}, "thinking")
	thread.SetContextBreakdown(&llmtypes.ContextBreakdown{InputTextTokens: 10, TotalTokens: 10})
	var wg sync.WaitGroup
	for _, response := range []string{"first", "second", "third", "fourth"} {
		wg.Go(func() {
			for range 20 {
				thread.RecordContextThinking(response, 10)
				thread.SetContextBreakdown(thread.GetUsage().ContextBreakdown)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, 40, *thread.GetUsage().ContextBreakdown.ThinkingTokens)
	assert.Equal(t, 50, thread.GetUsage().ContextBreakdown.TotalTokens)
}
