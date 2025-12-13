# ADR 020: Agentic Skills

## Status
Proposed

## Context

Kodelet currently supports fragments/recipes which are user-invoked templates with variable substitution. However, there is a need for **model-invoked** capabilities where the agent autonomously decides when to use specialized functionality based on the user's request.

**Problem Statement:**
- Users often need specialized domain knowledge for certain tasks (PDF handling, spreadsheet manipulation, etc.)
- Currently, users must manually invoke recipes or provide detailed instructions
- The model cannot autonomously discover and use specialized capabilities
- There's no mechanism for packaging expertise into discoverable units

**Goals:**
1. Enable the agent to autonomously invoke specialized capabilities when relevant
2. Provide a simple packaging format for domain expertise (similar to fragments)
3. Support both repository-local and user-global skills
4. Allow fine-grained configuration of available skills

## Decision

Introduce **Agentic Skills** - a model-invoked capability system where skills are discovered at startup and exposed as a tool. The agent autonomously decides when to invoke skills based on the task context and skill descriptions.

Key design decisions:
1. Skills are packaged as directories containing a `SKILL.md` file with YAML frontmatter
2. Discovery follows the same pattern as fragments (`.kodelet/skills/` in repo and home)
3. Skills are exposed through a single `skill` tool that the model calls with a skill name
4. The `SKILL.md` content is injected into the assistant-facing response when invoked
5. Configuration supports allowlisting and global enable/disable

## Architecture Overview

### Skill Structure

Each skill is a directory containing at minimum a `SKILL.md` file:

```
my-skill/
├── SKILL.md (required)
├── reference.md (optional documentation)
├── examples.md (optional examples)
├── scripts/
│   └── helper.py (optional utility)
└── templates/
    └── template.txt (optional template)
```

### Discovery Locations

Skills are discovered from two locations with the following precedence:

```
./.kodelet/skills/<skill_name>/SKILL.md  # Repository-local (higher precedence)
~/.kodelet/skills/<skill_name>/SKILL.md   # User-global
```

### SKILL.md Format

The `SKILL.md` file must contain YAML frontmatter with required fields:

```markdown
---
name: your-skill-name
description: Brief description of what this Skill does and when to use it
---

# Your Skill Name

## Instructions
Provide clear, step-by-step guidance for the agent.

## Examples
Show concrete examples of using this Skill.
```

The frontmatter fields:
- `name` (required): Unique identifier for the skill
- `description` (required): Brief description used for model decision-making

## Implementation Design

### 1. Skills Package

Create a new package `pkg/skills/` for skill discovery and management:

```go
// pkg/skills/skill.go
package skills

import (
    "os"
    "path/filepath"

    "github.com/pkg/errors"
)

// Skill represents a discovered skill with its metadata
type Skill struct {
    Name        string // Unique name from frontmatter
    Description string // Brief description for model decision-making
    Directory   string // Full path to the skill directory
    Content     string // Full content of SKILL.md (body, not frontmatter)
}

// Metadata represents the YAML frontmatter in SKILL.md files
type Metadata struct {
    Name        string `yaml:"name"`
    Description string `yaml:"description"`
}
```

