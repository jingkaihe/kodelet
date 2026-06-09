package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/skills"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// SkillTool provides access to agentic skills
type SkillTool struct {
	skills              map[string]*skills.Skill
	enabled             bool
	toolMode            llmtypes.ToolMode
	enableFSSearchTools bool
	activeSkills        map[string]bool
	mu                  sync.RWMutex
}

// SkillInput defines the input parameters for the skill tool
type SkillInput struct {
	SkillName string `json:"skill_name" jsonschema:"description=The name of the skill to invoke"`
}

// SkillToolResult represents the result of a skill invocation
type SkillToolResult struct {
	skillName string
	content   string
	directory string
	err       string
}

// NewSkillTool creates a new skill tool with discovered skills
func NewSkillTool(discoveredSkills map[string]*skills.Skill, enabled bool, enableFSSearchTools bool) *SkillTool {
	return NewSkillToolWithOptions(discoveredSkills, enabled, llmtypes.ToolModeFull, enableFSSearchTools)
}

// NewSkillToolWithOptions creates a new skill tool with rendering options.
func NewSkillToolWithOptions(discoveredSkills map[string]*skills.Skill, enabled bool, toolMode llmtypes.ToolMode, enableFSSearchTools bool) *SkillTool {
	return &SkillTool{
		skills:              discoveredSkills,
		enabled:             enabled,
		toolMode:            toolMode,
		enableFSSearchTools: enableFSSearchTools,
		activeSkills:        make(map[string]bool),
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
  - "kernel-dev" - invoke the kernel-dev skill
  - "xlsx" - invoke the xlsx skill

## Important
- When a skill is relevant, you must invoke this tool IMMEDIATELY as your first action
- NEVER just announce or mention a skill in your text response without actually calling this tool
- This is a BLOCKING REQUIREMENT: invoke the relevant Skill tool BEFORE generating any other response about the task
- Only use skills listed in "Available Skills" below
- Do not invoke a skill that is already running
- Each skill has a directory containing supporting files (references, examples, scripts, templates) that you can {{if .PatchOnly}}{{if .EnableFSSearchTools}}locate with glob_tool and inspect via bash using sed/cat/rg{{else}}inspect using fd/rg/sed/cat via bash{{end}}{{else}}read using file_read{{if .EnableFSSearchTools}} or glob_tool{{else}} or fd via bash{{end}}{{end}}
- Do NOT modify any files in the skill directory - treat skill contents as read-only
- If you need to modify a script or template from the skill directory, copy it to the working directory first{{if .PatchOnly}} and update it using apply_patch{{else}} then read it using file_read, and update using file_edit tool{{end}}
- For Python scripts, use uv for managing dependencies, preferably uv with inline metadata dependencies if the script to run is a single file - do NOT install packages using system pip

## Available Skills

`)

	tmpl := template.Must(template.New("skill_description").Parse(sb.String()))
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, struct {
		PatchOnly           bool
		EnableFSSearchTools bool
	}{
		PatchOnly:           t.toolMode.IsPatchMode(),
		EnableFSSearchTools: t.enableFSSearchTools,
	}); err == nil {
		sb.Reset()
		sb.WriteString(rendered.String())
	}

	if !t.enabled || len(t.skills) == 0 {
		sb.WriteString("Skills are currently not available.\n")
		return sb.String()
	}

	names := make([]string, 0, len(t.skills))
	for name := range t.skills {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		skill := t.skills[name]
		fmt.Fprintf(&sb, "### %s\n", skill.Name)
		fmt.Fprintf(&sb, "- **Description**: %s\n", skill.Description)
		fmt.Fprintf(&sb, "- **Directory**: `%s`\n\n", skill.Directory)
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
		sort.Strings(available)
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

	t.mu.Lock()
	if t.activeSkills[input.SkillName] {
		t.mu.Unlock()
		return &SkillToolResult{
			err: fmt.Sprintf("skill '%s' is already active", input.SkillName),
		}
	}
	t.activeSkills[input.SkillName] = true
	t.mu.Unlock()

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

// IsActive checks if a skill is currently active
func (t *SkillTool) IsActive(skillName string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.activeSkills[skillName]
}

// ResetActiveSkills clears all active skills
func (t *SkillTool) ResetActiveSkills() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeSkills = make(map[string]bool)
}

// GetSkills returns the discovered skills
func (t *SkillTool) GetSkills() map[string]*skills.Skill {
	return t.skills
}

// IsEnabled returns whether skills are enabled
func (t *SkillTool) IsEnabled() bool {
	return t.enabled
}
