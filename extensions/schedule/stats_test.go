package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCollectorKeepsTheFinalMessageAndCountsTools(t *testing.T) {
	c := newRunCollector()
	events := []agent.Event{
		{Type: agent.EventAssistantMessageDelta, Data: agent.AssistantMessageDelta{DeltaContent: "Let me check. "}},
		{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "bash", ToolCallID: "1"}},
		{Type: agent.EventToolResult, Data: agent.ToolResult{ToolName: "bash", ToolCallID: "1", Status: "failed"}},
		{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "bash", ToolCallID: "2"}},
		{Type: agent.EventToolResult, Data: agent.ToolResult{ToolName: "bash", ToolCallID: "2", Status: "completed"}},
		{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "file_read", ToolCallID: "3"}},
		{Type: agent.EventContextCompacted, Data: agent.ContextCompacted{}},
		{Type: agent.EventAssistantMessageDelta, Data: agent.AssistantMessageDelta{DeltaContent: "All "}},
		{Type: agent.EventAssistantMessageDelta, Data: agent.AssistantMessageDelta{DeltaContent: "good."}},
	}
	for _, event := range events {
		c.observe(event)
	}

	result := c.result()

	assert.Equal(t, "All good.", result.Output)
	assert.Equal(t, 3, result.Stats.ToolCalls)
	assert.Equal(t, 1, result.Stats.FailedToolCalls)
	assert.Equal(t, 1, result.Stats.Compactions)
	assert.Equal(t, map[string]int{"bash": 2, "file_read": 1}, result.Stats.Tools)
	assert.Equal(t, []toolCount{{"bash", 2}, {"file_read", 1}}, toolCounts(result.Stats.Tools))
}

func TestRunCollectorFallsBackToAllTextWithoutAFinalMessage(t *testing.T) {
	c := newRunCollector()
	c.observe(agent.Event{Type: agent.EventAssistantMessageDelta, Data: agent.AssistantMessageDelta{DeltaContent: "Partial work"}})
	c.observe(agent.Event{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "bash"}})

	result := c.result()

	assert.Equal(t, "Partial work", result.Output)
	assert.Equal(t, 1, result.Stats.ToolCalls)
	assert.Nil(t, result.Stats.Usage)
}

func TestConversationUsageReadsTheCLI(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "kodelet")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
echo "$@" > `+argsFile+`
cat <<'EOF'
{"id":"c1","usage":{"inputTokens":10,"outputTokens":20,"cacheCreationInputTokens":30,"cacheReadInputTokens":40,"inputCost":0.1,"outputCost":0.2,"cacheCreationCost":0.3,"cacheReadCost":0.4}}
EOF
`), 0o700))

	usage, err := conversationUsage(t.Context(), script, "https://kodelet.example", "c1")

	require.NoError(t, err)
	assert.Equal(t, int64(10), usage.InputTokens)
	assert.Equal(t, int64(20), usage.OutputTokens)
	assert.Equal(t, int64(30), usage.CacheWriteTokens)
	assert.Equal(t, int64(40), usage.CacheReadTokens)
	assert.InDelta(t, 1.0, usage.Cost, 1e-9)
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	assert.Equal(t, "conversation show c1 --format json --stats-only --server https://kodelet.example\n", string(args))
}

func TestConversationUsageReportsCLIFailures(t *testing.T) {
	script := filepath.Join(t.TempDir(), "kodelet")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'Error: conversation not found' >&2\nexit 1\n"), 0o700))

	_, err := conversationUsage(t.Context(), script, "", "c1")

	assert.ErrorContains(t, err, "conversation not found")
}
