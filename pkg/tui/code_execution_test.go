package tui

import (
	"encoding/json"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/conversations"
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
	assert.Empty(t, groups[1].body)
	assert.False(t, groups[1].active)
	block.tools[1].expanded = true
	groups = m.toolRenderGroups(block)
	require.Len(t, groups, 5)
	assert.Equal(t, "Code", groups[2].label)
	assert.Contains(t, groups[2].body, "Selected output")
	assert.Contains(t, groups[2].body, "private source")
	assert.Contains(t, groups[3].body, "not saved")
	assert.Equal(t, "write · blocked", groups[4].label)
	assert.True(t, groups[4].failed)
	result.Metadata.(tooltypes.CodeExecutionMetadata).Calls[0].DetailsOmitted = true
	assert.Contains(t, m.toolRenderGroups(block)[3].body, "exceeded the storage limit")
	block.tools[1].done = false
	groups = m.toolRenderGroups(block)
	assert.True(t, groups[1].active)
	assert.NotContains(t, groups[2].body, "Selected output")
	assert.Contains(t, groups[3].body, "when code execution finishes")
	block.tools[1].structured = nil
	groups = m.toolRenderGroups(block)
	require.Len(t, groups, 3)
	assert.Contains(t, groups[2].body, "private source", "show code before the first progress snapshot")
	block.tools[1].expandedCode = map[string]bool{"": false}
	groups = m.toolRenderGroups(block)
	require.Len(t, groups, 2, "running parents can still be collapsed")
	assert.Contains(t, m.renderToolGroupHeader(groups[1]), "▸")
}

func TestCodeExecutionNestedFoldsFromHistory(t *testing.T) {
	m := newModel(t.Context(), Config{})
	t.Cleanup(m.cancel)
	m.width, m.height = 100, 100
	m.resize()
	bash := tooltypes.StructuredToolResult{ToolName: "bash", Success: true, Metadata: tooltypes.BashMetadata{
		Command: "echo nested", Output: "nested command output",
	}}
	patch := tooltypes.StructuredToolResult{ToolName: "apply_patch", Success: true, Metadata: tooltypes.ApplyPatchMetadata{
		Changes: []tooltypes.ApplyPatchChange{
			{Path: "first.go", Operation: "update", UnifiedDiff: "@@ -1 +1 @@\n-old-first\n+new-first\n"},
			{Path: "second.go", Operation: "update", UnifiedDiff: "@@ -1 +1 @@\n-old-second\n+new-second\n"},
		},
	}}
	result := tooltypes.StructuredToolResult{ToolName: "code_execute", Success: true, Metadata: tooltypes.CodeExecutionMetadata{
		Status: "completed", Items: []tooltypes.CodeExecutionOutput{{Type: "json", Value: json.RawMessage(`"script output"`)}},
		Calls: []tooltypes.CodeExecutionCall{
			{CallID: "one", ToolName: "bash", Status: "completed", Result: &bash},
			{CallID: "two", ToolName: "bash", Status: "unknown", Result: &bash},
			{CallID: "patch", ToolName: "apply_patch", Status: "completed", Result: &patch},
			{CallID: "three", ToolName: "bash", Status: "completed", Result: &bash},
		},
	}}
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	m.entries = entriesFromHistory([]conversations.StreamableMessage{
		{Kind: "tool-use", ToolCallID: "parent", ToolName: "code_execute", Input: `{"code":"const value = 42;\nreturn value;"}`},
		{Kind: "tool-result", ToolCallID: "parent", Content: string(payload)},
	})
	render := func() string {
		m.refreshViewport(false)
		content, _ := m.renderTranscript()
		return xansi.Strip(content)
	}
	toggle := func(key string) {
		t.Helper()
		render()
		for _, region := range m.detailRegions {
			if region.codeKey == key {
				m.viewport.SetYOffset(max(0, region.line-2))
				require.True(t, m.toggleDetailAt(region.line-m.viewport.YOffset()))
				return
			}
		}
		t.Fatalf("missing fold %q", key)
	}
	assert.NotContains(t, render(), "Ran 2 commands")
	toggle("")
	content := render()
	assert.Contains(t, content, "  ✓ Code ▸")
	assert.Contains(t, content, "  ✗ Ran 2 commands ▸", "host failure status wins over the child result")
	assert.Contains(t, content, "first.go")
	assert.Contains(t, content, "second.go")
	assert.NotContains(t, content, "Applied patch")
	assert.NotContains(t, content, "script output")
	assert.NotContains(t, content, "nested command output")
	toggle("code")
	assert.Contains(t, render(), "const value = 42;")
	assert.Contains(t, render(), "script output")
	toggle("one:0:-1")
	assert.Equal(t, 2, strings.Count(render(), "nested command output"))
	toggle("patch:2:0")
	assert.Contains(t, render(), "new-first")
	assert.NotContains(t, render(), "new-second")
	toggle("")
	assert.NotContains(t, render(), "script output")
	toggle("")
	assert.Contains(t, render(), "new-first", "closing the parent preserves child fold state")
	m.toggleAllDetails()
	assert.Contains(t, render(), "new-second")
	toggle("patch:2:1")
	assert.NotContains(t, render(), "new-second", "individual folds work after expand-all")
	m.toggleAllDetails()
	assert.NotContains(t, render(), "Ran 2 commands")
}
