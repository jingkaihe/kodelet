package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInstaller(t *testing.T) {
	installer, err := NewInstaller()
	require.NoError(t, err)
	assert.NotNil(t, installer)
	assert.Equal(t, kodeletDir, installer.targetDir)
	assert.False(t, installer.global)
	assert.False(t, installer.force)
}

func TestNewInstallerWithOptions(t *testing.T) {
	installer, err := NewInstaller(
		WithGlobal(true),
		WithForce(true),
	)
	require.NoError(t, err)
	assert.True(t, installer.global)
	assert.True(t, installer.force)
	assert.Contains(t, installer.targetDir, ".kodelet")
}

func TestNewRemover(t *testing.T) {
	remover, err := NewRemover()
	require.NoError(t, err)
	assert.NotNil(t, remover)
	assert.Equal(t, kodeletDir, remover.baseDir)
	assert.False(t, remover.global)
}

func TestNewRemoverWithGlobal(t *testing.T) {
	remover, err := NewRemover(WithGlobal(true))
	require.NoError(t, err)
	assert.True(t, remover.global)
	assert.Contains(t, remover.baseDir, ".kodelet")
}

func TestRemoverRemove(t *testing.T) {
	tmpDir := t.TempDir()

	pluginPath := filepath.Join(tmpDir, "plugins", "test-plugin")
	require.NoError(t, os.MkdirAll(pluginPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginPath, "README.md"), []byte("test"), 0o644))

	remover := &Remover{
		baseDir: tmpDir,
		global:  false,
	}

	err := remover.Remove("test-plugin")
	require.NoError(t, err)

	_, err = os.Stat(pluginPath)
	assert.True(t, os.IsNotExist(err))
}

func TestRemoverRemoveNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	remover := &Remover{
		baseDir: tmpDir,
		global:  false,
	}

	err := remover.Remove("nonexistent-plugin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRemoverListPlugins(t *testing.T) {
	tmpDir := t.TempDir()

	pluginsDir := filepath.Join(tmpDir, "plugins")
	// Create valid plugins with skills/ or recipes/ subdirectories
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "plugin-a", "skills"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "plugin-b", "recipes"), 0o755))
	// This is not a valid plugin (no skills/ or recipes/)
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "not-a-plugin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginsDir, "file.txt"), []byte("file"), 0o644))

	remover := &Remover{
		baseDir: tmpDir,
		global:  false,
	}

	plugins, err := remover.ListPlugins()
	require.NoError(t, err)
	assert.Len(t, plugins, 2)
	assert.Contains(t, plugins, "plugin-a")
	assert.Contains(t, plugins, "plugin-b")
}

func TestRemoverListPluginsEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	remover := &Remover{
		baseDir: tmpDir,
		global:  false,
	}

	plugins, err := remover.ListPlugins()
	require.NoError(t, err)
	assert.Empty(t, plugins)
}

func TestRemoverListPluginsOrgRepo(t *testing.T) {
	tmpDir := t.TempDir()

	pluginsDir := filepath.Join(tmpDir, "plugins")
	// Create org@repo style plugins (flat directory structure)
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "jingkaihe@skills", "skills"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "anthropic@skills", "recipes"), 0o755))

	remover := &Remover{
		baseDir: tmpDir,
		global:  false,
	}

	plugins, err := remover.ListPlugins()
	require.NoError(t, err)
	assert.Len(t, plugins, 2)
	assert.Contains(t, plugins, "jingkaihe/skills")
	assert.Contains(t, plugins, "anthropic/skills")
}

func TestRemoverRemoveOrgRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Use org@repo format for directory naming
	pluginPath := filepath.Join(tmpDir, "plugins", "jingkaihe@skills")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginPath, "skills"), 0o755))

	remover := &Remover{
		baseDir: tmpDir,
		global:  false,
	}

	// Remove using org/repo format (user-facing format)
	err := remover.Remove("jingkaihe/skills")
	require.NoError(t, err)

	_, err = os.Stat(pluginPath)
	assert.True(t, os.IsNotExist(err))
}

