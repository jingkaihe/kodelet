package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTranscriptDetailsAndMouseToggle(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{
			{kind: blockThoughts, thoughts: []thoughtBlock{{text: "hidden thought", done: true}}},
			{kind: blockTools, tools: []toolCall{{name: "bash", input: "{\n  \"command\": \"pwd\"\n}", result: "ok", done: true}}},
		},
	}}

	m.refreshViewport(true)
	content, regions := m.renderTranscript()
	require.Len(t, regions, 2)
	assert.Contains(t, content, "Had 1 Thought")
	assert.Contains(t, content, "Ran 1 command")
	assert.NotContains(t, content, "hidden thought")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "hidden thought")

	m.toggleAllDetails()
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "$ pwd")
	assert.Contains(t, content, "ok")
}

func TestRenderTranscriptAddsSpacingBetweenAssistantBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{
			{kind: blockThoughts, thoughts: []thoughtBlock{{text: "thought", done: true}}},
			{kind: blockTools, tools: []toolCall{{name: "bash", done: true}}},
			{kind: blockText, text: "final answer"},
		},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "Had 1 Thought ▸\n\n")
	assert.Contains(t, plain, "Ran 1 command ▸\n\n")
	assert.Contains(t, plain, "\n\nfinal answer")
}

func TestRenderTranscriptUsesHeavyUserMessageBar(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 28
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{kind: entryUser, content: "please make this user message wrap"}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "┃ please make this user")
	assert.Contains(t, plain, "┃ message wrap")
	assert.NotContains(t, plain, "│ please")
}

func TestRenderTranscriptInformationEntryIsNotAChatRole(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{kind: entryInfo, title: "Drawing board saved", content: "./drawing.png\nCopy this path."}}

	content, regions := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Empty(t, regions)
	assert.Contains(t, plain, "◆ Drawing board saved")
	assert.Contains(t, plain, "  ./drawing.png")
	assert.NotContains(t, plain, "┃")
}

func TestRenderTranscriptGroupsToolBlocksByType(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 40
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{
				{name: "bash", done: true},
				{name: "bash", done: true},
				{
					name: "apply_patch",
					done: true,
					structured: &tooltypes.StructuredToolResult{
						ToolName: "apply_patch",
						Success:  true,
						Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{
							{Path: "edit.go", Operation: tooltypes.ApplyPatchOperationUpdate, UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n"},
							{Path: "new.go", Operation: tooltypes.ApplyPatchOperationAdd, NewContent: "package main\n"},
							{Path: "old.go", Operation: tooltypes.ApplyPatchOperationDelete, OldContent: "package old\n"},
						}},
					},
				},
				{
					name:  "web_fetch",
					input: `{"url":"https://example.com"}`,
					done:  true,
				},
				{name: "grep_tool", done: true},
				{name: "glob_tool", done: true},
			},
		}},
	}}

	content, regions := m.renderTranscript()

	assert.Contains(t, content, "Ran 2 commands")
	assert.Contains(t, content, "Edit edit.go")
	assert.Contains(t, content, "Write new.go")
	assert.Contains(t, content, "Delete old.go")
	assert.Contains(t, content, "Fetched https://example.com")
	assert.Contains(t, content, "Ran 2 tools")
	require.Len(t, regions, 6)
}

