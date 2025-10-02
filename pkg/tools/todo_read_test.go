package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTodoReadTool_GenerateSchema(t *testing.T) {
	tool := &TodoReadTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/todo-read-input", string(schema.ID))
}

func TestTodoReadTool_Description(t *testing.T) {
	tool := &TodoReadTool{}
	desc := tool.Description()

	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "Use TodoRead tool to read the current todo list")
}

func TestTodoReadTool_Execute(t *testing.T) {
	tool := &TodoReadTool{}
	ctx := context.Background()
	tempDir := t.TempDir()

	t.Run("successful read with todos", func(t *testing.T) {
		// Create a test state and set todo file path
		s := NewBasicState(context.TODO())
		todoFilePath := filepath.Join(tempDir, "todo_test.json")
		s.SetTodoFilePath(todoFilePath)

		// Create sample todos
		sampleTodos := TodoWriteInput{
			Todos: []Todo{
				{Status: Pending, Priority: High, Content: "High priority task"},
				{Status: Completed, Priority: Medium, Content: "Completed task"},
				{Status: InProgress, Priority: Low, Content: "In progress task"},
				{Status: Canceled, Priority: High, Content: "Canceled task"},
			},
		}

		// Write sample todos to file
		todoBytes, err := json.Marshal(sampleTodos)
		require.NoError(t, err)
		err = os.WriteFile(todoFilePath, todoBytes, 0o644)
		require.NoError(t, err)

		// Execute the tool
		result := tool.Execute(ctx, s, "")

		// Verify results
		assert.False(t, result.IsError())
		assert.NotEmpty(t, result.GetResult())

		// Check expected formatting
		assert.Contains(t, result.GetResult(), "Current todos:")
		assert.Contains(t, result.GetResult(), "ID\tStatus\tPriority\tContent")

		// Check expected order (sorted by status and priority)
		// Canceled should be first
		assert.Contains(t, result.GetResult(), "canceled\thigh\tCanceled task")
		// Completed should be next
		assert.Contains(t, result.GetResult(), "completed\tmedium\tCompleted task")
		// In Progress should follow
		assert.Contains(t, result.GetResult(), "in_progress\tlow\tIn progress task")
		// Pending should be last
		assert.Contains(t, result.GetResult(), "pending\thigh\tHigh priority task")
	})

	t.Run("empty todo file", func(t *testing.T) {
		// Create a test state and set todo file path
		s := NewBasicState(context.TODO())
		todoFilePath := filepath.Join(tempDir, "todo_empty.json")
		s.SetTodoFilePath(todoFilePath)

		// Create empty todos
		emptyTodos := TodoWriteInput{
			Todos: []Todo{},
		}

		// Write empty todos to file
		todoBytes, err := json.Marshal(emptyTodos)
		require.NoError(t, err)
		err = os.WriteFile(todoFilePath, todoBytes, 0o644)
		require.NoError(t, err)

		// Execute the tool
		result := tool.Execute(ctx, s, "")

		// Verify results
		assert.False(t, result.IsError())
		assert.NotEmpty(t, result.GetResult())
		assert.Contains(t, result.GetResult(), "Current todos:")
	})

	t.Run("non-existent todo file", func(t *testing.T) {
		// Create a test state and set todo file path to non-existent file
		s := NewBasicState(context.TODO())
		todoFilePath := filepath.Join(tempDir, "non_existent.json")
		s.SetTodoFilePath(todoFilePath)

		// Execute the tool
		result := tool.Execute(ctx, s, "")

		// Verify error
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "failed to read todos from file")
	})

	t.Run("invalid json in todo file", func(t *testing.T) {
		// Create a test state and set todo file path
		s := NewBasicState(context.TODO())
		todoFilePath := filepath.Join(tempDir, "invalid_todo.json")
		s.SetTodoFilePath(todoFilePath)

		// Write invalid JSON to file
		err := os.WriteFile(todoFilePath, []byte("invalid json"), 0o644)
		require.NoError(t, err)

		// Execute the tool
		result := tool.Execute(ctx, s, "")

		// Verify error
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "failed to unmarshal todos from file")
	})
}

func TestSortTodos(t *testing.T) {
	todos := []Todo{
		{Status: Pending, Priority: Low, Content: "Low priority pending"},
		{Status: Pending, Priority: High, Content: "High priority pending"},
		{Status: InProgress, Priority: Medium, Content: "Medium priority in progress"},
		{Status: Completed, Priority: High, Content: "High priority completed"},
		{Status: Canceled, Priority: Low, Content: "Low priority canceled"},
	}

	sorted := sortTodos(todos)
	require.Len(t, sorted, 5)

	// Check overall status ordering
	assert.Equal(t, Canceled, sorted[0].Status, "Canceled should be first")
	assert.Equal(t, Completed, sorted[1].Status, "Completed should be second")
	assert.Equal(t, InProgress, sorted[2].Status, "InProgress should be third")

	// Check priority ordering within the same status
	assert.Equal(t, High, sorted[3].Priority, "High priority should come before Low within Pending")
	assert.Equal(t, Low, sorted[4].Priority, "Low priority should come after High within Pending")
}

func TestFormatTodos(t *testing.T) {
	todos := []Todo{
		{Status: Pending, Priority: High, Content: "Task 1"},
		{Status: Completed, Priority: Medium, Content: "Task 2"},
	}

	formatted := formatTodos(todos)

	assert.Contains(t, formatted, "Current todos:")
	assert.Contains(t, formatted, "ID\tStatus\tPriority\tContent")
	assert.Contains(t, formatted, "1\tpending\thigh\tTask 1")
	assert.Contains(t, formatted, "2\tcompleted\tmedium\tTask 2")
}
