package plugins

import (
	"errors"
	"io/fs"
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
	require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "my-tool"), []byte(`#!/bin/bash
if [ "$1" = "description" ]; then
	echo '{"name":"my_tool","description":"A test tool","input_schema":{"type":"object"}}'
else
	echo hi
fi
`), 0o755))

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
	assert.Equal(t, []string{"my_tool"}, plugin.Tools)
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

func TestIsExecutableFile(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "executable"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "plain"), []byte("plain"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "directory"), 0o755))

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	byName := make(map[string]fs.DirEntry)
	for _, entry := range entries {
		byName[entry.Name()] = entry
	}

	assert.True(t, IsExecutableFile(byName["executable"]))
	assert.False(t, IsExecutableFile(byName["plain"]))
	assert.False(t, IsExecutableFile(byName["directory"]))
	assert.False(t, IsExecutableFile(errorDirEntry{}))
}

func TestSkillAndRecipeDirsIncludePluginDirsInPrecedenceOrder(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "repo")
	homeDir := filepath.Join(tmpDir, "home")

	repoPluginSkillsDir := filepath.Join(baseDir, "plugins", "org@skills", "skills")
	repoPluginRecipesDir := filepath.Join(baseDir, "plugins", "org@recipes", "recipes")
	globalPluginSkillsDir := filepath.Join(homeDir, kodeletDir, "plugins", "global@skills", "skills")
	globalPluginRecipesDir := filepath.Join(homeDir, kodeletDir, "plugins", "global@recipes", "recipes")
	require.NoError(t, os.MkdirAll(repoPluginSkillsDir, 0o755))
	require.NoError(t, os.MkdirAll(repoPluginRecipesDir, 0o755))
	require.NoError(t, os.MkdirAll(globalPluginSkillsDir, 0o755))
	require.NoError(t, os.MkdirAll(globalPluginRecipesDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "plugins", "no-skills-or-recipes"), 0o755))

	discovery, err := NewDiscovery(
		WithBaseDir(baseDir),
		WithHomeDir(homeDir),
	)
	require.NoError(t, err)

	assert.Equal(t, []string{
		filepath.Join(baseDir, "skills"),
		repoPluginSkillsDir,
		filepath.Join(homeDir, kodeletDir, "skills"),
		globalPluginSkillsDir,
	}, discovery.SkillDirs())
	assert.Equal(t, []string{
		filepath.Join(baseDir, "recipes"),
		repoPluginRecipesDir,
		filepath.Join(homeDir, kodeletDir, "recipes"),
		globalPluginRecipesDir,
	}, discovery.RecipeDirs())
}

func TestHookDirsIncludesStandaloneAndPluginDirsInPrecedenceOrder(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "repo")
	homeDir := filepath.Join(tmpDir, "home")

	repoPluginHooksDir := filepath.Join(baseDir, "plugins", "org@hooks", "hooks")
	globalPluginHooksDir := filepath.Join(homeDir, kodeletDir, "plugins", "global@hooks", "hooks")
	require.NoError(t, os.MkdirAll(repoPluginHooksDir, 0o755))
	require.NoError(t, os.MkdirAll(globalPluginHooksDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "plugins", "no-hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "plugins", "file.txt"), []byte("ignored"), 0o644))

	discovery, err := NewDiscovery(
		WithBaseDir(baseDir),
		WithHomeDir(homeDir),
	)
	require.NoError(t, err)

	assert.Equal(t, []PluginDirConfig{
		{Dir: filepath.Join(baseDir, "hooks"), Prefix: ""},
		{Dir: repoPluginHooksDir, Prefix: "org/hooks/"},
		{Dir: filepath.Join(homeDir, kodeletDir, "hooks"), Prefix: ""},
		{Dir: globalPluginHooksDir, Prefix: "global/hooks/"},
	}, discovery.HookDirs())
}

func TestDiscoverSkillsIncludesPluginSkillsWithUserFacingPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "repo")
	homeDir := filepath.Join(tmpDir, "home")

	localSkillDir := filepath.Join(baseDir, "plugins", "org@skills", "skills", "shared")
	globalSkillDir := filepath.Join(homeDir, kodeletDir, "plugins", "org@skills", "skills", "shared")
	writeSkill(t, localSkillDir, "shared", "Local plugin skill")
	writeSkill(t, globalSkillDir, "shared", "Global plugin skill")

	discovery, err := NewDiscovery(
		WithBaseDir(baseDir),
		WithHomeDir(homeDir),
	)
	require.NoError(t, err)

	skills, err := discovery.DiscoverSkills()
	require.NoError(t, err)

	skill, ok := skills["org/skills/shared"]
	require.True(t, ok)
	assert.Equal(t, "Local plugin skill", skill.Description())
	assert.Equal(t, localSkillDir, skill.Directory())
}

