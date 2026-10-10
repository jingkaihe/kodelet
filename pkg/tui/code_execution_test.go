package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/chat"
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
	result.Metadata.(tooltypes.CodeExecutionMetadata).Calls[0].DetailsOmitted = false
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
			{CallID: "one", ToolName: "bash", Status: "completed", Input: json.RawMessage(`{"description":"Run first command"}`), Result: &bash},
			{CallID: "two", ToolName: "bash", Status: "unknown", Input: json.RawMessage(`{"description":"Run second command"}`), Result: &bash},
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
	assert.NotContains(t, render(), "Run first command")
	toggle("")
	content := render()
	assert.Contains(t, content, "  ✓ Code ▸")
	assert.Contains(t, content, "  ✓ bash · Run first command ▸")
	assert.Contains(t, content, "  ✗ bash · Run second command ▸", "host failure status wins over the child result")
	assert.Contains(t, content, "first.go")
	assert.Contains(t, content, "second.go")
	assert.NotContains(t, content, "Applied patch")
	assert.NotContains(t, content, "script output")
	assert.NotContains(t, content, "nested command output")
	toggle("code")
	assert.Contains(t, render(), "const value = 42;")
	assert.Contains(t, render(), "script output")
	toggle("one:0:-1")
	assert.Equal(t, 1, strings.Count(render(), "nested command output"), "commands expand independently")
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
	assert.NotContains(t, render(), "Run first command")
}

func TestCodeExecutionLiveChildFolds(t *testing.T) {
	m := newModel(t.Context(), Config{})
	t.Cleanup(m.cancel)
	m.width, m.height = 100, 100
	m.resize()
	m.running = true
	m.applyChatEvent(chat.ChatEvent{Kind: "tool-use", ToolCallID: "parent", ToolName: "code_execute"})
	observedAt := time.Now().Add(time.Hour)
	first := tooltypes.CodeExecutionCall{
		CallID: "first", ToolName: "bash", Status: "running",
		Input: json.RawMessage(`{"command":"mise run test","description":"Run focused tests"}`),
	}
	second := tooltypes.CodeExecutionCall{
		CallID: "second", ToolName: "stream_tool", Status: "running",
		Result: &tooltypes.StructuredToolResult{
			ToolName: "stream_tool", Success: true,
			Metadata: tooltypes.ExtensionToolMetadata{Data: map[string]any{
				"presentation": map[string]any{
					"summary": "Streaming extension", "body": "extension in progress", "format": "markdown",
				},
			}},
		},
	}
	update := func(kind string) {
		m.applyChatEvent(chat.ChatEvent{
			Kind: kind, ToolCallID: "parent",
			ToolResult: &tooltypes.StructuredToolResult{
				ToolName: "code_execute", Success: true,
				Metadata: tooltypes.CodeExecutionMetadata{Calls: []tooltypes.CodeExecutionCall{first, second}},
			},
		})
	}
	render := func() string {
		m.refreshViewport(false)
		return xansi.Strip(m.View().Content)
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
	update("tool-update")
	content := render()
	assert.Contains(t, content, "bash · Run focused tests… ▸", "description is available before output")
	assert.Contains(t, content, "Streaming extension… ▸")
	assert.NotContains(t, content, "$ mise run test")
	assert.NotContains(t, content, "extension in progress")
	toggle("first:0:-1")
	assert.Contains(t, render(), "$ mise run test")
	first.Result = &tooltypes.StructuredToolResult{
		ToolName: "bash", Success: true, Timestamp: observedAt,
		Metadata: tooltypes.BashMetadata{Command: "mise run test", Output: "first test passed", ExecutionTime: 7 * time.Second},
	}
	update("tool-update")
	content = render()
	assert.Contains(t, content, "first test passed")
	assert.NotContains(t, content, "extension in progress", "parallel siblings remain folded")
	assert.Contains(t, content, "$ mise run test  ·  7s")
	assert.NotContains(t, content, transcriptElapsedPlaceholderSuffix)
	first.Result.Timestamp = time.Now().Add(-2 * time.Second)
	assert.Contains(t, xansi.Strip(m.View().Content), "$ mise run test  ·  9s", "nested elapsed time updates without rebuilding the transcript")
	first.Result.Timestamp = observedAt
	first.Result.Metadata = tooltypes.BashMetadata{Command: "mise run test", Output: "all tests passed", ExecutionTime: 8 * time.Second}
	first.Status = "completed"
	update("tool-update")
	content = render()
	assert.Contains(t, content, "✓ bash · Run focused tests ▾")
	assert.Contains(t, content, "all tests passed", "completed child details are available before the parent finishes")
	assert.NotContains(t, content, "first test passed", "snapshots replace rather than append output")
	toggle("first:0:-1")
	assert.NotContains(t, render(), "all tests passed")
	toggle("second:1:-1")
	assert.Contains(t, render(), "extension in progress")
	// Explicitly keep the parent open after completion, rather than relying on
	// its running-only auto-expansion. Child folds survive this toggle too.
	toggle("")
	assert.NotContains(t, render(), "extension in progress")
	toggle("")
	second.Status = "completed"
	update("tool-result")
	assert.Contains(t, render(), "✓ Streaming extension ▾", "final results preserve child fold choices")
	assert.NotContains(t, render(), "all tests passed")
}
