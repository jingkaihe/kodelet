package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	Question string            `json:"question" jsonschema:"description=The question to ask"`
	Workflow string            `json:"workflow,omitempty" jsonschema:"description=Optional workflow name to use for specialized tasks"`
	Args     map[string]string `json:"args,omitempty" jsonschema:"description=Optional arguments for the workflow as key-value pairs"`
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
- question: A description of the question to ask the subagent.
- workflow: (Optional) A workflow name to use for specialized tasks. See available workflows below.
- args: (Optional) Arguments for the workflow as key-value pairs.

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

	if input.Question == "" {
		return errors.New("question is required")
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

	return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (t *SubAgentTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &SubAgentInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	kvs := []attribute.KeyValue{
		attribute.String("question", input.Question),
	}
	if input.Workflow != "" {
		kvs = append(kvs, attribute.String("workflow", input.Workflow))
	}

	return kvs, nil
}

// BuildSubagentArgs builds the command-line arguments for spawning a subagent process.
// This is extracted as a separate function for testability.
// Returns the complete argument list including the base args, subagent_args from config, and the question.
func BuildSubagentArgs(ctx context.Context, subagentArgs string, input *SubAgentInput) []string {
	// Base arguments for subagent execution
	args := []string{"run", "--result-only", "--as-subagent"}

	// Append user-configured subagent args (e.g., "--profile openai --use-weak-model")
	if subagentArgs != "" {
		parsedArgs, err := shlex.Split(subagentArgs)
		if err != nil {
			logger.G(ctx).WithError(err).Warn("failed to parse subagent_args, ignoring")
		} else {
			args = append(args, parsedArgs...)
		}
	}

	// Add workflow if specified
	if input.Workflow != "" {
		args = append(args, "-r", input.Workflow)

		// Add workflow arguments
		for k, v := range input.Args {
			args = append(args, "--arg", fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Append the question as the final argument
	args = append(args, input.Question)

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
		}
	}

	// Build command arguments
	var subagentArgs string
	if llmConfig, ok := state.GetLLMConfig().(llmtypes.Config); ok {
		subagentArgs = llmConfig.SubagentArgs
	}
	args := BuildSubagentArgs(ctx, subagentArgs, input)

	cmd := exec.CommandContext(ctx, exe, args...)

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &SubAgentToolResult{
				err:      fmt.Sprintf("Subagent execution failed: %s\nstderr: %s", err, string(exitErr.Stderr)),
				question: input.Question,
			}
		}
		return &SubAgentToolResult{
			err:      fmt.Sprintf("Subagent execution failed: %s", err),
			question: input.Question,
		}
	}

	return &SubAgentToolResult{
		result:   strings.TrimSpace(string(output)),
		question: input.Question,
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
