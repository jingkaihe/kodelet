# ADR 022: Configurable System Prompts

## Status
Proposed

## Context

Kodelet currently uses a hardcoded system prompt rendered from embedded Go templates (`pkg/sysprompt/templates/system.tmpl`). While this provides consistency, users have legitimate needs to customize the system prompt for:

1. **Domain-specific use cases**: Tailoring agent behavior for specific workflows (e.g., code review focus, documentation writing, security auditing)
2. **Organizational policies**: Enforcing company-specific coding standards or compliance requirements
3. **Experimentation**: Testing different prompting strategies without modifying source code
4. **Dynamic context**: Injecting runtime information (current date, environment variables, project metadata)

**Problem Statement:**
- System prompts are currently immutable at runtime
- Users cannot customize agent behavior without forking the codebase
- No mechanism for injecting dynamic variables into system prompts
- The existing fragments system provides a template model that could be reused

**Goals:**
1. Allow users to specify custom system prompt templates via CLI flag
2. Support dynamic variable substitution (date, time, environment, bash output)
3. Enable reusing fragments/recipes as system prompts
4. Maintain backward compatibility with default prompts

## Decision

Introduce configurable system prompts through a `--system-prompt` (`-sp`) CLI flag that accepts a path to a template file. The template system will reuse the existing fragments infrastructure for variable substitution and bash command execution.

### Key Design Decisions

1. **File-based templates**: Custom system prompts are specified as file paths, not inline strings
2. **Unified template engine**: Reuse fragments' template functions (`bash`, `default`, etc.)
3. **Fragment compatibility**: Allow using existing fragments as system prompts via `--system-prompt-recipe` flag
4. **Variable injection**: Support a standard set of dynamic variables plus custom arguments
5. **Explicit failure**: Invalid custom prompts fail immediately with clear error messages (no silent fallback)

## Architecture Overview

### Template Variables

Custom system prompt templates have access to existing `PromptContext` fields plus user-provided arguments:

**Built-in (from existing PromptContext):**

| Variable | Description | Example |
|----------|-------------|---------|
| `{{.Date}}` | Current date (YYYY-MM-DD) | `2025-01-15` |
| `{{.WorkingDirectory}}` | Current working directory | `/home/user/project` |
| `{{.Platform}}` | Operating system | `linux` |
| `{{.OSVersion}}` | OS version string | `Linux 6.17.12-300.fc43.x86_64` |
| `{{.IsGitRepo}}` | Whether in a git repository | `true` |

**User-provided (via `--sp-arg`):**

Any custom key-value pairs passed via `--sp-arg key=value` are accessible as `{{.key}}`.

**Dynamic values via `bash` function:**

For values not in PromptContext, use the `bash` template function:

```
Current branch: {{bash "git" "rev-parse" "--abbrev-ref" "HEAD"}}
Current user: {{bash "whoami"}}
Hostname: {{bash "hostname"}}
```

### Template Functions

Reused from the fragments package:

| Function | Description | Example |
|----------|-------------|---------|
| `{{bash "cmd" "args"}}` | Execute bash command, return output | `{{bash "git" "rev-parse" "--short" "HEAD"}}` |
| `{{default .Value "fallback"}}` | Provide default for missing value | `{{default .Project "unknown"}}` |
| `{{env "VAR"}}` | Get environment variable | `{{env "GITHUB_REPOSITORY"}}` |

### CLI Interface

```bash
# Use custom system prompt template
kodelet run --system-prompt ./prompts/code-reviewer.md "review this PR"
kodelet run -sp ./prompts/security-audit.md "analyze security"

# Use fragment/recipe as system prompt
kodelet run --system-prompt-recipe code-review "review changes"
kodelet run -spr security-audit "check for vulnerabilities"

# Pass custom arguments to template
kodelet run -sp ./prompts/custom.md --sp-arg project=myapp --sp-arg version=1.0 "query"

# Combine with regular fragments (system prompt + user prompt fragment)
kodelet run -sp ./prompts/strict.md -r commit "commit changes"
```

### Template File Format

Custom system prompt templates use Markdown with optional YAML frontmatter:

```markdown
---
name: code-reviewer
description: Focused code review system prompt
defaults:
  language: go
  strictness: high
---
# Code Review Agent

You are a code review specialist focusing on {{default .language "go"}} code.

## Current Context
- Date: {{.Date}}
- Repository: {{.WorkingDirectory}}
- Git Branch: {{bash "git" "rev-parse" "--abbrev-ref" "HEAD"}}
- User: {{bash "whoami"}}

## Review Guidelines
- Strictness level: {{default .strictness "medium"}}
- Focus on: correctness, performance, security, readability

## Instructions
Review code changes thoroughly. Point out:
1. Potential bugs or logic errors
2. Performance concerns
3. Security vulnerabilities
4. Style and readability issues
```

