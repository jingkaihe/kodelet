package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTodoWriteTool_GenerateSchema(t *testing.T) {
	tool := &TodoWriteTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)
	// Verify the schema ID follows the pattern used in the repo
	assert.Contains(t, string(schema.ID), "github.com/jingkaihe/kodelet")
}

func TestTodoWriteTool_Name(t *testing.T) {
	tool := &TodoWriteTool{}
	assert.Equal(t, "todo_write", tool.Name())
}

func TestTodoWriteTool_Description(t *testing.T) {
	tool := &TodoWriteTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Use TodoWrite tool to create and update a list of todos")
	assert.Contains(t, desc, "Tool Structure")
	assert.Contains(t, desc, "Common Use Cases")
}

func TestTodoWriteTool_ValidateInput(t *testing.T) {
	tool := &TodoWriteTool{}
	tests := []struct {
		name        string
		input       TodoWriteInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid input",
			input: TodoWriteInput{
				Todos: []Todo{
					{
						Content:  "Test todo",
						Status:   Pending,
						Priority: Medium,
					},
				},
			},
			expectError: false,
		},
		{
			name: "multiple valid todos",
			input: TodoWriteInput{
				Todos: []Todo{
					{
						Content:  "First todo",
						Status:   Pending,
						Priority: High,
					},
					{
						Content:  "Second todo",
						Status:   InProgress,
						Priority: Medium,
					},
					{
						Content:  "Third todo",
						Status:   Completed,
						Priority: Low,
					},
				},
			},
			expectError: false,
		},
		{
			name: "empty todos",
			input: TodoWriteInput{
				Todos: []Todo{},
			},
			expectError: true,
			errorMsg:    "todos should have at least one todo",
		},
		{
			name: "empty content",
			input: TodoWriteInput{
				Todos: []Todo{
					{
						Content:  "",
						Status:   Pending,
						Priority: Medium,
					},
				},
			},
			expectError: true,
			errorMsg:    "todo 0 content is required",
		},
		{
			name: "empty status",
			input: TodoWriteInput{
				Todos: []Todo{
					{
						Content:  "Test todo",
						Status:   "",
						Priority: Medium,
					},
				},
			},
			expectError: true,
			errorMsg:    "todo 0 status must be one of",
		},
		{
			name: "empty priority",
			input: TodoWriteInput{
				Todos: []Todo{
					{
						Content:  "Test todo",
						Status:   Pending,
						Priority: "",
					},
				},
			},
			expectError: true,
			errorMsg:    "todo 0 priority must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			err := tool.ValidateInput(NewBasicState(), string(input))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTodoWriteTool_Execute(t *testing.T) {
	tool := &TodoWriteTool{}

	t.Run("write todos to file", func(t *testing.T) {
		// Create a temporary file path for todos
		tempDir := t.TempDir()
		todoPath := filepath.Join(tempDir, "test-todos.json")

		// Set up state with custom todo path
		s := NewBasicState()
		s.SetTodoFilePath(todoPath)

		input := TodoWriteInput{
			Todos: []Todo{
				{
					Content:  "Test todo",
					Status:   Pending,
					Priority: Medium,
				},
			},
		}
		params, _ := json.Marshal(input)
		result := execute(tool, context.Background(), s, string(params))

		assert.Empty(t, result.Error)
		assert.Contains(t, result.Result, "Todos have been written to")

		// Verify the file was created
		assert.FileExists(t, todoPath)

		// Verify file contents
		fileData, err := os.ReadFile(todoPath)
		assert.NoError(t, err)

		var savedInput TodoWriteInput
		err = json.Unmarshal(fileData, &savedInput)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(savedInput.Todos))
		assert.Equal(t, "Test todo", savedInput.Todos[0].Content)
		assert.Equal(t, Pending, savedInput.Todos[0].Status)
		assert.Equal(t, Medium, savedInput.Todos[0].Priority)
	})

	t.Run("write multiple todos to file", func(t *testing.T) {
		// Create a temporary file path for todos
		tempDir := t.TempDir()
		todoPath := filepath.Join(tempDir, "test-todos-multiple.json")

		// Set up state with custom todo path
		s := NewBasicState()
		s.SetTodoFilePath(todoPath)

		input := TodoWriteInput{
			Todos: []Todo{
				{
					Content:  "High priority todo",
					Status:   Pending,
					Priority: High,
				},
				{
					Content:  "Medium priority todo",
					Status:   InProgress,
					Priority: Medium,
				},
				{
					Content:  "Completed todo",
					Status:   Completed,
					Priority: Low,
				},
			},
		}
		params, _ := json.Marshal(input)
		result := execute(tool, context.Background(), s, string(params))

		assert.Empty(t, result.Error)

		// Verify file contents
		fileData, err := os.ReadFile(todoPath)
		assert.NoError(t, err)

		var savedInput TodoWriteInput
		err = json.Unmarshal(fileData, &savedInput)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(savedInput.Todos))
	})

	t.Run("invalid JSON", func(t *testing.T) {
		s := NewBasicState()
		result := execute(tool, context.Background(), s, "invalid json")
		assert.Contains(t, result.Error, "invalid input")
		assert.Empty(t, result.Result)
	})

	t.Run("handle non-writable file", func(t *testing.T) {
		// Set up state with a non-writable path
		s := NewBasicState()
		s.SetTodoFilePath("/non-existent-dir/non-writable-file.json")

		input := TodoWriteInput{
			Todos: []Todo{
				{
					Content:  "Test todo",
					Status:   Pending,
					Priority: Medium,
				},
			},
		}
		params, _ := json.Marshal(input)
		result := execute(tool, context.Background(), s, string(params))

		assert.Contains(t, result.Error, "failed to write todos to file")
		assert.Empty(t, result.Result)
	})
}