func TestRenderTranscriptUsesGenericExtensionPresentation(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 40
	m.resize()
	presentationTool := func(name, summary, body, output string) toolCall {
		return toolCall{
			name:   name,
			input:  `{"agent_id":"agt_123"}`,
			result: "Extension Tool: " + name + " (worker)\n\n" + output,
			done:   true,
			structured: &tooltypes.StructuredToolResult{
				ToolName: name,
				Success:  true,
				Metadata: &tooltypes.ExtensionToolMetadata{
					ToolName: name,
					Output:   output,
					Data: map[string]any{
						"presentation": map[string]any{
							"summary": summary,
							"body":    body,
							"format":  "markdown",
						},
					},
				},
			},
		}
	}
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{
				presentationTool(
					"launch_worker",
					"Spawn parser-reviewer",
					"Review the parser and tests",
					"Started run_456 for agt_123",
				),
				presentationTool(
					"inventory_workers",
					"List agents",
					"- **parser-reviewer** — completed",
					"Agent inventory including agt_123",
				),
				presentationTool(
					"send_instruction",
					"Follow up parser-reviewer",
					"Review **the parser**\nand tests",
					"Started run_456 for agt_123",
				),
				presentationTool(
					"redirect_worker",
					"Steer parser-reviewer",
					"Focus on error handling",
					"Queued update for agt_123",
				),
				presentationTool(
					"stop_worker",
					"Cancel parser-reviewer",
					"The agent is permanently canceled.",
					"Canceled agt_123",
				),
			},
		}},
	}}

	content, regions := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "Spawn parser-reviewer ▸")
	assert.Contains(t, plain, "List agents ▸")
	assert.Contains(t, plain, "Follow up parser-reviewer ▸")
	assert.Contains(t, plain, "Steer parser-reviewer ▸")
	assert.Contains(t, plain, "Cancel parser-reviewer ▸")
	assert.NotContains(t, plain, "Review the parser and tests")
	assert.NotContains(t, plain, "Focus on error handling")
	assert.NotContains(t, plain, "permanently canceled")
	assert.NotContains(t, plain, "agt_123")
	assert.NotContains(t, plain, "run_456")
	assert.NotContains(t, plain, "Ran 5 tools")
	require.Len(t, regions, 5)

	m.entries[0].blocks[0].tools[2].expanded = true
	content, _ = m.renderTranscript()
	plain = xansi.Strip(content)
	assert.Contains(t, plain, "Follow up parser-reviewer ▾")
	assert.Contains(t, plain, "Review the parser")
	assert.Contains(t, plain, "and tests")
	assert.NotContains(t, plain, "Started run_456")
	assert.NotContains(t, plain, "run_456")
	assert.NotContains(t, plain, "Extension Tool: send_instruction")
	assert.NotContains(t, plain, "input: {\"agent_id\"")

	m.entries[0].blocks[0].tools[2].expanded = false
	m.entries[0].blocks[0].tools[3].expanded = true
	content, _ = m.renderTranscript()
	plain = xansi.Strip(content)
	assert.Contains(t, plain, "Steer parser-reviewer ▾")
	assert.Contains(t, plain, "Focus on error handling")
	assert.NotContains(t, plain, "Queued update for agt_123")
	assert.NotContains(t, plain, "agt_123")
}

func TestTaskRunPresentationControlsCompactLabel(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 40
	m.resize()

	tool := toolCall{
		name: "observe_worker",
		structured: &tooltypes.StructuredToolResult{
			ToolName: "observe_worker",
			Success:  true,
			Metadata: &tooltypes.ExtensionToolMetadata{
				ToolName: "observe_worker",
				Data: map[string]any{
					"presentation": map[string]any{"summary": "Wait for parser-reviewer"},
					"taskRun": map[string]any{
						"version": 1, "revision": 1, "kind": "worker", "status": "running", "phase": "working",
						"title": "Waiting for a background worker", "detail": "reviewing parser tests", "elapsedMs": 1000,
						"counts": map[string]any{"succeeded": 1, "failed": 0, "running": 1},
						"activities": []any{map[string]any{
							"id": "activity-1", "sequence": 1, "kind": "bash", "label": "Run parser tests", "status": "running",
						}},
					},
				},
			},
		},
	}
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: []toolCall{tool}}}}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)
	assert.Contains(t, plain, "Wait for parser-reviewer… ▾")
	assert.NotContains(t, plain, "reviewing parser tests")
	assert.Contains(t, plain, "Run parser tests")

	tool.done = true
	tool.expanded = false
	metadata := tool.structured.Metadata.(*tooltypes.ExtensionToolMetadata)
	metadata.Output = "Parser review complete."
	taskRun := metadata.Data["taskRun"].(map[string]any)
	taskRun["status"] = "completed"
	taskRun["phase"] = "completed"
	taskRun["counts"].(map[string]any)["running"] = 0
	m.entries[0].blocks[0].tools[0] = tool

	content, _ = m.renderTranscript()
	plain = xansi.Strip(content)
	assert.Contains(t, plain, "Wait for parser-reviewer ▸")
	assert.NotContains(t, plain, "2 actions")
	assert.NotContains(t, plain, "Parser review complete.")
}

