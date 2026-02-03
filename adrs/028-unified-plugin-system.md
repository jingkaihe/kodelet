# ADR 028: Unified Plugin System

## Status
Accepted

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
6. **Collision-Free Naming**: Use `org@repo` format for directory naming to prevent collisions between different organizations' plugins

## Architecture Overview

### Directory Structure

**Installed Plugins** (using org@repo directory naming to avoid collisions):
```
.kodelet/plugins/org@repo/
├── skills/
│   └── <skill-name>/
│       └── SKILL.md
└── recipes/
    ├── <name>.md
    └── <category>/
        └── <name>.md

~/.kodelet/plugins/org@repo/
├── skills/
│   └── <skill-name>/
│       └── SKILL.md
└── recipes/
    └── <name>.md
```

For example, `kodelet plugin add jingkaihe/skills` creates:
```
.kodelet/plugins/jingkaihe@skills/
├── skills/
│   └── my-skill/
│       └── SKILL.md
└── recipes/
    └── my-recipe.md
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

**Directory naming** uses `@` separator to avoid filesystem issues:
- `jingkaihe/skills` → stored as `jingkaihe@skills/`
- `anthropic/recipes` → stored as `anthropic@recipes/`

**User-facing names** use `/` for readability:
- Plugin names: `jingkaihe/skills`, `anthropic/recipes`
- Skill/recipe names from plugins: `jingkaihe/skills/my-skill`, `anthropic/recipes/my-recipe`

**Standalone skills/recipes** use simple names:
```
my-skill              # Skill at .kodelet/skills/my-skill/SKILL.md
my-recipe             # Recipe at .kodelet/recipes/my-recipe.md
github/pr             # Recipe at .kodelet/recipes/github/pr.md
```

**Plugin-based skills/recipes** are prefixed with org/repo:
```
jingkaihe/skills/my-skill       # Skill from plugin "jingkaihe/skills"
anthropic/recipes/my-recipe     # Recipe from plugin "anthropic/recipes"
jingkaihe/skills/workflows/deploy  # Nested recipe from plugin
```

This ensures:
- Clear provenance - you know where each skill/recipe comes from
- No naming conflicts between plugins from different organizations
- Standalone skills/recipes can shadow plugin ones (higher precedence)

**Usage examples:**
```bash
# Invoke standalone recipe
kodelet run -r github/pr

# Invoke plugin recipe
kodelet run -r jingkaihe/skills/workflows/deploy

# Model invokes plugin skill automatically
# (skill tool sees "jingkaihe/skills/pdf" in available skills)
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
.kodelet/plugins/user@my-plugin-repo/
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

Common types in `pkg/plugins/types.go`:

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

// InstalledPlugin represents an installed plugin package
type InstalledPlugin struct {
    Name    string   // User-facing name (org/repo format)
    Dir     string   // Directory path
    Skills  []string // Skill names in this plugin
    Recipes []string // Recipe names in this plugin
}
```

### 2. Directory Naming Helpers

Helper functions in `pkg/plugins/installer.go`:

```go
// repoToPluginName converts "org/repo" to "org@repo" for filesystem storage
func repoToPluginName(repo string) string {
    return strings.Replace(repo, "/", "@", 1)
}

// pluginNameToPrefix converts "org@repo" to "org/repo/" for skill/recipe prefixing
func pluginNameToPrefix(pluginName string) string {
    return strings.Replace(pluginName, "@", "/", 1) + "/"
}

// pluginNameToRepo converts "org@repo" to "org/repo" for user-facing display
func pluginNameToRepo(pluginName string) string {
    return strings.Replace(pluginName, "@", "/", 1)
}
```

### 3. Unified Discovery

The `pkg/plugins/discovery.go` handles plugin discovery:

```go
// DiscoverAll discovers all plugins (skills and recipes) with proper naming
func (d *Discovery) DiscoverAll() ([]Plugin, error) {
    var plugins []Plugin
    seen := make(map[string]bool)

    // 1. Standalone skills/recipes (highest precedence, no prefix)
    // 2. Repo-local plugins (prefixed with org/repo/)
    // 3. Global standalone
    // 4. Global plugins
    // ...
}

// ListInstalledPlugins returns installed plugin packages
func (d *Discovery) ListInstalledPlugins(global bool) ([]InstalledPlugin, error) {
    // Scans plugins directory, converts org@repo dirs to org/repo names
    // ...
}
```

### 4. Unified Installer

The `pkg/plugins/installer.go` handles installation and removal:

```go
// Install installs plugins from a GitHub repository
func (i *Installer) Install(ctx context.Context, repo string, ref string) (*InstallResult, error) {
    // 1. Clone repo via gh CLI
    // 2. Convert repo name to org@repo directory format
    // 3. Copy skills/ and recipes/ to target directory
    // ...
}

// Remove removes a plugin by name (accepts org/repo format)
func (r *Remover) Remove(name string) error {
    // Convert org/repo to org@repo for filesystem lookup
    pluginName := repoToPluginName(name)
    // ...
}

// ListPlugins returns all installed plugin names in org/repo format
func (r *Remover) ListPlugins() ([]string, error) {
    // Read directory entries (org@repo format)
    // Convert to org/repo format for user display
    // ...
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

# List all installed plugins (always shows both local and global)
kodelet plugin list

# Remove plugins (supports multiple names, use org/repo format)
kodelet plugin remove user/repo1 user/repo2  # Remove multiple plugins
kodelet plugin remove user/repo -g           # Remove from global
```

Example output of `kodelet plugin list`:
```
NAME                LOCATION  SKILLS              RECIPES
----                --------  ------              -------
anthropic/recipes   global    0                   pr, commit, init
jingkaihe/skills    local     pdf, xlsx, docx     0
```

## Implementation Phases

### Phase 1: Plugin Infrastructure ✅
- [x] Create `pkg/plugins/` package with types, discovery, installer, and remover
- [x] Update `pkg/skills/discovery.go` to scan plugin directories
- [x] Update `pkg/fragments/fragments.go` to scan plugin directories
- [x] Write comprehensive tests

### Phase 2: CLI Commands ✅
- [x] Create `cmd/kodelet/plugin.go` with add/list/remove commands
- [x] Remove `cmd/kodelet/skill.go` (replaced by plugin commands)

### Phase 3: Documentation ✅
- [x] Update AGENTS.md documentation
- [x] Update docs/SKILLS.md
- [x] Update docs/FRAGMENTS.md

## Consequences

### Positive
- Plugins are first-class units, easy to install and remove as a whole
- Clear provenance via naming convention (`org/repo/skill-name`)
- No naming conflicts between plugins from different organizations (via org@repo directory naming)
- Standalone skills/recipes can shadow plugin ones for local customization
- Unified CLI (`kodelet plugin add/list/remove`) for all plugin management
- Simple repo structure for plugin authors (`skills/` and `recipes/` at root)
- Supports multiple plugins from multiple repos in single command
- `plugin list` always shows all plugins (local and global) for clarity

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
