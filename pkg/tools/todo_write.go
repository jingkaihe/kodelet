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
	return `Create or update the todo list for the current task.

Use this tool when work is non-trivial (more than 3 meaningful steps), when the user asks to track progress, or when the user gives a task list.
Do not use it for simple one-step commands or pure Q&A.

Input:
- todos: full current list (not partial)
- each todo: content (specific/actionable), status (pending|in_progress|completed|canceled), priority (high|medium|low)

Rules:
- Keep exactly one todo in "in_progress".
- Mark todos "completed" or "canceled" immediately when state changes.
- Add newly discovered work as "pending".
- Preserve existing todos when updating.
- Prefer execution order; if needed, sort by status (completed, canceled, in_progress, pending) then priority (high, medium, low).

Plan quality:
- Good: specific, decomposed, and verifiable (clear artifact/output, dependency-aware order, at least one validation step).
- Bad: vague or coarse (generic verbs, missing technical detail, no explicit verification).
- Write todos as concrete action + artifact.
- Break non-trivial work into meaningful steps (typically 4-7).
- Include key implementation detail when relevant (component/interface/data flow).
- Avoid vague text like "improve it", "set up stuff", or "test quickly".

Good example:
- Define API contract for task filters.
- Implement SQL query and indexes for filter performance.
- Add handler/service wiring for filter params.
- Add pagination and input validation.
- Add integration tests for filter combinations.

Bad example:
- Build API endpoint.
- Hook it up.
- Test it.
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