func TestRenderTranscriptPreservesIndentedToolOutput(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 40
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{{
				name:     "bash",
				result:   "pkg/tui/model.go:\n    func indented()\n\treturn nil",
				done:     true,
				expanded: true,
			}},
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "  pkg/tui/model.go:")
	assert.Contains(t, plain, "      func indented()")
	assert.Contains(t, plain, "      return nil")
}

func TestRenderTranscriptRendersBashGroupAsShellTranscript(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 40
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockTools,
			expanded: true,
			tools: []toolCall{
				{
					name:   "bash",
					input:  `{"command":"cd /repo && mise run build","description":"build"}`,
					done:   true,
					failed: true,
					structured: &tooltypes.StructuredToolResult{
						ToolName: "bash",
						Success:  false,
						Error:    "command is banned: cd",
					},
				},
				{
					name:  "bash",
					input: `{"command":"mise run build","description":"build"}`,
					done:  true,
					structured: &tooltypes.StructuredToolResult{
						ToolName: "bash",
						Success:  true,
						Metadata: &tooltypes.BashMetadata{
							Command:       "mise run build",
							Output:        "build ok\n",
							WorkingDir:    "/repo",
							ExecutionTime: 13903481875,
						},
					},
				},
			},
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "✗ Ran 2 commands")
	assert.Contains(t, plain, "$ cd /repo && mise run build")
	assert.Contains(t, plain, "command is banned: cd")
	assert.Contains(t, plain, "$ mise run build  ·  14s")
	assert.Contains(t, plain, "build ok")
	assert.NotContains(t, plain, "input: {")
	assert.NotContains(t, plain, "Exit Code:")
	assert.NotContains(t, plain, "Working Directory:")
	assert.NotContains(t, plain, "Execution Time:")
}

func TestBashToolBodyAdvancesElapsedTimeWhileRunning(t *testing.T) {
	observedAt := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	tool := toolCall{
		name:  "bash",
		input: `{"command":"mise run build"}`,
		structured: &tooltypes.StructuredToolResult{
			ToolName:  "bash",
			Success:   true,
			Timestamp: observedAt,
			Metadata: &tooltypes.BashMetadata{
				Command:       "mise run build",
				Output:        "compiling…\n",
				ExecutionTime: 7 * time.Second,
			},
		},
	}

	plain := xansi.Strip(bashToolBody(tool, observedAt.Add(2*time.Second), 120))

	assert.Contains(t, plain, "$ mise run build  ·  9s")
	assert.Contains(t, plain, "compiling…")
	assert.NotContains(t, plain, "(")
}

func TestBashToolBodyUsesStartTimeBeforeFirstUpdate(t *testing.T) {
	startedAt := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	tool := toolCall{name: "bash", input: `{"command":"mise run lint"}`, startedAt: startedAt}

	plain := xansi.Strip(bashToolBody(tool, startedAt.Add(2*time.Second), 120))

	assert.Contains(t, plain, "$ mise run lint  ·  2s")
}

func TestBashCommandLineUsesThemeAccentAndMutedElapsed(t *testing.T) {
	for _, themeName := range []string{DefaultThemeName, LightThemeName, "tokyo-night"} {
		t.Run(themeName, func(t *testing.T) {
			m := newModel(context.Background(), Config{Theme: themeName})
			t.Cleanup(m.cancel)

			line := bashCommandLine("mise run lint", "6s", 120)
			commandStart, _ := styleSequences(bashCommandStyle)
			elapsedStart, _ := styleSequences(mutedStyle)
			expectedCommandStart, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.theme.Markdown.Code)))
			purpleStart, _ := styleSequences(lipgloss.NewStyle().Foreground(themeColor(m.theme.Markdown.HeadingPrimary)))

			assert.Equal(t, expectedCommandStart, commandStart)
			assert.NotEqual(t, purpleStart, commandStart)
			assert.Contains(t, line, commandStart+"$ mise run lint")
			assert.Contains(t, line, elapsedStart+"  ·  6s")
		})
	}
}

