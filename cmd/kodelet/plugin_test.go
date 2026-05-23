package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRepoRef(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantRepo string
		wantRef  string
	}{
		{name: "no ref", arg: "owner/repo", wantRepo: "owner/repo", wantRef: ""},
		{name: "tag ref", arg: "owner/repo@v1.2.3", wantRepo: "owner/repo", wantRef: "v1.2.3"},
		{name: "last at wins", arg: "owner/repo@feature@sha", wantRepo: "owner/repo@feature", wantRef: "sha"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, ref := parseRepoRef(tt.arg)
			assert.Equal(t, tt.wantRepo, repo)
			assert.Equal(t, tt.wantRef, ref)
		})
	}
}

func TestOutputPluginsJSON(t *testing.T) {
	discovery, plugin := setupPluginOutputTest(t, "Short skill description", "Recipe description")
	entries := []pluginEntry{{plugin: plugin, location: "local"}}

	output := captureStdout(t, func() {
		require.NoError(t, outputPluginsJSON(discovery, entries))
	})

	var payload PluginListOutput
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Len(t, payload.Plugins, 1)
	info := payload.Plugins[0]
	assert.Equal(t, "owner/repo", info.Name)
	assert.Equal(t, "local", info.Location)
	assert.Equal(t, plugin.Path, info.Path)
	assert.Equal(t, []SkillInfo{{Name: "helper", Description: "Short skill description"}}, info.Skills)
	assert.Equal(t, []RecipeInfo{{Name: "build", Description: "Recipe description"}}, info.Recipes)
	assert.Equal(t, []ToolInfo{{Name: "runner"}}, info.Tools)
	assert.Equal(t, []HookInfo{{Name: "after_tool_call"}}, info.Hooks)
}

func TestOutputPluginShowJSON(t *testing.T) {
	discovery, plugin := setupPluginOutputTest(t, "Skill description", "Recipe description")

	output := captureStdout(t, func() {
		require.NoError(t, outputPluginShowJSON(discovery, &plugin, "global"))
	})

	var info PluginInfo
	require.NoError(t, json.Unmarshal([]byte(output), &info))
	assert.Equal(t, "owner/repo", info.Name)
	assert.Equal(t, "global", info.Location)
	assert.Equal(t, []SkillInfo{{Name: "helper", Description: "Skill description"}}, info.Skills)
	assert.Equal(t, []RecipeInfo{{Name: "build", Description: "Recipe description"}}, info.Recipes)
}

func TestOutputPluginShowTable(t *testing.T) {
	longSkillDesc := strings.Repeat("a", maxDescriptionDisplayLength+10)
	longRecipeDesc := strings.Repeat("b", maxDescriptionDisplayLength+10)
	discovery, plugin := setupPluginOutputTest(t, longSkillDesc, longRecipeDesc)

	output := captureStdout(t, func() {
		require.NoError(t, outputPluginShowTable(discovery, &plugin, "local"))
	})

	assert.Contains(t, output, "Name:     owner/repo")
	assert.Contains(t, output, "Location: local")
	assert.Contains(t, output, "Skills (1):")
	assert.Contains(t, output, "  • helper - "+strings.Repeat("a", truncatedDescriptionLength)+"...")
	assert.Contains(t, output, "Recipes (1):")
	assert.Contains(t, output, "  • build - "+strings.Repeat("b", truncatedDescriptionLength)+"...")
	assert.Contains(t, output, "Tools (1):")
	assert.Contains(t, output, "  • runner")
	assert.Contains(t, output, "Hooks (1):")
	assert.Contains(t, output, "  • after_tool_call")
}

func TestOutputPluginShowTableOmitsDescriptionsWhenMetadataMissing(t *testing.T) {
	baseDir := t.TempDir()
	homeDir := t.TempDir()
	discovery, err := plugins.NewDiscovery(plugins.WithBaseDir(baseDir), plugins.WithHomeDir(homeDir))
	require.NoError(t, err)
	plugin := plugins.InstalledPlugin{Name: "owner@repo", Path: filepath.Join(baseDir, "plugins", "owner@repo"), Skills: []string{"missing"}, Recipes: []string{"missing"}}

	output := captureStdout(t, func() {
		require.NoError(t, outputPluginShowTable(discovery, &plugin, "local"))
	})

	assert.Contains(t, output, "  • missing\n")
	assert.NotContains(t, output, "missing -")
}

func setupPluginOutputTest(t *testing.T, skillDescription, recipeDescription string) (*plugins.Discovery, plugins.InstalledPlugin) {
	t.Helper()

	baseDir := t.TempDir()
	homeDir := t.TempDir()
	pluginPath := filepath.Join(baseDir, "plugins", "owner@repo")
	skillDir := filepath.Join(pluginPath, "skills", "helper")
	recipeDir := filepath.Join(pluginPath, "recipes")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.MkdirAll(recipeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: helper\ndescription: "+skillDescription+"\n---\nSkill body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, "build.md"), []byte("---\ndescription: "+recipeDescription+"\n---\nRecipe body\n"), 0o644))

	discovery, err := plugins.NewDiscovery(plugins.WithBaseDir(baseDir), plugins.WithHomeDir(homeDir))
	require.NoError(t, err)

	plugin := plugins.InstalledPlugin{
		Name:    "owner@repo",
		Path:    pluginPath,
		Skills:  []string{"helper"},
		Recipes: []string{"build"},
		Tools:   []string{"runner"},
		Hooks:   []string{"after_tool_call"},
	}
	return discovery, plugin
}
