package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDiscovery(t *testing.T) {
	discovery, err := NewDiscovery()
	require.NoError(t, err)
	assert.NotNil(t, discovery)
	assert.Equal(t, kodeletDir, discovery.baseDir)
	assert.NotEmpty(t, discovery.homeDir)
}

func TestNewDiscoveryWithOptions(t *testing.T) {
	discovery, err := NewDiscovery(
		WithBaseDir("/custom/base"),
		WithHomeDir("/custom/home"),
	)
	require.NoError(t, err)
	assert.Equal(t, "/custom/base", discovery.baseDir)
	assert.Equal(t, "/custom/home", discovery.homeDir)
}

func TestSkillDirs(t *testing.T) {
	discovery, err := NewDiscovery(
		WithBaseDir("/repo"),
		WithHomeDir("/home/user"),
	)
	require.NoError(t, err)

	dirs := discovery.SkillDirs()
	assert.Contains(t, dirs, "/repo/skills")
	assert.Contains(t, dirs, "/home/user/.kodelet/skills")
}

func TestRecipeDirs(t *testing.T) {
	discovery, err := NewDiscovery(
		WithBaseDir("/repo"),
		WithHomeDir("/home/user"),
	)
	require.NoError(t, err)

	dirs := discovery.RecipeDirs()
	assert.Contains(t, dirs, "/repo/recipes")
	assert.Contains(t, dirs, "/home/user/.kodelet/recipes")
}

func TestToolDirs(t *testing.T) {
	discovery, err := NewDiscovery(
		WithBaseDir("/repo"),
		WithHomeDir("/home/user"),
	)
	require.NoError(t, err)

	dirs := discovery.ToolDirs()
	require.Len(t, dirs, 2)
	assert.Equal(t, "/repo/tools", dirs[0].Dir)
	assert.Equal(t, "", dirs[0].Prefix)
	assert.Equal(t, "/home/user/.kodelet/tools", dirs[1].Dir)
	assert.Equal(t, "", dirs[1].Prefix)
}

func TestDiscoverSkillsFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	skillDir := filepath.Join(tmpDir, "skills", "test-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	skillContent := `---
name: test-skill
description: A test skill
---

# Test Skill

This is a test skill.
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(tmpDir),
		WithHomeDir(tmpDir),
	)
	require.NoError(t, err)

	skills, err := discovery.discoverSkillsFromDir(filepath.Join(tmpDir, "skills"), "")
	require.NoError(t, err)
	require.Len(t, skills, 1)

	skill := skills[0]
	assert.Equal(t, "test-skill", skill.Name())
	assert.Equal(t, "A test skill", skill.Description())
	assert.Equal(t, PluginTypeSkill, skill.Type())
}