## Implementation Design

### 1. Extend PromptContext

Update `pkg/sysprompt/context.go` to add only `CustomArgs`:

```go
type PromptContext struct {
    // Existing fields (unchanged)
    WorkingDirectory string
    IsGitRepo        bool
    Platform         string
    OSVersion        string
    Date             string
    // ... other existing fields ...
    
    // New field for custom system prompts
    CustomArgs map[string]string // User-provided arguments via --sp-arg
}
```

### 2. Create Custom Prompt Renderer

Add `pkg/sysprompt/custom.go`:

```go
package sysprompt

import (
    "bytes"
    "context"
    "os"
    "os/exec"
    "strings"
    "text/template"
    
    "github.com/pkg/errors"
    "github.com/yuin/goldmark"
    meta "github.com/yuin/goldmark-meta"
    "github.com/yuin/goldmark/parser"
)

// CustomPromptConfig holds configuration for custom system prompts
type CustomPromptConfig struct {
    TemplatePath    string            // Path to custom template file
    RecipeName      string            // Name of fragment/recipe to use as system prompt
    Arguments       map[string]string // Custom arguments for template
}

// CustomPromptRenderer handles rendering of custom system prompt templates
type CustomPromptRenderer struct {
    fragmentDirs []string
}

// NewCustomPromptRenderer creates a new custom prompt renderer
func NewCustomPromptRenderer(fragmentDirs []string) *CustomPromptRenderer {
    return &CustomPromptRenderer{fragmentDirs: fragmentDirs}
}

// RenderCustomPrompt renders a custom system prompt template
func (r *CustomPromptRenderer) RenderCustomPrompt(ctx context.Context, config *CustomPromptConfig, promptCtx *PromptContext) (string, error) {
    var templateContent string
    var defaults map[string]string
    
    if config.TemplatePath != "" {
        content, meta, err := r.loadTemplateFile(config.TemplatePath)
        if err != nil {
            return "", errors.Wrap(err, "failed to load custom system prompt template")
        }
        templateContent = content
        defaults = meta.Defaults
    } else if config.RecipeName != "" {
        content, meta, err := r.loadFromRecipe(config.RecipeName)
        if err != nil {
            return "", errors.Wrap(err, "failed to load recipe as system prompt")
        }
        templateContent = content
        defaults = meta.Defaults
    } else {
        return "", errors.New("either template path or recipe name must be specified")
    }
    
    // Merge defaults with provided arguments (provided args take precedence)
    mergedArgs := make(map[string]string)
    for k, v := range defaults {
        mergedArgs[k] = v
    }
    for k, v := range config.Arguments {
        mergedArgs[k] = v
    }
    
    // Set CustomArgs on the context
    promptCtx.CustomArgs = mergedArgs
    
    // Create template with custom functions
    tmpl, err := template.New("custom-sysprompt").Funcs(template.FuncMap{
        "bash":    r.createBashFunc(ctx),
        "default": r.createDefaultFunc(),
        "env":     os.Getenv,
    }).Parse(templateContent)
    if err != nil {
        return "", errors.Wrap(err, "failed to parse custom system prompt template")
    }
    
    // Build template data from PromptContext + CustomArgs
    data := r.buildTemplateData(promptCtx)
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", errors.Wrap(err, "failed to execute custom system prompt template")
    }
    
    return buf.String(), nil
}

// buildTemplateData combines PromptContext fields with CustomArgs into a flat map
func (r *CustomPromptRenderer) buildTemplateData(ctx *PromptContext) map[string]interface{} {
    data := map[string]interface{}{
        "Date":             ctx.Date,
        "WorkingDirectory": ctx.WorkingDirectory,
        "Platform":         ctx.Platform,
        "OSVersion":        ctx.OSVersion,
        "IsGitRepo":        ctx.IsGitRepo,
    }
    
    // Merge custom args into the data map (allows {{.myarg}} access)
    for k, v := range ctx.CustomArgs {
        data[k] = v
    }
    
    return data
}

// createBashFunc creates the bash template function
func (r *CustomPromptRenderer) createBashFunc(ctx context.Context) func(string, ...string) string {
    return func(cmd string, args ...string) string {
        execCmd := exec.CommandContext(ctx, cmd, args...)
        output, err := execCmd.Output()
        if err != nil {
            return ""
        }
        return strings.TrimSpace(string(output))
    }
}

// createDefaultFunc creates the default template function
func (r *CustomPromptRenderer) createDefaultFunc() func(interface{}, string) interface{} {
    return func(value interface{}, defaultValue string) interface{} {
        if value == nil {
            return defaultValue
        }
        if str, ok := value.(string); ok && str == "" {
            return defaultValue
        }
        return value
    }
}

// loadTemplateFile loads and parses a template file with optional frontmatter
func (r *CustomPromptRenderer) loadTemplateFile(path string) (string, *TemplateMetadata, error) {
    content, err := os.ReadFile(path)
    if err != nil {
        return "", nil, errors.Wrap(err, "failed to read template file")
    }
    
    return r.parseTemplateContent(string(content))
}

// loadFromRecipe loads a fragment/recipe and extracts its content for use as system prompt
func (r *CustomPromptRenderer) loadFromRecipe(name string) (string, *TemplateMetadata, error) {
    // Search fragment directories for the recipe
    for _, dir := range r.fragmentDirs {
        path := filepath.Join(dir, name+".md")
        if _, err := os.Stat(path); err == nil {
            return r.loadTemplateFile(path)
        }
    }
    return "", nil, errors.Errorf("recipe '%s' not found", name)
}

// parseTemplateContent parses template content with optional YAML frontmatter
func (r *CustomPromptRenderer) parseTemplateContent(content string) (string, *TemplateMetadata, error) {
    md := goldmark.New(
        goldmark.WithExtensions(meta.Meta),
    )
    
    var buf bytes.Buffer
    pctx := parser.NewContext()
    
    if err := md.Convert([]byte(content), &buf, parser.WithContext(pctx)); err != nil {
        return "", nil, errors.Wrap(err, "failed to parse markdown")
    }
    
    metadata := &TemplateMetadata{}
    if metaData := meta.Get(pctx); metaData != nil {
        if defaults, ok := metaData["defaults"].(map[string]interface{}); ok {
            metadata.Defaults = make(map[string]string)
            for k, v := range defaults {
                if str, ok := v.(string); ok {
                    metadata.Defaults[k] = str
                }
            }
        }
    }
    
    // Extract body content (after frontmatter)
    bodyContent := extractBodyContent(content)
    
    return bodyContent, metadata, nil
}

// TemplateMetadata holds parsed frontmatter from custom templates
type TemplateMetadata struct {
    Name        string
    Description string
    Defaults    map[string]string
}

// extractBodyContent removes YAML frontmatter and returns the body
func extractBodyContent(content string) string {
    if !strings.HasPrefix(content, "---") {
        return content
    }
    
    lines := strings.Split(content, "\n")
    frontmatterEnd := -1
    
    for i := 1; i < len(lines); i++ {
        if strings.TrimSpace(lines[i]) == "---" {
            frontmatterEnd = i
            break
        }
    }
    
    if frontmatterEnd == -1 {
        return content
    }
    
    return strings.TrimSpace(strings.Join(lines[frontmatterEnd+1:], "\n"))
}
```

