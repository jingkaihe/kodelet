package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestGrepRenderer(t *testing.T) {
	renderer := &GrepRenderer{}

	t.Run("Successful grep with results", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "func.*Test",
				Path:    "/home/user/project",
				Include: "*.go",
				Results: []tools.SearchResult{
					{
						FilePath: "/home/user/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 10, Content: "func TestMain(t *testing.T) {"},
							{LineNumber: 20, Content: "func TestHelper(t *testing.T) {"},
						},
					},
					{
						FilePath: "/home/user/project/utils.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 5, Content: "func TestUtils(t *testing.T) {"},
						},
					},
				},
				Truncated: false,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: func.*Test")
		assert.Contains(t, output, "Path: /home/user/project")
		assert.Contains(t, output, "Include: *.go")
		assert.Contains(t, output, "Found 2 files with matches")
		assert.Contains(t, output, "/home/user/project/main.go")
		assert.Contains(t, output, "10: func TestMain(t *testing.T) {")
		assert.Contains(t, output, "20: func TestHelper(t *testing.T) {")
		assert.Contains(t, output, "5: func TestUtils(t *testing.T) {")
		assert.NotContains(t, output, "truncated")
	})

	t.Run("Grep with truncated results (generic)", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "TODO",
				Path:    "/project",
				Results: []tools.SearchResult{
					{
						FilePath: "/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 1, Content: "// TODO: implement this"},
						},
					},
				},
				Truncated: true,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: TODO")
		assert.Contains(t, output, "... [results truncated]")
	})

	t.Run("Grep truncated by file limit", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "func",
				Path:    "/project",
				Results: []tools.SearchResult{
					{
						FilePath: "/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 1, Content: "func main()"},
						},
					},
				},
				Truncated:        true,
				TruncationReason: tools.GrepTruncatedByFileLimit,
				MaxResults:       100,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "... [truncated: max 100 files]")
	})

	t.Run("Grep truncated by file limit with custom max_results", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "func",
				Path:    "/project",
				Results: []tools.SearchResult{
					{
						FilePath: "/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 1, Content: "func main()"},
						},
					},
				},
				Truncated:        true,
				TruncationReason: tools.GrepTruncatedByFileLimit,
				MaxResults:       25,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "... [truncated: max 25 files]")
	})

	t.Run("Grep truncated by output size", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "func",
				Path:    "/project",
				Results: []tools.SearchResult{
					{
						FilePath: "/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 1, Content: "func main()"},
						},
					},
				},
				Truncated:        true,
				TruncationReason: tools.GrepTruncatedByOutputSize,
				MaxResults:       50,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "... [truncated: output size limit (50KB)]")
	})

	t.Run("Grep truncated by file limit with zero max_results defaults to 100", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "func",
				Path:    "/project",
				Results: []tools.SearchResult{
					{
						FilePath: "/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 1, Content: "func main()"},
						},
					},
				},
				Truncated:        true,
				TruncationReason: tools.GrepTruncatedByFileLimit,
				MaxResults:       0,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "... [truncated: max 100 files]")
	})

	t.Run("Grep with no results", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "nonexistent",
				Path:    "/project",
				Results: []tools.SearchResult{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: nonexistent")
		assert.Contains(t, output, "Found 0 files with matches")
	})

	t.Run("Grep without path and include", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "error",
				Results: []tools.SearchResult{
					{
						FilePath: "main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 15, Content: "return error"},
						},
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: error")
		assert.NotContains(t, output, "Path:")
		assert.NotContains(t, output, "Include:")
	})

	t.Run("Grep error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   false,
			Error:     "Invalid pattern",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Invalid pattern", output)
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.GlobMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for grep_tool")
	})

	t.Run("Grep with context lines", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GrepMetadata{
				Pattern: "targetFunc",
				Path:    "/home/user/project",
				Results: []tools.SearchResult{
					{
						FilePath: "/home/user/project/main.go",
						Matches: []tools.SearchMatch{
							{LineNumber: 8, Content: "// context before", IsContext: true},
							{LineNumber: 9, Content: "// more context", IsContext: true},
							{LineNumber: 10, Content: "func targetFunc() {", IsContext: false},
							{LineNumber: 11, Content: "    return nil", IsContext: true},
							{LineNumber: 12, Content: "}", IsContext: true},
						},
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "8- // context before")
		assert.Contains(t, output, "9- // more context")
		assert.Contains(t, output, "10: func targetFunc() {")
		assert.Contains(t, output, "11- ")
		assert.Contains(t, output, "12- }")
	})
}

