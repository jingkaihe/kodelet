package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/google/shlex"
	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// SubAgentToolResult represents the result of a sub-agent tool execution
type SubAgentToolResult struct {
	result   string
	err      string
	question string
	workflow string
	cwd      string
}

// GetResult returns the sub-agent output
func (r *SubAgentToolResult) GetResult() string {
	return r.result
}

// GetError returns the error message
func (r *SubAgentToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *SubAgentToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *SubAgentToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.GetError())
}

// SubAgentTool provides functionality to spawn sub-agents for complex tasks
type SubAgentTool struct {
	workflows       map[string]*fragments.Fragment
	workflowEnabled bool
}

// NewSubAgentTool creates a new sub-agent tool with discovered workflows
func NewSubAgentTool(discoveredWorkflows map[string]*fragments.Fragment, workflowEnabled bool) *SubAgentTool {
	return &SubAgentTool{
		workflows:       discoveredWorkflows,
		workflowEnabled: workflowEnabled,
	}
}

// SubAgentInput defines the input parameters for the sub-agent tool
type SubAgentInput struct {
	Question string            `json:"question,omitempty" jsonschema:"description=The question to ask (required unless workflow is specified)"`
	Workflow string            `json:"workflow,omitempty" jsonschema:"description=Optional workflow name to use for specialized tasks"`
	Args     map[string]string `json:"args,omitempty" jsonschema:"description=Optional arguments for the workflow as key-value pairs"`
	Cwd      string            `json:"cwd,omitempty" jsonschema:"description=Working directory for subagent (absolute path)"`
}

// Name returns the name of the tool
func (t *SubAgentTool) Name() string {
	return "subagent"
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *SubAgentTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[SubAgentInput]()
}

// workflowTemplateData holds the data for rendering workflow descriptions
type workflowTemplateData struct {
	Workflows []workflowData
}

type workflowData struct {
	Name        string
	Description string
	Arguments   []workflowArgumentData
}

type workflowArgumentData struct {
	Name        string
	Description string
	Default     string
}

const subagentDescriptionTemplate = `Use this tool to delegate tasks to a sub-agent.
This tool is ideal for tasks that involves code searching, architecture analysis, codebase understanding and troubleshooting.

## Input
- question: A description of the question to ask the subagent (required unless workflow is specified).
- workflow: (Optional) A workflow name to use for specialized tasks. See available workflows below.
- args: (Optional) Arguments for the workflow as key-value pairs.
- cwd: (Optional) Specify when you want the subagent to work in a directory other than the current working directory. Must be an absolute path.

## Common Use Cases
* If you want to do multi-turn search using grep_tool and file_read, and you don't know exactly what keywords to use. You should use this subagent tool.

## DO NOT use this tool when:
* You are 100% sure about the keywords to use. e.g. "[Ll]og" - Use ${grep_tool} instead.
* You just want to find where certain files or directories are located - Use find command via ${bash} tool instead.
* You just want to look for the content of a file - Use ${file_read} tool instead.

## Important Notes
1. The subagent does not have any memory of previous invocations, and you cannot talk to it back and forth. As a result, your question must be concise and to the point.
   - contain a short and concise problem statement.
   - state what information you expect to get back.
   - state the format of the output in detail.
2. The agent returns a text response back to you, and you have no access to the subagent's internal messages.
3. The agent's response is not visible to the user. To show user the result you must send the result from the subagent back to the user.
4. When using a workflow, the question is optional - the workflow will execute with its predefined instructions.

## Available Workflows

{{if eq (len .Workflows) 0 -}}
<no_workflows_available />
{{else -}}
<workflows>
{{range .Workflows -}}
<workflow name="{{.Name}}">
{{if .Description}}  <description>{{.Description}}</description>
{{end -}}
{{if .Arguments}}  <arguments>
{{range .Arguments}}    <argument name="{{.Name}}"{{if .Default}} default="{{.Default}}"{{end}}>{{.Description}}</argument>
{{end}}  </arguments>
{{end -}}
</workflow>
{{end -}}
</workflows>
{{end}}`