func TestDiscoverRecipesPrecedenceAndPluginRecipes(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "repo")
	homeDir := filepath.Join(tmpDir, "home")

	localRecipePath := filepath.Join(baseDir, "recipes", "shared.md")
	globalRecipePath := filepath.Join(homeDir, kodeletDir, "recipes", "shared.md")
	localPluginRecipePath := filepath.Join(baseDir, "plugins", "org@recipes", "recipes", "workflows", "deploy.md")
	globalPluginRecipePath := filepath.Join(homeDir, kodeletDir, "plugins", "global@recipes", "recipes", "ops", "cleanup.md")
	writeRecipe(t, localRecipePath, "Local recipe")
	writeRecipe(t, globalRecipePath, "Global recipe")
	writeRecipe(t, localPluginRecipePath, "Plugin recipe")
	writeRecipe(t, globalPluginRecipePath, "Global plugin recipe")

	discovery, err := NewDiscovery(
		WithBaseDir(baseDir),
		WithHomeDir(homeDir),
	)
	require.NoError(t, err)

	recipes, err := discovery.DiscoverRecipes()
	require.NoError(t, err)

	shared, ok := recipes["shared"]
	require.True(t, ok)
	assert.Equal(t, "Local recipe", shared.Description())
	assert.Equal(t, filepath.Dir(localRecipePath), shared.Directory())

	pluginRecipe, ok := recipes["org/recipes/workflows/deploy"]
	require.True(t, ok)
	assert.Equal(t, "Plugin recipe", pluginRecipe.Description())
	assert.Equal(t, filepath.Dir(localPluginRecipePath), pluginRecipe.Directory())

	globalPluginRecipe, ok := recipes["global/recipes/ops/cleanup"]
	require.True(t, ok)
	assert.Equal(t, "Global plugin recipe", globalPluginRecipe.Description())
	assert.Equal(t, filepath.Dir(globalPluginRecipePath), globalPluginRecipe.Directory())
}

func TestListInstalledPluginsIncludesExecutableHooksOnly(t *testing.T) {
	tmpDir := t.TempDir()

	hooksDir := filepath.Join(tmpDir, "plugins", "org@hooks", "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hooksDir, "before_tool_call"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hooksDir, "README.md"), []byte("ignored"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(hooksDir, "nested"), 0o755))

	discovery, err := NewDiscovery(
		WithBaseDir(tmpDir),
		WithHomeDir(tmpDir),
	)
	require.NoError(t, err)

	plugins, err := discovery.ListInstalledPlugins(false)
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "org@hooks", plugins[0].Name)
	assert.Equal(t, []string{"before_tool_call"}, plugins[0].Hooks)
	assert.Empty(t, plugins[0].Skills)
	assert.Empty(t, plugins[0].Recipes)
	assert.Empty(t, plugins[0].Tools)
}

func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, skillFileName), []byte(content), 0o644))
}

func writeRecipe(t *testing.T, path, description string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	content := "---\ndescription: " + description + "\n---\n\n# Recipe\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

type errorDirEntry struct{}

func (errorDirEntry) Name() string               { return "error" }
func (errorDirEntry) IsDir() bool                { return false }
func (errorDirEntry) Type() fs.FileMode          { return 0 }
func (errorDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("stat failed") }
