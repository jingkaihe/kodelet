# ADR 028: Unified Plugin System

## Status
Proposed

## Context

Kodelet currently has two extensibility mechanisms that share similar patterns but have separate implementations:

1. **Skills** (`pkg/skills/`): Model-invoked capabilities discovered from `.kodelet/skills/`
2. **Recipes/Fragments** (`pkg/fragments/`): User-invoked templates discovered from `./recipes/`

**Problem Statement:**
- Skills can be installed via `kodelet skill add <repo>`, but recipes have no installation mechanism
- Inconsistent storage locations: skills use `.kodelet/skills/`, recipes use `./recipes/`
- Duplicate discovery and installation logic across both systems
- No unified CLI for managing extensibility features

**Current State:**

| Aspect | Skills | Recipes |
|--------|--------|---------|
| Storage (repo) | `.kodelet/skills/<name>/` | `./recipes/` |
| Storage (global) | `~/.kodelet/skills/<name>/` | `~/.kodelet/recipes/` |
| Installation CLI | `kodelet skill add <repo>` | None |
| Invocation | Model-invoked (automatic) | User-invoked (`-r` flag) |
| File format | `SKILL.md` with YAML frontmatter | `*.md` with YAML frontmatter |

**Goals:**
1. Unify storage locations under `.kodelet/` namespace
2. Create shared infrastructure for discovery and installation
3. Provide unified `kodelet plugin` CLI for managing both skills and recipes
4. Maintain distinct runtime behaviors (model-invoked vs user-invoked)

## Decision

Introduce a **Unified Plugin System** that shares common infrastructure while preserving the distinct runtime semantics of skills and recipes.

### Key Design Decisions

1. **Unified Storage**: Move recipes from `./recipes/` to `.kodelet/recipes/` for consistency
2. **Shared Infrastructure**: Create `pkg/plugins/` package for common discovery and installation logic
3. **Unified CLI**: Implement `kodelet plugin add|list|remove` commands that handle both types
4. **Auto-Detection**: When installing from GitHub, auto-detect plugin type from file structure
5. **Preserve Runtime Semantics**: Skills remain model-invoked, recipes remain user-invoked

## Architecture Overview

### Directory Structure

**Installed Plugins** (grouped by plugin name):
```
.kodelet/plugins/<plugin-name>/
├── skills/
│   └── <skill-name>/
│       └── SKILL.md
└── recipes/
    ├── <name>.md
    └── <category>/
        └── <name>.md

~/.kodelet/plugins/<plugin-name>/
├── skills/
│   └── <skill-name>/
│       └── SKILL.md
└── recipes/
    └── <name>.md
```

**Standalone Skills/Recipes** (for local development, not part of a plugin):
```
.kodelet/
├── skills/
│   └── <skill-name>/
│       └── SKILL.md
└── recipes/
    └── <name>.md

~/.kodelet/
├── skills/
│   └── <skill-name>/
│       └── SKILL.md
└── recipes/
    └── <name>.md
```

### Discovery Precedence

Skills and recipes are discovered from all locations, merged with precedence:

1. Repo-local standalone (`.kodelet/skills/`, `.kodelet/recipes/`) - highest
2. Repo-local plugins (`.kodelet/plugins/*/skills/`, `.kodelet/plugins/*/recipes/`)
3. User-global standalone (`~/.kodelet/skills/`, `~/.kodelet/recipes/`)
4. User-global plugins (`~/.kodelet/plugins/*/skills/`, `~/.kodelet/plugins/*/recipes/`)
5. Built-in embedded (recipes only) - lowest

### Naming Convention

**Standalone skills/recipes** use simple names:
```
my-skill              # Skill at .kodelet/skills/my-skill/SKILL.md
my-recipe             # Recipe at .kodelet/recipes/my-recipe.md
github/pr             # Recipe at .kodelet/recipes/github/pr.md
```

**Plugin-based skills/recipes** are prefixed with plugin name:
```
my-plugin/my-skill              # Skill from plugin "my-plugin"
my-plugin/my-recipe             # Recipe from plugin "my-plugin"
my-plugin/workflows/deploy      # Nested recipe from plugin "my-plugin"
```

This ensures:
- Clear provenance - you know where each skill/recipe comes from
- No naming conflicts between plugins
- Standalone skills/recipes can shadow plugin ones (higher precedence)

