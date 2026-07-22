package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/muesli/termenv"
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
			{kind: blockTools, tools: []toolCall{{name: "bash", input: "{\n  \"cmd\": \"pwd\"\n}", result: "ok", done: true}}},
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
	assert.Contains(t, content, "input: {\"cmd\":\"pwd\"}")
	assert.Contains(t, content, "result: ok")
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

	assert.Contains(t, content, "Had 1 Thought ▸\n\n")
	assert.Contains(t, content, "Ran 1 command ▸\n\n")
	assert.Contains(t, content, "\n\nfinal answer")
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

	assert.Contains(t, plain, "  result: pkg/tui/model.go:")
	assert.Contains(t, plain, "      func indented()")
	assert.Contains(t, plain, "      return nil")
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
	withANSI256ColorProfile(t)

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
	withANSI256ColorProfile(t)

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
	withANSI256ColorProfile(t)
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
	withANSI256ColorProfile(t)

	rendered := renderPersistentStyle(assistantStyle, "before \x1b[38;5;151mcode\x1b[39m after")
	start, _ := styleSequences(assistantStyle)

	assert.Contains(t, rendered, "\x1b[38;5;151mcode\x1b[39m"+start+" after")
}

func TestRenderPersistentStyleRestylesEachRenderedLine(t *testing.T) {
	withANSI256ColorProfile(t)

	rendered := renderPersistentStyle(assistantStyle, "first\nsecond \x1b[38;5;151mcode\x1b[0m after")
	start, _ := styleSequences(assistantStyle)

	assert.Contains(t, rendered, "\n"+start+"second \x1b[38;5;151mcode\x1b[0m"+start+" after")
}

func withANSI256ColorProfile(t *testing.T) {
	t.Helper()
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previous)
	})
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
