package codemode

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummary(t *testing.T) {
	for _, tt := range []struct {
		name       string
		definition Definition
		want       string
	}{
		{name: "explicit short wins", definition: Definition{Short: " Run\na  command. ", Description: "Ignored."}, want: "Run a command."},
		{name: "first sentence", definition: Definition{Description: "Fetch a URL. Then more."}, want: "Fetch a URL."},
		{name: "stops at heading", definition: Definition{Description: "Run a shell command\n# Restrictions\nBanned: cd"}, want: "Run a shell command"},
		{name: "stops at paragraph", definition: Definition{Description: "Search code\nusing regex\n\n## Notes"}, want: "Search code using regex"},
		{name: "skips leading heading", definition: Definition{Description: "# Tool discovery\n\nSearches tools. More."}, want: "Searches tools."},
		{name: "keeps abbreviations", definition: Definition{Description: "Use e.g. globs here. Next."}, want: "Use e.g. globs here."},
		{name: "strips bullets", definition: Definition{Description: "- Lists agents."}, want: "Lists agents."},
		{name: "control characters", definition: Definition{Short: "a\x00b\tc"}, want: "a b c"},
		{name: "empty", definition: Definition{}, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Summary(tt.definition))
		})
	}
	long := Summary(Definition{Description: strings.Repeat("word ", 60)})
	assert.LessOrEqual(t, utf8.RuneCountInString(long), summaryMaxRunes)
	assert.True(t, strings.HasSuffix(long, "word…"), "long summaries are cut at a word boundary")
}

func TestSignature(t *testing.T) {
	definition := Definition{
		Name: "spawn_agent",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"name", "task"},
			"properties": map[string]any{
				"task":         map[string]any{"type": "string"},
				"name":         map[string]any{"type": "string"},
				"context_mode": map[string]any{"enum": []any{"fork", "fresh"}, "default": "fork"},
				"cwd": map[string]any{"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "null"},
				}, "default": nil},
				"timeout_ms": map[string]any{"type": "integer", "default": 30000},
				"labels":     map[string]any{"type": "array", "items": map[string]any{"type": []any{"string", "number"}}},
				"options": map[string]any{"type": "object", "properties": map[string]any{
					"depth": map[string]any{"type": "number"},
				}, "required": []any{"depth"}},
			},
		},
		OutputSchema: map[string]any{
			"type":       "object",
			"required":   []string{"id"},
			"properties": map[string]any{"id": map[string]any{}, "status": map[string]any{}},
		},
	}
	assert.Equal(t,
		`spawn_agent({ name: string; task: string; context_mode?: "fork" | "fresh" = "fork"; cwd?: string | null; `+
			`labels?: (string | number)[]; options?: { depth: number }; timeout_ms?: number = 30000 }) → data: { id, status? }`,
		Signature(definition))

	assert.Equal(t, `"git-hub/x"({ "a-b"?: string })`, Signature(Definition{
		Name:        "git-hub/x",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"a-b": map[string]any{"type": "string"}}},
	}), "names that are not identifiers are quoted")
	assert.Equal(t, "noop({})", Signature(Definition{Name: "noop"}))
	assert.Equal(t, "noop({})", Signature(Definition{Name: "noop", InputSchema: map[string]any{"type": "object"}}))
	assert.Equal(t, "raw(unknown)", Signature(Definition{Name: "raw", InputSchema: map[string]any{"$ref": "#/x"}}))

	wide := map[string]any{}
	for i := range 20 {
		wide[fmt.Sprintf("field_%02d", i)] = map[string]any{"enum": []any{strings.Repeat("v", 30), strings.Repeat("w", 30)}}
	}
	untyped := Signature(Definition{Name: "wide", InputSchema: map[string]any{"type": "object", "properties": wide}})
	assert.True(t, strings.HasPrefix(untyped, "wide({ field_00?, field_01?"), "oversized signatures drop types first: %s", untyped)
	assert.Contains(t, untyped, ", …")
	assert.LessOrEqual(t, utf8.RuneCountInString(untyped), signatureMaxRunes)
}

