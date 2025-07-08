package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Subagent Response:") {
			t.Errorf("Expected subagent response header in output, got: %s", output)
		}
		if !strings.Contains(output, "Question: What are the best practices for Go error handling?") {
			t.Errorf("Expected question in output, got: %s", output)
		}
		if !strings.Contains(output, "Here are the key best practices") {
			t.Errorf("Expected response content in output, got: %s", output)
		}
		if !strings.Contains(output, "Always check errors explicitly") {
			t.Errorf("Expected detailed response content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Question: How do I implement a binary search in Go?") {
			t.Errorf("Expected question in output, got: %s", output)
		}
		if !strings.Contains(output, "func binarySearch") {
			t.Errorf("Expected code implementation in output, got: %s", output)
		}
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

		if strings.Contains(output, "Question: ") {
			t.Errorf("Should not show empty question, got: %s", output)
		}
		if !strings.Contains(output, "Based on the available information") {
			t.Errorf("Expected response content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Subagent Response:") {
			t.Errorf("Expected subagent response header in output, got: %s", output)
		}
		if strings.Contains(output, "Question: ") {
			t.Errorf("Should not show empty question field, got: %s", output)
		}
		if !strings.Contains(output, "This is a direct response without additional metadata.") {
			t.Errorf("Expected response content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "microservices architecture") {
			t.Errorf("Expected multiline response content in output, got: %s", output)
		}
		if !strings.Contains(output, "Architecture Design:") {
			t.Errorf("Expected section headers in response, got: %s", output)
		}
		if !strings.Contains(output, "Deployment and Operations:") {
			t.Errorf("Expected full response content in output, got: %s", output)
		}
		if !strings.Contains(output, "scalability, maintainability, and resilience") {
			t.Errorf("Expected complete response content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Question: What is the answer?") {
			t.Errorf("Expected question in output, got: %s", output)
		}
		// The response should still be there, just empty
		if !strings.Contains(output, "Subagent Response:") {
			t.Errorf("Expected subagent response header even with empty response, got: %s", output)
		}
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   false,
			Error:     "Subagent processing failed: timeout",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Subagent processing failed: timeout") {
			t.Errorf("Expected error message in output, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for subagent") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "subagent",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for subagent") {
			t.Errorf("Expected invalid metadata error for nil metadata, got: %s", output)
		}
	})
}
