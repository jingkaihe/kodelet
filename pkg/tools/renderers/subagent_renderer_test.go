package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestSubAgentRenderer(t *testing.T) {
	renderer := &SubAgentRenderer{}

	t.Run("Successful subagent response with all fields", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "What are the best practices for Go error handling?",
				Response: "Here are the key best practices for Go error handling:\n\n1. Always check errors explicitly\n2. Use meaningful error messages\n3. Wrap errors with context using fmt.Errorf\n4. Create custom error types when needed\n5. Don't ignore errors",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Subagent Response:", "Expected subagent response header in output")
		assert.Contains(t, output, "Question: What are the best practices for Go error handling?", "Expected question in output")
		assert.Contains(t, output, "Here are the key best practices", "Expected response content in output")
		assert.Contains(t, output, "Always check errors explicitly", "Expected detailed response content in output")
	})

	t.Run("Subagent response with question only", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "How do I implement a binary search in Go?",
				Response: "Here's a simple implementation of binary search in Go:\n\nfunc binarySearch(arr []int, target int) int {\n    left, right := 0, len(arr)-1\n    for left <= right {\n        mid := (left + right) / 2\n        if arr[mid] == target {\n            return mid\n        } else if arr[mid] < target {\n            left = mid + 1\n        } else {\n            right = mid - 1\n        }\n    }\n    return -1\n}",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Question: How do I implement a binary search in Go?", "Expected question in output")
		assert.Contains(t, output, "func binarySearch", "Expected code implementation in output")
	})

	t.Run("Subagent response with response only", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "",
				Response: "Based on the available information, this seems to be a straightforward question about basic programming concepts.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.NotContains(t, output, "Question: ", "Should not show empty question")
		assert.Contains(t, output, "Based on the available information", "Expected response content in output")
	})

	t.Run("Subagent response with only response content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "",
				Response: "This is a direct response without additional metadata.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Subagent Response:", "Expected subagent response header in output")
		assert.NotContains(t, output, "Question: ", "Should not show empty question field")
		assert.Contains(t, output, "This is a direct response without additional metadata.", "Expected response content in output")
	})

	t.Run("Subagent response with long multiline content", func(t *testing.T) {
		longResponse := `When implementing a microservices architecture, consider these key aspects:

Architecture Design:
- Service boundaries should be aligned with business capabilities
- Each service should own its data and have a single responsibility
- Use domain-driven design principles to identify service boundaries

Communication:
- Prefer asynchronous communication when possible
- Use message queues or event streams for inter-service communication
- Implement circuit breakers and retry mechanisms for resilience

Data Management:
- Each service should have its own database
- Avoid shared databases between services
- Consider eventual consistency patterns

Deployment and Operations:
- Containerize your services using Docker
- Use orchestration platforms like Kubernetes
- Implement comprehensive monitoring and logging
- Set up distributed tracing for debugging

This approach ensures scalability, maintainability, and resilience in your system.`

		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "What should I consider when implementing microservices?",
				Response: longResponse,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "microservices architecture", "Expected multiline response content in output")
		assert.Contains(t, output, "Architecture Design:", "Expected section headers in response")
		assert.Contains(t, output, "Deployment and Operations:", "Expected full response content in output")
		assert.Contains(t, output, "scalability, maintainability, and resilience", "Expected complete response content in output")
	})

	t.Run("Empty response content", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "What is the answer?",
				Response: "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Question: What is the answer?", "Expected question in output")
		// The response should still be there, just empty
		assert.Contains(t, output, "Subagent Response:", "Expected subagent response header even with empty response")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   false,
			Error:     "Subagent processing failed: timeout",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Subagent processing failed: timeout", "Expected error message in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for subagent", "Expected invalid metadata error")
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for subagent", "Expected invalid metadata error for nil metadata")
	})

	t.Run("Subagent response with workflow", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "Create a PR",
				Response: "PR created successfully",
				Workflow: "github/pr",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Workflow: github/pr", "Expected workflow in output")
		assert.Contains(t, output, "Question: Create a PR", "Expected question in output")
		assert.Contains(t, output, "PR created successfully", "Expected response in output")
	})

	t.Run("Subagent response with cwd", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "Analyze the code",
				Response: "Code analysis complete",
				Cwd:      "/home/user/project",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Directory: /home/user/project", "Expected cwd in output")
		assert.Contains(t, output, "Question: Analyze the code", "Expected question in output")
		assert.Contains(t, output, "Code analysis complete", "Expected response in output")
	})

	t.Run("Subagent response with workflow and cwd", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "Review changes",
				Response: "Review complete: LGTM",
				Workflow: "jingkaihe@skills/code/reviewer",
				Cwd:      "/tmp/myproject",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Workflow: jingkaihe@skills/code/reviewer", "Expected workflow in output")
		assert.Contains(t, output, "Directory: /tmp/myproject", "Expected cwd in output")
		assert.Contains(t, output, "Question: Review changes", "Expected question in output")
		assert.Contains(t, output, "Review complete: LGTM", "Expected response in output")
	})

	t.Run("Subagent response with workflow only no question", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.SubAgentMetadata{
				Question: "",
				Response: "Commit message generated",
				Workflow: "commit",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Workflow: commit", "Expected workflow in output")
		assert.NotContains(t, output, "Question:", "Should not show empty question")
		assert.Contains(t, output, "Commit message generated", "Expected response in output")
	})
}
