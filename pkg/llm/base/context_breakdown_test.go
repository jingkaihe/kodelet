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
			name:   "Chat Completions",
			system: `null`,
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
			assert.Equal(t, 1, breakdown.OtherTokens)
			assert.Equal(t, []llmtypes.ToolContextUsage{{Name: "code_execute", InputTokens: 4, OutputTokens: 1, Calls: 1}}, breakdown.Tools)
			assert.Equal(t, 11+breakdown.ToolDefinitionTokens, breakdown.TotalTokens)
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
		{"type":"reasoning","encrypted_content":"PAYLOAD"},
		{"type":"compaction","encrypted_content":"PAYLOAD"}
	]`)
	short := EstimateContextBreakdown(nil, nil, history, "")
	long := EstimateContextBreakdown(nil, nil, json.RawMessage(strings.ReplaceAll(string(history), "PAYLOAD", strings.Repeat("x", 100_000))), "")
	require.NotNil(t, short)
	assert.Equal(t, short, long)
	assert.Equal(t, 1, short.TotalTokens)
	assert.Equal(t, "off", short.CodeMode)
	assert.NotNil(t, short.Tools)
	assert.Empty(t, short.Tools)
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
	breakdown := &llmtypes.ContextBreakdown{
		SystemPromptTokens: 1, TotalTokens: 4, Estimated: true, CodeMode: "only",
		Tools: []llmtypes.ToolContextUsage{{Name: "code_execute", Calls: 1, InputTokens: 1, OutputTokens: 2}},
	}
	thread.SetContextBreakdown(breakdown)
	breakdown.Tools[0].Name = "mutated"
	usage := thread.GetUsage()
	assert.Equal(t, "code_execute", usage.ContextBreakdown.Tools[0].Name)
	usage.ContextBreakdown.Tools[0].InputTokens = 999
	usage.ContextBreakdown.TotalTokens = 999
	assert.Equal(t, 4, thread.GetUsage().ContextBreakdown.TotalTokens)
	assert.Equal(t, 1, thread.GetUsage().ContextBreakdown.Tools[0].InputTokens)
	thread.AggregateSubagentUsage(usage)
	assert.Equal(t, 4, thread.GetUsage().ContextBreakdown.TotalTokens)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				snapshot := thread.GetUsage().ContextBreakdown
				snapshot.Tools[0].Calls++
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