```go
// pkg/skills/discovery.go
package skills

import (
    "bytes"
    "io/fs"
    "os"
    "path/filepath"

    "github.com/pkg/errors"
    "github.com/yuin/goldmark"
    meta "github.com/yuin/goldmark-meta"
    "github.com/yuin/goldmark/parser"
)

const skillFileName = "SKILL.md"

// Discovery handles skill discovery from configured directories
type Discovery struct {
    skillDirs []string
}

// Option is a function that configures a Discovery
type Option func(*Discovery) error

// WithSkillDirs sets custom skill directories
func WithSkillDirs(dirs ...string) Option {
    return func(d *Discovery) error {
        d.skillDirs = dirs
        return nil
    }
}

// WithDefaultDirs initializes with default skill directories
func WithDefaultDirs() Option {
    return func(d *Discovery) error {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return errors.Wrap(err, "failed to get user home directory")
        }
        d.skillDirs = []string{
            "./.kodelet/skills",                           // Repo-local (higher precedence)
            filepath.Join(homeDir, ".kodelet", "skills"),  // User-global
        }
        return nil
    }
}

// NewDiscovery creates a new skill discovery instance
func NewDiscovery(opts ...Option) (*Discovery, error) {
    d := &Discovery{}

    if len(opts) == 0 {
        if err := WithDefaultDirs()(d); err != nil {
            return nil, err
        }
    } else {
        for _, opt := range opts {
            if err := opt(d); err != nil {
                return nil, err
            }
        }
    }

    return d, nil
}

// DiscoverSkills finds all available skills from configured directories
func (d *Discovery) DiscoverSkills() (map[string]*Skill, error) {
    skills := make(map[string]*Skill)

    for _, dir := range d.skillDirs {
        entries, err := os.ReadDir(dir)
        if err != nil {
            if os.IsNotExist(err) {
                continue // Skip non-existent directories
            }
            return nil, errors.Wrapf(err, "failed to read skill directory %s", dir)
        }

        for _, entry := range entries {
            if !entry.IsDir() {
                continue
            }

            skillPath := filepath.Join(dir, entry.Name(), skillFileName)
            skill, err := d.loadSkill(skillPath)
            if err != nil {
                continue // Skip invalid skills
            }

            // Only add if not already present (earlier directories have precedence)
            if _, exists := skills[skill.Name]; !exists {
                skill.Directory = filepath.Join(dir, entry.Name())
                skills[skill.Name] = skill
            }
        }
    }

    return skills, nil
}

// loadSkill loads a single skill from its SKILL.md file
func (d *Discovery) loadSkill(path string) (*Skill, error) {
    content, err := os.ReadFile(path)
    if err != nil {
        return nil, errors.Wrap(err, "failed to read skill file")
    }

    md := goldmark.New(
        goldmark.WithExtensions(meta.Meta),
    )

    var buf bytes.Buffer
    pctx := parser.NewContext()

    if err := md.Convert(content, &buf, parser.WithContext(pctx)); err != nil {
        return nil, errors.Wrap(err, "failed to parse markdown")
    }

    metaData := meta.Get(pctx)
    if metaData == nil {
        return nil, errors.New("missing frontmatter")
    }

    name, _ := metaData["name"].(string)
    description, _ := metaData["description"].(string)

    if name == "" {
        return nil, errors.New("skill name is required in frontmatter")
    }
    if description == "" {
        return nil, errors.New("skill description is required in frontmatter")
    }

    // Extract body content (after frontmatter)
    bodyContent := extractBodyContent(string(content))

    return &Skill{
        Name:        name,
        Description: description,
        Content:     bodyContent,
    }, nil
}

// extractBodyContent removes YAML frontmatter and returns the body
func extractBodyContent(content string) string {
    // Implementation similar to fragments package
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

    return strings.Join(lines[frontmatterEnd+1:], "\n")
}
```

### 2. Skill Tool

Create the skill tool in `pkg/tools/skill.go`:

