package tui

import (
	"encoding/json"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeExecutionParentCard(t *testing.T) {
	m := newModel(t.Context(), Config{})
	t.Cleanup(m.cancel)
	result := tooltypes.StructuredToolResult{ToolName: "code_execute", Success: true, Metadata: tooltypes.CodeExecutionMetadata{
		Status: "completed", Outputs: []json.RawMessage{json.RawMessage(`"Selected output"`)},
		Calls: []tooltypes.CodeExecutionCall{{ToolName: "search", Status: "completed"}, {ToolName: "write", Status: "blocked", ErrorKind: "blocked"}},
	}}
	block := assistantBlock{tools: []toolCall{
		{name: "other", done: true},
		{name: "code_execute", input: `{"code":"private source"}`, done: true, structured: &result},
	}}
	groups := m.toolRenderGroups(block)
	require.Len(t, groups, 2, "code execution must not be folded into adjacent tools")
	assert.Contains(t, groups[1].label, "1 succeeded · 1 failed")
	assert.Contains(t, groups[1].body, "Selected output")
	assert.NotContains(t, groups[1].body, "private source")
	assert.False(t, groups[1].active)
	block.tools[1].done = false
	assert.True(t, m.toolRenderGroups(block)[1].active)
}
