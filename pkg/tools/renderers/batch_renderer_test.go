package renderers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

		assert.Contains(t, output, "Batch Execution: Test batch", "Expected batch description")
		assert.Contains(t, output, "Success: 1", "Expected success count")
		assert.Contains(t, output, "Task 1: file_read", "Expected sub-task info")
	})
}