func TestGrepRendererMarkdownVariants(t *testing.T) {
	renderer := &GrepRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "grep_tool",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.GrepMetadata{
			Pattern:          "target",
			Path:             "/repo",
			Include:          "*.go",
			Truncated:        true,
			TruncationReason: tools.GrepTruncatedByFileLimit,
			MaxResults:       7,
			Results: []tools.SearchResult{
				{
					FilePath: "/repo/main.go",
					Matches: []tools.SearchMatch{
						{LineNumber: 4, Content: "before", IsContext: true},
						{LineNumber: 5, Content: "target()"},
					},
				},
			},
		},
	}

	rendered := renderer.RenderMarkdown(result)
	assert.Contains(t, rendered, "- **Pattern:** `target`")
	assert.Contains(t, rendered, "- **Path:** `/repo`")
	assert.Contains(t, rendered, "- **Include:** `*.go`")
	assert.Contains(t, rendered, "- **Files with matches:** 1")
	assert.Contains(t, rendered, "- **Truncated:** truncated at 7 files")
	assert.Contains(t, rendered, "#### `/repo/main.go`")
	assert.Contains(t, rendered, "4- before")
	assert.Contains(t, rendered, "5: target()")

	merged := renderer.RenderMergedMarkdown(result)
	assert.Contains(t, merged, "- **Files with matches:** 1")
	assert.NotContains(t, merged, "- **Pattern:**")
	assert.NotContains(t, merged, "- **Path:**")

	toolUse := renderer.RenderToolUseMarkdown(`{"pattern":"target","path":"/repo","include":"*.go","surround_lines":2,"max_results":5,"fixed_strings":true,"ignore_case":true}`)
	assert.Contains(t, toolUse, "- **Pattern:** `target`")
	assert.Contains(t, toolUse, "- **Path:** `/repo`")
	assert.Contains(t, toolUse, "- **Include:** `*.go`")
	assert.Contains(t, toolUse, "- **Context lines:** 2")
	assert.Contains(t, toolUse, "- **Max results:** 5")
	assert.Contains(t, toolUse, "- **Fixed strings:** true")
	assert.Contains(t, toolUse, "- **Ignore case:** true")
	assert.Empty(t, renderer.RenderToolUseMarkdown(`{"pattern":`))

	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "grep_tool", Success: false, Error: "bad pattern"}), "Error: bad pattern")
	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "grep_tool", Success: true, Metadata: &tools.GlobMetadata{}}), "Invalid metadata")
}

func TestGlobRenderer(t *testing.T) {
	renderer := &GlobRenderer{}

	t.Run("Successful glob with files", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GlobMetadata{
				Pattern: "*.go",
				Path:    "/home/user/project",
				Files: []tools.FileInfo{
					{
						Path: "/home/user/project/main.go",
						Type: "file",
						Size: 1024,
					},
					{
						Path: "/home/user/project/utils.go",
						Type: "file",
						Size: 512,
					},
					{
						Path: "/home/user/project/tests",
						Type: "directory",
						Size: 0,
					},
				},
				Truncated: false,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: *.go")
		assert.Contains(t, output, "Path: /home/user/project")
		assert.Contains(t, output, "Found 3 files")
		assert.Contains(t, output, "/home/user/project/main.go (1024 bytes)")
		assert.Contains(t, output, "/home/user/project/utils.go (512 bytes)")
		assert.Contains(t, output, "/home/user/project/tests/ (0 bytes)")
		assert.NotContains(t, output, "truncated")
	})

	t.Run("Glob with truncated results", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GlobMetadata{
				Pattern: "*",
				Path:    "/project",
				Files: []tools.FileInfo{
					{
						Path: "/project/file1.txt",
						Type: "file",
						Size: 100,
					},
					{
						Path: "/project/file2.txt",
						Type: "file",
						Size: 200,
					},
				},
				Truncated: true,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: *")
		assert.Contains(t, output, "... [results truncated]")
	})

	t.Run("Glob with no files", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GlobMetadata{
				Pattern: "*.nonexistent",
				Path:    "/project",
				Files:   []tools.FileInfo{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: *.nonexistent")
		assert.Contains(t, output, "Found 0 files")
	})

	t.Run("Glob without path", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.GlobMetadata{
				Pattern: "*.txt",
				Files: []tools.FileInfo{
					{
						Path: "file.txt",
						Type: "file",
						Size: 50,
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Pattern: *.txt")
		assert.NotContains(t, output, "Path:")
		assert.Contains(t, output, "file.txt (50 bytes)")
	})

	t.Run("Glob error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   false,
			Error:     "Invalid pattern",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Equal(t, "Error: Invalid pattern", output)
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.GrepMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for glob_tool")
	})
}

func TestGlobRendererMarkdownVariants(t *testing.T) {
	renderer := &GlobRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "glob_tool",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.GlobMetadata{
			Pattern:   "**/*.go",
			Path:      "/repo",
			Truncated: true,
			Files: []tools.FileInfo{
				{Path: "/repo/main.go", Type: "file", Size: 123},
				{Path: "/repo/pkg", Type: "directory", Size: 0},
				{Path: "/repo/unknown", Size: 1},
			},
		},
	}

	rendered := renderer.RenderMarkdown(result)
	assert.Contains(t, rendered, "- **Pattern:** `**/*.go`")
	assert.Contains(t, rendered, "- **Path:** `/repo`")
	assert.Contains(t, rendered, "- **Matches:** 3")
	assert.Contains(t, rendered, "- **Truncated:** yes")
	assert.Contains(t, rendered, "- `/repo/main.go` (file, 123 bytes)")
	assert.Contains(t, rendered, "- `/repo/pkg/` (directory, 0 bytes)")
	assert.Contains(t, rendered, "- `/repo/unknown` (file, 1 bytes)")

	merged := renderer.RenderMergedMarkdown(result)
	assert.Contains(t, merged, "- **Matches:** 3")
	assert.NotContains(t, merged, "- **Pattern:**")
	assert.NotContains(t, merged, "- **Path:**")

	toolUse := renderer.RenderToolUseMarkdown(`{"pattern":"**/*.go","path":"/repo","ignore_gitignore":true}`)
	assert.Contains(t, toolUse, "- **Pattern:** `**/*.go`")
	assert.Contains(t, toolUse, "- **Path:** `/repo`")
	assert.Contains(t, toolUse, "- **Ignore .gitignore:** true")
	assert.Empty(t, renderer.RenderToolUseMarkdown(`{"pattern":`))

	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "glob_tool", Success: false, Error: "bad glob"}), "Error: bad glob")
	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "glob_tool", Success: true, Metadata: &tools.GrepMetadata{}}), "Invalid metadata")
}
