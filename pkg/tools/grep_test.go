package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure we're using the same constant defined in grep.go
var surroundingLinesCount = CodeSearchSurroundingLines

func TestGrepTool_GenerateSchema(t *testing.T) {
	tool := &GrepTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/code-search-input", string(schema.ID))
}

func TestGrepTool_Name(t *testing.T) {
	tool := &GrepTool{}
	assert.Equal(t, "grep_tool", tool.Name())
}

func TestGrepTool_Description(t *testing.T) {
	tool := &GrepTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Search for a pattern in the codebase using regex")
	assert.Contains(t, desc, "pattern")
	assert.Contains(t, desc, "path")
	assert.Contains(t, desc, "include")

	// New features description tests
	assert.Contains(t, desc, "Binary files and hidden files/directories (starting with .) are skipped by default")
	assert.Contains(t, desc, "maximum 100 files sorted by modification time")
	assert.Contains(t, desc, "truncation notice")

	// Verify description mentions absolute path
	assert.Contains(t, desc, "absolute path")
}

func TestGrepTool_ValidateInput(t *testing.T) {
	tool := &GrepTool{}
	state := NewBasicState(context.TODO())

	tests := []struct {
		name        string
		input       CodeSearchInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid input with pattern only",
			input: CodeSearchInput{
				Pattern: "func Test",
			},
			expectError: false,
		},
		{
			name: "valid input with all fields and absolute path",
			input: CodeSearchInput{
				Pattern: "func Test",
				Path:    "/tmp",
				Include: "*.go",
			},
			expectError: false,
		},
		{
			name: "invalid relative path",
			input: CodeSearchInput{
				Pattern: "func Test",
				Path:    "./",
				Include: "*.go",
			},
			expectError: true,
			errorMsg:    "path must be an absolute path",
		},
		{
			name: "invalid path with dot prefix",
			input: CodeSearchInput{
				Pattern: "func Test",
				Path:    ".hidden/path",
				Include: "*.go",
			},
			expectError: true,
			errorMsg:    "path must be an absolute path",
		},
		{
			name: "missing pattern",
			input: CodeSearchInput{
				Path:    "/tmp",
				Include: "*.go",
			},
			expectError: true,
			errorMsg:    "pattern is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			err := tool.ValidateInput(state, string(input))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGrepTool_Execute(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files with known content
	testFiles := map[string]string{
		"test1.go": "package main\n\nfunc TestFunc1() {\n\treturn\n}\n",
		"test2.go": "package main\n\nfunc TestFunc2() {\n\treturn\n}\n",
		"test.txt": "This is a test file\nwith multiple lines\nand some test patterns\n",
	}

	// Create a file with enough lines to test surrounding lines feature
	var multilineContent strings.Builder
	multilineContent.WriteString("package main\n\n")

	// Add numbered lines (enough before and after the target line)
	for i := 1; i <= surroundingLinesCount*2+1; i++ {
		if i == surroundingLinesCount+1 {
			multilineContent.WriteString("// Target Line - This is the line we'll search for\n")
		} else {
			multilineContent.WriteString(fmt.Sprintf("// Line %d - Context line\n", i))
		}
	}

	testFiles["multiline.go"] = multilineContent.String()

	// Create a file with content at specific line numbers for testing
	lineNumberTestContent := "package main\n\n" + // Line 1-2
		"// Comment line 3\n" + // Line 3
		"// Comment line 4\n" + // Line 4
		"func LineNumberTest() {\n" + // Line 5 - This is the line we'll search for
		"    // Some code\n" + // Line 6
		"    fmt.Println(\"test\")\n" + // Line 7
		"}\n" // Line 8

	testFiles["linenumber_test.go"] = lineNumberTestContent

	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		require.NoError(t, os.WriteFile(filePath, []byte(content), 0o644))
	}

	tests := []struct {
		name            string
		input           CodeSearchInput
		expectError     bool
		expectedResults []string
		notExpected     string
	}{
		{
			name: "search for func pattern",
			input: CodeSearchInput{
				Pattern: "func Test",
				Path:    tempDir,
			},
			expectError:     false,
			expectedResults: []string{"TestFunc1", "TestFunc2"},
		},
		{
			name: "search with file type filter",
			input: CodeSearchInput{
				Pattern: "test",
				Path:    tempDir,
				Include: "*.txt",
			},
			expectError:     false,
			expectedResults: []string{"test file"},
			notExpected:     "TestFunc",
		},
		{
			name: "search with no matches",
			input: CodeSearchInput{
				Pattern: "NonExistentPattern",
				Path:    tempDir,
			},
			expectError:     false,
			expectedResults: []string{"No matches found"},
		},
		{
			name: "search with surrounding lines",
			input: CodeSearchInput{
				Pattern: "Target Line",
				Path:    tempDir,
				Include: "*.go",
			},
			expectError:     false,
			expectedResults: []string{"Target Line - This is the line we'll search for"},
		},
		{
			name: "search with line numbers",
			input: CodeSearchInput{
				Pattern: "func LineNumberTest",
				Path:    tempDir,
				Include: "*.go",
			},
			expectError:     false,
			expectedResults: []string{"Pattern found in file", "linenumber_test.go", "5:func LineNumberTest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			result := tool.Execute(ctx, state, string(input))

			if tt.expectError {
				assert.False(t, result.IsError())
			} else {
				assert.False(t, result.IsError())

				// Skip the "Search results" check for the no matches case
				if tt.name != "search with no matches" {
					assert.Contains(t, result.GetResult(), "Search results for pattern")
				}

				for _, expected := range tt.expectedResults {
					assert.Contains(t, result.GetResult(), expected)
				}

				if tt.notExpected != "" {
					assert.NotContains(t, result.GetResult(), tt.notExpected)
				}

				// Additional verification for surrounding lines test
				// if tt.name == "search with surrounding lines" {
				// 	// Verify we get the exact number of surrounding lines we expect
				// 	for i := 1; i <= surroundingLinesCount*2+1; i++ {
				// 		if i == surroundingLinesCount+1 {
				// 			// This is the target line, already verified
				// 			continue
				// 		}

				// 		contextLine := fmt.Sprintf("Line %d - Context line", i)
				// 		assert.Contains(t, result.GetResult(), contextLine,
				// 			fmt.Sprintf("Should contain context line %d", i))
				// 	}
				// }

				// Verify line numbers are present for all test cases except "no matches"
				if tt.name != "search with no matches" && !tt.expectError {
					// Look for pattern headers
					assert.Contains(t, result.GetResult(), "Pattern found in file",
						"Output should contain file headers")

					// Check for line number format in matches
					if tt.name != "search with surrounding lines" {
						assert.Regexp(t, `\d+:`, result.GetResult(), "Output should contain line numbers with colon for matches")
					}

					// Check for context line format
					// if tt.name == "search with surrounding lines" || tt.name == "search with line numbers" {
					// 	assert.Regexp(t, `\d+-`, result.GetResult(), "Output should contain line numbers with dash for context lines")
					// }
				}

				// Additional verification for the line numbers test case
				if tt.name == "search with line numbers" {
					// Get the temp file path from the result (it's dynamic)
					assert.Contains(t, result.GetResult(), "Pattern found in file",
						"Output should contain the file header")

					// Check for the exact match on line 5
					assert.Contains(t, result.GetResult(), "5:func LineNumberTest",
						"Output should contain the exact match with correct line number")

					// Check for context lines with their line numbers
					// assert.Contains(t, result.GetResult(), "3-// Comment line 3",
					// 	"Output should show line number for context lines before match")
					// assert.Contains(t, result.GetResult(), "7-    fmt.Println",
					// 	"Output should show line number for context lines after match")
				}
			}
		})
	}
}