```go
// pkg/tools/skill.go
package tools

import (
    "context"
    "encoding/json"
    "fmt"
    "sort"
    "strings"
    "time"

    "github.com/invopop/jsonschema"
    "github.com/jingkaihe/kodelet/pkg/skills"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
    "github.com/pkg/errors"
    "go.opentelemetry.io/otel/attribute"
)

// SkillTool provides access to agentic skills
type SkillTool struct {
    skills       map[string]*skills.Skill
    enabled      bool
    activeSkills map[string]bool // Track currently active skills to prevent re-invocation
}

// SkillInput defines the input parameters for the skill tool
type SkillInput struct {
    SkillName string `json:"skill_name" jsonschema:"description=The name of the skill to invoke"`
}

// SkillToolResult represents the result of a skill invocation
type SkillToolResult struct {
    skillName   string
    content     string
    directory   string
    err         string
}

// NewSkillTool creates a new skill tool with discovered skills
func NewSkillTool(discoveredSkills map[string]*skills.Skill, enabled bool) *SkillTool {
    return &SkillTool{
        skills:       discoveredSkills,
        enabled:      enabled,
        activeSkills: make(map[string]bool),
    }
}

// Name returns the tool name
func (t *SkillTool) Name() string {
    return "skill"
}

// Description returns the tool description with available skills
func (t *SkillTool) Description() string {
    var sb strings.Builder

    sb.WriteString(`When users ask you to perform tasks, check if any of the available skills below can help complete the task more effectively. Skills provide specialized capabilities and domain knowledge.

# Usage
- Use this tool with the skill name only
- Examples:
  - "pdf" - invoke the pdf skill
  - "xlsx" - invoke the xlsx skill

## Important
- When a skill is relevant, you must invoke this tool IMMEDIATELY as your first action
- NEVER just announce or mention a skill in your text response without actually calling this tool
- This is a BLOCKING REQUIREMENT: invoke the relevant Skill tool BEFORE generating any other response about the task
- Only use skills listed in "Available Skills" below
- Do not invoke a skill that is already running
- Each skill has a directory containing supporting files (references, examples, scripts, templates) that you can read using file_read or glob_tool
- Do NOT modify any files in the skill directory - treat skill contents as read-only
- If you need to modify a script from the skill directory, copy it to the working directory first using file_write, then use file_edit to update it
- For Python scripts, use uv for managing dependencies - do NOT install packages using system pip

## Available Skills

`)

    if !t.enabled || len(t.skills) == 0 {
        sb.WriteString("Skills are currently not available.\n")
        return sb.String()
    }

    // Sort skills by name for consistent output
    names := make([]string, 0, len(t.skills))
    for name := range t.skills {
        names = append(names, name)
    }
    sort.Strings(names)

    for _, name := range names {
        skill := t.skills[name]
        sb.WriteString(fmt.Sprintf("### %s\n", skill.Name))
        sb.WriteString(fmt.Sprintf("- **Description**: %s\n", skill.Description))
        sb.WriteString(fmt.Sprintf("- **Directory**: `%s`\n\n", skill.Directory))
    }

    return sb.String()
}

// GenerateSchema generates the JSON schema for the tool's input
func (t *SkillTool) GenerateSchema() *jsonschema.Schema {
    return GenerateSchema[SkillInput]()
}

// ValidateInput validates the input parameters
func (t *SkillTool) ValidateInput(_ tooltypes.State, parameters string) error {
    var input SkillInput
    if err := json.Unmarshal([]byte(parameters), &input); err != nil {
        return errors.Wrap(err, "invalid input")
    }

    if input.SkillName == "" {
        return errors.New("skill_name is required")
    }

    if !t.enabled {
        return errors.New("skills are disabled")
    }

    if _, exists := t.skills[input.SkillName]; !exists {
        available := make([]string, 0, len(t.skills))
        for name := range t.skills {
            available = append(available, name)
        }
        return errors.Errorf("unknown skill '%s'. Available skills: %s",
            input.SkillName, strings.Join(available, ", "))
    }

    return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (t *SkillTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
    var input SkillInput
    if err := json.Unmarshal([]byte(parameters), &input); err != nil {
        return nil, err
    }

    return []attribute.KeyValue{
        attribute.String("skill_name", input.SkillName),
    }, nil
}

// Execute invokes the skill and returns its content
func (t *SkillTool) Execute(_ context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
    var input SkillInput
    if err := json.Unmarshal([]byte(parameters), &input); err != nil {
        return &SkillToolResult{err: err.Error()}
    }

    skill, exists := t.skills[input.SkillName]
    if !exists {
        return &SkillToolResult{
            err: fmt.Sprintf("skill '%s' not found", input.SkillName),
        }
    }

    // Check if skill is already active
    if t.activeSkills[input.SkillName] {
        return &SkillToolResult{
            err: fmt.Sprintf("skill '%s' is already active", input.SkillName),
        }
    }

    // Mark skill as active
    t.activeSkills[input.SkillName] = true

    return &SkillToolResult{
        skillName: skill.Name,
        content:   skill.Content,
        directory: skill.Directory,
    }
}

// GetResult returns the result string
func (r *SkillToolResult) GetResult() string {
    return fmt.Sprintf("Skill '%s' loaded", r.skillName)
}

// GetError returns the error string
func (r *SkillToolResult) GetError() string {
    return r.err
}

// IsError returns true if there was an error
func (r *SkillToolResult) IsError() bool {
    return r.err != ""
}

// AssistantFacing returns the content to be fed to the LLM
func (r *SkillToolResult) AssistantFacing() string {
    if r.err != "" {
        return tooltypes.StringifyToolResult("", r.err)
    }

    result := fmt.Sprintf(`# Skill: %s

