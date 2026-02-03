package plugins

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/pkg/errors"
)

// ValidateRepoName validates a GitHub repository name format.
// Expected format: "owner/repo" (e.g., "jingkaihe/skills").
// Returns an error if the format is invalid.
func ValidateRepoName(repo string) error {
	if repo == "" {
		return errors.New("repository name cannot be empty")
	}
	if !strings.Contains(repo, "/") {
		return errors.Errorf("invalid repository format %q: expected 'owner/repo'", repo)
	}
	parts := strings.SplitN(repo, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return errors.Errorf("invalid repository format %q: owner and repo cannot be empty", repo)
	}
	return nil
}

// repoToPluginName converts a GitHub repo path to a plugin name.
// Expected format: "owner/repo" (e.g., "jingkaihe/skills" -> "jingkaihe@skills").
// If the input doesn't contain a slash, it returns the input unchanged.
// Only the first slash is replaced to handle nested paths correctly.
func repoToPluginName(repo string) string {
	if !strings.Contains(repo, "/") {
		return repo
	}
	return strings.Replace(repo, "/", "@", 1)
}

// pluginNameToPrefix converts a plugin name to a prefix for skills/recipes
// e.g., "jingkaihe@skills" -> "jingkaihe/skills/"
func pluginNameToPrefix(name string) string {
	return strings.Replace(name, "@", "/", 1) + "/"
}

// Installer handles plugin installation from GitHub repositories
type Installer struct {
	global    bool
	force     bool
	targetDir string
	homeDir   string
}

// InstallerOption configures an Installer instance
type InstallerOption func(*Installer)

// WithGlobal installs plugins to the global directory
func WithGlobal(global bool) InstallerOption {
	return func(i *Installer) {
		i.global = global
	}
}

// WithForce overwrites existing plugins
func WithForce(force bool) InstallerOption {
	return func(i *Installer) {
		i.force = force
	}
}

// NewInstaller creates a new plugin installer
func NewInstaller(opts ...InstallerOption) (*Installer, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get home directory")
	}

	i := &Installer{
		homeDir: homeDir,
	}

	for _, opt := range opts {
		opt(i)
	}

	if i.global {
		i.targetDir = filepath.Join(homeDir, kodeletDir)
	} else {
		i.targetDir = kodeletDir
	}

	return i, nil
}

// InstallResult contains information about installed plugins
type InstallResult struct {
	PluginName string
	Skills     []string
	Recipes    []string
	Hooks      []string
}

// Install installs plugins from a GitHub repository
func (i *Installer) Install(ctx context.Context, repo string, ref string) (*InstallResult, error) {
	if err := ValidateRepoName(repo); err != nil {
		return nil, err
	}

	if err := i.validateGHCLI(); err != nil {
		return nil, err
	}

	tempDir, err := i.cloneRepo(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	// Use org@repo format to avoid collisions and nested directories
	// e.g., "jingkaihe/skills" becomes "jingkaihe@skills"
	pluginName := repoToPluginName(repo)

	pluginDir := filepath.Join(i.targetDir, pluginsSubdir, pluginName)
	if err := i.checkExisting(pluginDir); err != nil {
		return nil, err
	}

	result := &InstallResult{
		PluginName: pluginName,
	}

	skillsDir := filepath.Join(tempDir, skillsSubdir)
	if skills, err := i.findSkills(skillsDir); err == nil && len(skills) > 0 {
		destSkillsDir := filepath.Join(pluginDir, skillsSubdir)
		if err := os.MkdirAll(destSkillsDir, 0o755); err != nil {
			return nil, errors.Wrap(err, "failed to create skills directory")
		}
		for _, skill := range skills {
			skillName := filepath.Base(skill)
			if err := i.copyDir(skill, filepath.Join(destSkillsDir, skillName)); err != nil {
				return nil, errors.Wrapf(err, "failed to install skill %s", skillName)
			}
			result.Skills = append(result.Skills, skillName)
		}
	}

	recipesDir := filepath.Join(tempDir, recipesSubdir)
	if recipes, err := i.findRecipes(recipesDir); err == nil && len(recipes) > 0 {
		destRecipesDir := filepath.Join(pluginDir, recipesSubdir)
		if err := os.MkdirAll(destRecipesDir, 0o755); err != nil {
			return nil, errors.Wrap(err, "failed to create recipes directory")
		}
		for _, recipe := range recipes {
			if err := i.installRecipe(recipe, recipesDir, destRecipesDir); err != nil {
				relPath, _ := filepath.Rel(recipesDir, recipe)
				return nil, errors.Wrapf(err, "failed to install recipe %s", relPath)
			}
			relPath, _ := filepath.Rel(recipesDir, recipe)
			recipeName := strings.TrimSuffix(relPath, ".md")
			result.Recipes = append(result.Recipes, recipeName)
		}
	}

	hooksDir := filepath.Join(tempDir, hooksSubdir)
	if hooks, err := i.findHooks(hooksDir); err == nil && len(hooks) > 0 {
		destHooksDir := filepath.Join(pluginDir, hooksSubdir)
		if err := os.MkdirAll(destHooksDir, 0o755); err != nil {
			return nil, errors.Wrap(err, "failed to create hooks directory")
		}
		for _, hook := range hooks {
			hookName := filepath.Base(hook)
			if err := i.copyFile(hook, filepath.Join(destHooksDir, hookName)); err != nil {
				return nil, errors.Wrapf(err, "failed to install hook %s", hookName)
			}
			result.Hooks = append(result.Hooks, hookName)
		}
	}

	if len(result.Skills) == 0 && len(result.Recipes) == 0 && len(result.Hooks) == 0 {
		os.RemoveAll(pluginDir)
		return nil, errors.New("no plugins found in repository (expected skills/, recipes/, or hooks/ directories)")
	}

	return result, nil
}

func (i *Installer) validateGHCLI() error {
	return osutil.ValidateGHCLI()
}

func (i *Installer) cloneRepo(ctx context.Context, repo, ref string) (string, error) {
	tempDir, err := os.MkdirTemp("", "kodelet-plugin-*")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp directory")
	}

	args := []string{"repo", "clone", repo, tempDir}
	if ref != "" {
		args = append(args, "--", "--branch", ref, "--depth", "1")
	} else {
		args = append(args, "--", "--depth", "1")
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tempDir)
		return "", errors.Wrapf(err, "failed to clone repository: %s", string(output))
	}

	return tempDir, nil
}

