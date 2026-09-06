package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
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

func TestAppendExtensionRecipeOutputs(t *testing.T) {
	commands := []extensions.Command{
		{ExtensionID: "reviewer", Registration: extensions.CommandRegistration{Name: "review", Description: "Run extension review", Kind: "recipe"}},
		{ExtensionID: "doctor", Registration: extensions.CommandRegistration{Name: "doctor", Description: "Inspect health", Kind: "command"}},
	}
	output := &RecipeListOutput{Format: RecipeJSONFormat}

	appendExtensionRecipeOutputs(output, commands, false)

	require.Len(t, output.Recipes, 1)
	assert.Equal(t, "review", output.Recipes[0].ID)
	assert.Equal(t, "Run extension review", output.Recipes[0].Description)
	assert.Equal(t, "extension:reviewer/review", output.Recipes[0].Path)
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

func TestRunRecipeListWithTempDirs(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	withTempHomeAndCWD(t, home, cwd)

	recipesDir := filepath.Join(cwd, ".kodelet", "recipes")
	recipePath := filepath.Join(recipesDir, "local.md")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))
	require.NoError(t, os.WriteFile(recipePath, []byte("---\nname: Local Recipe\ndescription: From temp dir\n---\nBody\n"), 0o644))

	output := captureAllStdout(t, func() {
		err := runRecipeList(context.Background(), &RecipeListConfig{ShowPath: true})
		require.NoError(t, err)
	})

	assert.Contains(t, output, "local")
	assert.Contains(t, output, "Local Recipe")
	assert.Contains(t, output, "From temp dir")
	assert.Contains(t, output, filepath.Join(".kodelet", "recipes", "local.md"))
}

