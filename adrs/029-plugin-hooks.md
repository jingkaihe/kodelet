# ADR 029: Plugin Hooks

## Status
Accepted (Implemented)

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

Update `kodelet plugin list` to show hook counts (consistent with skills/recipes - counts only, use `--json` for details):

```bash
$ kodelet plugin list
NAME                LOCATION  SKILLS  RECIPES  HOOKS
----                --------  ------  -------  -----
anthropic/recipes   global    0       3        0
jingkaihe/skills    local     2       0        2
my-hooks            local     0       0        3
```

Update `kodelet plugin add` output:

```bash
$ kodelet plugin add jingkaihe/hooks-collection
Installed jingkaihe/hooks-collection:
  Skills:  0
  Recipes: 0
  Hooks:   3
```

For detailed information, use `--json`:

```bash
$ kodelet plugin list --json
[
  {
    "name": "jingkaihe/skills",
    "location": "local",
    "path": "/home/user/project/.kodelet/plugins/jingkaihe@skills",
    "skills": ["pdf", "xlsx"],
    "recipes": [],
    "hooks": ["validate-output", "log-calls"]
  }
]
```

## Recipe Integration

### Design Approach

Rather than complex recipe-scoped hook binding, use a simple **payload-based filtering** approach:

1. All hooks receive `recipe_name` in their payload
2. Hooks decide whether to act based on the recipe context
3. No special syntax or separate directories needed

This keeps the hook system simple while enabling recipe-aware behavior.

### Hook Payload Context

The recipe name flows through the existing `Trigger` struct which already carries conversation context:

**1. Add `RecipeName` to `Trigger` struct** in `pkg/hooks/trigger.go`:

```go
// Trigger provides methods to invoke lifecycle hooks.
type Trigger struct {
    Manager        HookManager
    ConversationID string
    IsSubAgent     bool
    RecipeName     string  // NEW: Active recipe name, empty if none
}

// NewTrigger creates a new hook trigger with the given parameters.
func NewTrigger(manager HookManager, conversationID string, isSubAgent bool, recipeName string) Trigger {
    return Trigger{
        Manager:        manager,
        ConversationID: conversationID,
        IsSubAgent:     isSubAgent,
        RecipeName:     recipeName,
    }
}
```

**2. Add `RecipeName` to `BasePayload`** in `pkg/hooks/payload.go`:

```go
// BasePayload contains fields common to all hook payloads
type BasePayload struct {
    Event      HookType  `json:"event"`
    ConvID     string    `json:"conv_id"`
    CWD        string    `json:"cwd"`
    InvokedBy  InvokedBy `json:"invoked_by"`
    RecipeName string    `json:"recipe_name,omitempty"`  // NEW
}
```

**3. Set `RecipeName` in payload construction** (already in trigger methods):

```go
func (t Trigger) TriggerBeforeToolCall(...) (bool, string, string) {
    payload := BeforeToolCallPayload{
        BasePayload: BasePayload{
            Event:      HookTypeBeforeToolCall,
            ConvID:     t.ConversationID,
            CWD:        t.getCwd(ctx),
            InvokedBy:  t.invokedBy(),
            RecipeName: t.RecipeName,  // NEW: automatically included
        },
        // ...
    }
    // ...
}
```

**4. Set recipe name when creating thread** - the recipe name is already known at thread creation time (from `-r` flag or fragment resolution):

```go
// In cmd/kodelet/run.go or similar
trigger := hooks.NewTrigger(hookManager, convID, false, fragmentName)
```

This means **no changes to hook execution flow** - the recipe name is simply carried through existing infrastructure.

### Example: Recipe-Aware Hook

A hook that only validates for the `code-review` recipe:

```python
#!/usr/bin/env python3
import sys, json

if sys.argv[1] == "hook":
    print("before_tool_call")
elif sys.argv[1] == "run":
    payload = json.load(sys.stdin)
    recipe = payload.get("recipe_name", "")
    
    # Only act for code-review recipe
    if recipe != "code-review":
        print(json.dumps({"blocked": false}))
        sys.exit(0)
    
    # Validation logic for code-review recipe...
    tool = payload.get("tool_name", "")
    if tool == "file_edit":
        # Validate the edit...
        pass
    
    print(json.dumps({"blocked": false}))
```

### Example: Multi-Recipe Hook

A hook that behaves differently for different recipes:

