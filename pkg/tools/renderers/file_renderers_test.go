package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestFileReadRenderer(t *testing.T) {
	renderer := &FileReadRenderer{}

	t.Run("Successful file read", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath:  "/test/file.go",
				Lines:     []string{"package main", "func main() {", "}"},
				Offset:    1,
				Truncated: false,
				Language:  "go",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "File Read: /test/file.go", "Expected file path in output")
		assert.Contains(t, output, "Offset: 1", "Expected offset in output")
		assert.Contains(t, output, "package main", "Expected file content in output")
	})

	t.Run("Truncated file read", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileReadMetadata{
				FilePath:  "/test/large.txt",
				Lines:     []string{"line1", "line2"},
				Offset:    0,
				Truncated: true,
				Language:  "text",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "[truncated]", "Expected truncation indicator in output")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   false,
			Error:     "File not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: File not found", "Expected error message in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_read",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileWriteMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Invalid metadata type", "Expected invalid metadata error")
	})
}

func TestFileReadRendererMarkdownVariants(t *testing.T) {
	renderer := &FileReadRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "file_read",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.FileReadMetadata{
			FilePath:       "/tmp/test.go",
			Offset:         3,
			LineLimit:      2,
			Lines:          []string{"func main() {", "}"},
			Language:       "go",
			Truncated:      true,
			RemainingLines: 9,
		},
	}

	rendered := renderer.RenderMarkdown(result)
	assert.Contains(t, rendered, "- **Path:** `/tmp/test.go`")
	assert.Contains(t, rendered, "- **Offset:** 3")
	assert.Contains(t, rendered, "- **Lines:** 2")
	assert.Contains(t, rendered, "- **Language:** `go`")
	assert.Contains(t, rendered, "- **Truncated:** yes (9 lines remaining)")
	assert.Contains(t, rendered, "3: func main() {")

	merged := renderer.RenderMergedMarkdown(result)
	assert.Contains(t, merged, "- **Lines:** 2")
	assert.NotContains(t, merged, "- **Path:**")
	assert.NotContains(t, merged, "- **Offset:**")

	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "file_read", Success: false, Error: "missing"}), "Error: missing")
	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "file_read", Success: true, Metadata: &tools.FileWriteMetadata{}}), "Invalid metadata")
}

func TestFileEditRenderer(t *testing.T) {
	renderer := &FileEditRenderer{}

	t.Run("Successful file edit", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath: "/test/file.go",
				Language: "go",
				Edits: []tools.Edit{
					{
						StartLine:  5,
						EndLine:    7,
						OldContent: "old code here",
						NewContent: "new code here",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		// Should contain unified diff output
		assert.NotEmpty(t, output, "Expected diff output")
		// Basic check that it looks like a diff (udiff will handle actual formatting)
		assert.Contains(t, output, "/test/file.go", "Expected file path in diff output")
	})

	t.Run("No edits", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath: "/test/file.go",
				Language: "go",
				Edits:    []tools.Edit{},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "(no changes)", "Expected no changes message")
	})

	t.Run("Multiple replacements with ReplaceAll", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath:      "/test/file.go",
				Language:      "go",
				ReplaceAll:    true,
				ReplacedCount: 3,
				Edits: []tools.Edit{
					{
						StartLine:  5,
						EndLine:    5,
						OldContent: "old",
						NewContent: "new",
					},
					{
						StartLine:  10,
						EndLine:    10,
						OldContent: "old",
						NewContent: "new",
					},
					{
						StartLine:  15,
						EndLine:    15,
						OldContent: "old",
						NewContent: "new",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "(3 replacements)", "Expected replacement count in output")
		assert.Contains(t, output, "Edit 1 (lines 5-5)", "Expected first edit info")
		assert.Contains(t, output, "Edit 2 (lines 10-10)", "Expected second edit info")
		assert.Contains(t, output, "Edit 3 (lines 15-15)", "Expected third edit info")
	})

	t.Run("Many replacements with ReplaceAll - shows all edits", func(t *testing.T) {
		edits := make([]tools.Edit, 5)
		for i := 0; i < 5; i++ {
			edits[i] = tools.Edit{
				StartLine:  (i + 1) * 5,
				EndLine:    (i + 1) * 5,
				OldContent: "old",
				NewContent: "new",
			}
		}

		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath:      "/test/file.go",
				Language:      "go",
				ReplaceAll:    true,
				ReplacedCount: 5,
				Edits:         edits,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "(5 replacements)", "Expected replacement count in output")
		assert.Contains(t, output, "Edit 1 (lines 5-5)", "Expected first edit info")
		assert.Contains(t, output, "Edit 2 (lines 10-10)", "Expected second edit info")
		assert.Contains(t, output, "Edit 3 (lines 15-15)", "Expected third edit info")
		assert.Contains(t, output, "Edit 4 (lines 20-20)", "Expected fourth edit info")
		assert.Contains(t, output, "Edit 5 (lines 25-25)", "Expected fifth edit info")
		assert.NotContains(t, output, "... and", "Should not contain summary message")
	})

	t.Run("Single replacement with ReplaceAll", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "file_edit",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.FileEditMetadata{
				FilePath:      "/test/file.go",
				Language:      "go",
				ReplaceAll:    true,
				ReplacedCount: 1,
				Edits: []tools.Edit{
					{
						StartLine:  5,
						EndLine:    7,
						OldContent: "old code here",
						NewContent: "new code here",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "(1 replacement)", "Expected single replacement count in output")
		assert.Contains(t, output, "/test/file.go", "Expected file path in output")
	})
}