**Usage examples:**
```bash
# Invoke standalone recipe
kodelet run -r github/pr

# Invoke plugin recipe
kodelet run -r my-plugin/workflows/deploy

# Model invokes plugin skill automatically
# (skill tool sees "my-plugin/pdf" in available skills)
```

### GitHub Repository Structure for Plugins

Plugin authors structure their repositories as:

```
my-plugin-repo/
├── skills/
│   └── my-skill/
│       └── SKILL.md
└── recipes/
    ├── my-recipe.md
    └── workflows/
        └── deploy.md
```

When installed via `kodelet plugin add user/my-plugin-repo`:
```
.kodelet/plugins/my-plugin-repo/
├── skills/
│   └── my-skill/
│       └── SKILL.md
└── recipes/
    ├── my-recipe.md
    └── workflows/
        └── deploy.md
```

## Implementation Design

### 1. Plugin Types Package

Create common types in `pkg/plugins/types.go`:

```go
package plugins

// PluginType represents the type of plugin
type PluginType string

const (
    PluginTypeSkill  PluginType = "skill"
    PluginTypeRecipe PluginType = "recipe"
)

// Plugin represents a discoverable plugin (skill or recipe)
type Plugin interface {
    Name() string
    Description() string
    Directory() string
    Type() PluginType
}

// PluginMetadata contains common metadata for all plugins
type PluginMetadata struct {
    Name        string `yaml:"name"`
    Description string `yaml:"description"`
}
```

### 2. Unified Discovery

Create `pkg/plugins/discovery.go`:

