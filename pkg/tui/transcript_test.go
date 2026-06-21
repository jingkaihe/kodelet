package tui

import (
	"context"
	"testing"

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

func TestApplyPatchGroupsHandleFallbackMetadataAndMoveLabels(t *testing.T) {
	metadataOnly := toolCall{
		name: "apply_patch",
		done: true,
		structured: &tooltypes.StructuredToolResult{
			ToolName: "apply_patch",
			Success:  true,
			Metadata: &tooltypes.ApplyPatchMetadata{
				Added:    []string{"added.go"},
				Modified: []string{"edited.go"},
				Deleted:  []string{"deleted.go"},
			},
		},
	}

	groups := buildApplyPatchToolGroups(assistantBlock{tools: []toolCall{metadataOnly}}, 0)
	require.Len(t, groups, 3)
	assert.Equal(t, "Write added.go", groups[0].label)
	assert.Equal(t, "Edit edited.go", groups[1].label)
	assert.Equal(t, "Delete deleted.go", groups[2].label)

	assert.Equal(t, "Move old.go -> new.go", applyPatchChangeLabel(tooltypes.ApplyPatchChange{Path: "old.go", MovePath: "new.go"}))
	assert.Contains(t, applyPatchChangeDiff(tooltypes.ApplyPatchChange{
		Path:       "old.go",
		MovePath:   "new.go",
		Operation:  "move",
		OldContent: "old\n",
		NewContent: "new\n",
	}), "new.go")
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

func TestRenderDiffLineUsesAdditionAndRemovalStyles(t *testing.T) {
	assert.Equal(t, diffAddedStyle.Render("  +new"), renderDiffLine("  +new"))
	assert.Equal(t, diffRemovedStyle.Render("  -old"), renderDiffLine("  -old"))
	assert.Equal(t, toolBodyStyle.Render("  +++ b/file.go"), renderDiffLine("  +++ b/file.go"))
	assert.Equal(t, toolBodyStyle.Render("  --- a/file.go"), renderDiffLine("  --- a/file.go"))
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
