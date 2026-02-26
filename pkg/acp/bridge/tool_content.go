// Package bridge provides the bridge between kodelet's message handler
// system and the ACP session update protocol.
package bridge

import (
	"fmt"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ToolCallContentType constants for tool call content
const (
	ToolCallContentTypeContent = "content"
	ToolCallContentTypeDiff    = "diff"
)

// ToolContentGenerator generates ACP-specific content for tool results
type ToolContentGenerator struct{}

// GenerateToolContent converts a ToolResult into ACP-formatted content array
func (g *ToolContentGenerator) GenerateToolContent(result tooltypes.ToolResult) []map[string]any {
	structured := result.StructuredData()

	switch structured.ToolName {
	case "bash", "bash_background":
		return g.generateBashContent(structured)
	case "code_execution":
		return g.generateCodeExecutionContent(structured)
	case "file_read":
		return g.generateFileReadContent(structured)
	case "file_write":
		return g.generateFileWriteContent(structured)
	case "file_edit":
		return g.generateFileEditContent(structured)
	case "apply_patch":
		return g.generateApplyPatchContent(structured)
	case "subagent":
		return g.generateSubAgentContent(structured)
	case "grep_tool", "glob_tool":
		return g.generateCodeBlockContent(structured, result)
	default:
		return g.generateDefaultContent(result)
	}
}

// generateBashContent generates content for bash command results
func (g *ToolContentGenerator) generateBashContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var bashMeta tooltypes.BashMetadata
	var bgMeta tooltypes.BackgroundBashMetadata

	if tooltypes.ExtractMetadata(structured.Metadata, &bashMeta) {
		// Error case: include command output (stdout/stderr) when available
		if structured.Error != "" {
			errText := structured.Error
			if strings.TrimSpace(bashMeta.Output) != "" {
				errText = fmt.Sprintf("%s\n\n%s", structured.Error, bashMeta.Output)
			}

			return []map[string]any{
				{
					"type": ToolCallContentTypeContent,
					"content": map[string]any{
						"type": acptypes.ContentTypeText,
						"text": markdownEscape(errText),
					},
				},
			}
		}

		// Success case: wrap output in code block to preserve newlines
		if bashMeta.Output != "" {
			return []map[string]any{
				{
					"type": ToolCallContentTypeContent,
					"content": map[string]any{
						"type": acptypes.ContentTypeText,
						"text": markdownEscape(bashMeta.Output),
					},
				},
			}
		}

		return []map[string]any{}
	}

	// Background processes still show their info
	if tooltypes.ExtractMetadata(structured.Metadata, &bgMeta) {
		return []map[string]any{
			{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": fmt.Sprintf("Background process started (PID: %d)\nLog: %s", bgMeta.PID, bgMeta.LogPath),
				},
			},
		}
	}

	return g.generateTextContent(structured.Error)
}

// markdownEscape wraps text in code fences, handling nested backticks
func markdownEscape(text string) string {
	escape := "```"
	// Find all code fence sequences and ensure our fence is longer
	for i := 0; i < len(text); {
		if text[i] == '`' {
			j := i
			for j < len(text) && text[j] == '`' {
				j++
			}
			fenceLen := j - i
			for fenceLen >= len(escape) {
				escape += "`"
			}
			i = j
		} else {
			i++
		}
	}

	result := escape + "\n" + text
	if !strings.HasSuffix(text, "\n") {
		result += "\n"
	}
	result += escape
	return result
}

// generateCodeExecutionContent generates content for code execution results
// Following the same pattern as bash: wrap output in code blocks to preserve formatting
func (g *ToolContentGenerator) generateCodeExecutionContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var meta tooltypes.CodeExecutionMetadata
	if !tooltypes.ExtractMetadata(structured.Metadata, &meta) {
		return g.generateTextContent(structured.Error)
	}

	// Error case: wrap in code block
	if structured.Error != "" {
		return []map[string]any{
			{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": markdownEscape(structured.Error),
				},
			},
		}
	}

	// Success case: wrap output in code block to preserve newlines
	if meta.Output != "" {
		return []map[string]any{
			{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": markdownEscape(meta.Output),
				},
			},
		}
	}

	return []map[string]any{}
}

