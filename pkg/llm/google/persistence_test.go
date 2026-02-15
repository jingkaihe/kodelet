package google

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestExtractMessages(t *testing.T) {
	// Test basic message extraction with Google GenAI format
	rawMessages := `[
		{
			"role": "user",
			"parts": [
				{
					"text": "Hello there!"
				}
			]
		},
		{
			"role": "model",
			"parts": [
				{
					"text": "Hi! How can I help you today?"
				}
			]
		}
	]`

	messages, err := ExtractMessages([]byte(rawMessages), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 2)

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Hello there!", messages[0].Content)

	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "Hi! How can I help you today?", messages[1].Content)
}

func TestExtractMessagesWithToolUse(t *testing.T) {
	// Test with tool use and tool result in Google format
	rawMessages := `[
		{
			"role": "user",
			"parts": [
				{
					"text": "List the files in the current directory"
				}
			]
		},
		{
			"role": "model",
			"parts": [
				{
					"text": "I'll list the files for you."
				},
				{
					"functionCall": {
						"name": "bash",
						"args": {
							"command": "ls -la"
						}
					}
				}
			]
		},
		{
			"role": "user",
			"parts": [
				{
					"functionResponse": {
						"name": "bash",
						"response": {
							"call_id": "call_123",
							"result": "total 8\ndrwxr-xr-x 2 user user 4096 Jan 1 10:00 .\ndrwxr-xr-x 3 user user 4096 Jan 1 09:59 ..\n-rw-r--r-- 1 user user    0 Jan 1 10:00 file.txt",
							"error": false
						}
					}
				}
			]
		},
		{
			"role": "model",
			"parts": [
				{
					"text": "Here are the files in the current directory. There's one file called file.txt in the directory."
				}
			]
		}
	]`

	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_123": {
			ToolName: "bash",
			Success:  true,
		},
	}

	messages, err := ExtractMessages([]byte(rawMessages), toolResults)
	assert.NoError(t, err)
	assert.Len(t, messages, 5) // user + assistant text + tool call + tool result + final assistant

	// User message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "List the files in the current directory", messages[0].Content)

	// Assistant response before tool call
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "I'll list the files for you.", messages[1].Content)

	// Tool call message
	assert.Equal(t, "assistant", messages[2].Role)
	assert.Contains(t, messages[2].Content, "bash")
	assert.Contains(t, messages[2].Content, "ls -la")

	// Tool result message
	assert.Equal(t, "user", messages[3].Role)
	assert.Contains(t, messages[3].Content, "file.txt")

	// Final assistant message
	assert.Equal(t, "assistant", messages[4].Role)
	assert.Contains(t, messages[4].Content, "Here are the files")
}

func TestExtractMessagesWithThinking(t *testing.T) {
	// Test with thinking content (should be skipped in extraction)
	rawMessages := `[
		{
			"role": "user",
			"parts": [
				{
					"text": "What is 2+2?"
				}
			]
		},
		{
			"role": "model",
			"parts": [
				{
					"text": "I need to calculate 2+2. This is a simple addition problem.",
					"thought": true
				},
				{
					"text": "2 + 2 = 4"
				}
			]
		}
	]`

	messages, err := ExtractMessages([]byte(rawMessages), nil)
	assert.NoError(t, err)
	assert.Len(t, messages, 2) // User message + assistant response (thinking should be filtered out)

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "What is 2+2?", messages[0].Content)

	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "2 + 2 = 4", messages[1].Content)
	// The thinking text should not appear in the extracted message
	assert.NotContains(t, messages[1].Content, "I need to calculate")
}

func TestExtractMessagesWithMultipleToolResults(t *testing.T) {
	// Test with multiple tool calls and results
	rawMessages := `[
		{
			"role": "user",
			"parts": [
				{
					"text": "Get weather and time"
				}
			]
		},
		{
			"role": "model",
			"parts": [
				{
					"functionCall": {
						"name": "get_time",
						"args": {}
					}
				},
				{
					"functionCall": {
						"name": "get_weather",
						"args": {
							"location": "NYC"
						}
					}
				}
			]
		},
		{
			"role": "user",
			"parts": [
				{
					"functionResponse": {
						"name": "get_time",
						"response": {
							"call_id": "call_time",
							"result": "Current time: 10:30 AM",
							"error": false
						}
					}
				}
			]
		},
		{
			"role": "user",
			"parts": [
				{
					"functionResponse": {
						"name": "get_weather",
						"response": {
							"call_id": "call_weather",
							"result": "Weather: 72°F, sunny",
							"error": false
						}
					}
				}
			]
		},
		{
			"role": "model",
			"parts": [
				{
					"text": "Here's the info you requested."
				}
			]
		}
	]`

	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_time": {
			ToolName: "get_time",
			Success:  true,
		},
		"call_weather": {
			ToolName: "get_weather",
			Success:  true,
		},
	}

	messages, err := ExtractMessages([]byte(rawMessages), toolResults)
	assert.NoError(t, err)
	assert.Len(t, messages, 6) // user + 2 tool calls + 2 tool results + assistant final

	// Check user message
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Get weather and time", messages[0].Content)

	// Check first tool call
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Contains(t, messages[1].Content, "get_time")

	// Check second tool call
	assert.Equal(t, "assistant", messages[2].Role)
	assert.Contains(t, messages[2].Content, "get_weather")
	assert.Contains(t, messages[2].Content, "NYC")

	// Check first tool result
	assert.Equal(t, "user", messages[3].Role)
	assert.Contains(t, messages[3].Content, "10:30 AM")

	// Check second tool result
	assert.Equal(t, "user", messages[4].Role)
	assert.Contains(t, messages[4].Content, "72°F, sunny")

	// Check final assistant message
	assert.Equal(t, "assistant", messages[5].Role)
	assert.Equal(t, "Here's the info you requested.", messages[5].Content)
}

func TestExtractMessages_UsesStructuredToolResultByCallID(t *testing.T) {
	rawMessages := `[
		{
			"role": "user",
			"parts": [
				{
					"text": "Run command"
				}
			]
		},
		{
			"role": "user",
			"parts": [
				{
					"functionResponse": {
						"name": "bash",
						"response": {
							"call_id": "call_abc",
							"result": "raw-output",
							"error": false
						}
					}
				}
			]
		}
	]`

	toolResults := map[string]tooltypes.StructuredToolResult{
		"call_abc": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: tooltypes.BashMetadata{
				Command:  "echo hi",
				ExitCode: 0,
				Output:   "structured-marker",
			},
		},
	}

	messages, err := ExtractMessages([]byte(rawMessages), toolResults)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Contains(t, messages[1].Content, "structured-marker")
	assert.NotContains(t, messages[1].Content, "raw-output")
}

func TestExtractMessages_FallsBackToToolNameWhenCallIDMissing(t *testing.T) {
	rawMessages := `[
		{
			"role": "user",
			"parts": [
				{
					"text": "Run command"
				}
			]
		},
		{
			"role": "user",
			"parts": [
				{
					"functionResponse": {
						"name": "bash",
						"response": {
							"result": "raw-output-without-call-id",
							"error": false
						}
					}
				}
			]
		}
	]`

	toolResults := map[string]tooltypes.StructuredToolResult{
		"bash": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: tooltypes.BashMetadata{
				Command:  "echo fallback",
				ExitCode: 0,
				Output:   "tool-name-fallback-marker",
			},
		},
	}

	messages, err := ExtractMessages([]byte(rawMessages), toolResults)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Contains(t, messages[1].Content, "tool-name-fallback-marker")
	assert.NotContains(t, messages[1].Content, "raw-output-without-call-id")
}
