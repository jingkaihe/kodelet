package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecipeConfigDefaults(t *testing.T) {
	listConfig := NewRecipeListConfig()
	assert.False(t, listConfig.ShowPath)
	assert.False(t, listConfig.JSONOutput)

	showConfig := NewRecipeShowConfig()
	assert.NotNil(t, showConfig.Arguments)
	assert.Empty(t, showConfig.Arguments)
}

func TestNewRecipeListOutputUsesMetadataAndPathRules(t *testing.T) {
	frags := []*fragments.Fragment{
		{ID: "with-meta", Metadata: fragments.Metadata{Name: "Friendly", Description: "desc"}, Path: "/tmp/a.md"},
		{ID: "fallback", Metadata: fragments.Metadata{}, Path: "/tmp/b.md"},
	}

	tableOutput := NewRecipeListOutput(frags, RecipeTableFormat, false)
	require.Len(t, tableOutput.Recipes, 2)
	assert.Equal(t, "Friendly", tableOutput.Recipes[0].Name)
	assert.Equal(t, "fallback", tableOutput.Recipes[1].Name)
	assert.Empty(t, tableOutput.Recipes[0].Path)
	assert.False(t, tableOutput.hasPath())

	withPath := NewRecipeListOutput(frags, RecipeTableFormat, true)
	assert.Equal(t, "/tmp/a.md", withPath.Recipes[0].Path)
	assert.True(t, withPath.hasPath())

	jsonOutput := NewRecipeListOutput(frags, RecipeJSONFormat, false)
	assert.Equal(t, "/tmp/a.md", jsonOutput.Recipes[0].Path)
}

func TestRecipeListOutputRenderTable(t *testing.T) {
	output := &RecipeListOutput{
		Format: RecipeTableFormat,
		Recipes: []RecipeOutput{
			{ID: "a", Name: "Alpha", Description: "first"},
			{ID: "b", Name: "Beta", Description: "second", Path: "/tmp/b.md"},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf))

	rendered := buf.String()
	assert.Contains(t, rendered, "ID")
	assert.Contains(t, rendered, "Path")
	assert.Contains(t, rendered, "Alpha")
	assert.Contains(t, rendered, "/tmp/b.md")
}

