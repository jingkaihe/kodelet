# ADR 029: Plugin Hooks

## Status
Proposed

## Context

Kodelet has three extensibility mechanisms:

1. **Skills** (`pkg/skills/`): Model-invoked capabilities
2. **Recipes/Fragments** (`pkg/fragments/`): User-invoked templates
3. **Hooks** (`pkg/hooks/`): Agent lifecycle interceptors

ADR 028 unified skills and recipes under the plugin system, allowing installation via `kodelet plugin add`. However, hooks remain outside this system and can only be installed manually by placing executables in `.kodelet/hooks/` or `~/.kodelet/hooks/`.

**Problem Statement:**
- Hooks cannot be distributed via the plugin system
- Recipe authors cannot bundle hooks with their recipes
- No mechanism to share reusable hooks across projects
- Manual hook installation is error-prone and undiscoverable

**Current Hook System:**

Hooks are external executables that implement a simple protocol:
- `./hook hook` → Returns hook type (e.g., `before_tool_call`, `turn_end`)
- `echo '{"payload":...}' | ./hook run` → Executes with JSON stdin, returns JSON stdout

Hooks are typically bash or Python scripts, making them inherently cross-platform. Binary-based hooks are rare in practice since scripted hooks cover most use cases.

**Goals:**
1. Allow hooks to be distributed via the plugin system
2. Enable recipe authors to bundle hooks with their recipes
3. Maintain the simple executable protocol (no changes to hook runtime)
4. Support script-based hooks (bash, Python) as the primary use case

## Decision

Extend the unified plugin system to support hooks as a third plugin component alongside skills and recipes.

### Key Design Decisions

1. **Simple Extension**: Add `hooks/` directory to plugin structure, parallel to `skills/` and `recipes/`
2. **Reuse Existing Protocol**: Plugin hooks use the same executable protocol as standalone hooks
3. **No Binary Management**: Script-based hooks (bash, Python) are the primary target; complex binary distribution is out of scope
4. **Discovery Integration**: Extend `HookManager` to scan plugin directories during hook discovery
5. **Unified CLI**: `kodelet plugin add/list/remove` handles hooks automatically

## Architecture Overview

### Directory Structure

**Plugin Repository Structure:**
```
my-plugin-repo/
├── skills/
│   └── my-skill/
│       └── SKILL.md
├── recipes/
│   └── my-recipe.md
└── hooks/                  # NEW
    ├── validate-output     # Bash script
    ├── log-tool-calls      # Python script
    └── custom-handler      # Any executable
```

**Installed Plugin Structure:**
```
.kodelet/plugins/org@repo/
├── skills/
├── recipes/
└── hooks/                  # NEW
    ├── validate-output
    ├── log-tool-calls
    └── custom-handler

~/.kodelet/plugins/org@repo/
├── skills/
├── recipes/
└── hooks/                  # NEW
```

**Standalone Hooks** (unchanged):
```
.kodelet/hooks/
└── my-local-hook

~/.kodelet/hooks/
└── my-global-hook
```

### Discovery Precedence

Hook discovery follows the same precedence pattern as skills and recipes:

1. Repo-local standalone (`.kodelet/hooks/`) - highest
2. Repo-local plugins (`.kodelet/plugins/*/hooks/`)
3. User-global standalone (`~/.kodelet/hooks/`)
4. User-global plugins (`~/.kodelet/plugins/*/hooks/`) - lowest

When hooks have the same name, higher precedence hooks shadow lower ones.

### Hook Naming

**Standalone hooks** use simple names:
```
my-hook              # Hook at .kodelet/hooks/my-hook
```

**Plugin hooks** are prefixed with org/repo:
```
jingkaihe/skills/validate-output    # Hook from plugin "jingkaihe/skills"
anthropic/recipes/log-tool-calls    # Hook from plugin "anthropic/recipes"
```

This ensures clear provenance and prevents naming conflicts between plugins.

## Implementation Design

### 1. Extend Plugin Types

Update `pkg/plugins/types.go`:

```go
// InstalledPlugin represents an installed plugin package
type InstalledPlugin struct {
    Name    string   // User-facing name (org/repo format)
    Path    string   // Directory path
    Skills  []string // Skill names in this plugin
    Recipes []string // Recipe names in this plugin
    Hooks   []string // Hook names in this plugin (NEW)
}
```

### 2. Extend Plugin Scanner

Update `pkg/plugins/scanner.go`:

```go
// ScanPluginSubdirs scans a plugins directory for skill, recipe, and hook subdirectories
func ScanPluginSubdirs(baseDir string) (skills, recipes, hooks []PluginDirConfig, err error) {
    entries, err := os.ReadDir(baseDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil, nil, nil
        }
        return nil, nil, nil, err
    }

    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }

        pluginDir := filepath.Join(baseDir, entry.Name())
        prefix := pluginNameToPrefix(entry.Name())

        // Check for skills/ subdirectory
        if skillsDir := filepath.Join(pluginDir, "skills"); dirExists(skillsDir) {
            skills = append(skills, PluginDirConfig{Dir: skillsDir, Prefix: prefix})
        }

        // Check for recipes/ subdirectory
        if recipesDir := filepath.Join(pluginDir, "recipes"); dirExists(recipesDir) {
            recipes = append(recipes, PluginDirConfig{Dir: recipesDir, Prefix: prefix})
        }

        // Check for hooks/ subdirectory (NEW)
        if hooksDir := filepath.Join(pluginDir, "hooks"); dirExists(hooksDir) {
            hooks = append(hooks, PluginDirConfig{Dir: hooksDir, Prefix: prefix})
        }
    }

    return skills, recipes, hooks, nil
}
```

### 3. Extend Hook Manager

Update `pkg/hooks/manager.go`:

```go
// Discover scans all hook directories and registers discovered hooks
func (m *HookManager) Discover(ctx context.Context) error {
    dirs := m.getHookDirectories()

    for _, dir := range dirs {
        if err := m.discoverFromDirectory(ctx, dir.Path, dir.Prefix); err != nil {
            logger.G(ctx).WithError(err).WithField("dir", dir.Path).Warn("Failed to discover hooks")
        }
    }

    return nil
}

// HookDirectory represents a directory to scan for hooks
type HookDirectory struct {
    Path   string
    Prefix string // Empty for standalone, "org/repo/" for plugins
}

// getHookDirectories returns all directories to scan for hooks in precedence order
func (m *HookManager) getHookDirectories() []HookDirectory {
    var dirs []HookDirectory

    // 1. Repo-local standalone (highest precedence)
    dirs = append(dirs, HookDirectory{
        Path:   filepath.Join(m.repoRoot, ".kodelet", "hooks"),
        Prefix: "",
    })

    // 2. Repo-local plugins
    pluginsDir := filepath.Join(m.repoRoot, ".kodelet", "plugins")
    if _, _, hooks, err := plugins.ScanPluginSubdirs(pluginsDir); err == nil {
        for _, h := range hooks {
            dirs = append(dirs, HookDirectory{Path: h.Dir, Prefix: h.Prefix})
        }
    }

    // 3. Global standalone
    dirs = append(dirs, HookDirectory{
        Path:   filepath.Join(m.homeDir, ".kodelet", "hooks"),
        Prefix: "",
    })

    // 4. Global plugins (lowest precedence)
    globalPluginsDir := filepath.Join(m.homeDir, ".kodelet", "plugins")
    if _, _, hooks, err := plugins.ScanPluginSubdirs(globalPluginsDir); err == nil {
        for _, h := range hooks {
            dirs = append(dirs, HookDirectory{Path: h.Dir, Prefix: h.Prefix})
        }
    }

    return dirs
}

// discoverFromDirectory scans a directory for hook executables
func (m *HookManager) discoverFromDirectory(ctx context.Context, dir, prefix string) error {
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil
        }
        return err
    }

    for _, entry := range entries {
        if entry.IsDir() {
            continue
        }

        hookPath := filepath.Join(dir, entry.Name())
        hookName := prefix + entry.Name()

        // Skip if already discovered (higher precedence)
        if m.hasHook(hookName) {
            continue
        }

        // Check if executable
        if !isExecutable(hookPath) {
            continue
        }

        // Query hook type via protocol
        hookType, err := m.queryHookType(ctx, hookPath)
        if err != nil {
            logger.G(ctx).WithError(err).WithField("hook", hookName).Debug("Failed to query hook type")
            continue
        }

        m.registerHook(&Hook{
            Name:     hookName,
            Path:     hookPath,
            HookType: hookType,
        })
    }

    return nil
}
```

### 4. Update Plugin Installer

Update `pkg/plugins/installer.go` to report hooks:

