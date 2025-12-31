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
	case "file_read":
		return g.generateFileReadContent(structured)
	case "file_write":
		return g.generateFileWriteContent(structured)
	case "file_edit":
		return g.generateFileEditContent(structured)
	case "subagent":
		return g.generateSubAgentContent(structured)
	default:
		return g.generateDefaultContent(result)
	}
}

// generateBashContent generates content for bash command results
func (g *ToolContentGenerator) generateBashContent(structured tooltypes.StructuredToolResult) []map[string]any {
	var bashMeta tooltypes.BashMetadata
	var bgMeta tooltypes.BackgroundBashMetadata

	if tooltypes.ExtractMetadata(structured.Metadata, &bashMeta) {
		content := []map[string]any{}

		if bashMeta.Command != "" {
			content = append(content, map[string]any{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": fmt.Sprintf("$ %s", bashMeta.Command),
				},
			})
		}

		if bashMeta.Output != "" {
			content = append(content, map[string]any{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": bashMeta.Output,
				},
			})
		}

		if structured.Error != "" {
			content = append(content, map[string]any{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": fmt.Sprintf("Error: %s (exit code: %d)", structured.Error, bashMeta.ExitCode),
				},
			})
		} else if bashMeta.ExitCode != 0 {
			content = append(content, map[string]any{
				"type": ToolCallContentTypeContent,
				"content": map[string]any{
					"type": acptypes.ContentTypeText,
					"text": fmt.Sprintf("Exit code: %d", bashMeta.ExitCode),
				},
			})
		}

		return content
	}

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