### 3. Update System Prompt Generation

Modify `pkg/sysprompt/system.go` to handle custom prompts internally:

```go
// SystemPrompt generates a system prompt, using custom template if configured in config
func SystemPrompt(ctx context.Context, model string, config *llmtypes.Config, contexts []types.AgentContext) (string, error) {
    // If custom prompt is configured, use it
    if config.CustomPrompt != nil && (config.CustomPrompt.TemplatePath != "" || config.CustomPrompt.RecipeName != "") {
        promptConfig := NewPromptConfig(model)
        promptCtx := NewPromptContext(config, contexts, promptConfig)
        renderer := NewCustomPromptRenderer(getFragmentDirs())
        return renderer.RenderCustomPrompt(ctx, config.CustomPrompt, promptCtx)
    }
    
    // Default behavior - render from embedded templates
    promptConfig := NewPromptConfig(model)
    promptCtx := NewPromptContext(config, contexts, promptConfig)
    return defaultRenderer.RenderPrompt("templates/system.tmpl", promptCtx)
}

// SubAgentPrompt generates a subagent system prompt, using custom template if configured
func SubAgentPrompt(ctx context.Context, model string, config *llmtypes.Config, contexts []types.AgentContext) (string, error) {
    // If custom prompt is configured, use it (same template for subagent)
    if config.CustomPrompt != nil && (config.CustomPrompt.TemplatePath != "" || config.CustomPrompt.RecipeName != "") {
        promptConfig := NewPromptConfig(model)
        promptCtx := NewPromptContext(config, contexts, promptConfig)
        renderer := NewCustomPromptRenderer(getFragmentDirs())
        return renderer.RenderCustomPrompt(ctx, config.CustomPrompt, promptCtx)
    }
    
    // Default behavior - render subagent template
    promptConfig := NewPromptConfig(model)
    promptCtx := NewPromptContext(config, contexts, promptConfig)
    return defaultRenderer.RenderPrompt("templates/subagent.tmpl", promptCtx)
}
```