func TestBashCommandAccentContinuesAcrossWrappedLines(t *testing.T) {
	m := newModel(context.Background(), Config{Theme: LightThemeName})
	t.Cleanup(m.cancel)

	command := "TMP=$(mktemp -d); gh release download 0.16.0 --repo astral-sh/ruff"
	line := bashCommandLine(command, "38s", 24)
	rendered := renderPersistentStyle(toolBodyStyle, indentText(line))
	commandStart, _ := styleSequences(bashCommandStyle)
	elapsedStart, _ := styleSequences(mutedStyle)
	commandLineCount := 0

	for _, wrappedLine := range strings.Split(rendered, "\n") {
		plain := strings.TrimSpace(xansi.Strip(wrappedLine))
		if strings.HasPrefix(plain, "·") {
			assert.Contains(t, wrappedLine, elapsedStart)
			continue
		}
		commandLineCount++
		assert.Contains(t, wrappedLine, commandStart)
	}
	require.Greater(t, commandLineCount, 1)
}

func TestDedicatedBuiltinToolLabels(t *testing.T) {
	tests := []struct {
		name string
		tool toolCall
		want string
	}{
		{
			name: "web search metadata search",
			tool: toolCall{structured: &tooltypes.StructuredToolResult{Metadata: &tooltypes.OpenAIWebSearchMetadata{Action: "search", Queries: []string{"golang tui"}}}},
			want: "Searched web for \"golang tui\"",
		},
		{
			name: "web search metadata open page",
			tool: toolCall{structured: &tooltypes.StructuredToolResult{Metadata: &tooltypes.OpenAIWebSearchMetadata{Action: "open_page", URL: "https://example.com"}}},
			want: "Opened https://example.com",
		},
		{
			name: "web search input query",
			tool: toolCall{name: "web_search", input: `{"query":"fallback"}`},
			want: "Searched web for \"fallback\"",
		},
		{
			name: "web fetch metadata",
			tool: toolCall{structured: &tooltypes.StructuredToolResult{Metadata: &tooltypes.WebFetchMetadata{URL: "https://example.com"}}},
			want: "Fetched https://example.com",
		},
		{
			name: "view image metadata",
			tool: toolCall{structured: &tooltypes.StructuredToolResult{Metadata: &tooltypes.ViewImageMetadata{Path: "/tmp/image.png"}}},
			want: "Viewed image /tmp/image.png",
		},
		{
			name: "skill metadata",
			tool: toolCall{structured: &tooltypes.StructuredToolResult{Metadata: &tooltypes.SkillMetadata{SkillName: "kodelet"}}},
			want: "Loaded skill kodelet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, _ := dedicatedBuiltinToolLabels(tt.tool)
			assert.Equal(t, tt.want, label)
		})
	}
}

