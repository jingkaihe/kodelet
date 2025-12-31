package bridge

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type mockToolResult struct {
	result         string
	err            string
	structuredData tooltypes.StructuredToolResult
}

func (m *mockToolResult) GetResult() string                              { return m.result }
func (m *mockToolResult) GetError() string                               { return m.err }
func (m *mockToolResult) IsError() bool                                  { return m.err != "" }
func (m *mockToolResult) AssistantFacing() string                        { return m.result }
func (m *mockToolResult) StructuredData() tooltypes.StructuredToolResult { return m.structuredData }

func TestToolContentGenerator_GenerateBashContent(t *testing.T) {
	gen := &ToolContentGenerator{}

	t.Run("successful bash command", func(t *testing.T) {
		// Output wrapped in code block to preserve newlines
		result := &mockToolResult{
			result: "hello world",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "bash",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.BashMetadata{
					Command:       "echo hello",
					ExitCode:      0,
					Output:        "hello world",
					ExecutionTime: time.Second,
					WorkingDir:    "/home/user",
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		outputContent := content[0]["content"].(map[string]any)
		assert.Equal(t, acptypes.ContentTypeText, outputContent["type"])
		// Output wrapped in code fences
		assert.Equal(t, "```\nhello world\n```", outputContent["text"])
	})

	t.Run("bash command with multiline output", func(t *testing.T) {
		result := &mockToolResult{
			result: "line1\nline2\n",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "bash",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.BashMetadata{
					Command:  "echo -e 'line1\\nline2'",
					ExitCode: 0,
					Output:   "line1\nline2\n",
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		outputContent := content[0]["content"].(map[string]any)
		// Trailing newline preserved, no extra newline added
		assert.Equal(t, "```\nline1\nline2\n```", outputContent["text"])
	})

	t.Run("successful bash command with no output", func(t *testing.T) {
		result := &mockToolResult{
			result: "",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "bash",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.BashMetadata{
					Command:       "touch file.txt",
					ExitCode:      0,
					Output:        "",
					ExecutionTime: time.Second,
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 0)
	})

	t.Run("bash command with error", func(t *testing.T) {
		// Errors also wrapped in code blocks
		result := &mockToolResult{
			err: "command failed",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "bash",
				Success:   false,
				Error:     "command failed",
				Timestamp: time.Now(),
				Metadata: &tooltypes.BashMetadata{
					Command:       "failing-cmd",
					ExitCode:      1,
					Output:        "error output",
					ExecutionTime: time.Second,
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		errContent := content[0]["content"].(map[string]any)
		assert.Equal(t, acptypes.ContentTypeText, errContent["type"])
		assert.Equal(t, "```\ncommand failed\n```", errContent["text"])
	})

	t.Run("background bash process", func(t *testing.T) {
		result := &mockToolResult{
			result: "Process started",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "bash_background",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.BackgroundBashMetadata{
					Command:   "sleep 100",
					PID:       12345,
					LogPath:   "/tmp/out.log",
					StartTime: time.Now(),
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		bgContent := content[0]["content"].(map[string]any)
		assert.Contains(t, bgContent["text"], "PID: 12345")
		assert.Contains(t, bgContent["text"], "/tmp/out.log")
	})
}

func TestToolContentGenerator_GenerateFileReadContent(t *testing.T) {
	gen := &ToolContentGenerator{}

	t.Run("successful file read", func(t *testing.T) {
		result := &mockToolResult{
			result: "file contents",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "file_read",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.FileReadMetadata{
					FilePath:       "/home/user/main.go",
					Offset:         1,
					LineLimit:      100,
					Lines:          []string{"package main", "", "func main() {}"},
					Language:       "go",
					Truncated:      false,
					RemainingLines: 0,
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		resourceContent := content[0]["content"].(map[string]any)
		assert.Equal(t, acptypes.ContentTypeResource, resourceContent["type"])

		resource := resourceContent["resource"].(map[string]any)
		assert.Equal(t, "file:///home/user/main.go", resource["uri"])
		assert.Equal(t, "text/x-go", resource["mimeType"])
		assert.Contains(t, resource["text"], "package main")
	})

	t.Run("truncated file read", func(t *testing.T) {
		result := &mockToolResult{
			result: "file contents",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "file_read",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.FileReadMetadata{
					FilePath:       "/home/user/large.txt",
					Offset:         1,
					LineLimit:      100,
					Lines:          []string{"line 1", "line 2"},
					Truncated:      true,
					RemainingLines: 500,
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 2)

		assert.Equal(t, ToolCallContentTypeContent, content[1]["type"])
		truncContent := content[1]["content"].(map[string]any)
		assert.Contains(t, truncContent["text"], "500 lines remaining")
	})
}

func TestToolContentGenerator_GenerateFileWriteContent(t *testing.T) {
	gen := &ToolContentGenerator{}

	t.Run("successful file write", func(t *testing.T) {
		result := &mockToolResult{
			result: "file written",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "file_write",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.FileWriteMetadata{
					FilePath: "/home/user/new.txt",
					Content:  "new content",
					Size:     11,
					Language: "text",
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeDiff, content[0]["type"])
		assert.Equal(t, "/home/user/new.txt", content[0]["path"])
		assert.Nil(t, content[0]["oldText"])
		assert.Equal(t, "new content", content[0]["newText"])
	})
}

func TestToolContentGenerator_GenerateFileEditContent(t *testing.T) {
	gen := &ToolContentGenerator{}

	t.Run("single edit", func(t *testing.T) {
		result := &mockToolResult{
			result: "file edited",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "file_edit",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.FileEditMetadata{
					FilePath: "/home/user/main.go",
					Edits: []tooltypes.Edit{
						{
							StartLine:  5,
							EndLine:    7,
							OldContent: "old code",
							NewContent: "new code",
						},
					},
					Language:      "go",
					ReplaceAll:    false,
					ReplacedCount: 1,
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeDiff, content[0]["type"])
		assert.Equal(t, "/home/user/main.go", content[0]["path"])
		assert.Equal(t, "old code", content[0]["oldText"])
		assert.Equal(t, "new code", content[0]["newText"])
	})

	t.Run("replace all with multiple edits", func(t *testing.T) {
		result := &mockToolResult{
			result: "file edited",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "file_edit",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.FileEditMetadata{
					FilePath: "/home/user/main.go",
					Edits: []tooltypes.Edit{
						{StartLine: 1, EndLine: 1, OldContent: "foo", NewContent: "bar"},
						{StartLine: 10, EndLine: 10, OldContent: "foo", NewContent: "bar"},
						{StartLine: 20, EndLine: 20, OldContent: "foo", NewContent: "bar"},
					},
					Language:      "go",
					ReplaceAll:    true,
					ReplacedCount: 3,
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 4) // 3 diffs + 1 summary

		for i := 0; i < 3; i++ {
			assert.Equal(t, ToolCallContentTypeDiff, content[i]["type"])
		}

		assert.Equal(t, ToolCallContentTypeContent, content[3]["type"])
		summaryContent := content[3]["content"].(map[string]any)
		assert.Contains(t, summaryContent["text"], "Replaced 3 occurrences")
	})
}

func TestToolContentGenerator_GenerateSubAgentContent(t *testing.T) {
	gen := &ToolContentGenerator{}

	t.Run("successful subagent", func(t *testing.T) {
		result := &mockToolResult{
			result: "found the answer",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "subagent",
				Success:   true,
				Timestamp: time.Now(),
				Metadata: &tooltypes.SubAgentMetadata{
					Question: "What is the meaning of life?",
					Response: "42",
				},
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 2)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		qContent := content[0]["content"].(map[string]any)
		assert.Contains(t, qContent["text"], "What is the meaning of life?")

		assert.Equal(t, ToolCallContentTypeContent, content[1]["type"])
		aContent := content[1]["content"].(map[string]any)
		assert.Equal(t, "42", aContent["text"])
	})
}

func TestToolContentGenerator_GenerateDefaultContent(t *testing.T) {
	gen := &ToolContentGenerator{}

	t.Run("unknown tool", func(t *testing.T) {
		result := &mockToolResult{
			result: "some result",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "unknown_tool",
				Success:   true,
				Timestamp: time.Now(),
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		textContent := content[0]["content"].(map[string]any)
		assert.Equal(t, acptypes.ContentTypeText, textContent["type"])
		assert.Equal(t, "some result", textContent["text"])
	})

	t.Run("error result", func(t *testing.T) {
		result := &mockToolResult{
			err: "something went wrong",
			structuredData: tooltypes.StructuredToolResult{
				ToolName:  "unknown_tool",
				Success:   false,
				Error:     "something went wrong",
				Timestamp: time.Now(),
			},
		}

		content := gen.GenerateToolContent(result)
		require.Len(t, content, 1)

		assert.Equal(t, ToolCallContentTypeContent, content[0]["type"])
		textContent := content[0]["content"].(map[string]any)
		assert.Contains(t, textContent["text"], "Error:")
	})
}

func TestLanguageToMimeType(t *testing.T) {
	tests := []struct {
		lang     string
		expected string
	}{
		{"go", "text/x-go"},
		{"Go", "text/x-go"},
		{"python", "text/x-python"},
		{"javascript", "text/javascript"},
		{"typescript", "text/typescript"},
		{"json", "application/json"},
		{"yaml", "text/yaml"},
		{"unknown", "text/plain"},
		{"", "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			assert.Equal(t, tt.expected, languageToMimeType(tt.lang))
		})
	}
}

func TestMarkdownEscape(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple text",
			input:    "hello world",
			expected: "```\nhello world\n```",
		},
		{
			name:     "text with trailing newline",
			input:    "hello\n",
			expected: "```\nhello\n```",
		},
		{
			name:     "multiline text",
			input:    "line1\nline2",
			expected: "```\nline1\nline2\n```",
		},
		{
			name:     "text with code fence",
			input:    "some ```code``` here",
			expected: "````\nsome ```code``` here\n````",
		},
		{
			name:     "text with longer fence",
			input:    "````nested````",
			expected: "`````\n````nested````\n`````",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, markdownEscape(tt.input))
		})
	}
}
