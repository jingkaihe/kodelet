package main

import (
	"testing"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/stretchr/testify/assert"
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
}