The skill directory is located at: %s

## Instructions

%s`, r.skillName, r.directory, r.content)

    return tooltypes.StringifyToolResult(result, "")
}

// StructuredData returns structured metadata for rendering
func (r *SkillToolResult) StructuredData() tooltypes.StructuredToolResult {
    result := tooltypes.StructuredToolResult{
        ToolName:  "skill",
        Success:   !r.IsError(),
        Timestamp: time.Now(),
    }

    if r.IsError() {
        result.Error = r.GetError()
        return result
    }

    result.Metadata = &tooltypes.SkillMetadata{
        SkillName: r.skillName,
        Directory: r.directory,
    }

    return result
}
```

### 3. Tool Integration

Update `pkg/tools/tools.go` to register the skill tool:

```go
// In toolRegistry map, add:
// "skill": &SkillTool{}, // Will be configured at runtime

// Add to defaultMainTools:
var defaultMainTools = []string{
    // ... existing tools ...
    "skill",
}
```

Update `pkg/tools/state.go` to configure the skill tool:

```go
// Add new option for skills
func WithSkillTool(discoveredSkills map[string]*skills.Skill, enabled bool) BasicStateOption {
    return func(ctx context.Context, s *BasicState) error {
        skillTool := NewSkillTool(discoveredSkills, enabled)
        // Replace the placeholder skill tool with the configured one
        for i, tool := range s.tools {
            if tool.Name() == "skill" {
                s.tools[i] = skillTool
                return nil
            }
        }
        // If not in the default list, append it
        s.tools = append(s.tools, skillTool)
        return nil
    }
}
```

Update tool configuration in `configureTools()`:

```go
func (s *BasicState) configureTools() {
    for i, tool := range s.tools {
        switch tool.Name() {
        case "bash":
            s.tools[i] = NewBashTool(s.llmConfig.AllowedCommands)
        case "web_fetch":
            s.tools[i] = NewWebFetchTool(s.llmConfig.AllowedDomainsFile)
        case "skill":
            // Skill tool is configured via WithSkillTool option
            // Keep existing if already configured, otherwise use disabled placeholder
            if _, ok := tool.(*SkillTool); !ok {
                s.tools[i] = NewSkillTool(nil, false)
            }
        }
    }
}
```

### 4. Renderer

Add skill renderer in `pkg/tools/renderers/skill_renderer.go`:

```go
// pkg/tools/renderers/skill_renderer.go
package renderers

import (
    "fmt"

    "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SkillRenderer renders skill tool results
type SkillRenderer struct{}

// RenderCLI renders skill results in CLI format
func (r *SkillRenderer) RenderCLI(result tools.StructuredToolResult) string {
    if !result.Success {
        return fmt.Sprintf("Error: %s", result.Error)
    }

    var meta tools.SkillMetadata
    if !tools.ExtractMetadata(result.Metadata, &meta) {
        return "Error: Invalid metadata type for skill"
    }

    return fmt.Sprintf("Skill %s loaded", meta.SkillName)
}
```

