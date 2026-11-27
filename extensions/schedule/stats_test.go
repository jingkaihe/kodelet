package main

import (
	"testing"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/stretchr/testify/assert"
)

func TestRunCollectorKeepsTheFinalMessageAndCountsTools(t *testing.T) {
	c := newRunCollector()
	delta := func(text string) {
		c.observe(agent.Event{Type: agent.EventAssistantMessageDelta, Data: agent.AssistantMessageDelta{DeltaContent: text}})
	}
	delta("Let me check. ")
	for _, event := range []agent.Event{
		{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "bash"}},
		{Type: agent.EventToolResult, Data: agent.ToolResult{Status: "failed"}},
		{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "bash"}},
		{Type: agent.EventToolResult, Data: agent.ToolResult{Status: "completed"}},
		{Type: agent.EventToolCall, Data: agent.ToolCall{ToolName: "file_read"}},
		{Type: agent.EventContextCompacted},
	} {
		c.observe(event)
	}
	delta(" ")
	assert.Equal(t, "Let me check.", c.result().Output, "no final message yet: fall back to earlier text")

	delta("All ")
	delta("good.")
	result := c.result()
	assert.Equal(t, "All good.", result.Output)
	assert.Equal(t, RunStats{ToolCalls: 3, FailedToolCalls: 1, Compactions: 1, Tools: map[string]int{"bash": 2, "file_read": 1}}, result.Stats)
}