func TestGrepTool_InvalidJSON(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	result := tool.Execute(ctx, state, "invalid json")
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "invalid input")
}

// TestGrepHiddenFilesIgnored tests that files and directories starting with a dot are ignored
func TestGrepHiddenFilesIgnored(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_hidden_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Get the absolute path
	tempDirAbs, err := filepath.Abs(tempDir)
	require.NoError(t, err)

	// Create visible and hidden files
	testFiles := map[string]string{
		"visible.go":     "func TestVisibleFunc() {}\n",
		".hidden.go":     "func TestHiddenFunc() {}\n",
		"normal/test.go": "func TestNormalDirFunc() {}\n",
		".git/test.go":   "func TestHiddenDirFunc() {}\n",
	}

	// Create the files
	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)

		// Ensure directory exists
		dir := filepath.Dir(filePath)
		require.NoError(t, os.MkdirAll(dir, 0o755))

		require.NoError(t, os.WriteFile(filePath, []byte(content), 0o644))
	}

	// Search for "func Test" pattern
	input := CodeSearchInput{
		Pattern: "func Test",
		Path:    tempDirAbs,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Should not find hidden files
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "TestVisibleFunc")
	assert.Contains(t, result.GetResult(), "TestNormalDirFunc")
	assert.NotContains(t, result.GetResult(), "TestHiddenFunc")
	assert.NotContains(t, result.GetResult(), "TestHiddenDirFunc")
}