Add metadata type in `pkg/types/tools/structured.go`:

```go
// SkillMetadata contains metadata about a skill invocation
type SkillMetadata struct {
    SkillName string `json:"skill_name"`
    Directory string `json:"directory"`
}
```

Register the renderer in `pkg/tools/renderers/registry.go`:

```go
// In NewRendererRegistry():
registry.Register("skill", &SkillRenderer{})
```

### 5. Configuration

Add configuration options to `pkg/types/llm/config.go`:

```go
type Config struct {
    // ... existing fields ...

    // Skills configuration
    Skills *SkillsConfig `mapstructure:"skills" json:"skills,omitempty" yaml:"skills,omitempty"`
}

// SkillsConfig holds configuration for the agentic skills system
type SkillsConfig struct {
    Enabled bool     `mapstructure:"enabled" json:"enabled" yaml:"enabled"`         // Global enable/disable (default: true)
    Allowed []string `mapstructure:"allowed" json:"allowed" yaml:"allowed"`         // Allowlist of skill names (empty = all)
}
```

Add CLI flag in `cmd/kodelet/run.go` and `cmd/kodelet/chat.go`:

```go
runCmd.Flags().Bool("no-skills", false, "Disable agentic skills")
```

Update skill discovery to respect configuration:

```go
// In main initialization code
func initializeSkills(config llmtypes.Config, noSkillsFlag bool) (map[string]*skills.Skill, bool) {
    // Check if globally disabled
    enabled := true
    if config.Skills != nil && !config.Skills.Enabled {
        enabled = false
    }
    if noSkillsFlag {
        enabled = false
    }

    if !enabled {
        return nil, false
    }

    // Discover skills
    discovery, err := skills.NewDiscovery()
    if err != nil {
        return nil, false
    }

    allSkills, err := discovery.DiscoverSkills()
    if err != nil {
        return nil, false
    }

    // Apply allowlist filter
    if config.Skills != nil && len(config.Skills.Allowed) > 0 {
        filtered := make(map[string]*skills.Skill)
        for _, name := range config.Skills.Allowed {
            if skill, exists := allSkills[name]; exists {
                filtered[name] = skill
            }
        }
        return filtered, true
    }

    return allSkills, true
}
```

## Implementation Phases

### Phase 1: Core Skill Infrastructure (Week 1)
- [ ] Create `pkg/skills/` package with `Skill` type and `Metadata`
- [ ] Implement skill discovery from `.kodelet/skills/` directories
- [ ] Add YAML frontmatter parsing with goldmark-meta
- [ ] Write unit tests for discovery and parsing

### Phase 2: Skill Tool Implementation (Week 1-2)
- [ ] Implement `SkillTool` with description and execution
- [ ] Add `SkillToolResult` with proper `AssistantFacing()` output
- [ ] Register tool in `toolRegistry` and `defaultMainTools`
- [ ] Add `WithSkillTool` option to `BasicState`
- [ ] Write unit tests for tool execution

### Phase 3: Renderer and UI (Week 2)
- [ ] Create `SkillRenderer` for CLI output
- [ ] Add `SkillMetadata` type to structured tool results
- [ ] Register renderer in `RendererRegistry`
- [ ] Write renderer tests

### Phase 4: Configuration and CLI (Week 2)
- [ ] Add `SkillsConfig` to LLM config
- [ ] Implement `--no-skills` CLI flag
- [ ] Add allowlist filtering logic
- [ ] Write integration tests

### Phase 5: Documentation (Week 2-3)
- [ ] Create `docs/SKILLS.md` with comprehensive skills documentation
- [ ] Add skills configuration examples to `config.sample.yaml`
- [ ] Update `docs/MANUAL.md` with skills CLI usage
- [ ] Update `AGENTS.md` with skills system overview
- [ ] Create example skills for common use cases

