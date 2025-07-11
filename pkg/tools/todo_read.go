package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

type TodoToolResult struct {
	filePath string
	todos    []Todo
	err      string
	isWrite  bool
}

func (r *TodoToolResult) GetResult() string {
	if r.IsError() {
		return ""
	}
	if r.isWrite {
		return fmt.Sprintf("Todos have been written to %s", r.filePath)
	}
	sortedTodos := sortTodos(r.todos)
	return formatTodos(sortedTodos)
}

func (r *TodoToolResult) GetError() string {
	return r.err
}

func (r *TodoToolResult) IsError() bool {
	return r.err != ""
}

func (r *TodoToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.GetResult(), r.GetError())
}

type TodoReadTool struct{}

type TodoReadInput struct{}

func (t *TodoReadTool) Name() string {
	return "todo_read"
}

func (t *TodoReadTool) Description() string {
	return `Use TodoRead tool to read the current todo list.

This tool is useful for reviewing the progress of your current task.

# Use Cases
* Check the current pending todo item.
* You are asked by user to review the current todo list.
* Check the todo items remaining and make sure you are making progress.
* You are under the impression that you are lost track of the task.
`
}

func (t *TodoReadTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[TodoReadInput]()
}

func (t *TodoReadTool) ValidateInput(state tooltypes.State, parameters string) error {
	return nil
}

func (t *TodoReadTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
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

func (t *TodoReadTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	filePath, err := state.TodoFilePath()
	if err != nil {
		return &TodoToolResult{
			filePath: filePath,
			err:      fmt.Sprintf("failed to get todo file path: %s", err.Error()),
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return &TodoToolResult{
			filePath: filePath,
			err:      fmt.Sprintf("failed to read todos from file: %s", err.Error()),
		}
	}

	var todoInput TodoWriteInput
	if err := json.Unmarshal(content, &todoInput); err != nil {
		return &TodoToolResult{
			filePath: filePath,
			err:      fmt.Sprintf("failed to unmarshal todos from file: %s", err.Error()),
		}
	}

	return &TodoToolResult{
		filePath: filePath,
		todos:    todoInput.Todos,
		isWrite:  false,
	}
}

func sortTodos(todos []Todo) []Todo {
	statusOrder := map[Status]int{
		Canceled:   0,
		Completed:  1,
		InProgress: 2,
		Pending:    3,
	}
	priorityOrder := map[Priority]int{
		High:   0,
		Medium: 1,
		Low:    2,
	}

	sorted := make([]Todo, len(todos))
	copy(sorted, todos)
	sort.Slice(sorted, func(i, j int) bool {
		todoA, todoB := sorted[i], sorted[j]
		statusA, statusB := statusOrder[todoA.Status], statusOrder[todoB.Status]
		if statusA != statusB {
			return statusA < statusB
		}
		priorityA, priorityB := priorityOrder[todoA.Priority], priorityOrder[todoB.Priority]
		return priorityA < priorityB
	})
	return sorted
}

func formatTodos(todos []Todo) string {
	formatted := ""
	formatted += "Current todos:\n"
	formatted += fmt.Sprintf("%s\t%s\t%s\t%s\n", "ID", "Status", "Priority", "Content")
	for idx, todo := range todos {
		formatted += fmt.Sprintf("%d\t%s\t%s\t%s\n", idx+1, todo.Status, todo.Priority, todo.Content)
	}
	return formatted
}

func (r *TodoToolResult) StructuredData() tooltypes.StructuredToolResult {
	toolName := "todo_read"
	action := "read"
	if r.isWrite {
		toolName = "todo_write"
		action = "write"
	}

	result := tooltypes.StructuredToolResult{
		ToolName:  toolName,
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Convert Todo items to structured format
	todoItems := make([]tooltypes.TodoItem, 0, len(r.todos))
	stats := tooltypes.TodoStats{}

	for i, todo := range r.todos {
		todoItems = append(todoItems, tooltypes.TodoItem{
			ID:       fmt.Sprintf("%d", i+1), // Simple numeric IDs
			Content:  todo.Content,
			Status:   string(todo.Status),
			Priority: string(todo.Priority),
		})

		// Update statistics
		stats.Total++
		switch todo.Status {
		case "completed":
			stats.Completed++
		case "in_progress":
			stats.InProgress++
		case "pending":
			stats.Pending++
		}
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.TodoMetadata{
		Action:     action,
		TodoList:   todoItems,
		Statistics: stats,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}