func TestApplyPatchRenderer(t *testing.T) {
	renderer := &ApplyPatchRenderer{}

	t.Run("successful apply patch", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "apply_patch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ApplyPatchMetadata{
				Changes: []tools.ApplyPatchChange{
					{
						Path:       "/tmp/new.txt",
						Operation:  tools.ApplyPatchOperationAdd,
						NewContent: "hello\n",
						UnifiedDiff: "--- /dev/null\n" +
							"+++ /tmp/new.txt\n" +
							"@@ -0,0 +1,1 @@\n" +
							"+hello\n",
					},
					{
						Path:        "/tmp/edit.go",
						Operation:   tools.ApplyPatchOperationUpdate,
						OldContent:  "old\n",
						NewContent:  "new\n",
						UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
					},
					{
						Path:       "/tmp/old.txt",
						Operation:  tools.ApplyPatchOperationDelete,
						OldContent: "bye\n",
						UnifiedDiff: "--- /tmp/old.txt\n" +
							"+++ /dev/null\n" +
							"@@ -1,1 +0,0 @@\n" +
							"-bye\n",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Success. Updated files (+2 -2):")
		assert.Contains(t, output, "Write /tmp/new.txt (+1 -0)")
		assert.Contains(t, output, "Edit /tmp/edit.go (+1 -1)")
		assert.Contains(t, output, "Delete /tmp/old.txt (+0 -1)")
		assert.Contains(t, output, "  1 │ +hello")
		assert.Contains(t, output, "1   │ -old")
		assert.Contains(t, output, "  1 │ +new")
		assert.Contains(t, output, "@@ -1 +1 @@")
	})

	t.Run("error with partial metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "apply_patch",
			Success:   false,
			Error:     "patch failed after first file",
			Timestamp: time.Now(),
			Metadata: &tools.ApplyPatchMetadata{Changes: []tools.ApplyPatchChange{{
				Path:        "/tmp/partial.go",
				Operation:   tools.ApplyPatchOperationUpdate,
				UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
			}}},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Patch failed (+1 -1):")
		assert.Contains(t, output, "Edit /tmp/partial.go (+1 -1)")
		assert.Contains(t, output, "Error: patch failed after first file")
	})

	t.Run("error", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "apply_patch",
			Success:   false,
			Error:     "parse error",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Error: parse error")
	})
}

func TestFileEditRendererRenderMarkdown(t *testing.T) {
	renderer := &FileEditRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "file_edit",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.FileEditMetadata{
			FilePath:      "/test/file.go",
			ReplaceAll:    true,
			ReplacedCount: 2,
			Edits: []tools.Edit{
				{
					StartLine:  5,
					EndLine:    5,
					OldContent: "old",
					NewContent: "new",
				},
				{
					StartLine:  10,
					EndLine:    10,
					OldContent: "before",
					NewContent: "after",
				},
			},
		},
	}

	output := renderer.RenderMarkdown(result)
	assert.Contains(t, output, "File edited: /test/file.go (2 replacements)")
	assert.Contains(t, output, "Edit 1 (lines 5-5):")
	assert.Contains(t, output, "Edit 2 (lines 10-10):")
	assert.Contains(t, output, "```diff")
	assert.NotContains(t, output, "####")
	assert.NotContains(t, output, "- **Language:**")
}