## Documentation

### docs/SKILLS.md

Comprehensive guide for creating and using skills:

```markdown
# Agentic Skills

## Overview
Skills package domain expertise into discoverable capabilities that Kodelet 
autonomously invokes when relevant to your task.

## How Skills Work
- Skills are model-invoked (not user-invoked like fragments/recipes)
- Kodelet reads skill descriptions and decides when to use them
- When invoked, the skill's instructions are loaded into context

## Creating a Skill

### Directory Structure
\`\`\`
~/.kodelet/skills/my-skill/
├── SKILL.md (required)
├── reference.md (optional)
├── examples.md (optional)
├── scripts/
│   └── helper.py (optional)
└── templates/
    └── template.txt (optional)
\`\`\`

### SKILL.md Format
\`\`\`markdown
---
name: my-skill
description: Brief description for model decision-making
---

# My Skill

## Instructions
Step-by-step guidance for the agent...

## Examples
Concrete usage examples...
\`\`\`

## Skill Locations
- `./.kodelet/skills/` - Repository-local (higher precedence)
- `~/.kodelet/skills/` - User-global

## Configuration
See config.sample.yaml for configuration options.

## CLI Flags
- `--no-skills` - Disable all skills for this session
```

### config.sample.yaml additions

```yaml
# Skills configuration
skills:
  # Enable/disable skills globally (default: true)
  enabled: true
  
  # Allowlist of skill names (empty = all discovered skills enabled)
  # When specified, only these skills will be available
  allowed:
    - pdf
    - xlsx
    - kubernetes
```

### docs/MANUAL.md additions

Add to the CLI reference section:

```markdown
## Skills

Kodelet supports agentic skills - specialized capabilities that are 
automatically invoked when relevant to your task.

### Disabling Skills

To run without skills:
\`\`\`bash
kodelet run --no-skills "your query"
kodelet chat --no-skills
\`\`\`

### Skill Locations

Skills are discovered from:
- `./.kodelet/skills/<skill_name>/SKILL.md` (repository-local)
- `~/.kodelet/skills/<skill_name>/SKILL.md` (user-global)

Repository-local skills take precedence over user-global skills.

For detailed skill creation guide, see [docs/SKILLS.md](docs/SKILLS.md).
```

### AGENTS.md additions

Add to project structure and features:

```markdown
## Agentic Skills

Kodelet supports model-invoked skills that package domain expertise:

- **Location**: `.kodelet/skills/<name>/SKILL.md` (repo) or `~/.kodelet/skills/<name>/SKILL.md` (global)
- **Invocation**: Automatic - model decides when skills are relevant
- **Configuration**: `skills.enabled` and `skills.allowed` in config
- **CLI**: `--no-skills` flag to disable

See [docs/SKILLS.md](docs/SKILLS.md) for creating custom skills.
```

## Testing Strategy

### Unit Tests
1. **Discovery tests**: Verify skill discovery from multiple directories with precedence
2. **Parsing tests**: Test SKILL.md parsing with various frontmatter formats
3. **Tool tests**: Test skill tool execution, validation, and error handling
4. **Renderer tests**: Verify CLI output formatting

## Conclusion

The Agentic Skills feature extends Kodelet's capabilities by enabling the model to autonomously invoke specialized domain knowledge. The design:

1. **Follows Existing Patterns**: Similar structure to fragments, familiar discovery mechanism
2. **Minimal API Surface**: Single tool with skill name parameter
3. **Flexible Configuration**: Global enable/disable, allowlists, CLI override
4. **Clean Separation**: Skills are model-invoked, fragments remain user-invoked
5. **Extensible**: Supporting files allow for complex skill implementations

The tool-based approach ensures skills are discoverable, controllable, and integrate cleanly with Kodelet's existing architecture while providing the model autonomy to decide when specialized capabilities are needed.