```python
#!/usr/bin/env python3
import sys, json

if sys.argv[1] == "hook":
    print("turn_end")
elif sys.argv[1] == "run":
    payload = json.load(sys.stdin)
    recipe = payload.get("recipe_name", "")
    
    if recipe == "code-review":
        # Post review summary to Slack
        post_to_slack(payload)
    elif recipe == "deploy":
        # Notify ops channel
        notify_ops(payload)
    elif recipe == "":
        # No recipe - skip or do default behavior
        pass
    
    print(json.dumps({"success": true}))
```

### Plugin Structure (Simplified)

No need for separate `recipe-hooks/` directory - all hooks live in `hooks/`:

```
my-plugin/
├── hooks/
│   ├── audit-log           # Logs all operations (ignores recipe_name)
│   ├── review-validator    # Only acts when recipe_name == "code-review"
│   └── deploy-notifier     # Only acts when recipe_name == "deploy"
├── recipes/
│   ├── code-review.md
│   └── deploy.md
└── skills/
    └── ...
```

### Benefits

| Aspect | Payload Filtering | Recipe-Scoped Binding |
|--------|-------------------|----------------------|
| Complexity | Simple - one mechanism | Complex - special syntax, directories |
| Flexibility | Hook can handle multiple recipes | One hook per recipe binding |
| Discoverability | All hooks visible in `hooks/` | Split across `hooks/` and `recipe-hooks/` |
| Explicit behavior | Clear in hook code | Hidden in recipe frontmatter |

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
- [x] Update `pkg/plugins/types.go` with Hooks field
- [x] Update `pkg/plugins/discovery.go` to scan hooks directories
- [x] Update `pkg/plugins/installer.go` to report hooks
- [x] Write tests for new functionality

### Phase 2: Hook Manager Integration
- [x] Update `pkg/hooks/discovery.go` to scan plugin directories
- [x] Implement prefix-based naming for plugin hooks
- [x] Implement `GetHookByName()` for hook lookup
- [x] Update hook discovery precedence logic
- [x] Write integration tests

### Phase 3: Recipe Context in Payloads
- [x] Add `RecipeName` field to `Trigger` struct in `pkg/hooks/trigger.go`
- [x] Add `RecipeName` field to `BasePayload` struct in `pkg/hooks/payload.go`
- [x] Update `NewTrigger()` to accept recipe name parameter
- [x] Update all `Trigger*` methods to set `RecipeName` in payload
- [x] Update call sites (run.go, etc.) to pass recipe name to `NewTrigger()`
- [x] Add example hook demonstrating recipe filtering (`.kodelet/hooks/intro-logger`)
- [x] Write tests for recipe-aware hooks

### Phase 4: CLI Updates
- [x] Update `kodelet plugin list` output format
- [x] Update `kodelet plugin add` output to show hooks
- [ ] Update documentation (docs/HOOKS.md)

## Consequences

### Positive
- Hooks can be distributed alongside skills and recipes in plugins
- Recipe authors can bundle workflow-specific hooks
- Simple payload-based filtering - hooks check `recipe_name` to decide behavior
- One hook can serve multiple recipes with different logic
- Clear provenance via naming convention (`org/repo/hook-name`)
- No changes to existing hook protocol or runtime
- Standalone hooks continue to work unchanged
- All hooks discoverable in one place (`hooks/` directory)

### Negative
- Additional complexity in hook discovery (multiple directories)
- Plugin hooks require explicit `org/repo/` prefix when referenced by name
- Hooks must implement their own recipe filtering logic

### Neutral
- Script-based hooks (bash, Python) remain the primary use case
- Binary hook distribution is out of scope (can be added later if needed)
- Hook shadowing follows same precedence as skills/recipes

## Future Considerations

1. **Hook Versioning**: Track hook versions for compatibility
2. **Binary Hooks**: If needed, add `hooks/*.spec` files for downloading pre-built binaries (similar to `pkg/binaries/` pattern)
3. **Recipe-Declared Hooks**: If payload filtering proves insufficient, add `external:` field to recipe `HookConfig` for explicit binding
4. **Hook Templates**: Provide starter templates for common hook patterns (recipe filtering, logging, etc.)

## References

- [ADR 021: Agent Lifecycle Hooks](./021-agent-lifecycle-hooks.md)
- [ADR 028: Unified Plugin System](./028-unified-plugin-system.md)
- [docs/HOOKS.md](../docs/HOOKS.md)