func TestFileWriteRendererMarkdownVariants(t *testing.T) {
	renderer := &FileWriteRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "file_write",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.FileWriteMetadata{
			FilePath: "/tmp/test.go",
			Content:  "package main\n",
			Size:     13,
			Language: "go",
		},
	}

	rendered := renderer.RenderMarkdown(result)
	assert.Contains(t, rendered, "- **Path:** `/tmp/test.go`")
	assert.Contains(t, rendered, "- **Size:** 13 bytes")
	assert.Contains(t, rendered, "- **Language:** `go`")
	assert.Contains(t, rendered, "Written content")
	assert.Contains(t, rendered, "```go\npackage main\n```")

	merged := renderer.RenderMergedMarkdown(result)
	assert.Contains(t, merged, "- **Size:** 13 bytes")
	assert.NotContains(t, merged, "- **Path:**")
	assert.NotContains(t, merged, "Written content")

	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "file_write", Success: false, Error: "denied"}), "Error: denied")
	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "file_write", Success: true, Metadata: &tools.FileReadMetadata{}}), "Invalid metadata")
}

func TestFileEditRendererMergedAndFallbackMarkdown(t *testing.T) {
	renderer := &FileEditRenderer{}

	noChanges := tools.StructuredToolResult{
		ToolName: "file_edit",
		Success:  true,
		Metadata: &tools.FileEditMetadata{FilePath: "/tmp/test.go"},
	}
	assert.Contains(t, renderer.RenderMarkdown(noChanges), "- **Path:** `/tmp/test.go`")
	assert.Equal(t, "- **Changes:** none", renderer.RenderMergedMarkdown(noChanges))

	single := tools.StructuredToolResult{
		ToolName: "file_edit",
		Success:  true,
		Metadata: &tools.FileEditMetadata{
			FilePath: "/tmp/test.go",
			Edits: []tools.Edit{{
				StartLine:  1,
				EndLine:    1,
				OldContent: "old",
				NewContent: "new",
			}},
		},
	}
	merged := renderer.RenderMergedMarkdown(single)
	assert.Contains(t, merged, "Lines 1-1:")
	assert.Contains(t, merged, "```diff")
	assert.NotContains(t, merged, "File edited:")

	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "file_edit", Success: false, Error: "missing old text"}), "Error: missing old text")
	assert.Contains(t, renderer.RenderMarkdown(tools.StructuredToolResult{ToolName: "file_edit", Success: true, Metadata: &tools.FileReadMetadata{}}), "Invalid metadata")
}

func TestApplyPatchRendererRenderMarkdown(t *testing.T) {
	renderer := &ApplyPatchRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "apply_patch",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.ApplyPatchMetadata{
			Changes: []tools.ApplyPatchChange{
				{
					Path:       "/tmp/new.txt",
					Operation:  tools.ApplyPatchOperationAdd,
					NewContent: "hello\n",
					UnifiedDiff: "--- /dev/null\n" +
						"+++ /tmp/new.txt\n" +
						"@@ -0,0 +1,1 @@\n" +
						"+hello\n",
				},
				{
					Path:        "/tmp/edit.go",
					Operation:   tools.ApplyPatchOperationUpdate,
					UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
				},
			},
		},
	}

	output := renderer.RenderMarkdown(result)
	assert.Contains(t, output, "Success. Updated files (+2 -1).")
	assert.Contains(t, output, "- **Write /tmp/new.txt (+1 -0)**")
	assert.Contains(t, output, "- **Edit /tmp/edit.go (+1 -1)**")
	assert.Contains(t, output, "```diff")
	assert.Contains(t, output, "@@ -1 +1 @@")
	assert.Contains(t, output, "1   │ -old")
	assert.Contains(t, output, "  1 │ +new")
	assert.NotContains(t, output, "####")
	assert.NotContains(t, output, "- **Added:**")
}