func TestRunRecipeListJSONWithTempDirs(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	withTempHomeAndCWD(t, home, cwd)

	recipesDir := filepath.Join(cwd, ".kodelet", "recipes")
	recipePath := filepath.Join(recipesDir, "local-json.md")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))
	require.NoError(t, os.WriteFile(recipePath, []byte("---\nname: JSON Recipe\ndescription: JSON temp dir\n---\nBody\n"), 0o644))

	output := captureAllStdout(t, func() {
		err := runRecipeList(context.Background(), &RecipeListConfig{JSONOutput: true})
		require.NoError(t, err)
	})

	var parsed struct {
		Recipes []RecipeOutput `json:"recipes"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &parsed))
	require.NotEmpty(t, parsed.Recipes)
	var local RecipeOutput
	for _, recipe := range parsed.Recipes {
		if recipe.ID == "local-json" {
			local = recipe
			break
		}
	}
	assert.Equal(t, "JSON Recipe", local.Name)
	assert.Equal(t, "JSON temp dir", local.Description)
	assert.Equal(t, filepath.Join(".kodelet", "recipes", "local-json.md"), local.Path)
}

func TestRunRecipeShowWithTempDirsAndArguments(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	withTempHomeAndCWD(t, home, cwd)

	recipesDir := filepath.Join(cwd, ".kodelet", "recipes")
	recipePath := filepath.Join(recipesDir, "show-me.md")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))
	require.NoError(t, os.WriteFile(recipePath, []byte(strings.TrimSpace(`
---
name: Show Recipe
description: Shows metadata and rendered content
arguments:
  subject:
    description: Thing to greet
    default: world
---
Hello {{.subject}}!
`)+"\n"), 0o644))

	output := captureAllStdout(t, func() {
		err := runRecipeShow(context.Background(), "show-me", &RecipeShowConfig{Arguments: map[string]string{"subject": "kodelet"}})
		require.NoError(t, err)
	})

	assert.Contains(t, output, "Recipe Metadata")
	assert.Contains(t, output, "Name: Show Recipe")
	assert.Contains(t, output, "Description: Shows metadata and rendered content")
	assert.Contains(t, output, "Path: "+filepath.Join(".kodelet", "recipes", "show-me.md"))
	assert.Contains(t, output, "subject: Thing to greet (default: world)")
	assert.Contains(t, output, "Recipe Content")
	assert.Contains(t, output, "Hello kodelet!")
}

type hostInspectionFixture struct {
	cwd, home, extensionPath, extensionMarker, templateMarker string
	env                                                       []string
}

func newHostInspectionFixture(t *testing.T) hostInspectionFixture {
	t.Helper()
	root := t.TempDir()
	fixture := hostInspectionFixture{
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
description: Host-only template execution fixture
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
		"KODELET_SERVER=http://127.0.0.1:1", "KODELET_TEST_EXTENSION_MARKER=" + fixture.extensionMarker,
		"KODELET_TEST_TEMPLATE_MARKER=" + fixture.templateMarker,
	}
	return fixture
}

func TestHostInspectionMigrationRejectsOldPathsBeforeEffects(t *testing.T) {
	for _, test := range []struct {
		path string
		args []string
	}{
		{"recipe", []string{"recipe"}},
		{"recipe list", []string{"recipe", "list", "--json", "--show-path"}},
		{"recipe list", []string{"recipe", "list", "--json=false", "--show-path=false"}},
		{"recipe show", []string{"recipe", "show", "poison", "--arg=subject=unused", "-a", "another=value"}},
		{"recipe show", []string{"recipe", "show", "missing"}},
		{"extension", []string{"extension"}},
		{"extension list", []string{"extension", "list", "--json"}},
		{"extension inspect", []string{"extension", "inspect", "poison", "--json=false"}},
		{"extension inspect", []string{"extension", "inspect", "missing", "--json"}},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fixture := newHostInspectionFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			output, err := daemonCLIProcess(ctx, t, fixture.cwd, fixture.env, test.args...).CombinedOutput()
			require.Error(t, err, "%s", output)
			assert.Contains(t, string(output), fmt.Sprintf("'%s' has moved to 'kodelet host %s'; run it in the workspace on the machine where the files are installed", test.path, test.path))
			assert.NotContains(t, string(output), "unknown flag")
			assert.NoFileExists(t, fixture.extensionMarker)
			assert.NoFileExists(t, fixture.templateMarker)
			assert.NoDirExists(t, filepath.Join(fixture.home, ".kodelet"))
		})
	}
}

func TestHostRecipeExplicitOperatorEffects(t *testing.T) {
	for _, command := range []string{"list", "show"} {
		t.Run(command, func(t *testing.T) {
			fixture := newHostInspectionFixture(t)
			args := []string{"host", "recipe", command}
			if command == "list" {
				args = append(args, "--show-path")
			} else {
				args = append(args, "poison", "-a", "subject=operator")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			output, err := daemonCLIProcess(ctx, t, fixture.cwd, fixture.env, args...).CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Contains(t, string(output), "Poison recipe")
			if command == "list" {
				assert.FileExists(t, fixture.extensionMarker)
				assert.NoFileExists(t, fixture.templateMarker)
				assert.DirExists(t, filepath.Join(fixture.home, ".kodelet", "extensions", "data", "poison"))
			} else {
				assert.Contains(t, string(output), "Hello operator! template-output")
				assert.FileExists(t, fixture.templateMarker)
				assert.NoFileExists(t, fixture.extensionMarker)
				assert.NoDirExists(t, filepath.Join(fixture.home, ".kodelet"))
			}
			assert.NoFileExists(t, filepath.Join(fixture.home, ".kodelet", "storage.db"))
		})
	}
}

func TestHostInspectionHelpDisclosesEffects(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"host", "recipe", "list", "--help"}, "Starts local extensions"},
		{[]string{"host", "recipe", "show", "--help"}, "Template functions may execute commands on this host"},
		{[]string{"host", "extension", "list", "--help"}, "without starting extension processes"},
		{[]string{"host", "extension", "inspect", "--help"}, "without starting extension processes"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fixture := newHostInspectionFixture(t)
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