func TestRenderTranscriptShowsTaskRunProgressAndFinalMarkdown(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 40
	m.resize()

	progress := toolCall{
		name: "code_search",
		structured: &tooltypes.StructuredToolResult{
			ToolName: "code_search",
			Success:  true,
			Metadata: &tooltypes.ExtensionToolMetadata{
				ToolName: "code_search",
				Data: map[string]any{"taskRun": map[string]any{
					"version":   1,
					"revision":  7,
					"kind":      "code_search",
					"status":    "running",
					"phase":     "working",
					"title":     "Searching code",
					"detail":    "2 actions running",
					"elapsedMs": 68000,
					"counts": map[string]any{
						"succeeded": 10,
						"failed":    0,
						"running":   2,
					},
					"activities": []any{
						map[string]any{"id": "1", "sequence": 1, "kind": "grep_tool", "label": "Search \"HandleToolUpdate\" in pkg/", "status": "succeeded"},
						map[string]any{"id": "2", "sequence": 2, "kind": "file_read", "label": "Read pkg/llm/base/tool_execution.go", "status": "running"},
					},
					"omittedSucceeded": 9,
				}},
			},
		},
	}
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: []toolCall{progress}}}}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)
	assert.Contains(t, plain, "⣾ Searching code - 2 actions running… ▾")
	assert.NotContains(t, plain, "⣾  Searching code - 2 actions running… ▾")
	assert.Contains(t, plain, "Searching code - 2 actions running")
	assert.Contains(t, plain, "10 done · 2 running · 1m 08s")
	assert.Contains(t, plain, "Search \"HandleToolUpdate\" in pkg/")
	assert.Contains(t, plain, "⣾ Read pkg/llm/base/tool_execution.go")
	assert.NotContains(t, plain, "⣾  Read pkg/llm/base/tool_execution.go")
	assert.Contains(t, plain, "+9 earlier completed")

	progress.done = true
	progress.expanded = true
	metadata := progress.structured.Metadata.(*tooltypes.ExtensionToolMetadata)
	metadata.Output = "## Findings\n\nThe update path starts in `tool_execution.go`."
	metadata.Data["taskRun"].(map[string]any)["status"] = "completed"
	metadata.Data["taskRun"].(map[string]any)["phase"] = "completed"
	metadata.Data["taskRun"].(map[string]any)["title"] = "Searched code"
	metadata.Data["taskRun"].(map[string]any)["detail"] = ""
	metadata.Data["taskRun"].(map[string]any)["counts"].(map[string]any)["running"] = 0
	m.entries[0].blocks[0].tools[0] = progress

	content, _ = m.renderTranscript()
	plain = xansi.Strip(content)
	assert.Contains(t, plain, "Searched code · 10 actions · 1m 08s")
	assert.Contains(t, plain, "Findings")
	assert.Contains(t, plain, "The update path starts in tool_execution.go")
}

func TestTaskRunElapsedAdvancesBetweenRunningSnapshots(t *testing.T) {
	observedAt := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	snapshot := tooltypes.TaskRunSnapshot{Status: "running", ElapsedMS: 68000}
	tool := toolCall{structured: &tooltypes.StructuredToolResult{Timestamp: observedAt}}

	assert.Equal(t, int64(70000), taskRunElapsedMS(snapshot, tool, observedAt.Add(2*time.Second)))

	tool.done = true
	assert.Equal(t, int64(68000), taskRunElapsedMS(snapshot, tool, observedAt.Add(2*time.Second)))

	tool.done = false
	snapshot.Status = "completed"
	assert.Equal(t, int64(68000), taskRunElapsedMS(snapshot, tool, observedAt.Add(3*time.Second)))
}

func TestTaskRunActivityPreviewHidesMarkdownFences(t *testing.T) {
	assert.Empty(t, taskRunActivityPreview("```"))
	assert.Empty(t, taskRunActivityPreview("```text"))
	assert.Empty(t, taskRunActivityPreview("~~~"))
	assert.Equal(t, "TypeScript tests failed", taskRunActivityPreview("```text\nTypeScript tests failed\n```"))
	assert.Equal(t, "TypeScript tests failed", taskRunActivityPreview("TypeScript tests failed"))
}

func TestActiveToolHeaderLeavesOneThirdOfTranscriptWidthEmpty(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 120
	m.height = 24
	m.resize()

	header := m.renderToolGroupHeader(toolRenderGroup{
		active:       true,
		runningLabel: "Searching code - " + strings.Repeat("find the exact implementation ", 10),
	})
	plain := xansi.Strip(header)

	assert.LessOrEqual(t, lipgloss.Width(plain), m.transcriptTextWidth()*2/3)
	assert.Contains(t, plain, "Searching code - find the exact implementation")
	assert.True(t, strings.HasSuffix(plain, "… ▾"))
}

func TestRenderTranscriptShowsFailedTaskRun(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 40
	m.resize()

	failed := toolCall{
		name:   "code_search",
		done:   true,
		failed: true,
		result: "code_search timed out while waiting for kodelet to finish",
		structured: &tooltypes.StructuredToolResult{
			ToolName: "code_search",
			Success:  false,
			Error:    "code_search timed out while waiting for kodelet to finish",
			Metadata: &tooltypes.ExtensionToolMetadata{
				ToolName: "code_search",
				Output:   "code_search timed out while waiting for kodelet to finish",
				Data: map[string]any{"taskRun": map[string]any{
					"version":   1,
					"revision":  2,
					"kind":      "code_search",
					"status":    "failed",
					"phase":     "failed",
					"title":     "Code search failed",
					"detail":    "failed",
					"elapsedMs": 1200,
					"counts": map[string]any{
						"succeeded": 0,
						"failed":    0,
						"running":   0,
					},
					"activities": []any{},
				}},
			},
		},
	}
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: []toolCall{failed}}}}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)
	assert.Contains(t, plain, "✗ Code search failed · 1s")
	assert.Contains(t, plain, "code_search timed out while waiting for kodelet to finish")
}