```go
package plugins

import (
    "os"
    "path/filepath"

    "github.com/pkg/errors"
)

// Discovery handles plugin discovery from configured directories
type Discovery struct {
    baseDir string // ".kodelet" or absolute path
    homeDir string
}

// DiscoveryOption configures a Discovery instance
type DiscoveryOption func(*Discovery) error

// WithBaseDir sets a custom base directory (for testing)
func WithBaseDir(dir string) DiscoveryOption {
    return func(d *Discovery) error {
        d.baseDir = dir
        return nil
    }
}

// NewDiscovery creates a new plugin discovery instance
func NewDiscovery(opts ...DiscoveryOption) (*Discovery, error) {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        return nil, errors.Wrap(err, "failed to get user home directory")
    }

    d := &Discovery{
        baseDir: ".kodelet",
        homeDir: homeDir,
    }

    for _, opt := range opts {
        if err := opt(d); err != nil {
            return nil, err
        }
    }

    return d, nil
}

// SkillDirs returns the skill discovery directories in precedence order
func (d *Discovery) SkillDirs() []string {
    dirs := []string{
        // Standalone (highest precedence)
        filepath.Join(d.baseDir, "skills"),
    }

    // Repo-local plugins
    dirs = append(dirs, d.pluginSkillDirs(d.baseDir)...)

    // Global standalone
    dirs = append(dirs, filepath.Join(d.homeDir, ".kodelet", "skills"))

    // Global plugins
    dirs = append(dirs, d.pluginSkillDirs(filepath.Join(d.homeDir, ".kodelet"))...)

    return dirs
}

// RecipeDirs returns the recipe discovery directories in precedence order
func (d *Discovery) RecipeDirs() []string {
    dirs := []string{
        // Standalone (highest precedence)
        filepath.Join(d.baseDir, "recipes"),
    }

    // Repo-local plugins
    dirs = append(dirs, d.pluginRecipeDirs(d.baseDir)...)

    // Global standalone
    dirs = append(dirs, filepath.Join(d.homeDir, ".kodelet", "recipes"))

    // Global plugins
    dirs = append(dirs, d.pluginRecipeDirs(filepath.Join(d.homeDir, ".kodelet"))...)

    return dirs
}

// pluginSkillDirs returns skill directories from all plugins under baseDir
func (d *Discovery) pluginSkillDirs(baseDir string) []string {
    pluginsDir := filepath.Join(baseDir, "plugins")
    entries, err := os.ReadDir(pluginsDir)
    if err != nil {
        return nil
    }

    var dirs []string
    for _, entry := range entries {
        if entry.IsDir() {
            skillDir := filepath.Join(pluginsDir, entry.Name(), "skills")
            if _, err := os.Stat(skillDir); err == nil {
                dirs = append(dirs, skillDir)
            }
        }
    }
    return dirs
}

// pluginRecipeDirs returns recipe directories from all plugins under baseDir
func (d *Discovery) pluginRecipeDirs(baseDir string) []string {
    pluginsDir := filepath.Join(baseDir, "plugins")
    entries, err := os.ReadDir(pluginsDir)
    if err != nil {
        return nil
    }

    var dirs []string
    for _, entry := range entries {
        if entry.IsDir() {
            recipeDir := filepath.Join(pluginsDir, entry.Name(), "recipes")
            if _, err := os.Stat(recipeDir); err == nil {
                dirs = append(dirs, recipeDir)
            }
        }
    }
    return dirs
}

// DiscoverAll discovers all plugins (skills and recipes) with proper naming
func (d *Discovery) DiscoverAll() ([]Plugin, error) {
    var plugins []Plugin
    seen := make(map[string]bool) // Track names to handle precedence

    // 1. Standalone skills/recipes (highest precedence, no prefix)
    skills, _ := d.discoverSkillsFromDir(filepath.Join(d.baseDir, "skills"), "")
    for _, s := range skills {
        if !seen[s.Name()] {
            plugins = append(plugins, s)
            seen[s.Name()] = true
        }
    }

    recipes, _ := d.discoverRecipesFromDir(filepath.Join(d.baseDir, "recipes"), "")
    for _, r := range recipes {
        if !seen[r.Name()] {
            plugins = append(plugins, r)
            seen[r.Name()] = true
        }
    }

    // 2. Repo-local plugins (prefixed with plugin name)
    d.discoverFromPluginsDir(filepath.Join(d.baseDir, "plugins"), &plugins, seen)

    // 3. Global standalone
    skills, _ = d.discoverSkillsFromDir(filepath.Join(d.homeDir, ".kodelet", "skills"), "")
    for _, s := range skills {
        if !seen[s.Name()] {
            plugins = append(plugins, s)
            seen[s.Name()] = true
        }
    }

    recipes, _ = d.discoverRecipesFromDir(filepath.Join(d.homeDir, ".kodelet", "recipes"), "")
    for _, r := range recipes {
        if !seen[r.Name()] {
            plugins = append(plugins, r)
            seen[r.Name()] = true
        }
    }

    // 4. Global plugins
    d.discoverFromPluginsDir(filepath.Join(d.homeDir, ".kodelet", "plugins"), &plugins, seen)

    return plugins, nil
}

// discoverFromPluginsDir discovers skills/recipes from all plugins under a plugins directory
func (d *Discovery) discoverFromPluginsDir(pluginsDir string, plugins *[]Plugin, seen map[string]bool) {
    entries, err := os.ReadDir(pluginsDir)
    if err != nil {
        return
    }

    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }
        pluginName := entry.Name()
        prefix := pluginName + "/"

        // Skills from this plugin
        skillsDir := filepath.Join(pluginsDir, pluginName, "skills")
        skills, _ := d.discoverSkillsFromDir(skillsDir, prefix)
        for _, s := range skills {
            if !seen[s.Name()] {
                *plugins = append(*plugins, s)
                seen[s.Name()] = true
            }
        }

        // Recipes from this plugin
        recipesDir := filepath.Join(pluginsDir, pluginName, "recipes")
        recipes, _ := d.discoverRecipesFromDir(recipesDir, prefix)
        for _, r := range recipes {
            if !seen[r.Name()] {
                *plugins = append(*plugins, r)
                seen[r.Name()] = true
            }
        }
    }
}

// discoverSkillsFromDir discovers skills from a directory with optional name prefix
func (d *Discovery) discoverSkillsFromDir(dir, prefix string) ([]Plugin, error) {
    // Implementation: scan for SKILL.md, prepend prefix to names
    // e.g., prefix="my-plugin/", skill="pdf" -> name="my-plugin/pdf"
}

// discoverRecipesFromDir discovers recipes from a directory with optional name prefix
func (d *Discovery) discoverRecipesFromDir(dir, prefix string) ([]Plugin, error) {
    // Implementation: scan for *.md, prepend prefix to names
    // e.g., prefix="my-plugin/", recipe="workflows/deploy" -> name="my-plugin/workflows/deploy"
}
```

### 3. Unified Installer

Create `pkg/plugins/installer.go`:

