package main

import (
	"sort"
	"strings"
	"sync"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
)

// RunStats summarizes what a run did. It is stored as JSON, so fields can be
// added without a migration.
type RunStats struct {
	ToolCalls       int            `json:"toolCalls" jsonschema:"Number of tool calls"`
	FailedToolCalls int            `json:"failedToolCalls" jsonschema:"Number of tool calls that failed"`
	Tools           map[string]int `json:"tools,omitempty" jsonschema:"Tool calls by tool name"`
	Compactions     int            `json:"compactions" jsonschema:"Number of context compactions"`
}

// runResult is what an executor reports about a run, even a failed one.
type runResult struct {
	// Output is the agent's final message: its text after the last tool call.
	Output string
	Stats  RunStats
}

// runCollector derives a runResult from session events as they arrive.
type runCollector struct {
	mu       sync.Mutex
	stats    RunStats
	all      strings.Builder
	final    strings.Builder
	afterUse bool
}

func newRunCollector() *runCollector {
	return &runCollector{stats: RunStats{Tools: map[string]int{}}}
}

func (c *runCollector) observe(event agent.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.Type {
	case agent.EventAssistantMessageDelta:
		delta, ok := event.Data.(agent.AssistantMessageDelta)
		if !ok {
			return
		}
		if c.afterUse {
			c.final.Reset()
			c.afterUse = false
		}
		c.all.WriteString(delta.DeltaContent)
		c.final.WriteString(delta.DeltaContent)
	case agent.EventToolCall:
		c.afterUse = true
		c.stats.ToolCalls++
		if call, ok := event.Data.(agent.ToolCall); ok && call.ToolName != "" {
			c.stats.Tools[call.ToolName]++
		}
	case agent.EventToolResult:
		c.afterUse = true
		if result, ok := event.Data.(agent.ToolResult); ok && result.Status == "failed" {
			c.stats.FailedToolCalls++
		}
	case agent.EventContextCompacted:
		c.stats.Compactions++
	}
}

func (c *runCollector) result() runResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := c.stats
	stats.Tools = nil
	if len(c.stats.Tools) > 0 {
		stats.Tools = make(map[string]int, len(c.stats.Tools))
		for name, calls := range c.stats.Tools {
			stats.Tools[name] = calls
		}
	}
	output := strings.TrimSpace(c.final.String())
	if output == "" {
		output = strings.TrimSpace(c.all.String())
	}
	return runResult{Output: output, Stats: stats}
}

type toolCount struct {
	Name  string
	Calls int
}

// toolCounts returns tool names with their call counts, most used first.
func toolCounts(tools map[string]int) []toolCount {
	counts := make([]toolCount, 0, len(tools))
	for name, calls := range tools {
		counts = append(counts, toolCount{Name: name, Calls: calls})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Calls != counts[j].Calls {
			return counts[i].Calls > counts[j].Calls
		}
		return counts[i].Name < counts[j].Name
	})
	return counts
}