func TestRenderTranscriptShowsDistinctFailedTaskRunErrorAndOutput(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 40
	m.resize()

	failed := toolCall{
		name: "subagent", done: true, failed: true,
		structured: &tooltypes.StructuredToolResult{
			ToolName: "subagent", Success: false, Error: "subagent exited unexpectedly",
			Metadata: &tooltypes.ExtensionToolMetadata{
				ToolName: "subagent", Output: "Partial findings were preserved.",
				Data: map[string]any{"taskRun": map[string]any{
					"version": 1, "revision": 2, "kind": "subagent", "status": "failed", "phase": "failed", "title": "Delegated task failed", "detail": "failed", "elapsedMs": 1200,
					"counts": map[string]any{"succeeded": 1, "failed": 1, "running": 0}, "activities": []any{},
				}},
			},
		},
	}
	m.entries = []chatEntry{{kind: entryAssistant, blocks: []assistantBlock{{kind: blockTools, tools: []toolCall{failed}}}}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)
	assert.Contains(t, plain, "subagent exited unexpectedly")
	assert.Contains(t, plain, "Partial findings were preserved.")
}

func TestApplyPatchGroupsRenderMoveLabelsCountsAndLineGutter(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	patchTool := toolCall{
		name: "apply_patch",
		done: true,
		structured: &tooltypes.StructuredToolResult{
			ToolName: "apply_patch",
			Success:  true,
			Metadata: &tooltypes.ApplyPatchMetadata{
				Changes: []tooltypes.ApplyPatchChange{{
					Path:        "old.go",
					MovePath:    "new.go",
					Operation:   tooltypes.ApplyPatchOperationUpdate,
					UnifiedDiff: "--- old.go\n+++ new.go\n@@ -1,2 +1,2 @@\n old context\n-old\n+new\n",
				}},
			},
		},
	}

	groups := m.buildApplyPatchToolGroups(assistantBlock{tools: []toolCall{patchTool}}, 0)
	require.Len(t, groups, 1)
	assert.Equal(t, "Move old.go → new.go (+1 -1)", groups[0].label)
	body := xansi.Strip(renderDiffRenderedLines(groups[0].bodyLines))
	assert.Contains(t, body, "1 1 │  old context")
	assert.Contains(t, body, "2   │ -old")
	assert.Contains(t, body, "  2 │ +new")
}

func TestApplyPatchHeaderStylesCountsWithThemeDiffColors(t *testing.T) {
	for _, themeName := range []string{DefaultThemeName, LightThemeName, "tokyo-night"} {
		t.Run(themeName, func(t *testing.T) {
			m := newModel(context.Background(), Config{Theme: themeName})
			t.Cleanup(m.cancel)
			m.width = 80
			m.height = 24
			m.resize()

			patchTool := toolCall{
				name: "apply_patch",
				done: true,
				structured: &tooltypes.StructuredToolResult{
					ToolName: "apply_patch",
					Success:  true,
					Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{{
						Path:        "new.go",
						Operation:   tooltypes.ApplyPatchOperationAdd,
						UnifiedDiff: "--- /dev/null\n+++ new.go\n@@ -0,0 +1,1 @@\n+package main\n",
					}}},
				},
			}

			groups := m.buildApplyPatchToolGroups(assistantBlock{tools: []toolCall{patchTool}}, 0)
			require.Len(t, groups, 1)

			header := m.renderToolGroupHeader(groups[0])
			plain := xansi.Strip(header)
			addedStart, _ := styleSequences(diffAddedStyle)
			removedStart, _ := styleSequences(diffRemovedStyle)

			assert.Contains(t, plain, "Write new.go (+1 -0) ▸")
			assert.Contains(t, header, addedStart+"+1")
			assert.Contains(t, header, removedStart+"-0")
		})
	}
}