```go
package plugins

import (
    "context"
    "io/fs"
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "github.com/pkg/errors"
)

// Installer handles plugin installation from GitHub repositories
type Installer struct {
    global    bool
    force     bool
    targetDir string
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
    i := &Installer{}
    for _, opt := range opts {
        opt(i)
    }

    if i.global {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return nil, errors.Wrap(err, "failed to get home directory")
        }
        i.targetDir = filepath.Join(homeDir, ".kodelet")
    } else {
        i.targetDir = ".kodelet"
    }

    return i, nil
}

// InstallResult contains information about installed plugins
type InstallResult struct {
    Skills  []string
    Recipes []string
}

// Install installs plugins from a GitHub repository
func (i *Installer) Install(ctx context.Context, repo string, ref string) (*InstallResult, error) {
    // 1. Validate gh CLI is available
    if err := i.validateGHCLI(); err != nil {
        return nil, err
    }

    // 2. Clone repo to temp directory
    tempDir, err := i.cloneRepo(ctx, repo, ref)
    if err != nil {
        return nil, err
    }
    defer os.RemoveAll(tempDir)

    // 3. Extract plugin name from repo (e.g., "user/my-plugin" -> "my-plugin")
    pluginName := filepath.Base(repo)

    // 4. Create plugin directory
    pluginDir := filepath.Join(i.targetDir, "plugins", pluginName)
    if err := i.checkExisting(pluginDir); err != nil {
        return nil, err
    }

    result := &InstallResult{}

    // 5. Find and copy skills from repo root skills/
    skillsDir := filepath.Join(tempDir, "skills")
    if skills, err := i.findSkills(skillsDir); err == nil && len(skills) > 0 {
        destSkillsDir := filepath.Join(pluginDir, "skills")
        if err := os.MkdirAll(destSkillsDir, 0755); err != nil {
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

    // 6. Find and copy recipes from repo root recipes/
    recipesDir := filepath.Join(tempDir, "recipes")
    if recipes, err := i.findRecipes(recipesDir); err == nil && len(recipes) > 0 {
        destRecipesDir := filepath.Join(pluginDir, "recipes")
        if err := os.MkdirAll(destRecipesDir, 0755); err != nil {
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

    if len(result.Skills) == 0 && len(result.Recipes) == 0 {
        // Clean up empty plugin directory
        os.RemoveAll(pluginDir)
        return nil, errors.New("no plugins found in repository (expected skills/ or recipes/ directories)")
    }

    return result, nil
}

func (i *Installer) validateGHCLI() error {
    cmd := exec.Command("gh", "auth", "status")
    if err := cmd.Run(); err != nil {
        return errors.New("GitHub CLI (gh) is not installed or not authenticated. Run 'gh auth login' first")
    }
    return nil
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
        if _, err := os.Stat(filepath.Join(skillPath, "SKILL.md")); err == nil {
            skills = append(skills, skillPath)
        }
    }
    return skills, nil
}

func (i *Installer) findRecipes(dir string) ([]string, error) {
    var recipes []string

    // Walk directory recursively to find all .md files
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

func (i *Installer) installRecipe(srcPath, recipesRoot, destRecipesDir string) error {
    // Preserve directory structure relative to recipes root
    relPath, err := filepath.Rel(recipesRoot, srcPath)
    if err != nil {
        return err
    }

    destPath := filepath.Join(destRecipesDir, relPath)

    if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
        return err
    }

    return i.copyFile(srcPath, destPath)
}

func (i *Installer) checkExisting(path string) error {
    if _, err := os.Stat(path); err == nil && !i.force {
        return errors.Errorf("plugin already exists at %s (use --force to overwrite)", path)
    }
    return nil
}

func (i *Installer) copyDir(src, dst string) error {
    // Remove existing if force
    if i.force {
        os.RemoveAll(dst)
    }

    // Create parent directories
    if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
        return err
    }

    // Use cp -r for simplicity
    cmd := exec.Command("cp", "-r", src, dst)
    return cmd.Run()
}

func (i *Installer) copyFile(src, dst string) error {
    if i.force {
        os.Remove(dst)
    }

    if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
        return err
    }

    cmd := exec.Command("cp", src, dst)
    return cmd.Run()
}

// Remover handles plugin removal
type Remover struct {
    global  bool
    baseDir string
}

// NewRemover creates a new plugin remover
func NewRemover(opts ...InstallerOption) (*Remover, error) {
    r := &Remover{}

    // Reuse installer options
    i := &Installer{}
    for _, opt := range opts {
        opt(i)
    }
    r.global = i.global

    if r.global {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return nil, errors.Wrap(err, "failed to get home directory")
        }
        r.baseDir = filepath.Join(homeDir, ".kodelet")
    } else {
        r.baseDir = ".kodelet"
    }

    return r, nil
}

// Remove removes a plugin by name
func (r *Remover) Remove(name string) error {
    // Plugins are stored as directories under .kodelet/plugins/<name>/
    pluginPath := filepath.Join(r.baseDir, "plugins", name)

    if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
        return errors.Errorf("plugin '%s' not found", name)
    }

    if err := os.RemoveAll(pluginPath); err != nil {
        return errors.Wrap(err, "failed to remove plugin")
    }

    return nil
}

// ListPlugins returns all installed plugin names
func (r *Remover) ListPlugins() ([]string, error) {
    pluginsDir := filepath.Join(r.baseDir, "plugins")
    entries, err := os.ReadDir(pluginsDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, err
    }

    var plugins []string
    for _, entry := range entries {
        if entry.IsDir() {
            plugins = append(plugins, entry.Name())
        }
    }
    return plugins, nil
}
```