// TestGrepResultLimitAndTruncation tests the limit of 100 results with truncation message
func TestGrepResultLimitAndTruncation(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_limit_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Get the absolute path
	tempDirAbs, err := filepath.Abs(tempDir)
	require.NoError(t, err)

	// Create 120 files with the same pattern for testing truncation
	const filesToCreate = 120
	for i := 0; i < filesToCreate; i++ {
		filename := filepath.Join(tempDir, filepath.Clean(filepath.Join("dir"+fmt.Sprintf("%d", i%10), "file"+fmt.Sprintf("%d", i)+".txt")))

		// Ensure directory exists
		dir := filepath.Dir(filename)
		require.NoError(t, os.MkdirAll(dir, 0o755))

		content := "This is a test file with a FIND_ME pattern inside"
		require.NoError(t, os.WriteFile(filename, []byte(content), 0o644))
	}

	// Search for the pattern
	input := CodeSearchInput{
		Pattern: "FIND_ME",
		Path:    tempDirAbs,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Count the number of "Pattern found in file" occurrences
	count := strings.Count(result.GetResult(), "Pattern found in file")

	// We should have exactly 100 results
	assert.Equal(t, 100, count, "Should return exactly 100 results")

	// Should contain truncation notice
	assert.Contains(t, result.GetResult(), "[TRUNCATED DUE TO MAXIMUM 100 RESULT LIMIT]")
}

// TestSortSearchResultsByModTime tests the dedicated sorting function
func TestSortSearchResultsByModTime(t *testing.T) {
	// Create temporary files with different timestamps
	tempDir, err := os.MkdirTemp("", "grep_sort_func_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files with specific content and timestamps
	fileNames := []string{"file1.txt", "file2.txt", "file3.txt"}
	fileTimes := []time.Time{
		time.Now().Add(-2 * time.Hour),
		time.Now().Add(-1 * time.Hour),
		time.Now(),
	}

	// Create files and set mod times
	for i, name := range fileNames {
		path := filepath.Join(tempDir, name)
		require.NoError(t, os.WriteFile(path, []byte("test content"), 0o644))
		require.NoError(t, os.Chtimes(path, fileTimes[i], fileTimes[i]))
	}

	// Create search results in reverse order (oldest first)
	results := []SearchResult{
		{Filename: filepath.Join(tempDir, fileNames[0])},
		{Filename: filepath.Join(tempDir, fileNames[1])},
		{Filename: filepath.Join(tempDir, fileNames[2])},
	}

	// Sort the results
	sortSearchResultsByModTime(results)

	// Check the order is newest first
	assert.Equal(t, filepath.Join(tempDir, fileNames[2]), results[0].Filename, "Newest file should be first")
	assert.Equal(t, filepath.Join(tempDir, fileNames[1]), results[1].Filename, "Second newest file should be second")
	assert.Equal(t, filepath.Join(tempDir, fileNames[0]), results[2].Filename, "Oldest file should be last")
}

// TestFileIncludedWithDoublestar tests the isFileIncluded function with the doublestar library
func TestFileIncludedWithDoublestar(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		includePattern string
		expected       bool
	}{
		{
			name:           "simple pattern match",
			filename:       "test.go",
			includePattern: "*.go",
			expected:       true,
		},
		{
			name:           "simple pattern no match",
			filename:       "test.go",
			includePattern: "*.py",
			expected:       false,
		},
		{
			name:           "extended pattern match with braces",
			filename:       "test.go",
			includePattern: "*.{go,py}",
			expected:       true,
		},
		{
			name:           "extended pattern match with braces another extension",
			filename:       "test.py",
			includePattern: "*.{go,py}",
			expected:       true,
		},
		{
			name:           "extended pattern with braces no match",
			filename:       "test.js",
			includePattern: "*.{go,py}",
			expected:       false,
		},
		{
			name:           "recursive match",
			filename:       "path/to/test.go",
			includePattern: "**/*.go",
			expected:       true,
		},
		{
			name:           "recursive match with brace pattern",
			filename:       "path/to/deep/test.py",
			includePattern: "**/*.{go,py}",
			expected:       true,
		},
		{
			name:           "empty pattern always matches",
			filename:       "anything.txt",
			includePattern: "",
			expected:       true,
		},
		{
			name:           "invalid pattern",
			filename:       "test.go",
			includePattern: "**/*.[", // Invalid pattern
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isFileIncluded(tt.filename, tt.includePattern)
			assert.Equal(t, tt.expected, result, "isFileIncluded() result mismatch")
		})
	}
}