func TestApplyPatchGroupsRenderPartialDiffAndErrorOnFailure(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	patchTool := toolCall{
		name:   "apply_patch",
		done:   true,
		failed: true,
		result: "Patch failed (+1 -1):\nEdit edit.go (+1 -1)\n@@ -1 +1 @@\n-old\n+new\n\nError: could not apply hunk",
		structured: &tooltypes.StructuredToolResult{
			ToolName: "apply_patch",
			Success:  false,
			Error:    "could not apply hunk",
			Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{{
				Path:        "edit.go",
				Operation:   tooltypes.ApplyPatchOperationUpdate,
				UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
			}}},
		},
	}

	groups := m.buildApplyPatchToolGroups(assistantBlock{tools: []toolCall{patchTool}}, 0)
	require.Len(t, groups, 1)
	assert.Equal(t, "Edit edit.go (+1 -1)", groups[0].label)
	body := xansi.Strip(renderDiffRenderedLines(groups[0].bodyLines))
	assert.Contains(t, body, "1   │ -old")
	assert.Contains(t, body, "  1 │ +new")
	assert.Contains(t, body, "could not apply hunk")
	assert.NotContains(t, body, "Patch failed (+1 -1):")
}

func TestRenderTranscriptShowsQueuedSteeringErrorOnEmptyTranscript(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.steerError = "could not queue"

	content, regions := m.renderTranscript()

	assert.Empty(t, regions)
	assert.Contains(t, content, "Hello! What would you like me to work on?")
	assert.Contains(t, content, "could not queue")
}

func TestRenderTranscriptApplyPatchDiffToggle(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{{
				name: "apply_patch",
				done: true,
				structured: &tooltypes.StructuredToolResult{
					ToolName: "apply_patch",
					Success:  true,
					Metadata: &tooltypes.ApplyPatchMetadata{Changes: []tooltypes.ApplyPatchChange{{
						Path:        "edit.go",
						Operation:   tooltypes.ApplyPatchOperationUpdate,
						UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
					}}},
				},
			}},
		}},
	}}

	m.refreshViewport(true)
	content, regions := m.renderTranscript()
	require.Len(t, regions, 1)
	m.detailRegions = regions
	assert.Contains(t, content, "Edit edit.go")
	assert.NotContains(t, content, "@@ -1 +1 @@")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "@@ -1 +1 @@")
	assert.Contains(t, content, "-old")
	assert.Contains(t, content, "+new")
}

func TestRenderTranscriptFileEditUsesDiffHeaderAndAbsoluteLineGutter(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{{
				name: "file_edit",
				done: true,
				structured: &tooltypes.StructuredToolResult{
					ToolName: "file_edit",
					Success:  true,
					Metadata: &tooltypes.FileEditMetadata{
						FilePath:    "edit.go",
						UnifiedDiff: "@@ -42,3 +42,3 @@\n before\n-old\n+new\n after\n",
						Edits: []tooltypes.Edit{{
							StartLine:  42,
							EndLine:    44,
							OldContent: "before\nold\nafter",
							NewContent: "before\nnew\nafter",
						}},
					},
				},
			}},
		}},
	}}

	m.refreshViewport(true)
	content, regions := m.renderTranscript()
	plain := xansi.Strip(content)
	require.Len(t, regions, 1)
	assert.Contains(t, plain, "Edit edit.go (+1 -1)")
	assert.NotContains(t, plain, "Ran 1 tool")
	assert.NotContains(t, plain, "@@ -42,3 +42,3 @@")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	plain = xansi.Strip(content)
	assert.Contains(t, plain, "@@ -42,3 +42,3 @@")
	assert.Contains(t, plain, "42 42 │  before")
	assert.Contains(t, plain, "43    │ -old")
	assert.Contains(t, plain, "   43 │ +new")
	assert.Contains(t, plain, "44 44 │  after")
}