func TestToolIndex(t *testing.T) {
	assert.Equal(t, "No other tools are callable in this turn.", ToolIndex(nil))

	definitions := []Definition{
		{Name: "wait_agent", Group: "extension/agents", Description: "Wait for an agent."},
		{Name: "bash", Short: "Run a command.", InputSchema: map[string]any{
			"type": "object", "required": []any{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string"}},
		}},
		{Name: "spawn_agent", Group: "extension/agents", Description: "Start an agent."},
		{Name: "bash", Description: "duplicate ignored"},
		{Name: "lookup", Group: "mcp/data", Description: "Look up data."},
	}
	index := ToolIndex(definitions)
	assert.Equal(t, toolIndexHeader+`
Built-in:
  bash({ command: string }) — Run a command.
extension/agents:
  spawn_agent({}) — Start an agent.
  wait_agent({}) — Wait for an agent.
mcp/data:
  lookup({}) — Look up data.`, index)
	reversed := []Definition{definitions[4], definitions[3], definitions[2], definitions[1], definitions[0]}
	assert.Equal(t, ToolIndex(definitions[:3]), ToolIndex([]Definition{definitions[2], definitions[0], definitions[1]}),
		"output does not depend on input order")
	assert.NotEqual(t, index, ToolIndex(reversed), "the first definition of a duplicated name wins")
}

func TestToolIndexBudget(t *testing.T) {
	var definitions []Definition
	for i := range 40 {
		definitions = append(definitions, Definition{Name: fmt.Sprintf("core_%02d", i), Description: strings.Repeat("Core tool. ", 20)})
	}
	for _, group := range []string{"github", "gitlab"} {
		for i := range 60 {
			definitions = append(definitions, Definition{
				Name:        fmt.Sprintf("%s_%02d", group, i),
				Group:       "mcp/" + group,
				Description: "Manage issues and pull requests across every repository that the authenticated user can reach.",
			})
		}
	}
	for i := range 3 {
		definitions = append(definitions, Definition{Name: fmt.Sprintf("note_%d", i), Group: "mcp/notes", Description: "Take notes."})
	}
	index := ToolIndex(definitions)
	assert.LessOrEqual(t, len(index), toolIndexMaxBytes)
	assert.Contains(t, index, "  core_39({}) — Core tool.", "built-in tools are never reduced")
	assert.Contains(t, index, "mcp/notes:\n  note_0({}) — Take notes.", "small groups are completed in early rounds")
	shown := make(map[string]int)
	for _, group := range []string{"github", "gitlab"} {
		match := regexp.MustCompile("mcp/" + group + ` \(60 tools, (\d+) shown in full\):`).FindStringSubmatch(index)
		require.Len(t, match, 2, "%s is partially listed", group)
		shown[group], _ = strconv.Atoi(match[1])
		assert.Positive(t, shown[group])
		assert.Contains(t, index, "  Also callable (call catalog.describe(name) for details): "+group+"_")
	}
	assert.LessOrEqual(t, max(shown["github"], shown["gitlab"])-min(shown["github"], shown["gitlab"]), 1,
		"groups take turns, so equally sized groups get an even share")
	assert.Equal(t, index, ToolIndex(definitions), "the budget allocation is deterministic")

	for i := range 600 {
		definitions = append(definitions, Definition{Name: fmt.Sprintf("huge_tool_number_%03d", i), Group: "mcp/huge"})
	}
	index = ToolIndex(definitions)
	assert.LessOrEqual(t, len(index), toolIndexMaxBytes)
	assert.Contains(t, index, `mcp/huge (600 tools): browse with catalog.list({group: "mcp/huge"})`,
		"when names alone do not fit, the largest group is reduced to a count")
	assert.Contains(t, index, "mcp/notes:\n  note_0({})", "other groups keep their lines")
	assert.Contains(t, index, "  core_00({})", "built-in tools stay listed")
}

func TestIndexGroupRendering(t *testing.T) {
	members := []Definition{
		{Name: "b-tool", Group: "mcp/x", Description: "Second tool."},
		{Name: "a_tool", Group: "mcp/x", Description: "First tool."},
	}
	group := newIndexGroup("mcp/x", members)
	assert.Equal(t, `mcp/x (2 tools, names only; call catalog.describe(name) for details): a_tool, "b-tool"`, group.render())
	group.full[0] = true
	assert.Equal(t, "mcp/x (2 tools, 1 shown in full):\n  a_tool({}) — First tool.\n"+
		`  Also callable (call catalog.describe(name) for details): "b-tool"`, group.render())
	group.full[1] = true
	assert.Equal(t, "mcp/x:\n  a_tool({}) — First tool.\n  \"b-tool\"({}) — Second tool.", group.render())
	group.countOnly = true
	assert.Equal(t, `mcp/x (2 tools): browse with catalog.list({group: "mcp/x"})`, group.render())

	builtIn := newIndexGroup("", []Definition{{Name: "bash", Short: "Run a command."}})
	assert.Equal(t, "Built-in:\n  bash({}) — Run a command.", builtIn.render(), "built-in tools start with full lines")
}