func (i *Installer) findSkills(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name())
		if _, err := os.Stat(filepath.Join(skillPath, skillFileName)); err == nil {
			skills = append(skills, skillPath)
		}
	}
	return skills, nil
}

func (i *Installer) findRecipes(dir string) ([]string, error) {
	var recipes []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			recipes = append(recipes, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return recipes, nil
}

func (i *Installer) findHooks(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var hooks []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		hookPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0o111 != 0 {
			hooks = append(hooks, hookPath)
		}
	}
	return hooks, nil
}

func (i *Installer) installRecipe(srcPath, recipesRoot, destRecipesDir string) error {
	relPath, err := filepath.Rel(recipesRoot, srcPath)
	if err != nil {
		return err
	}

	destPath := filepath.Join(destRecipesDir, relPath)

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}

	return i.copyFile(srcPath, destPath)
}

func (i *Installer) checkExisting(path string) error {
	if _, err := os.Stat(path); err == nil {
		if !i.force {
			return errors.Errorf("plugin already exists at %s (use --force to overwrite)", path)
		}
		if err := os.RemoveAll(path); err != nil {
			return errors.Wrap(err, "failed to remove existing plugin")
		}
	}
	return nil
}

func (i *Installer) copyDir(src, dst string) error {
	if i.force {
		os.RemoveAll(dst)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		return i.copyFile(path, destPath)
	})
}

func (i *Installer) copyFile(src, dst string) error {
	if i.force {
		os.Remove(dst)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// Remover handles plugin removal
type Remover struct {
	global  bool
	baseDir string
}

// NewRemover creates a new plugin remover
func NewRemover(opts ...InstallerOption) (*Remover, error) {
	i := &Installer{}
	for _, opt := range opts {
		opt(i)
	}

	r := &Remover{
		global: i.global,
	}

	if r.global {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get home directory")
		}
		r.baseDir = filepath.Join(homeDir, kodeletDir)
	} else {
		r.baseDir = kodeletDir
	}

	return r, nil
}

// Remove removes a plugin by name
// Accepts both "org/repo" format (converted to "org@repo") and direct "org@repo" format
func (r *Remover) Remove(name string) error {
	// Convert org/repo to org@repo if needed
	pluginName := name
	if strings.Contains(name, "/") {
		pluginName = repoToPluginName(name)
	}

	pluginPath := filepath.Join(r.baseDir, pluginsSubdir, pluginName)

	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		return errors.Errorf("plugin '%s' not found", name)
	}

	if err := os.RemoveAll(pluginPath); err != nil {
		return errors.Wrap(err, "failed to remove plugin")
	}

	return nil
}

// PluginNameToUserFacing converts "org@repo" directory format to "org/repo" user-facing format.
func PluginNameToUserFacing(pluginName string) string {
	return strings.Replace(pluginName, "@", "/", 1)
}

// ListPlugins returns all installed plugin names in "org/repo" user-facing format.
func (r *Remover) ListPlugins() ([]string, error) {
	pluginsDir := filepath.Join(r.baseDir, pluginsSubdir)

	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var plugins []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if this directory is a valid plugin (has skills/, recipes/, or hooks/)
		pluginPath := filepath.Join(pluginsDir, entry.Name())
		hasSkills := false
		hasRecipes := false
		hasHooks := false

		if _, err := os.Stat(filepath.Join(pluginPath, skillsSubdir)); err == nil {
			hasSkills = true
		}
		if _, err := os.Stat(filepath.Join(pluginPath, recipesSubdir)); err == nil {
			hasRecipes = true
		}
		if _, err := os.Stat(filepath.Join(pluginPath, hooksSubdir)); err == nil {
			hasHooks = true
		}

		if hasSkills || hasRecipes || hasHooks {
			// Convert org@repo to org/repo for user-facing output
			plugins = append(plugins, PluginNameToUserFacing(entry.Name()))
		}
	}

	return plugins, nil
}
