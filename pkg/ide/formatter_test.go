package ide

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatContextPrompt(t *testing.T) {
	t.Run("Empty context", func(t *testing.T) {
		result := FormatContextPrompt(nil)
		assert.Empty(t, result)
	})

	t.Run("Only open files", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file1.go", Language: "go"},
				{Path: "/path/to/file2.py", Language: "python"},
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "## Currently Open Files in IDE")
		assert.Contains(t, result, "/path/to/file1.go (go)")
		assert.Contains(t, result, "/path/to/file2.py (python)")
	})

	t.Run("Open files without language", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.txt"},
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "## Currently Open Files in IDE")
		assert.Contains(t, result, "/path/to/file.txt")
		assert.NotContains(t, result, "()")
	})

	t.Run("With selection", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
			Selection: &SelectionInfo{
				FilePath:  "/path/to/file.go",
				StartLine: 10,
				EndLine:   15,
				Content:   "func Example() {\n\treturn nil\n}",
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "## Currently Selected Code in IDE")
		assert.Contains(t, result, "File: /path/to/file.go (lines 10-15)")
		assert.Contains(t, result, "func Example()")
		assert.Contains(t, result, "return nil")
	})

	t.Run("With diagnostics - errors", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
			Diagnostics: []DiagnosticInfo{
				{
					FilePath: "/path/to/file.go",
					Line:     10,
					Column:   5,
					Severity: "error",
					Message:  "undefined variable foo",
					Source:   "gopls",
					Code:     "UndeclaredName",
				},
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "## IDE Diagnostics")
		assert.Contains(t, result, "### Errors")
		assert.Contains(t, result, "file.go:10:5")
		assert.Contains(t, result, "[gopls/UndeclaredName]")
		assert.Contains(t, result, "undefined variable foo")
	})

	t.Run("With diagnostics - warnings", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
			Diagnostics: []DiagnosticInfo{
				{
					FilePath: "/path/to/file.go",
					Line:     20,
					Column:   10,
					Severity: "warning",
					Message:  "unused variable bar",
					Source:   "gopls",
					Code:     "UnusedVar",
				},
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "## IDE Diagnostics")
		assert.Contains(t, result, "### Warnings")
		assert.Contains(t, result, "file.go:20:10")
		assert.Contains(t, result, "[gopls/UnusedVar]")
		assert.Contains(t, result, "unused variable bar")
	})

	t.Run("With multiple diagnostics", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
			Diagnostics: []DiagnosticInfo{
				{
					FilePath: "/path/to/file.go",
					Line:     10,
					Severity: "error",
					Message:  "error 1",
					Source:   "gopls",
				},
				{
					FilePath: "/path/to/file.go",
					Line:     20,
					Severity: "warning",
					Message:  "warning 1",
					Source:   "gopls",
				},
				{
					FilePath: "/path/to/file.go",
					Line:     30,
					Severity: "info",
					Message:  "info 1",
					Source:   "gopls",
				},
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "### Errors")
		assert.Contains(t, result, "### Warnings")
		assert.Contains(t, result, "### Other Diagnostics")
		assert.Contains(t, result, "error 1")
		assert.Contains(t, result, "warning 1")
		assert.Contains(t, result, "info 1")
	})

	t.Run("Diagnostic without source or code", func(t *testing.T) {
		context := &IDEContext{
			Diagnostics: []DiagnosticInfo{
				{
					FilePath: "/path/to/file.go",
					Line:     10,
					Column:   5,
					Severity: "error",
					Message:  "some error",
				},
			},
		}

		result := FormatContextPrompt(context)
		assert.Contains(t, result, "file.go:10:5 - some error")
		// Should not have empty brackets
		assert.NotContains(t, result, "[] some error")
		assert.NotContains(t, result, "[/]")
	})

	t.Run("Complete context", func(t *testing.T) {
		context := &IDEContext{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file1.go", Language: "go"},
				{Path: "/path/to/file2.go", Language: "go"},
			},
			Selection: &SelectionInfo{
				FilePath:  "/path/to/file1.go",
				StartLine: 5,
				EndLine:   10,
				Content:   "selected code",
			},
			Diagnostics: []DiagnosticInfo{
				{
					FilePath: "/path/to/file1.go",
					Line:     15,
					Severity: "error",
					Message:  "syntax error",
					Source:   "gopls",
				},
			},
		}

		result := FormatContextPrompt(context)
		
		// All sections should be present
		assert.Contains(t, result, "## Currently Open Files in IDE")
		assert.Contains(t, result, "## Currently Selected Code in IDE")
		assert.Contains(t, result, "## IDE Diagnostics")
		
		// Content should be present
		assert.Contains(t, result, "file1.go")
		assert.Contains(t, result, "file2.go")
		assert.Contains(t, result, "selected code")
		assert.Contains(t, result, "syntax error")
		
		// Check section ordering (open files, then selection, then diagnostics)
		openFilesIdx := strings.Index(result, "## Currently Open Files")
		selectionIdx := strings.Index(result, "## Currently Selected Code")
		diagnosticsIdx := strings.Index(result, "## IDE Diagnostics")
		
		assert.Less(t, openFilesIdx, selectionIdx)
		assert.Less(t, selectionIdx, diagnosticsIdx)
	})
}