### 4. CLI Commands

Create `cmd/kodelet/plugin.go`:

```go
package main

import (
    "fmt"
    "strings"

    "github.com/jingkaihe/kodelet/pkg/plugins"
    "github.com/jingkaihe/kodelet/pkg/presenter"
    "github.com/pkg/errors"
    "github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
    Use:   "plugin",
    Short: "Manage kodelet plugins (skills and recipes)",
    Long:  `Install, list, and remove kodelet plugins from GitHub repositories.`,
}

var pluginAddCmd = &cobra.Command{
    Use:   "add <repo>[@ref]...",
    Short: "Install plugins from GitHub repositories",
    Long: `Install plugins from one or more GitHub repositories.

The repository should contain a .kodelet/ directory with:
  - .kodelet/skills/<name>/SKILL.md for skills
  - .kodelet/recipes/<name>.md for recipes

Examples:
  kodelet plugin add user/repo              # Install all plugins from repo
  kodelet plugin add user/repo1 user/repo2  # Install from multiple repos
  kodelet plugin add user/repo@v1.0.0       # Install from specific tag
  kodelet plugin add user/repo -g           # Install globally
  kodelet plugin add user/repo --force      # Overwrite existing plugins
`,
    Args: cobra.MinimumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        global, _ := cmd.Flags().GetBool("global")
        force, _ := cmd.Flags().GetBool("force")

        installer, err := plugins.NewInstaller(
            plugins.WithGlobal(global),
            plugins.WithForce(force),
        )
        if err != nil {
            return err
        }

        var allSkills, allRecipes []string
        for _, arg := range args {
            repo, ref := parseRepoRef(arg)
            presenter.Info("Installing plugins from %s...", repo)

            result, err := installer.Install(cmd.Context(), repo, ref)
            if err != nil {
                return errors.Wrapf(err, "failed to install from %s", repo)
            }

            allSkills = append(allSkills, result.Skills...)
            allRecipes = append(allRecipes, result.Recipes...)
        }

        if len(allSkills) > 0 {
            presenter.Success("Installed skills: %s", strings.Join(allSkills, ", "))
        }
        if len(allRecipes) > 0 {
            presenter.Success("Installed recipes: %s", strings.Join(allRecipes, ", "))
        }

        return nil
    },
}

var pluginListCmd = &cobra.Command{
    Use:   "list",
    Short: "List all installed plugins",
    RunE: func(cmd *cobra.Command, args []string) error {
        global, _ := cmd.Flags().GetBool("global")

        remover, err := plugins.NewRemover(plugins.WithGlobal(global))
        if err != nil {
            return err
        }

        pluginList, err := remover.ListPlugins()
        if err != nil {
            return err
        }

        if len(pluginList) == 0 {
            presenter.Info("No plugins installed")
            return nil
        }

        for _, name := range pluginList {
            fmt.Println(name)
        }

        return nil
    },
}

var pluginRemoveCmd = &cobra.Command{
    Use:   "remove <name>...",
    Short: "Remove one or more plugins",
    Long: `Remove one or more installed plugins by name.

Examples:
  kodelet plugin remove my-plugin          # Remove a single plugin
  kodelet plugin remove plugin1 plugin2    # Remove multiple plugins
  kodelet plugin remove my-plugin -g       # Remove from global directory