// TestDefaultPathIsAbsolute tests that the default path is an absolute path
func TestDefaultPathIsAbsolute(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Input with no path specified
	input := CodeSearchInput{
		Pattern: "test pattern",
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// The test should not error due to path issues
	assert.NotContains(t, result.GetError(), "path must be an absolute path")
	assert.NotContains(t, result.GetError(), "failed to get current working directory")
}

// TestGrepSortByModTime tests that results are sorted by modification time
func TestGrepSortByModTime(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_sort_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Get the absolute path
	tempDirAbs, err := filepath.Abs(tempDir)
	require.NoError(t, err)

	// Create files with different timestamps
	testFiles := []struct {
		name    string
		content string
		modTime time.Time
	}{
		{
			name:    "file_old.txt",
			content: "This is an old file with TIMESTAMP_TEST pattern",
			modTime: time.Now().Add(-2 * time.Hour),
		},
		{
			name:    "file_newer.txt",
			content: "This is a newer file with TIMESTAMP_TEST pattern",
			modTime: time.Now().Add(-1 * time.Hour),
		},
		{
			name:    "file_newest.txt",
			content: "This is the newest file with TIMESTAMP_TEST pattern",
			modTime: time.Now(),
		},
	}

	// Create the files with specific timestamps
	for _, fileInfo := range testFiles {
		filePath := filepath.Join(tempDir, fileInfo.name)

		require.NoError(t, os.WriteFile(filePath, []byte(fileInfo.content), 0o644))

		// Set modification time
		require.NoError(t, os.Chtimes(filePath, fileInfo.modTime, fileInfo.modTime))
	}

	// Search for the pattern
	input := CodeSearchInput{
		Pattern: "TIMESTAMP_TEST",
		Path:    tempDirAbs,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Verify order in output (newest first)
	firstOccurrence := strings.Index(result.GetResult(), "file_newest.txt")
	secondOccurrence := strings.Index(result.GetResult(), "file_newer.txt")
	thirdOccurrence := strings.Index(result.GetResult(), "file_old.txt")

	// Assert the files appear in order of newest to oldest
	assert.Greater(t, secondOccurrence, firstOccurrence, "Newest file should appear first")
	assert.Greater(t, thirdOccurrence, secondOccurrence, "Files should be in order of decreasing modification time")
}

// TestGrepFileMatchingByRelativePathOrBaseName tests that files are matched by either their relative path or base name
func TestGrepFileMatchingByRelativePathOrBaseName(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_path_match_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Get the absolute path
	tempDirAbs, err := filepath.Abs(tempDir)
	require.NoError(t, err)

	// Create a directory structure with test files
	testFiles := map[string]string{
		"top_level.go":              "package main\n\nfunc TopLevel() {}\n",
		"pkg/nested/nested_file.go": "package nested\n\nfunc NestedFunc() {}\n",
		"another/path/another.go":   "package another\n\nfunc AnotherFunc() {}\n",
		"docs/readme.md":            "# Test Documentation\n",
	}

	// Create the files
	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)

		// Ensure directory exists
		dir := filepath.Dir(filePath)
		require.NoError(t, os.MkdirAll(dir, 0o755))

		require.NoError(t, os.WriteFile(filePath, []byte(content), 0o644))
	}

	// Test cases
	tests := []struct {
		name            string
		includePattern  string
		expectedMatches []string
		unexpectedFiles []string
	}{
		{
			name:           "match by base name only",
			includePattern: "*.go",
			expectedMatches: []string{
				"top_level.go",
				"nested_file.go",
				"another.go",
			},
			unexpectedFiles: []string{
				"readme.md",
			},
		},
		{
			name:           "match by full relative path",
			includePattern: "pkg/**/*.go",
			expectedMatches: []string{
				"nested_file.go",
			},
			unexpectedFiles: []string{
				"top_level.go",
				"another.go",
				"readme.md",
			},
		},
		{
			name:           "match by partial relative path",
			includePattern: "**/nested/*.go",
			expectedMatches: []string{
				"nested_file.go",
			},
			unexpectedFiles: []string{
				"top_level.go",
				"another.go",
				"readme.md",
			},
		},
		{
			name:           "multiple extensions with braces",
			includePattern: "*.{go,md}",
			expectedMatches: []string{
				"top_level.go",
				"nested_file.go",
				"another.go",
				"readme.md",
			},
			unexpectedFiles: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a simple pattern that will match in all files
			input := CodeSearchInput{
				Pattern: "package|func|Test",
				Path:    tempDirAbs,
				Include: tt.includePattern,
			}

			inputJSON, _ := json.Marshal(input)
			result := tool.Execute(ctx, state, string(inputJSON))

			// Check that there's no error
			assert.False(t, result.IsError())

			// Check that expected matches are found
			for _, expectedMatch := range tt.expectedMatches {
				assert.Contains(t, result.GetResult(), expectedMatch,
					fmt.Sprintf("Should find matches in file %s with pattern %s",
						expectedMatch, tt.includePattern))
			}

			// Check that unexpected files are not matched
			for _, unexpectedFile := range tt.unexpectedFiles {
				assert.NotContains(t, result.GetResult(), unexpectedFile,
					fmt.Sprintf("Should NOT find matches in file %s with pattern %s",
						unexpectedFile, tt.includePattern))
			}
		})
	}
}