func TestDiscoverSkillsFromDirWithPrefix(t *testing.T) {
	tmpDir := t.TempDir()

	skillDir := filepath.Join(tmpDir, "plugins", "my-plugin", "skills", "cool-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))

	skillContent := `---
name: cool-skill
description: A cool skill
---

# Cool Skill
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(tmpDir),
		WithHomeDir(tmpDir),
	)
	require.NoError(t, err)

	skills, err := discovery.discoverSkillsFromDir(
		filepath.Join(tmpDir, "plugins", "my-plugin", "skills"),
		"my-plugin/",
	)
	require.NoError(t, err)
	require.Len(t, skills, 1)

	skill := skills[0]
	assert.Equal(t, "my-plugin/cool-skill", skill.Name())
}

func TestDiscoverRecipesFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	recipesDir := filepath.Join(tmpDir, "recipes")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))

	recipeContent := `---
name: test-recipe
description: A test recipe
---

# Test Recipe

Do something useful.
`
	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "test-recipe.md"), []byte(recipeContent), 0o644))

	subDir := filepath.Join(recipesDir, "workflows")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "deploy.md"), []byte(recipeContent), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(tmpDir),
		WithHomeDir(tmpDir),
	)
	require.NoError(t, err)

	recipes, err := discovery.discoverRecipesFromDir(recipesDir, "")
	require.NoError(t, err)
	require.Len(t, recipes, 2)

	names := make([]string, len(recipes))
	for i, r := range recipes {
		names[i] = r.Name()
	}
	assert.Contains(t, names, "test-recipe")
	assert.Contains(t, names, "workflows/deploy")
}

func TestDiscoverRecipesFromDirWithPrefix(t *testing.T) {
	tmpDir := t.TempDir()

	recipesDir := filepath.Join(tmpDir, "plugins", "my-plugin", "recipes")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))

	recipeContent := `---
description: A plugin recipe
---

# Plugin Recipe
`
	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "cool-recipe.md"), []byte(recipeContent), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(tmpDir),
		WithHomeDir(tmpDir),
	)
	require.NoError(t, err)

	recipes, err := discovery.discoverRecipesFromDir(recipesDir, "my-plugin/")
	require.NoError(t, err)
	require.Len(t, recipes, 1)

	recipe := recipes[0]
	assert.Equal(t, "my-plugin/cool-recipe", recipe.Name())
}

func TestDiscoverSkillsPrecedence(t *testing.T) {
	tmpDir := t.TempDir()

	localSkillDir := filepath.Join(tmpDir, "local", "skills", "shared-skill")
	require.NoError(t, os.MkdirAll(localSkillDir, 0o755))
	localSkillContent := `---
name: shared-skill
description: Local version
---
`
	require.NoError(t, os.WriteFile(filepath.Join(localSkillDir, "SKILL.md"), []byte(localSkillContent), 0o644))

	globalSkillDir := filepath.Join(tmpDir, "global", ".kodelet", "skills", "shared-skill")
	require.NoError(t, os.MkdirAll(globalSkillDir, 0o755))
	globalSkillContent := `---
name: shared-skill
description: Global version
---
`
	require.NoError(t, os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(globalSkillContent), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(filepath.Join(tmpDir, "local")),
		WithHomeDir(filepath.Join(tmpDir, "global")),
	)
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)

	skill, exists := skills["shared-skill"]
	require.True(t, exists)
	assert.Equal(t, "Local version", skill.Description())
}

func TestListInstalledPlugins(t *testing.T) {
	tmpDir := t.TempDir()

	pluginDir := filepath.Join(tmpDir, "plugins", "test-plugin")

	skillDir := filepath.Join(pluginDir, "skills", "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	skillContent := `---
name: my-skill
description: A skill
---
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644))

	recipesDir := filepath.Join(pluginDir, "recipes")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "my-recipe.md"), []byte("# Recipe"), 0o644))

	toolsDir := filepath.Join(pluginDir, "tools")
	require.NoError(t, os.MkdirAll(toolsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "my-tool"), []byte("#!/bin/sh\necho ok\n"), 0o755))

	discovery, err := NewDiscovery(
		WithBaseDir(tmpDir),
		WithHomeDir(tmpDir),
	)
	require.NoError(t, err)

	plugins, err := discovery.ListInstalledPlugins(false)
	require.NoError(t, err)
	require.Len(t, plugins, 1)

	plugin := plugins[0]
	assert.Equal(t, "test-plugin", plugin.Name)
	assert.Equal(t, []string{"my-skill"}, plugin.Skills)
	assert.Equal(t, []string{"my-recipe"}, plugin.Recipes)
	assert.Equal(t, []string{"my-tool"}, plugin.Tools)
}

func TestDiscoverAll(t *testing.T) {
	tmpDir := t.TempDir()

	localSkillDir := filepath.Join(tmpDir, "local", "skills", "local-skill")
	require.NoError(t, os.MkdirAll(localSkillDir, 0o755))
	skillContent := `---
name: local-skill
description: A local skill
---
`
	require.NoError(t, os.WriteFile(filepath.Join(localSkillDir, "SKILL.md"), []byte(skillContent), 0o644))

	localRecipesDir := filepath.Join(tmpDir, "local", "recipes")
	require.NoError(t, os.MkdirAll(localRecipesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(localRecipesDir, "local-recipe.md"), []byte("# Recipe"), 0o644))

	pluginDir := filepath.Join(tmpDir, "local", "plugins", "my-plugin")
	pluginSkillDir := filepath.Join(pluginDir, "skills", "plugin-skill")
	require.NoError(t, os.MkdirAll(pluginSkillDir, 0o755))
	pluginSkillContent := `---
name: plugin-skill
description: A plugin skill
---
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginSkillDir, "SKILL.md"), []byte(pluginSkillContent), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(filepath.Join(tmpDir, "local")),
		WithHomeDir(filepath.Join(tmpDir, "global")),
	)
	require.NoError(t, err)

	allPlugins, err := discovery.DiscoverAll()
	require.NoError(t, err)

	names := make([]string, len(allPlugins))
	for i, p := range allPlugins {
		names[i] = p.Name()
	}

	assert.Contains(t, names, "local-skill")
	assert.Contains(t, names, "local-recipe")
	assert.Contains(t, names, "my-plugin/plugin-skill")
}
