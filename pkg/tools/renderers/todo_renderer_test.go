package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestTodoRenderer(t *testing.T) {
	renderer := &TodoRenderer{}

	t.Run("Todo list with items", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "todo",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.TodoMetadata{
				Action: "read",
				TodoList: []tools.TodoItem{
					{
						ID:       "1",
						Content:  "Fix the bug",
						Status:   "pending",
						Priority: "high",
					},
					{
						ID:       "2",
						Content:  "Add tests",
						Status:   "completed",
						Priority: "medium",
					},
					{
						ID:       "3",
						Content:  "Update docs",
						Status:   "in_progress",
						Priority: "low",
					},
				},
				Statistics: tools.TodoStats{
					Total:      3,
					Pending:    1,
					InProgress: 1,
					Completed:  1,
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Todo List:", "Expected todo list header")
		assert.Contains(t, output, "Total: 3", "Expected statistics")
		assert.Contains(t, output, "Fix the bug", "Expected todo content")
		assert.Contains(t, output, "✓", "Expected completed icon")
		assert.Contains(t, output, "→", "Expected in progress icon")
		assert.Contains(t, output, "○", "Expected pending icon")
	})

	t.Run("Todo update action", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "todo",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.TodoMetadata{
				Action:   "write",
				TodoList: []tools.TodoItem{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Todo List Updated:", "Expected todo list updated header")
	})
}
