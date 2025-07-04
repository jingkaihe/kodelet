package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestBatchRenderer(t *testing.T) {
	renderer := &BatchRenderer{}

	t.Run("Batch execution with sub-results", func(t *testing.T) {
		subResult := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath: "/test/file.txt",
				Lines:    []string{"content"},
			},
		}

		result := tools.StructuredToolResult{
			ToolName:  "batch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.BatchMetadata{
				Description:   "Test batch",
				SuccessCount:  1,
				FailureCount:  0,
				ExecutionTime: time.Second,
				SubResults:    []tools.StructuredToolResult{subResult},
			},
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Batch Execution: Test batch") {
			t.Errorf("Expected batch description, got: %s", output)
		}
		if !strings.Contains(output, "Success: 1") {
			t.Errorf("Expected success count, got: %s", output)
		}
		if !strings.Contains(output, "Task 1: file_read") {
			t.Errorf("Expected sub-task info, got: %s", output)
		}
	})
}