// Description returns the description of the tool
func (t *SubAgentTool) Description() string {
	data := workflowTemplateData{}

	if t.workflowEnabled && len(t.workflows) > 0 {
		// Build sorted workflow data
		names := make([]string, 0, len(t.workflows))
		for name := range t.workflows {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			workflow := t.workflows[name]
			wf := workflowData{
				Name:        name,
				Description: workflow.Metadata.Description,
			}

			// Sort arguments
			argNames := make([]string, 0, len(workflow.Metadata.Arguments))
			for argName := range workflow.Metadata.Arguments {
				argNames = append(argNames, argName)
			}
			sort.Strings(argNames)

			for _, argName := range argNames {
				argMeta := workflow.Metadata.Arguments[argName]
				wf.Arguments = append(wf.Arguments, workflowArgumentData{
					Name:        argName,
					Description: argMeta.Description,
					Default:     argMeta.Default,
				})
			}
			data.Workflows = append(data.Workflows, wf)
		}
	}

	return renderSubagentDescription(data)
}

func renderSubagentDescription(data workflowTemplateData) string {
	tmpl := template.Must(template.New("subagent").Parse(subagentDescriptionTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "Error rendering description: " + err.Error()
	}
	return buf.String()
}

// ValidateInput validates the input parameters for the tool
func (t *SubAgentTool) ValidateInput(_ tooltypes.State, parameters string) error {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return err
	}

	// Question is required unless a workflow is specified
	if input.Question == "" && input.Workflow == "" {
		return errors.New("question is required when workflow is not specified")
	}

	// Args can only be used with a workflow
	if len(input.Args) > 0 && input.Workflow == "" {
		return errors.New("args can only be used with a workflow")
	}

	if input.Workflow != "" && t.workflowEnabled {
		if _, exists := t.workflows[input.Workflow]; !exists {
			available := make([]string, 0, len(t.workflows))
			for name := range t.workflows {
				available = append(available, name)
			}
			sort.Strings(available)
			return errors.Errorf("unknown workflow '%s'. Available workflows: %s",
				input.Workflow, strings.Join(available, ", "))
		}
	}

	// Validate cwd is an absolute path, exists, and is a directory
	if input.Cwd != "" {
		if !filepath.IsAbs(input.Cwd) {
			return errors.Errorf("cwd must be an absolute path, got: %s", input.Cwd)
		}
		// Resolve symlinks to prevent symlink attacks
		resolvedPath, err := filepath.EvalSymlinks(input.Cwd)
		if err != nil {
			if os.IsNotExist(err) {
				return errors.Errorf("cwd directory does not exist: %s", input.Cwd)
			}
			return errors.Wrapf(err, "failed to resolve cwd path: %s", input.Cwd)
		}
		stat, err := os.Stat(resolvedPath)
		if err != nil {
			return errors.Wrapf(err, "failed to access cwd: %s", input.Cwd)
		}
		if !stat.IsDir() {
			return errors.Errorf("cwd is not a directory: %s", input.Cwd)
		}
	}

	return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (t *SubAgentTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	var kvs []attribute.KeyValue
	if input.Question != "" {
		kvs = append(kvs, attribute.String("question", input.Question))
	}
	if input.Workflow != "" {
		kvs = append(kvs, attribute.String("workflow", input.Workflow))
	}
	if input.Cwd != "" {
		kvs = append(kvs, attribute.String("cwd", input.Cwd))
	}

	return kvs, nil
}

// stripProfileFlag removes all --profile flags and their values from args slice
// Handles both "--profile value" and "--profile=value" formats
func stripProfileFlag(args []string) []string {
	result := slices.Clone(args)
	// Handle "--profile value" format - remove all occurrences
	for {
		if i := slices.Index(result, "--profile"); i >= 0 && i+1 < len(result) {
			result = slices.Delete(result, i, i+2)
		} else {
			break
		}
	}
	// Handle "--profile=value" format - remove all occurrences
	result = slices.DeleteFunc(result, func(s string) bool {
		return strings.HasPrefix(s, "--profile=")
	})
	return result
}

// BuildSubagentArgs builds the command-line arguments for spawning a subagent process.
// This is extracted as a separate function for testability.
// Returns the complete argument list including the base args, subagent_args from config, and the question.
// The workflow parameter is optional and provides workflow metadata (profile).
func BuildSubagentArgs(ctx context.Context, subagentArgs string, input *SubAgentInput, workflow *fragments.Fragment) []string {
	// Base arguments for subagent execution
	args := []string{"run", "--result-only", "--as-subagent"}

	// Append user-configured subagent args (e.g., "--profile openai --use-weak-model")
	if subagentArgs != "" {
		parsedArgs, err := shlex.Split(subagentArgs)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to parse subagent_args, ignoring")
		} else {
			// If workflow has a profile, strip --profile from subagent_args to avoid conflicts
			if workflow != nil && workflow.Metadata.Profile != "" {
				parsedArgs = stripProfileFlag(parsedArgs)
			}
			args = append(args, parsedArgs...)
		}
	}

	// Add profile from workflow metadata
	if workflow != nil && workflow.Metadata.Profile != "" {
		args = append(args, "--profile", workflow.Metadata.Profile)
	}

	// Add workflow if specified
	if input.Workflow != "" {
		args = append(args, "-r", input.Workflow)

		// Add workflow arguments in sorted order for deterministic output
		argKeys := make([]string, 0, len(input.Args))
		for k := range input.Args {
			argKeys = append(argKeys, k)
		}
		sort.Strings(argKeys)
		for _, k := range argKeys {
			args = append(args, "--arg", fmt.Sprintf("%s=%s", k, input.Args[k]))
		}
	}

	// Append the question as the final argument (only if non-empty)
	if input.Question != "" {
		args = append(args, input.Question)
	}

	return args
}

