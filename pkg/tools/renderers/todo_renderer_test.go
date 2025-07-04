package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Todo List:") {
			t.Errorf("Expected todo list header, got: %s", output)
		}
		if !strings.Contains(output, "Total: 3") {
			t.Errorf("Expected statistics, got: %s", output)
		}
		if !strings.Contains(output, "Fix the bug") {
			t.Errorf("Expected todo content, got: %s", output)
		}
		if !strings.Contains(output, "✓") {
			t.Errorf("Expected completed icon, got: %s", output)
		}
		if !strings.Contains(output, "→") {
			t.Errorf("Expected in progress icon, got: %s", output)
		}
		if !strings.Contains(output, "○") {
			t.Errorf("Expected pending icon, got: %s", output)
		}
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

		if !strings.Contains(output, "Todo List Updated:") {
			t.Errorf("Expected todo list updated header, got: %s", output)
		}
	})
}