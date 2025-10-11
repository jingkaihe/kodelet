package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// TodoWriteTool provides functionality to write and manage the todo list
type TodoWriteTool struct{}

// Status represents the status of a todo item
type Status string

const (
	// Pending indicates the todo is pending
	Pending Status = "pending"
	// InProgress indicates the todo is in progress
	InProgress Status = "in_progress"
	// Completed indicates the todo is completed
	Completed Status = "completed"
	// Canceled indicates the todo was canceled
	Canceled Status = "canceled"
)

// Priority represents the priority of a todo item
type Priority string

const (
	// Low priority
	Low Priority = "low"
	// Medium priority
	Medium Priority = "medium"
	// High priority
	High Priority = "high"
)

// Todo represents a single todo item
type Todo struct {
	Content  string   `json:"content" jsonschema:"description=The content of the todo in 1-2 sentences"`
	Status   Status   `json:"status" jsonschema:"description=The status of the todo"`
	Priority Priority `json:"priority" jsonschema:"description=The priority of the todo"`
}

// TodoWriteInput defines the input parameters for the todo_write tool
type TodoWriteInput struct {
	Todos []Todo `json:"todos" jsonschema:"description=The full list of todos including all the pending in_progress and completed ones"`
}

// Name returns the name of the tool
func (t *TodoWriteTool) Name() string {
	return "todo_write"
}

// Description returns the description of the tool
func (t *TodoWriteTool) Description() string {
	return `Use TodoWrite tool to create and update a list of todos for your current task.

This tool helps you to manage and plan tasks for any non-trivial tasks that require multiple steps to complete.

# Tool Structure
The tool takes a list of todos as input. Each todo item is composed of:
- content: The content of the todo in 1-2 sentences, while being specific and actionable.
- status: The status of the todo, one of "pending", "in_progress", "completed",
- priority: The priority of the todo, one of "low", "medium", "high"

The list of todos must be sorted at the order of (completed < canceled < in_progress < pending) in status and (high < medium < low) in priority.

# Common Use Cases
You must use this tool proactively in the following use cases:

- The task is non-trivial and requires careful planning and multiple steps to complete.
- The user explicitly asks you to keep track of the progress using a todo list.
- The user explicitly gives you a list of todos to complete.

# When NOT to User This Tool
- The task can be completed with 1-3 simple steps.
- The task is conversational where you can answer the questions directly based on your knowledge and converstation history.

# How to use this tool
- Write down the plan as todos when you start a new task.
- When you start a new task, mark it as "in_progress".
- You MUST mark a todo as "completed" AS SOON AS you have completed it. If there are new todos surface, add them as new todos in "pending" status.
- You MUST complete todos one at a time, focusing on a single task.
- You MUST have only one todo in "in_progress" status at any time.
- When you are given new instructions, and you already have a todo list working in progress, add the new todos to the existing list.
- Mark a todo as "canceled" if a task is no longer needed.
## Examples

<example>
User: Write terraform to deploy the current app in Google Cloud Run.
Assistant: [write the following todos to the todo list using ${todoWriteTool}:
- Explore the current app repo and understand the architecture
- Write Dockerfile to contaierise the app
- Confirm the config and secrets management solution
- Implement the terraform code to deploy the app in Google Cloud Run
]
<reasoning>
The assistant uses the TodoWrite tool because:
- Implement IaC is non-trivial and requires understanding the current app, containerisation, config and secrets management solutions.
- The app needs to be containerised if not already.
- The technical choice for config and secrets management is not obvious and requires user confirmation.
- The IaC should be written in terraform.
</reasoning>
</example>

<example>
User: I need to 1. create a Dockerfile for the current app, 2. create github actions workflow to release the container image to Github Container Registry
Assistant: [
- create a Dockerfile for the current app
- create github actions workflow to release the container image to Github Container Registry
]
<reasoning>
The user explicitly gives you a list of todos to complete.
</reasoning>
</example>

<example>
User: I need to containerise the current app. Please create a todo list to track the progress.
Assistant: [
- create a Dockerfile for the current app
- create github actions workflow to release the container image to Github Container Registry
]
<reasoning>
The user explicitly asks you to keep track of the progress using a todo list.
</reasoning>
</example>

<example>
User: run "go mod tidy"
Assistant: [run go mod tidy successfully]
<reasoning>
The task is trivial to complete thus no need to use TodoWrite tool.
</reasoning>
</example>

<example>
User: how can I make sure the app dependencies are up to date?
Assistant: You can run "go mod tidy" to make sure the app dependencies are up to date.
<reasoning>
The task is conversational and can be answered directly based on your knowledge and converstation history.
</reasoning>
</example>
`
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *TodoWriteTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[TodoWriteInput]()
}

// ValidateInput validates the input parameters for the tool
func (t *TodoWriteTool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input TodoWriteInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid input")
	}

	if len(input.Todos) == 0 {
		return errors.New("todos should have at least one todo")
	}

	for i, todo := range input.Todos {
		if todo.Content == "" {
			return errors.Errorf("todo %d content is required", i)
		}
		if todo.Status == "" {
			return errors.Errorf("todo %d status must be one of %v", i, []Status{Pending, InProgress, Completed, Canceled})
		}
		if todo.Priority == "" {
			return errors.Errorf("todo %d priority must be one of %v", i, []Priority{Low, Medium, High})
		}
	}

	return nil
}

// TracingKVs returns tracing key-value pairs for observability
func (t *TodoWriteTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	var todos TodoWriteInput
	if err := json.Unmarshal([]byte(parameters), &todos); err != nil {
		return nil, err
	}

	kvs := []attribute.KeyValue{}
	for i, todo := range todos.Todos {
		kvs = append(kvs, attribute.String(fmt.Sprintf("todo.%d.Status", i), string(todo.Status)))
		kvs = append(kvs, attribute.String(fmt.Sprintf("todo.%d.Priority", i), string(todo.Priority)))
		kvs = append(kvs, attribute.String(fmt.Sprintf("todo.%d.Content", i), todo.Content))
	}

	return kvs, nil
}

// Execute writes the todo list to the file
func (t *TodoWriteTool) Execute(_ context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input TodoWriteInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &TodoToolResult{
			err: fmt.Sprintf("invalid input: %s", err.Error()),
		}
	}

	todosFilePath, err := state.TodoFilePath()
	if err != nil {
		return &TodoToolResult{
			filePath: todosFilePath,
			err:      fmt.Sprintf("failed to get todo file path: %s", err.Error()),
		}
	}

	// make the directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(todosFilePath), 0o755); err != nil {
		return &TodoToolResult{
			filePath: todosFilePath,
			err:      fmt.Sprintf("failed to write todos to file: %s", err.Error()),
		}
	}

	// write the todos to the file
	err = os.WriteFile(todosFilePath, []byte(parameters), 0o644)
	if err != nil {
		return &TodoToolResult{
			filePath: todosFilePath,
			err:      fmt.Sprintf("failed to write todos to file: %s", err.Error()),
		}
	}

	return &TodoToolResult{
		filePath: todosFilePath,
		todos:    input.Todos,
		isWrite:  true,
	}
}
