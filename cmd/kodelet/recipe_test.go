package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
