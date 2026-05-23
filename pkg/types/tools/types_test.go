package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringifyToolResult(t *testing.T) {
	tests := []struct {
		name           string
		result         string
		err            string
		expectedOutput string
	}{
		{
			name:   "both result and error provided",
			result: "operation successful",
			err:    "minor warning occurred",
			expectedOutput: `<error>
minor warning occurred
</error>
<result>
operation successful
</result>
`,
		},
		{
			name:   "only result provided",
			result: "command executed successfully",
			err:    "",
			expectedOutput: `<result>
command executed successfully
</result>
`,
		},
		{
			name:   "only error provided",
			result: "",
			err:    "command failed",
			expectedOutput: `<error>
command failed
</error>
<result>
(No output)
</result>
`,
		},
		{
			name:   "neither result nor error provided",
			result: "",
			err:    "",
			expectedOutput: `<result>
(No output)
</result>
`,
		},
		{
			name:   "result with whitespace",
			result: "  output with spaces  ",
			err:    "",
			expectedOutput: `<result>
  output with spaces  
</result>
`,
		},
		{
			name:   "multiline result",
			result: "line 1\nline 2\nline 3",
			err:    "",
			expectedOutput: `<result>
line 1
line 2
line 3
</result>
`,
		},
		{
			name:   "multiline error",
			result: "some output",
			err:    "error line 1\nerror line 2",
			expectedOutput: `<error>
error line 1
error line 2
</error>
<result>
some output
</result>
`,
		},
		{
			name:   "empty strings with spaces",
			result: "   ",
			err:    "",
			expectedOutput: `<result>
   
</result>
`,
		},
		{
			name:   "special characters in result",
			result: "output with <>&\"' special chars",
			err:    "",
			expectedOutput: `<result>
output with <>&"' special chars
</result>
`,
		},
		{
			name:   "special characters in error",
			result: "normal output",
			err:    "error with <>&\"' special chars",
			expectedOutput: `<error>
error with <>&"' special chars
</error>
<result>
normal output
</result>
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := StringifyToolResult(tt.result, tt.err)
			assert.Equal(t, tt.expectedOutput, actual)
		})
	}
}

func TestStringifyToolResult_Behavior_Changes(t *testing.T) {
	t.Run("empty result now shows '(No output)' instead of no result section", func(t *testing.T) {
		// This test specifically validates the behavioral change made in the diff
		result := StringifyToolResult("", "")

		// Should always contain a result section now
		assert.Contains(t, result, "<result>")
		assert.Contains(t, result, "</result>")
		assert.Contains(t, result, "(No output)")

		// Should not contain error section when no error
		assert.NotContains(t, result, "<error>")
		assert.NotContains(t, result, "</error>")
	})

	t.Run("empty result with error still shows '(No output)' in result section", func(t *testing.T) {
		result := StringifyToolResult("", "some error")

		// Should contain both error and result sections
		assert.Contains(t, result, "<error>")
		assert.Contains(t, result, "</error>")
		assert.Contains(t, result, "<result>")
		assert.Contains(t, result, "</result>")
		assert.Contains(t, result, "(No output)")
		assert.Contains(t, result, "some error")
	})

	t.Run("non-empty result should not show '(No output)'", func(t *testing.T) {
		result := StringifyToolResult("actual output", "")

		// Should contain result section with actual content
		assert.Contains(t, result, "<result>")
		assert.Contains(t, result, "</result>")
		assert.Contains(t, result, "actual output")

		// Should not show the fallback message
		assert.NotContains(t, result, "(No output)")
	})
}

func TestStringifyToolResult_XMLFormatting(t *testing.T) {
	t.Run("validates proper XML structure", func(t *testing.T) {
		result := StringifyToolResult("test output", "test error")

		// Check that error comes before result
		errorStart := []byte("<error>")
		errorEnd := []byte("</error>")
		resultStart := []byte("<result>")
		resultEnd := []byte("</result>")

		output := []byte(result)

		errorStartPos := indexOf(output, errorStart)
		errorEndPos := indexOf(output, errorEnd)
		resultStartPos := indexOf(output, resultStart)
		resultEndPos := indexOf(output, resultEnd)

		// All positions should be found
		assert.NotEqual(t, -1, errorStartPos)
		assert.NotEqual(t, -1, errorEndPos)
		assert.NotEqual(t, -1, resultStartPos)
		assert.NotEqual(t, -1, resultEndPos)

		// Error should come before result
		assert.Less(t, errorStartPos, resultStartPos)
		assert.Less(t, errorEndPos, resultStartPos)

		// Tags should be properly nested
		assert.Less(t, errorStartPos, errorEndPos)
		assert.Less(t, resultStartPos, resultEndPos)
	})
}

func TestBaseToolResultMethodsAndStructuredData(t *testing.T) {
	result := BaseToolResult{Result: "done"}

	assert.Equal(t, `<result>
done
</result>
`, result.AssistantFacing())
	assert.False(t, result.IsError())
	assert.Empty(t, result.GetError())
	assert.Equal(t, "done", result.GetResult())

	structured := result.StructuredData()
	assert.Equal(t, "unknown", structured.ToolName)
	assert.True(t, structured.Success)
	assert.Empty(t, structured.Error)
	assert.Nil(t, structured.Metadata)
	assert.False(t, structured.Timestamp.IsZero())

	errorResult := BaseToolResult{Error: "failed"}
	assert.Equal(t, `<error>
failed
</error>
`, errorResult.AssistantFacing())
	assert.True(t, errorResult.IsError())
	assert.Equal(t, "failed", errorResult.GetError())
	assert.Empty(t, errorResult.GetResult())
	assert.False(t, errorResult.StructuredData().Success)
}

func TestBlockedToolResultMethodsAndStructuredData(t *testing.T) {
	result := NewBlockedToolResult("bash", "policy denied")

	assert.Equal(t, "bash", result.ToolName)
	assert.Equal(t, "policy denied", result.Reason)
	assert.Contains(t, result.AssistantFacing(), "Tool execution was blocked by security hook: policy denied")
	assert.True(t, result.IsError())
	assert.Equal(t, "blocked by hook: policy denied", result.GetError())
	assert.Empty(t, result.GetResult())

	structured := result.StructuredData()
	assert.Equal(t, "bash", structured.ToolName)
	assert.False(t, structured.Success)
	assert.Equal(t, "blocked by hook: policy denied", structured.Error)
	require.IsType(t, BlockedMetadata{}, structured.Metadata)
	metadata := structured.Metadata.(BlockedMetadata)
	assert.Equal(t, "blocked", metadata.ToolType())
	assert.Equal(t, "bash", metadata.ToolName)
	assert.Equal(t, "policy denied", metadata.Reason)
}

// Helper function to find byte slice in byte slice
func indexOf(haystack, needle []byte) int {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		found := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				found = false
				break
			}
		}
		if found {
			return i
		}
	}
	return -1
}