func TestRecipeListOutputRenderJSON(t *testing.T) {
	output := &RecipeListOutput{
		Format:  RecipeJSONFormat,
		Recipes: []RecipeOutput{{ID: "a", Name: "Alpha", Description: "first", Path: "/tmp/a.md"}},
	}

	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf))

	var parsed struct {
		Recipes []RecipeOutput `json:"recipes"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))
	require.Len(t, parsed.Recipes, 1)
	assert.Equal(t, "a", parsed.Recipes[0].ID)
	assert.Equal(t, "/tmp/a.md", parsed.Recipes[0].Path)
}

func TestRenderRecipeMetadataAndArguments(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, renderRecipeShow(&output, &fragments.Fragment{Path: "/runner/recipe.md", Content: "Hello kodelet!", Metadata: fragments.Metadata{Name: "Show Recipe", Description: "Example", Arguments: map[string]fragments.ArgumentMeta{"subject": {Description: "Target", Default: "world"}}}}))
	for _, want := range []string{"Recipe Metadata", "Name: Show Recipe", "Description: Example", "subject: Target (default: world)", "Recipe Content", "Hello kodelet!"} {
		assert.Contains(t, output.String(), want)
	}
}

type workspaceInspectionFixture struct {
	cwd, home, extensionPath, extensionMarker, templateMarker string
	env                                                       []string
}

func newWorkspaceInspectionFixture(t *testing.T) workspaceInspectionFixture {
	t.Helper()
	root := t.TempDir()
	fixture := workspaceInspectionFixture{
		cwd: filepath.Join(root, "workspace"), home: filepath.Join(root, "home"),
		extensionMarker: filepath.Join(root, "extension-started"), templateMarker: filepath.Join(root, "template-executed"),
	}
	fixture.extensionPath = filepath.Join(fixture.cwd, ".kodelet", "extensions", "kodelet-extension-poison")
	recipeDir := filepath.Join(fixture.cwd, ".kodelet", "recipes")
	for _, path := range []string{fixture.home, recipeDir, filepath.Dir(fixture.extensionPath)} {
		require.NoError(t, os.MkdirAll(path, 0o700))
	}
	// A local runtime would start this executable even though initialization
	// fails. Filesystem-only inspection must not run it at all.
	script := "#!/bin/sh\nprintf started > \"$KODELET_TEST_EXTENSION_MARKER\"\nexit 73\n"
	require.NoError(t, os.WriteFile(fixture.extensionPath, []byte(script), 0o700))
	recipe := `---
name: Poison recipe
description: Runner template execution fixture
arguments:
  subject:
    default: world
---
Hello {{.subject}}! {{bash "/bin/sh" "-c" "printf rendered > \"$KODELET_TEST_TEMPLATE_MARKER\"; printf template-output"}}
`
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, "poison.md"), []byte(recipe), 0o600))
	state := filepath.Join(root, "not-a-state-directory")
	require.NoError(t, os.WriteFile(state, []byte("no conversation store"), 0o600))
	fixture.env = []string{
		"HOME=" + fixture.home, "PATH=" + root, "KODELET_BASE_PATH=" + state, "KODELET_TEST_CLI_PROCESS=1",
		"KODELET_SERVER=http://127.0.0.1:1", "KODELET_AUTH_TOKEN=client", "KODELET_TEST_EXTENSION_MARKER=" + fixture.extensionMarker,
		"KODELET_TEST_TEMPLATE_MARKER=" + fixture.templateMarker,
	}
	return fixture
}

func TestWorkspaceInspectionUnavailableDoesNotUseClientFiles(t *testing.T) {
	for _, test := range []struct {
		path string
		args []string
	}{
		{"recipe list", []string{"recipe", "list", "--json", "--show-path"}},
		{"recipe list", []string{"recipe", "list", "--json=false", "--show-path=false"}},
		{"recipe show", []string{"recipe", "show", "poison", "--arg=subject=unused", "-a", "another=value"}},
		{"recipe show", []string{"recipe", "show", "missing"}},
		{"extension list", []string{"extension", "list", "--json"}},
		{"extension inspect", []string{"extension", "inspect", "poison", "--json=false"}},
		{"extension inspect", []string{"extension", "inspect", "missing", "--json"}},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fixture := newWorkspaceInspectionFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			output, err := daemonCLIProcess(ctx, t, fixture.cwd, fixture.env, test.args...).CombinedOutput()
			require.Error(t, err, "%s", output)
			assert.Contains(t, string(output), "connect")
			assert.NotContains(t, string(output), "unknown flag")
			assert.NoFileExists(t, fixture.extensionMarker)
			assert.NoFileExists(t, fixture.templateMarker)
			assert.NoDirExists(t, filepath.Join(fixture.home, ".kodelet"))
		})
	}
}

func TestWorkspaceInspectionHelpDisclosesEffects(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"recipe", "list", "--help"}, "Starts extensions on the runner"},
		{[]string{"recipe", "show", "--help"}, "Template functions may execute commands on the runner"},
		{[]string{"extension", "list", "--help"}, "without starting extension processes"},
		{[]string{"extension", "inspect", "--help"}, "without starting extension processes"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fixture := newWorkspaceInspectionFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			output, err := daemonCLIProcess(ctx, t, fixture.cwd, fixture.env, test.args...).CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Contains(t, string(output), test.want)
			assert.NoFileExists(t, fixture.extensionMarker)
			assert.NoFileExists(t, fixture.templateMarker)
		})
	}
}

func TestWorkspaceInspectionRejectsIgnoredExecutionFlags(t *testing.T) {
	for _, flag := range []string{"--model=unused", "--no-skills", "--allowed-tools=unused"} {
		fixture := newWorkspaceInspectionFixture(t)
		output, err := daemonCLIProcess(t.Context(), t, fixture.cwd, fixture.env, "recipe", "list", flag).CombinedOutput()
		require.Error(t, err, string(output))
		assert.Contains(t, string(output), "does not apply to workspace inspection")
		assert.NoFileExists(t, fixture.extensionMarker)
		assert.NoFileExists(t, fixture.templateMarker)
	}
}