func TestRenderTranscriptFailedFileEditCanBeCollapsed(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockTools,
			tools: []toolCall{{
				name:   "file_edit",
				done:   true,
				failed: true,
				structured: &tooltypes.StructuredToolResult{
					ToolName: "file_edit",
					Success:  false,
					Error:    "failed to write file",
					Metadata: &tooltypes.FileEditMetadata{
						FilePath:    "edit.go",
						UnifiedDiff: "@@ -7 +7 @@\n-old\n+new\n",
						Edits: []tooltypes.Edit{{
							StartLine:  7,
							EndLine:    7,
							OldContent: "old",
							NewContent: "new",
						}},
					},
				},
			}},
		}},
	}}

	m.refreshViewport(true)
	content, regions := m.renderTranscript()
	require.Len(t, regions, 1)
	assert.NotContains(t, xansi.Strip(content), "failed to write file")

	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.Contains(t, xansi.Strip(content), "failed to write file")

	m.refreshViewport(true)
	_, regions = m.renderTranscript()
	require.Len(t, regions, 1)
	assert.True(t, m.toggleDetailAt(regions[0].line))
	content, _ = m.renderTranscript()
	assert.NotContains(t, xansi.Strip(content), "failed to write file")
}

func TestRenderTranscriptRendersAssistantMarkdown(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockText,
			text: "Here is `code`:\n\n- first\n- second",
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "Here is")
	assert.Contains(t, plain, "code")
	assert.Contains(t, plain, "• first")
	assert.Contains(t, plain, "• second")
}

func TestRenderTranscriptRendersTokyoNightCodeBlock(t *testing.T) {
	m := newModel(context.Background(), Config{Theme: "tokyo-night"})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockText,
			text: "Here is code:\n\n```go\nfmt.Println(len(items))\n```",
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, plain, "Here is code:")
	assert.Contains(t, plain, "fmt.Println")
	assert.NotContains(t, plain, "```")
}

func TestRenderTranscriptRestylesAssistantTextAfterInlineCode(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 160
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind: blockText,
			text: "before `styles.go` after",
		}},
	}}

	m.refreshViewport(true)
	content := m.viewport.View()
	plain := xansi.Strip(content)
	start, _ := styleSequences(assistantStyle)

	assert.Contains(t, plain, "before styles.go after")
	assert.NotContains(t, plain, "before  styles.go  after")
	assert.Contains(t, content, ansiResetSequence+start+" after")
}

func TestRenderTranscriptRestylesThoughtTextAfterInlineCode(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockThoughts,
			expanded: true,
			thoughts: []thoughtBlock{{text: "before `styles.go` after", done: true}},
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)
	start, _ := styleSequences(thoughtBodyStyle)

	assert.Contains(t, plain, "before styles.go after")
	assert.Contains(t, content, ansiResetSequence+start+" after")
}

func TestRenderPersistentStyleRestylesAfterForegroundReset(t *testing.T) {
	rendered := renderPersistentStyle(assistantStyle, "before \x1b[38;5;151mcode\x1b[39m after")
	start, _ := styleSequences(assistantStyle)

	assert.Contains(t, rendered, "\x1b[38;5;151mcode\x1b[39m"+start+" after")
}

func TestRenderPersistentStyleRestylesEachRenderedLine(t *testing.T) {
	rendered := renderPersistentStyle(assistantStyle, "first\nsecond \x1b[38;5;151mcode\x1b[0m after")
	start, _ := styleSequences(assistantStyle)

	assert.Contains(t, rendered, "\n"+start+"second \x1b[38;5;151mcode\x1b[0m"+start+" after")
}

func TestRenderTranscriptSeparatesThinkingMarkdownBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockThoughts,
			expanded: true,
			thoughts: []thoughtBlock{
				{text: "First thought"},
				{text: "Second thought"},
			},
		}},
	}}

	content, _ := m.renderTranscript()
	plain := xansi.Strip(content)

	assert.Contains(t, joinThoughts(m.entries[0].blocks[0].thoughts), "First thought\n\nSecond thought")
	assert.Contains(t, plain, "First thought")
	assert.Regexp(t, `First thought\s*\n\s*\n\s*Second thought`, plain)
}