// generateFileReadContent generates content for file read results using resource type
func (g *ToolContentGenerator) generateFileReadContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var meta tooltypes.FileReadMetadata
	if !tooltypes.ExtractMetadata(structured.Metadata, &meta) {
		return g.generateTextContent(structured.Error)
	}

	if structured.Error != "" {
		return g.generateTextContent(structured.Error)
	}

	content := strings.Join(meta.Lines, "\n")
	contentWithLineNumbers := osutil.ContentWithLineNumber(meta.Lines, meta.Offset)

	mimeType := "text/plain"
	if meta.Language != "" {
		mimeType = languageToMimeType(meta.Language)
	}

	result := []map[string]any{
		{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeResource,
				"resource": map[string]any{
					"uri":      fmt.Sprintf("file://%s", meta.FilePath),
					"mimeType": mimeType,
					"text":     contentWithLineNumbers,
				},
			},
		},
	}

	if meta.Truncated && meta.RemainingLines > 0 {
		result = append(result, map[string]any{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": fmt.Sprintf("... [%d lines remaining - use offset=%d to continue reading]",
					meta.RemainingLines, meta.Offset+len(meta.Lines)),
			},
		})
	}

	_ = content // suppress unused warning, may be used for non-line-numbered content later
	return result
}

// generateFileWriteContent generates content for file write results using diff type
func (g *ToolContentGenerator) generateFileWriteContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var meta tooltypes.FileWriteMetadata
	if !tooltypes.ExtractMetadata(structured.Metadata, &meta) {
		return g.generateTextContent(structured.Error)
	}

	if structured.Error != "" {
		return g.generateTextContent(structured.Error)
	}

	return []map[string]any{
		{
			"type":    ToolCallContentTypeDiff,
			"path":    meta.FilePath,
			"oldText": nil,
			"newText": meta.Content,
		},
	}
}

// generateFileEditContent generates content for file edit results using diff type
func (g *ToolContentGenerator) generateFileEditContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var meta tooltypes.FileEditMetadata
	if !tooltypes.ExtractMetadata(structured.Metadata, &meta) {
		return g.generateTextContent(structured.Error)
	}

	if structured.Error != "" {
		return g.generateTextContent(structured.Error)
	}

	var content []map[string]any

	for _, edit := range meta.Edits {
		content = append(content, map[string]any{
			"type":    ToolCallContentTypeDiff,
			"path":    meta.FilePath,
			"oldText": edit.OldContent,
			"newText": edit.NewContent,
		})
	}

	if meta.ReplaceAll && meta.ReplacedCount > 1 {
		content = append(content, map[string]any{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": fmt.Sprintf("Replaced %d occurrences", meta.ReplacedCount),
			},
		})
	}

	return content
}

// generateApplyPatchContent generates content for apply_patch results using diff type.
func (g *ToolContentGenerator) generateApplyPatchContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var meta tooltypes.ApplyPatchMetadata
	if !tooltypes.ExtractMetadata(structured.Metadata, &meta) {
		return g.generateTextContent(structured.Error)
	}

	if structured.Error != "" {
		return g.generateTextContent(structured.Error)
	}

	content := make([]map[string]any, 0, len(meta.Changes)+1)
	for _, change := range meta.Changes {
		switch change.Operation {
		case tooltypes.ApplyPatchOperationAdd:
			content = append(content, map[string]any{
				"type":    ToolCallContentTypeDiff,
				"path":    change.Path,
				"oldText": nil,
				"newText": change.NewContent,
			})
		case tooltypes.ApplyPatchOperationDelete:
			content = append(content, map[string]any{
				"type":    ToolCallContentTypeDiff,
				"path":    change.Path,
				"oldText": change.OldContent,
				"newText": nil,
			})
		case tooltypes.ApplyPatchOperationUpdate:
			path := change.Path
			if change.MovePath != "" {
				path = change.MovePath
			}
			content = append(content, map[string]any{
				"type":    ToolCallContentTypeDiff,
				"path":    path,
				"oldText": change.OldContent,
				"newText": change.NewContent,
			})
		}
	}

	if len(content) == 0 {
		return g.generateTextContent("No files were modified.")
	}

	return content
}

