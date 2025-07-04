package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Pattern: func.*Test") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "Path: /home/user/project") {
			t.Errorf("Expected path in output, got: %s", output)
		}
		if !strings.Contains(output, "Include: *.go") {
			t.Errorf("Expected include pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "Found 2 files with matches") {
			t.Errorf("Expected file count in output, got: %s", output)
		}
		if !strings.Contains(output, "/home/user/project/main.go") {
			t.Errorf("Expected file path in output, got: %s", output)
		}
		if !strings.Contains(output, "10: func TestMain(t *testing.T) {") {
			t.Errorf("Expected line match in output, got: %s", output)
		}
		if !strings.Contains(output, "20: func TestHelper(t *testing.T) {") {
			t.Errorf("Expected line match in output, got: %s", output)
		}
		if !strings.Contains(output, "5: func TestUtils(t *testing.T) {") {
			t.Errorf("Expected line match in output, got: %s", output)
		}
		if strings.Contains(output, "truncated") {
			t.Errorf("Should not show truncated when not truncated, got: %s", output)
		}
	})

	t.Run("Grep with truncated results", func(t *testing.T) {
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

		if !strings.Contains(output, "Pattern: TODO") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "... [results truncated]") {
			t.Errorf("Expected truncation indicator, got: %s", output)
		}
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

		if !strings.Contains(output, "Pattern: nonexistent") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "Found 0 files with matches") {
			t.Errorf("Expected zero files message, got: %s", output)
		}
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

		if !strings.Contains(output, "Pattern: error") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if strings.Contains(output, "Path:") {
			t.Errorf("Should not show path when empty, got: %s", output)
		}
		if strings.Contains(output, "Include:") {
			t.Errorf("Should not show include when empty, got: %s", output)
		}
	})

	t.Run("Grep error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   false,
			Error:     "Invalid pattern",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "Error: Invalid pattern" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "grep",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.GlobMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for grep_tool") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
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

		if !strings.Contains(output, "Pattern: *.go") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "Path: /home/user/project") {
			t.Errorf("Expected path in output, got: %s", output)
		}
		if !strings.Contains(output, "Found 3 files") {
			t.Errorf("Expected file count in output, got: %s", output)
		}
		if !strings.Contains(output, "/home/user/project/main.go (1024 bytes)") {
			t.Errorf("Expected file with size in output, got: %s", output)
		}
		if !strings.Contains(output, "/home/user/project/utils.go (512 bytes)") {
			t.Errorf("Expected file with size in output, got: %s", output)
		}
		if !strings.Contains(output, "/home/user/project/tests/ (0 bytes)") {
			t.Errorf("Expected directory with / indicator in output, got: %s", output)
		}
		if strings.Contains(output, "truncated") {
			t.Errorf("Should not show truncated when not truncated, got: %s", output)
		}
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

		if !strings.Contains(output, "Pattern: *") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "... [results truncated]") {
			t.Errorf("Expected truncation indicator, got: %s", output)
		}
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

		if !strings.Contains(output, "Pattern: *.nonexistent") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if !strings.Contains(output, "Found 0 files") {
			t.Errorf("Expected zero files message, got: %s", output)
		}
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

		if !strings.Contains(output, "Pattern: *.txt") {
			t.Errorf("Expected pattern in output, got: %s", output)
		}
		if strings.Contains(output, "Path:") {
			t.Errorf("Should not show path when empty, got: %s", output)
		}
		if !strings.Contains(output, "file.txt (50 bytes)") {
			t.Errorf("Expected file in output, got: %s", output)
		}
	})

	t.Run("Glob error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   false,
			Error:     "Invalid pattern",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if output != "Error: Invalid pattern" {
			t.Errorf("Expected error message, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "glob",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.GrepMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for glob_tool") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})
}