func TestInstallerFindSkills(t *testing.T) {
	tmpDir := t.TempDir()

	skillDir := filepath.Join(tmpDir, "skills", "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("test"), 0o644))

	noSkillDir := filepath.Join(tmpDir, "skills", "not-a-skill")
	require.NoError(t, os.MkdirAll(noSkillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(noSkillDir, "README.md"), []byte("test"), 0o644))

	installer := &Installer{}
	skills, err := installer.findSkills(filepath.Join(tmpDir, "skills"))
	require.NoError(t, err)
	require.Len(t, skills, 1)
	assert.Equal(t, skillDir, skills[0])
}

func TestInstallerFindRecipes(t *testing.T) {
	tmpDir := t.TempDir()

	recipesDir := filepath.Join(tmpDir, "recipes")
	require.NoError(t, os.MkdirAll(recipesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "recipe-a.md"), []byte("test"), 0o644))

	subDir := filepath.Join(recipesDir, "workflows")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "deploy.md"), []byte("test"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "not-a-recipe.txt"), []byte("test"), 0o644))

	installer := &Installer{}
	recipes, err := installer.findRecipes(recipesDir)
	require.NoError(t, err)
	assert.Len(t, recipes, 2)
}

func TestInstallerCopyDir(t *testing.T) {
	tmpDir := t.TempDir()

	srcDir := filepath.Join(tmpDir, "src")
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("content1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "subdir", "file2.txt"), []byte("content2"), 0o644))

	dstDir := filepath.Join(tmpDir, "dst")

	installer := &Installer{force: false}
	err := installer.copyDir(srcDir, dstDir)
	require.NoError(t, err)

	content1, err := os.ReadFile(filepath.Join(dstDir, "file1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content1", string(content1))

	content2, err := os.ReadFile(filepath.Join(dstDir, "subdir", "file2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content2", string(content2))
}

func TestInstallerCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "src.txt")
	dstFile := filepath.Join(tmpDir, "dst.txt")

	require.NoError(t, os.WriteFile(srcFile, []byte("hello world"), 0o644))

	installer := &Installer{force: false}
	err := installer.copyFile(srcFile, dstFile)
	require.NoError(t, err)

	content, err := os.ReadFile(dstFile)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

func TestInstallerCheckExisting(t *testing.T) {
	tmpDir := t.TempDir()

	existingPath := filepath.Join(tmpDir, "existing")
	require.NoError(t, os.MkdirAll(existingPath, 0o755))

	installer := &Installer{force: false}
	err := installer.checkExisting(existingPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	installerForce := &Installer{force: true}
	err = installerForce.checkExisting(existingPath)
	require.NoError(t, err)

	_, err = os.Stat(existingPath)
	assert.True(t, os.IsNotExist(err))
}

func TestInstallerCheckExistingNonExistent(t *testing.T) {
	tmpDir := t.TempDir()

	nonExistentPath := filepath.Join(tmpDir, "non-existent")

	installer := &Installer{force: false}
	err := installer.checkExisting(nonExistentPath)
	require.NoError(t, err)
}

func TestInstallerInstallRecipe(t *testing.T) {
	tmpDir := t.TempDir()

	srcRecipesDir := filepath.Join(tmpDir, "src", "recipes")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRecipesDir, "workflows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRecipesDir, "workflows", "deploy.md"), []byte("# Deploy"), 0o644))

	dstRecipesDir := filepath.Join(tmpDir, "dst", "recipes")
	require.NoError(t, os.MkdirAll(dstRecipesDir, 0o755))

	installer := &Installer{force: false}
	err := installer.installRecipe(
		filepath.Join(srcRecipesDir, "workflows", "deploy.md"),
		srcRecipesDir,
		dstRecipesDir,
	)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dstRecipesDir, "workflows", "deploy.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Deploy", string(content))
}

func TestValidateRepoName(t *testing.T) {
	tests := []struct {
		name    string
		repo    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid repo",
			repo:    "owner/repo",
			wantErr: false,
		},
		{
			name:    "valid repo with dashes",
			repo:    "my-org/my-repo",
			wantErr: false,
		},
		{
			name:    "valid repo with underscores",
			repo:    "my_org/my_repo",
			wantErr: false,
		},
		{
			name:    "empty string",
			repo:    "",
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name:    "no slash",
			repo:    "justrepo",
			wantErr: true,
			errMsg:  "expected 'owner/repo'",
		},
		{
			name:    "empty owner",
			repo:    "/repo",
			wantErr: true,
			errMsg:  "owner and repo cannot be empty",
		},
		{
			name:    "empty repo name",
			repo:    "owner/",
			wantErr: true,
			errMsg:  "owner and repo cannot be empty",
		},
		{
			name:    "just slash",
			repo:    "/",
			wantErr: true,
			errMsg:  "owner and repo cannot be empty",
		},
		{
			name:    "multiple slashes preserved",
			repo:    "owner/repo/extra",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoName(tt.repo)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRepoToPluginName(t *testing.T) {
	tests := []struct {
		name     string
		repo     string
		expected string
	}{
		{
			name:     "standard repo",
			repo:     "owner/repo",
			expected: "owner@repo",
		},
		{
			name:     "repo with dashes",
			repo:     "my-org/my-repo",
			expected: "my-org@my-repo",
		},
		{
			name:     "no slash returns unchanged",
			repo:     "justrepo",
			expected: "justrepo",
		},
		{
			name:     "empty string",
			repo:     "",
			expected: "",
		},
		{
			name:     "multiple slashes only replaces first",
			repo:     "owner/repo/extra",
			expected: "owner@repo/extra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repoToPluginName(tt.repo)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInstallerFindRecipesDeepNesting(t *testing.T) {
	tmpDir := t.TempDir()

	recipesDir := filepath.Join(tmpDir, "recipes")
	// Create deeply nested recipes
	deepPath := filepath.Join(recipesDir, "workflows", "ci", "github", "actions")
	require.NoError(t, os.MkdirAll(deepPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deepPath, "deploy.md"), []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(recipesDir, "root.md"), []byte("test"), 0o644))

	installer := &Installer{}
	recipes, err := installer.findRecipes(recipesDir)
	require.NoError(t, err)
	assert.Len(t, recipes, 2)
}

func TestScanPluginSubdirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create plugins with skills subdirectory
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "org1@repo1", "skills"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "org2@repo2", "skills"), 0o755))
	// Create a plugin without the target subdir (should be ignored)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "org3@repo3", "other"), 0o755))
	// Create a file (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("test"), 0o644))

	dirs := ScanPluginSubdirs(tmpDir, "skills")
	assert.Len(t, dirs, 2)

	// Verify prefixes are set correctly
	prefixes := make(map[string]bool)
	for _, d := range dirs {
		prefixes[d.Prefix] = true
	}
	assert.True(t, prefixes["org1@repo1/"])
	assert.True(t, prefixes["org2@repo2/"])
}

func TestScanPluginSubdirsNonExistentDir(t *testing.T) {
	dirs := ScanPluginSubdirs("/nonexistent/path", "skills")
	assert.Empty(t, dirs)
}

func TestPluginDirConfigPrefixedName(t *testing.T) {
	tests := []struct {
		name     string
		config   PluginDirConfig
		input    string
		expected string
	}{
		{
			name:     "with prefix",
			config:   PluginDirConfig{Dir: "/some/dir", Prefix: "org/repo/"},
			input:    "skill-name",
			expected: "org/repo/skill-name",
		},
		{
			name:     "empty prefix",
			config:   PluginDirConfig{Dir: "/some/dir", Prefix: ""},
			input:    "skill-name",
			expected: "skill-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.PrefixedName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