// generateSubAgentContent generates content for subagent results
func (g *ToolContentGenerator) generateSubAgentContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var meta tooltypes.SubAgentMetadata
	if !tooltypes.ExtractMetadata(structured.Metadata, &meta) {
		return g.generateTextContent(structured.Error)
	}

	if structured.Error != "" {
		return g.generateTextContent(fmt.Sprintf("Subagent error: %s", structured.Error))
	}

	content := []map[string]any{}

	if meta.Workflow != "" {
		content = append(content, map[string]any{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": fmt.Sprintf("Workflow: %s", meta.Workflow),
			},
		})
	}

	if meta.Cwd != "" {
		content = append(content, map[string]any{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": fmt.Sprintf("Directory: %s", meta.Cwd),
			},
		})
	}

	if meta.Question != "" {
		content = append(content, map[string]any{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": fmt.Sprintf("Question: %s", meta.Question),
			},
		})
	}

	if meta.Response != "" {
		content = append(content, map[string]any{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": meta.Response,
			},
		})
	}

	return content
}

// generateCodeBlockContent generates content wrapped in code blocks
// Used for tools like grep and glob where output should preserve formatting
func (g *ToolContentGenerator) generateCodeBlockContent(structured tooltypes.StructuredToolResult, result tooltypes.ToolResult) []map[string]any {
	if structured.Error != "" {
		return []map[string]any{
			{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": markdownEscape(structured.Error),
				},
			},
		}
	}

	output := result.GetResult()
	if output != "" {
		return []map[string]any{
			{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": markdownEscape(output),
				},
			},
		}
	}

	return []map[string]any{}
}

// generateDefaultContent generates default text content for unhandled tools
func (g *ToolContentGenerator) generateDefaultContent(result tooltypes.ToolResult) []map[string]any {
	if result.IsError() {
		return g.generateTextContent(fmt.Sprintf("Error: %s", result.GetError()))
	}
	return g.generateTextContent(result.GetResult())
}

// generateTextContent creates a simple text content array
func (g *ToolContentGenerator) generateTextContent(text string) []map[string]any {
	return []map[string]any{
		{
			"type": ToolCallContentTypeContent,
			"content": map[string]any{
				"type": acptypes.ContentTypeText,
				"text": text,
			},
		},
	}
}

// languageToMimeType converts a programming language identifier to a MIME type
func languageToMimeType(lang string) string {
	mimeTypes := map[string]string{
		"go":         "text/x-go",
		"python":     "text/x-python",
		"javascript": "text/javascript",
		"typescript": "text/typescript",
		"java":       "text/x-java",
		"c":          "text/x-c",
		"cpp":        "text/x-c++",
		"rust":       "text/x-rust",
		"ruby":       "text/x-ruby",
		"php":        "text/x-php",
		"swift":      "text/x-swift",
		"kotlin":     "text/x-kotlin",
		"scala":      "text/x-scala",
		"shell":      "text/x-shellscript",
		"bash":       "text/x-shellscript",
		"yaml":       "text/yaml",
		"json":       "application/json",
		"xml":        "application/xml",
		"html":       "text/html",
		"css":        "text/css",
		"sql":        "text/x-sql",
		"markdown":   "text/markdown",
	}

	if mime, ok := mimeTypes[strings.ToLower(lang)]; ok {
		return mime
	}
	return "text/plain"
}