`,
    Args: cobra.MinimumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        global, _ := cmd.Flags().GetBool("global")

        remover, err := plugins.NewRemover(plugins.WithGlobal(global))
        if err != nil {
            return err
        }

        var removed []string
        for _, name := range args {
            if err := remover.Remove(name); err != nil {
                return errors.Wrapf(err, "failed to remove %s", name)
            }
            removed = append(removed, name)
        }

        presenter.Success("Removed plugins: %s", strings.Join(removed, ", "))
        return nil
    },
}

func parseRepoRef(arg string) (repo, ref string) {
    if idx := strings.LastIndex(arg, "@"); idx != -1 {
        return arg[:idx], arg[idx+1:]
    }
    return arg, ""
}

func init() {
    pluginAddCmd.Flags().BoolP("global", "g", false, "Install to global directory (~/.kodelet/)")
    pluginAddCmd.Flags().Bool("force", false, "Overwrite existing plugins")

    pluginListCmd.Flags().BoolP("global", "g", false, "List plugins from global directory")

    pluginRemoveCmd.Flags().BoolP("global", "g", false, "Remove from global directory")

    pluginCmd.AddCommand(pluginAddCmd)
    pluginCmd.AddCommand(pluginListCmd)
    pluginCmd.AddCommand(pluginRemoveCmd)

    rootCmd.AddCommand(pluginCmd)
}
```

### 5. Migration: Update Fragment Discovery

Update `pkg/fragments/fragments.go`:

```go
// WithDefaultDirs resets to default fragment directories
func WithDefaultDirs() Option {
    return func(fp *Processor) error {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return errors.Wrap(err, "failed to get user home directory")
        }

        fp.fragmentDirs = []string{
            ".kodelet/recipes",                              // Repo-local (canonical)
            filepath.Join(homeDir, ".kodelet", "recipes"),   // User global
        }
        return nil
    }
}
```

## CLI Interface

### Plugin Management

```bash
# Install plugins from GitHub (supports multiple repos)
kodelet plugin add user/repo1 user/repo2  # Install from multiple repos
kodelet plugin add user/repo@v1.0.0       # Install from specific tag/ref
kodelet plugin add user/repo -g           # Install to global directory
kodelet plugin add user/repo --force      # Overwrite existing plugins

# List plugins
kodelet plugin list                       # List all installed plugins
kodelet plugin list -g                    # List global plugins

# Remove plugins (supports multiple names)
kodelet plugin remove plugin1 plugin2     # Remove multiple plugins
kodelet plugin remove my-plugin -g        # Remove from global
```

## Implementation Phases

### Phase 1: Plugin Infrastructure
- [ ] Create `pkg/plugins/` package with types, discovery, installer, and remover
- [ ] Update `pkg/skills/discovery.go` to scan plugin directories
- [ ] Update `pkg/fragments/fragments.go` to scan plugin directories
- [ ] Write comprehensive tests

### Phase 2: CLI Commands
- [ ] Create `cmd/kodelet/plugin.go` with add/list/remove commands
- [ ] Remove `cmd/kodelet/skill.go` (replaced by plugin commands)

### Phase 3: Cleanup
- [ ] Remove old `kodelet recipe` commands (if any)
- [ ] Update documentation

## Consequences

### Positive
- Plugins are first-class units, easy to install and remove as a whole
- Clear provenance via naming convention (`plugin-name/skill-name`)
- No naming conflicts between plugins (namespaced by plugin name)
- Standalone skills/recipes can shadow plugin ones for local customization
- Unified CLI (`kodelet plugin add/list/remove`) for all plugin management
- Simple repo structure for plugin authors (`skills/` and `recipes/` at root)
- Supports multiple plugins from multiple repos in single command

### Negative
- Additional complexity in discovery (must scan multiple locations with prefixing)
- Breaking change for existing `kodelet skill add` users

### Neutral
- Skills and recipes maintain distinct runtime behaviors
- Built-in recipes remain embedded in binary (no prefix)
- Standalone skills/recipes still work for local development (no prefix)

## References

- [ADR 020: Agentic Skills](./020-agentic-skills.md)
- [docs/SKILLS.md](../docs/SKILLS.md)
- [docs/FRAGMENTS.md](../docs/FRAGMENTS.md)