The LLM clients remain unchanged - they just call `SystemPrompt` or `SubAgentPrompt`:

```go
// In pkg/llm/anthropic/anthropic.go (and other clients)
func (t *Thread) Run(ctx context.Context, input string, handler ...types.ResponseHandler) error {
    // ...
    var systemPrompt string
    var err error
    if t.config.IsSubAgent {
        systemPrompt, err = sysprompt.SubAgentPrompt(ctx, string(model), t.config, contexts)
    } else {
        systemPrompt, err = sysprompt.SystemPrompt(ctx, string(model), t.config, contexts)
    }
    if err != nil {
        return errors.Wrap(err, "failed to generate system prompt")
    }
    // ...
}
```

**Note**: This changes the function signatures to accept `context.Context` and return `error`. Existing callers will need to be updated.

### 4. CLI Integration

Update `cmd/kodelet/run.go` and `cmd/kodelet/chat.go`:

```go
func init() {
    // System prompt flags
    runCmd.Flags().StringP("system-prompt", "sp", "", "Path to custom system prompt template")
    runCmd.Flags().StringP("system-prompt-recipe", "spr", "", "Use fragment/recipe as system prompt")
    runCmd.Flags().StringArray("sp-arg", []string{}, "Arguments for system prompt template (key=value)")
}

func parseSystemPromptArgs(args []string) map[string]string {
    result := make(map[string]string)
    for _, arg := range args {
        parts := strings.SplitN(arg, "=", 2)
        if len(parts) == 2 {
            result[parts[0]] = parts[1]
        }
    }
    return result
}
```

## Alternatives Considered

### Fragment Integration Only

**Description**: Only allow using fragments as system prompts, no standalone templates.

**Pros**:
- Maximum code reuse
- Consistent user experience

**Cons**:
- Fragments are designed for user prompts, not system prompts
- May contain irrelevant metadata fields
- Confusing mental model

**Decision**: Partially adopted - we support fragments as system prompts BUT also support standalone templates for clarity.

## Implementation Phases

### Phase 1: Core Template Infrastructure (Week 1)
- [ ] Add `CustomArgs map[string]string` field to `PromptContext`
- [ ] Add `CustomPrompt` field to `llmtypes.Config`
- [ ] Create `CustomPromptRenderer` with template functions
- [ ] Update `SystemPrompt` and `SubAgentPrompt` signatures to accept `context.Context` and return `error`
- [ ] Write unit tests for template rendering

### Phase 2: CLI Integration (Week 1-2)
- [ ] Add `--system-prompt` and `--system-prompt-recipe` flags
- [ ] Add `--sp-arg` flag for custom arguments
- [ ] Update flag parsing in `run.go` and `chat.go`
- [ ] Update LLM client callers to handle error returns
- [ ] Write integration tests

### Phase 3: Documentation (Week 2)
- [ ] Create `docs/CUSTOM-PROMPTS.md` guide
- [ ] Add examples to `config.sample.yaml`
- [ ] Update `docs/MANUAL.md` with CLI usage
- [ ] Create example templates in `examples/prompts/`

## Security Considerations

1. **Bash Command Execution**: The `bash` template function allows arbitrary command execution. This is consistent with the existing fragments system and appropriate since users control their own templates.

2. **File Access**: Custom prompts can only read files accessible to the running user. No privilege escalation is possible.

3. **Environment Variables**: The `env` function exposes environment variables. Users should be aware of this when sharing templates.

## Conclusion

This ADR proposes a flexible system for custom system prompts that:

1. **Reuses Existing Infrastructure**: Leverages the fragments template engine for consistency
2. **Provides Multiple Entry Points**: File path, recipe name, or fragment reference
3. **Supports Dynamic Content**: Built-in variables and bash command execution
4. **Maintains Backward Compatibility**: Default prompts unchanged when flags not used
5. **Enables Experimentation**: Users can iterate on prompts without code changes

The design follows Kodelet's existing patterns (fragments, skills) while providing the flexibility users need for domain-specific customization.
