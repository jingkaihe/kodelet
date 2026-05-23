package slashcommands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantCommand string
		wantArgs    string
		wantFound   bool
	}{
		{name: "recipe only", text: "/init", wantCommand: "init", wantFound: true},
		{name: "recipe with args", text: "/github/pr target=main polish", wantCommand: "github/pr", wantArgs: "target=main polish", wantFound: true},
		{name: "trims whitespace", text: "  /init focus  ", wantCommand: "init", wantArgs: "focus", wantFound: true},
		{name: "not command", text: "hello /init", wantFound: false},
		{name: "just slash", text: "/", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, args, found := Parse(tt.text)
			assert.Equal(t, tt.wantCommand, command)
			assert.Equal(t, tt.wantArgs, args)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantKV         map[string]string
		wantAdditional string
	}{
		{name: "empty", args: "", wantKV: map[string]string{}, wantAdditional: ""},
		{name: "kv only", args: "target=main draft=false", wantKV: map[string]string{"target": "main", "draft": "false"}, wantAdditional: ""},
		{name: "kv and text", args: "target=main fix the bug", wantKV: map[string]string{"target": "main"}, wantAdditional: "fix the bug"},
		{name: "quoted value", args: `title="my feature" draft=true`, wantKV: map[string]string{"title": "my feature", "draft": "true"}, wantAdditional: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv, additional := ParseArgs(tt.args)
			assert.Equal(t, tt.wantKV, kv)
			assert.Equal(t, tt.wantAdditional, additional)
		})
	}
}

func TestBuildCommandHint(t *testing.T) {
	assert.Equal(t, "additional instructions (optional)", BuildCommandHint(nil))

	arguments := map[string]fragments.ArgumentMeta{
		"target": {Default: "main"},
		"draft":  {},
	}
	assert.Equal(t, "[draft=<value> target=main] additional instructions", BuildCommandHint(arguments))
}

func TestBuildCommandPlaceholder(t *testing.T) {
	assert.Equal(t, "/init additional instructions (optional)", BuildCommandPlaceholder("init", nil))

	arguments := map[string]fragments.ArgumentMeta{
		"name":       {},
		"occupation": {Default: "engineer"},
	}
	assert.Equal(t, "/intro [name=<value> occupation=engineer] additional instructions", BuildCommandPlaceholder("intro", arguments))
}

func TestBuiltIns(t *testing.T) {
	commands := BuiltIns()

	require.Len(t, commands, 1)
	assert.Equal(t, Command{
		Name:        "goal",
		Description: "Set the active goal for this thread",
		Hint:        "objective",
		Placeholder: "/goal <objective>",
	}, commands[0])
}

func TestListAndRecipeCommands(t *testing.T) {
	ctx := context.Background()
	processor := newSlashCommandTestProcessor(t)

	commands := List(ctx, processor)

	assert.Contains(t, commands, BuiltIns()[0])
	review := findSlashCommand(t, commands, "review")
	assert.Equal(t, "Review code", review.Description)
	assert.Equal(t, "[target=main topic=<value>] additional instructions", review.Hint)
	assert.Equal(t, "/review [target=main topic=<value>] additional instructions", review.Placeholder)

	assert.Nil(t, recipeCommands(ctx, nil))
}

func TestExpand(t *testing.T) {
	ctx := context.Background()
	processor := newSlashCommandTestProcessor(t)

	expansion, err := Expand(ctx, processor, "review", `topic=security target=develop audit auth code`)
	require.NoError(t, err)

	assert.Equal(t, "review", expansion.Command)
	assert.Equal(t, "/review topic=security target=develop audit auth code", expansion.Display)
	assert.Equal(t, map[string]string{"topic": "security", "target": "develop"}, expansion.Arguments)
	assert.Equal(t, "audit auth code", expansion.Instructions)
	assert.Contains(t, expansion.Prompt, "Review develop for security.")
	assert.Contains(t, expansion.Prompt, additionalInstructionsHeader+"audit auth code")
	assert.Equal(t, "Review code", expansion.Metadata.Description)
	assert.Equal(t, []string{"bash"}, expansion.Metadata.AllowedTools)
}

func TestExpandWithNilProcessor(t *testing.T) {
	expansion, err := Expand(context.Background(), nil, "review", "")

	assert.Nil(t, expansion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slash commands are unavailable")
}

func TestExpandUnknownCommandListsAvailableRecipes(t *testing.T) {
	processor := newSlashCommandTestProcessor(t)

	expansion, err := Expand(context.Background(), processor, "missing", "")

	assert.Nil(t, expansion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown recipe '/missing'")
	assert.Contains(t, err.Error(), "/review")
}

func TestAvailableRecipeNames(t *testing.T) {
	ctx := context.Background()

	assert.Equal(t, "(none available)", AvailableRecipeNames(ctx, nil))

	processor := newSlashCommandTestProcessor(t)
	assert.Contains(t, AvailableRecipeNames(ctx, processor), "/review")
}

func TestBuildDisplay(t *testing.T) {
	assert.Equal(t, "/review", BuildDisplay(" review ", "   "))
	assert.Equal(t, "/review target=main", BuildDisplay(" review ", "  target=main  "))
}

func newSlashCommandTestProcessor(t *testing.T) *fragments.Processor {
	t.Helper()

	dir := t.TempDir()
	recipe := `---
description: Review code
allowed_tools:
  - bash
arguments:
  target:
    default: main
  topic:
    description: Topic to review
---

Review {{.target}} for {{.topic}}.
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "review.md"), []byte(recipe), 0o644))

	processor, err := fragments.NewFragmentProcessor(fragments.WithFragmentDirs(dir))
	require.NoError(t, err)
	return processor
}

func findSlashCommand(t *testing.T, commands []Command, name string) Command {
	t.Helper()

	for _, command := range commands {
		if command.Name == name {
			return command
		}
	}
	require.Failf(t, "command not found", "missing command %q in %#v", name, commands)
	return Command{}
}