```go
// InstallResult contains the result of a plugin installation
type InstallResult struct {
    Name    string
    Path    string
    Skills  []string
    Recipes []string
    Hooks   []string // NEW
}

// Install installs plugins from a GitHub repository
func (i *Installer) Install(ctx context.Context, repo string, ref string) (*InstallResult, error) {
    // ... existing clone logic ...

    result := &InstallResult{
        Name: repo,
        Path: targetDir,
    }

    // Scan for skills
    if skillsDir := filepath.Join(targetDir, "skills"); dirExists(skillsDir) {
        result.Skills = scanSkillNames(skillsDir)
    }

    // Scan for recipes
    if recipesDir := filepath.Join(targetDir, "recipes"); dirExists(recipesDir) {
        result.Recipes = scanRecipeNames(recipesDir)
    }

    // Scan for hooks (NEW)
    if hooksDir := filepath.Join(targetDir, "hooks"); dirExists(hooksDir) {
        result.Hooks = scanHookNames(hooksDir)
    }

    return result, nil
}

// scanHookNames returns names of hooks in a directory
func scanHookNames(dir string) []string {
    var names []string
    entries, err := os.ReadDir(dir)
    if err != nil {
        return names
    }

    for _, entry := range entries {
        if entry.IsDir() {
            continue
        }
        // Check if executable
        path := filepath.Join(dir, entry.Name())
        if isExecutable(path) {
            names = append(names, entry.Name())
        }
    }
    return names
}
```

### 5. Update CLI Output

Update `kodelet plugin list` to show hooks:

```bash
$ kodelet plugin list
NAME                LOCATION  SKILLS         RECIPES           HOOKS
----                --------  ------         -------           -----
anthropic/recipes   global    0              pr, commit, init  0
jingkaihe/skills    local     pdf, xlsx      0                 validate-output, log-calls
my-hooks            local     0              0                 pre-commit, post-run
```

Update `kodelet plugin add` output:

```bash
$ kodelet plugin add jingkaihe/hooks-collection
Installed jingkaihe/hooks-collection:
  Skills:  0
  Recipes: 0
  Hooks:   3 (validate-output, log-tool-calls, audit-trail)
```

## Example Plugin with Hooks

A plugin repository for a code review workflow:

```
code-review-plugin/
├── skills/
│   └── code-review/
│       └── SKILL.md           # Code review expertise
├── recipes/
│   └── review.md              # Review workflow recipe
└── hooks/
    ├── validate-diff          # Validates diff before review (bash)
    └── post-review-slack      # Posts review summary to Slack (Python)
```

`hooks/validate-diff`:
```bash
#!/bin/bash

case "$1" in
    hook)
        echo "before_tool_call"
        ;;
    run)
        # Read payload from stdin
        payload=$(cat)
        tool_name=$(echo "$payload" | jq -r '.tool_name')
        
        # Only validate for file_edit tool
        if [ "$tool_name" = "file_edit" ]; then
            # Validation logic...
            echo '{"blocked": false}'
        else
            echo '{"blocked": false}'
        fi
        ;;
esac
```

`hooks/post-review-slack` (Python):
```python
#!/usr/bin/env python3
import sys
import json

if sys.argv[1] == "hook":
    print("turn_end")
elif sys.argv[1] == "run":
    payload = json.load(sys.stdin)
    # Post to Slack...
    print(json.dumps({"success": True}))
```

## Implementation Phases

### Phase 1: Core Infrastructure
- [ ] Update `pkg/plugins/types.go` with Hooks field
- [ ] Update `pkg/plugins/scanner.go` to scan hooks directories
- [ ] Update `pkg/plugins/installer.go` to report hooks
- [ ] Write tests for new functionality

### Phase 2: Hook Manager Integration
- [ ] Update `pkg/hooks/manager.go` to scan plugin directories
- [ ] Implement prefix-based naming for plugin hooks
- [ ] Update hook discovery precedence logic
- [ ] Write integration tests

### Phase 3: CLI Updates
- [ ] Update `kodelet plugin list` output format
- [ ] Update `kodelet plugin add` output to show hooks
- [ ] Update documentation

## Consequences

### Positive
- Hooks can be distributed alongside skills and recipes in plugins
- Recipe authors can bundle workflow-specific hooks
- Clear provenance via naming convention (`org/repo/hook-name`)
- No changes to existing hook protocol or runtime
- Standalone hooks continue to work unchanged
- Simple implementation leveraging existing plugin infrastructure

### Negative
- Additional complexity in hook discovery (multiple directories)
- Plugin hooks require explicit `org/repo/` prefix when referenced by name

### Neutral
- Script-based hooks (bash, Python) remain the primary use case
- Binary hook distribution is out of scope (can be added later if needed)
- Hook shadowing follows same precedence as skills/recipes

## Future Considerations

1. **Hook Dependencies**: Recipes could declare required hooks in frontmatter
2. **Hook Versioning**: Track hook versions for compatibility
3. **Binary Hooks**: If needed, add `hooks/*.spec` files for downloading pre-built binaries (similar to `pkg/binaries/` pattern)

## References

- [ADR 021: Agent Lifecycle Hooks](./021-agent-lifecycle-hooks.md)
- [ADR 028: Unified Plugin System](./028-unified-plugin-system.md)
- [docs/HOOKS.md](../docs/HOOKS.md)