// Execute runs the sub-agent via shell-out and returns the result
func (t *SubAgentTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return &SubAgentToolResult{
			err: err.Error(),
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return &SubAgentToolResult{
			err:      errors.Wrap(err, "failed to get executable path").Error(),
			question: input.Question,
			workflow: input.Workflow,
			cwd:      input.Cwd,
		}
	}

	// Build command arguments
	var subagentArgs string
	if llmConfig, ok := state.GetLLMConfig().(llmtypes.Config); ok {
		subagentArgs = llmConfig.SubagentArgs
	}

	// Look up workflow fragment for metadata (profile/provider/model)
	var workflow *fragments.Fragment
	if input.Workflow != "" && t.workflowEnabled {
		workflow = t.workflows[input.Workflow]
	}
	args := BuildSubagentArgs(ctx, subagentArgs, input, workflow)

	cmd := exec.CommandContext(ctx, exe, args...)

	// Set working directory if specified (use resolved path to prevent TOCTOU issues)
	if input.Cwd != "" {
		resolvedCwd, err := filepath.EvalSymlinks(input.Cwd)
		if err != nil {
			return &SubAgentToolResult{
				err:      errors.Wrapf(err, "failed to resolve cwd path: %s", input.Cwd).Error(),
				question: input.Question,
				workflow: input.Workflow,
				cwd:      input.Cwd,
			}
		}
		cmd.Dir = resolvedCwd
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &SubAgentToolResult{
			err:      fmt.Sprintf("Subagent execution failed: %s\noutput: %s", err, string(output)),
			question: input.Question,
			workflow: input.Workflow,
			cwd:      input.Cwd,
		}
	}

	return &SubAgentToolResult{
		result:   strings.TrimSpace(string(output)),
		question: input.Question,
		workflow: input.Workflow,
		cwd:      input.Cwd,
	}
}

// StructuredData returns structured metadata about the sub-agent execution
func (r *SubAgentToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "subagent",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.SubAgentMetadata{
		Question: r.question,
		Response: r.result,
		Workflow: r.workflow,
		Cwd:      r.cwd,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

// GetWorkflows returns the discovered workflows
func (t *SubAgentTool) GetWorkflows() map[string]*fragments.Fragment {
	return t.workflows
}

// IsWorkflowEnabled returns whether workflows are enabled
func (t *SubAgentTool) IsWorkflowEnabled() bool {
	return t.workflowEnabled
}
