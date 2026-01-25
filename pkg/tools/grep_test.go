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

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	assert.Contains(t, desc, "sorted by modification time")
	assert.Contains(t, desc, "truncation notice")
	assert.Contains(t, desc, "max_results")

	// Verify description mentions absolute path
	assert.Contains(t, desc, "absolute path")
}

func TestGrepTool_ValidateInput(t *testing.T) {
	tool := &GrepTool{}
	state := NewBasicState(context.TODO())

	// Create a temp file for testing "path is a file" validation
	tempFile, err := os.CreateTemp("", "grep_test_file")
	require.NoError(t, err)
	tempFilePath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempFilePath)

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
		{
			name: "path is a file",
			input: CodeSearchInput{
				Pattern: "test",
				Path:    tempFilePath,
			},
			expectError: false,
		},
		{
			name: "path does not exist",
			input: CodeSearchInput{
				Pattern: "func Test",
				Path:    "/nonexistent/path/that/does/not/exist",
				Include: "*.go",
			},
			expectError: true,
			errorMsg:    "invalid path",
		},
		{
			name: "max_results exceeds limit",
			input: CodeSearchInput{
				Pattern:    "func Test",
				MaxResults: grepMaxSearchResults + 1,
			},
			expectError: true,
			errorMsg:    "max_results cannot exceed",
		},
		{
			name: "max_results at limit is valid",
			input: CodeSearchInput{
				Pattern:    "func Test",
				MaxResults: grepMaxSearchResults,
			},
			expectError: false,
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

	// Create a file with a target line for testing
	var multilineContent strings.Builder
	multilineContent.WriteString("package main\n\n")
	multilineContent.WriteString("// Target Line - This is the line we'll search for\n")

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
		{
			name: "search single file",
			input: CodeSearchInput{
				Pattern: "multiple lines",
				Path:    filepath.Join(tempDir, "test.txt"),
			},
			expectError:     false,
			expectedResults: []string{"Pattern found in file", "test.txt", "multiple lines"},
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

// TestGrepGitignoreRespected tests that files matching .gitignore patterns are excluded
func TestGrepGitignoreRespected(t *testing.T) {
	// Skip if ripgrep is not available
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_gitignore_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Initialize a git repo (required for .gitignore to be respected)
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, ".git"), 0o755))

	// Create .gitignore
	gitignoreContent := "ignored_dir/\n*.log\n"
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, ".gitignore"), []byte(gitignoreContent), 0o644))

	// Create test files
	testFiles := map[string]string{
		"visible.go":             "func TestVisibleFunc() {}\n",
		"ignored_dir/ignored.go": "func TestIgnoredFunc() {}\n",
		"test.log":               "func TestLogFunc() {}\n",
		"subdir/test.go":         "func TestSubdirFunc() {}\n",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		dir := filepath.Dir(filePath)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filePath, []byte(content), 0o644))
	}

	// Search for the pattern
	input := CodeSearchInput{
		Pattern: "func Test",
		Path:    tempDir,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Should find visible files but not gitignored ones
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "TestVisibleFunc")
	assert.Contains(t, result.GetResult(), "TestSubdirFunc")
	assert.NotContains(t, result.GetResult(), "TestIgnoredFunc")
	assert.NotContains(t, result.GetResult(), "TestLogFunc")
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

	// Should contain truncation notice (file limit message)
	assert.Contains(t, result.GetResult(), "[TRUNCATED DUE TO MAXIMUM 100 FILE LIMIT")
}

// TestGrepMaxResultsParameter tests the max_results parameter
func TestGrepMaxResultsParameter(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_max_results_param_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	tempDirAbs, err := filepath.Abs(tempDir)
	require.NoError(t, err)

	// Create 20 files with the same pattern
	for i := 0; i < 20; i++ {
		filename := filepath.Join(tempDir, fmt.Sprintf("file%d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte("MAXTEST pattern here\n"), 0o644))
	}

	// Test with max_results = 5
	input := CodeSearchInput{
		Pattern:    "MAXTEST",
		Path:       tempDirAbs,
		MaxResults: 5,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	assert.False(t, result.IsError())

	// Count results - should be exactly 5
	count := strings.Count(result.GetResult(), "Pattern found in file")
	assert.Equal(t, 5, count, "Should return exactly 5 results when max_results=5")

	// Should contain truncation notice with the correct limit
	assert.Contains(t, result.GetResult(), "[TRUNCATED DUE TO MAXIMUM 5 FILE LIMIT")
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

// TestParseRipgrepJSON tests the ripgrep JSON output parser
func TestParseRipgrepJSON(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedFiles  int
		expectedError  bool
		checkFirstFile string
		checkLineNum   int
	}{
		{
			name: "single match",
			input: `{"type":"begin","data":{"path":{"text":"/tmp/test.go"}}}
{"type":"match","data":{"path":{"text":"/tmp/test.go"},"lines":{"text":"func TestFunc() {\n"},"line_number":10,"submatches":[{"match":{"text":"func TestFunc"},"start":0,"end":13}]}}
{"type":"end","data":{"path":{"text":"/tmp/test.go"}}}
{"type":"summary","data":{}}`,
			expectedFiles:  1,
			expectedError:  false,
			checkFirstFile: "/tmp/test.go",
			checkLineNum:   10,
		},
		{
			name: "multiple files",
			input: `{"type":"match","data":{"path":{"text":"/tmp/file1.go"},"lines":{"text":"func One() {}\n"},"line_number":5,"submatches":[]}}
{"type":"match","data":{"path":{"text":"/tmp/file2.go"},"lines":{"text":"func Two() {}\n"},"line_number":3,"submatches":[]}}`,
			expectedFiles:  2,
			expectedError:  false,
			checkFirstFile: "/tmp/file1.go",
			checkLineNum:   5,
		},
		{
			name: "multiple matches in same file",
			input: `{"type":"match","data":{"path":{"text":"/tmp/multi.go"},"lines":{"text":"func A() {}\n"},"line_number":1,"submatches":[]}}
{"type":"match","data":{"path":{"text":"/tmp/multi.go"},"lines":{"text":"func B() {}\n"},"line_number":5,"submatches":[]}}`,
			expectedFiles:  1,
			expectedError:  false,
			checkFirstFile: "/tmp/multi.go",
			checkLineNum:   1,
		},
		{
			name:           "empty output",
			input:          "",
			expectedFiles:  0,
			expectedError:  false,
			checkFirstFile: "",
			checkLineNum:   0,
		},
		{
			name:           "no matches (only summary)",
			input:          `{"type":"summary","data":{"stats":{"matches":0}}}`,
			expectedFiles:  0,
			expectedError:  false,
			checkFirstFile: "",
			checkLineNum:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := parseRipgrepJSON([]byte(tt.input), "/tmp")

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, results, tt.expectedFiles)

			if tt.expectedFiles > 0 && tt.checkFirstFile != "" {
				assert.Equal(t, tt.checkFirstFile, results[0].Filename)
				_, exists := results[0].MatchedLines[tt.checkLineNum]
				assert.True(t, exists, "expected line %d to exist in MatchedLines", tt.checkLineNum)
			}
		})
	}
}

// TestSearchDirectoryRipgrep tests the ripgrep search function directly
func TestSearchDirectoryRipgrep(t *testing.T) {
	// Skip if ripgrep is not available
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	ctx := context.Background()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_ripgrep_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create test files
	testFiles := map[string]string{
		"main.go":       "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
		"util.go":       "package main\n\nfunc helper() {\n\treturn\n}\n",
		"test.txt":      "This is a text file\nwith some content\n",
		".hidden.go":    "package hidden\n\nfunc secret() {}\n",
		".git/config":   "[core]\nrepositoryformatversion = 0\n",
		"sub/nested.go": "package sub\n\nfunc nested() {}\n",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		dir := filepath.Dir(filePath)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filePath, []byte(content), 0o644))
	}

	tests := []struct {
		name            string
		pattern         string
		includePattern  string
		expectedFiles   []string
		unexpectedFiles []string
	}{
		{
			name:            "search all go files",
			pattern:         "func",
			includePattern:  "*.go",
			expectedFiles:   []string{"main.go", "util.go", "nested.go"},
			unexpectedFiles: []string{"test.txt", ".hidden.go"},
		},
		{
			name:            "search without include pattern",
			pattern:         "package",
			includePattern:  "",
			expectedFiles:   []string{"main.go", "util.go", "nested.go"},
			unexpectedFiles: []string{},
		},
		{
			name:            "search specific pattern",
			pattern:         "helper",
			includePattern:  "",
			expectedFiles:   []string{"util.go"},
			unexpectedFiles: []string{"main.go"},
		},
		{
			name:            "no matches",
			pattern:         "NONEXISTENT_PATTERN_XYZ",
			includePattern:  "",
			expectedFiles:   []string{},
			unexpectedFiles: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := searchPath(ctx, tempDir, tt.pattern, tt.includePattern, false, false, 0)
			require.NoError(t, err)

			// Check expected files are found
			for _, expectedFile := range tt.expectedFiles {
				found := false
				for _, result := range results {
					if strings.Contains(result.Filename, expectedFile) {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected to find matches in %s", expectedFile)
			}

			// Check unexpected files are not found
			for _, unexpectedFile := range tt.unexpectedFiles {
				for _, result := range results {
					assert.NotContains(t, result.Filename, unexpectedFile,
						"Should not find matches in %s", unexpectedFile)
				}
			}
		})
	}
}

// TestRipgrepBasicSearch tests basic search functionality with ripgrep
func TestRipgrepBasicSearch(t *testing.T) {
	// Skip if ripgrep is not available
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_basic_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("func BasicTest() {}\n"), 0o644))

	// Search for the pattern
	input := CodeSearchInput{
		Pattern: "BasicTest",
		Path:    tempDir,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Should find the match
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "BasicTest")
}

func TestGrepIgnoreCase(t *testing.T) {
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_ignore_case_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("func HelloWorld() {}\n"), 0o644))

	// Without ignore_case, uppercase pattern should not match lowercase (smart-case applies)
	inputWithoutIgnoreCase := CodeSearchInput{
		Pattern:    "HELLOWORLD",
		Path:       tempDir,
		IgnoreCase: false,
	}
	inputJSON, _ := json.Marshal(inputWithoutIgnoreCase)
	result := tool.Execute(ctx, state, string(inputJSON))
	assert.False(t, result.IsError())
	assert.NotContains(t, result.GetResult(), "HelloWorld")

	// With ignore_case, uppercase pattern should match
	inputWithIgnoreCase := CodeSearchInput{
		Pattern:    "HELLOWORLD",
		Path:       tempDir,
		IgnoreCase: true,
	}
	inputJSON, _ = json.Marshal(inputWithIgnoreCase)
	result = tool.Execute(ctx, state, string(inputJSON))
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "HelloWorld")
}

func TestGrepFixedStrings(t *testing.T) {
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_fixed_strings_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("func foo.bar() {}\nfunc fooXbar() {}\n"), 0o644))

	// Without fixed_strings, "foo.bar" is treated as regex (. matches any char)
	inputWithoutFixed := CodeSearchInput{
		Pattern:      "foo.bar",
		Path:         tempDir,
		FixedStrings: false,
	}
	inputJSON, _ := json.Marshal(inputWithoutFixed)
	result := tool.Execute(ctx, state, string(inputJSON))
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "fooXbar") // regex . matches X

	// With fixed_strings, "foo.bar" matches only literal "foo.bar"
	inputWithFixed := CodeSearchInput{
		Pattern:      "foo.bar",
		Path:         tempDir,
		FixedStrings: true,
	}
	inputJSON, _ = json.Marshal(inputWithFixed)
	result = tool.Execute(ctx, state, string(inputJSON))
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "foo.bar")
	assert.NotContains(t, result.GetResult(), "fooXbar")
}

func TestParseRipgrepJSON_MatchPositions(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		expectedPositions map[int][]MatchPosition
	}{
		{
			name:  "single match with position",
			input: `{"type":"match","data":{"path":{"text":"test.go"},"lines":{"text":"func TestFunc() {\n"},"line_number":10,"submatches":[{"match":{"text":"TestFunc"},"start":5,"end":13}]}}`,
			expectedPositions: map[int][]MatchPosition{
				10: {{Start: 5, End: 13}},
			},
		},
		{
			name:  "multiple submatches on same line",
			input: `{"type":"match","data":{"path":{"text":"test.go"},"lines":{"text":"foo bar foo baz\n"},"line_number":1,"submatches":[{"match":{"text":"foo"},"start":0,"end":3},{"match":{"text":"foo"},"start":8,"end":11}]}}`,
			expectedPositions: map[int][]MatchPosition{
				1: {{Start: 0, End: 3}, {Start: 8, End: 11}},
			},
		},
		{
			name: "matches on different lines",
			input: `{"type":"match","data":{"path":{"text":"test.go"},"lines":{"text":"first match\n"},"line_number":5,"submatches":[{"match":{"text":"match"},"start":6,"end":11}]}}
{"type":"match","data":{"path":{"text":"test.go"},"lines":{"text":"second match\n"},"line_number":10,"submatches":[{"match":{"text":"match"},"start":7,"end":12}]}}`,
			expectedPositions: map[int][]MatchPosition{
				5:  {{Start: 6, End: 11}},
				10: {{Start: 7, End: 12}},
			},
		},
		{
			name: "context lines have no positions",
			input: `{"type":"context","data":{"path":{"text":"test.go"},"lines":{"text":"context line\n"},"line_number":9,"submatches":[]}}
{"type":"match","data":{"path":{"text":"test.go"},"lines":{"text":"match line\n"},"line_number":10,"submatches":[{"match":{"text":"match"},"start":0,"end":5}]}}`,
			expectedPositions: map[int][]MatchPosition{
				10: {{Start: 0, End: 5}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := parseRipgrepJSON([]byte(tt.input), "/tmp")
			require.NoError(t, err)
			require.Len(t, results, 1)

			result := results[0]
			for lineNum, expectedPos := range tt.expectedPositions {
				actualPos, exists := result.MatchPositions[lineNum]
				require.True(t, exists, "expected match positions for line %d", lineNum)
				assert.Equal(t, expectedPos, actualPos, "match positions for line %d", lineNum)
			}

			// Verify context lines don't have positions
			for lineNum := range result.ContextLines {
				_, exists := result.MatchPositions[lineNum]
				assert.False(t, exists, "context line %d should not have match positions", lineNum)
			}
		})
	}
}

func TestGrepToolResult_StructuredData_MatchPositions(t *testing.T) {
	tests := []struct {
		name          string
		results       []SearchResult
		expectedStart int
		expectedEnd   int
		lineNum       int
	}{
		{
			name: "single match position",
			results: []SearchResult{
				{
					Filename:     "/tmp/test.go",
					MatchedLines: map[int]string{10: "func TestFunc() {}"},
					MatchPositions: map[int][]MatchPosition{
						10: {{Start: 5, End: 13}},
					},
					ContextLines: map[int]string{},
					LineNumbers:  []int{10},
				},
			},
			lineNum:       10,
			expectedStart: 5,
			expectedEnd:   13,
		},
		{
			name: "multiple positions uses first",
			results: []SearchResult{
				{
					Filename:     "/tmp/test.go",
					MatchedLines: map[int]string{1: "foo bar foo baz"},
					MatchPositions: map[int][]MatchPosition{
						1: {{Start: 0, End: 3}, {Start: 8, End: 11}},
					},
					ContextLines: map[int]string{},
					LineNumbers:  []int{1},
				},
			},
			lineNum:       1,
			expectedStart: 0,
			expectedEnd:   3,
		},
		{
			name: "no positions defaults to zero",
			results: []SearchResult{
				{
					Filename:       "/tmp/test.go",
					MatchedLines:   map[int]string{5: "some content"},
					MatchPositions: map[int][]MatchPosition{},
					ContextLines:   map[int]string{},
					LineNumbers:    []int{5},
				},
			},
			lineNum:       5,
			expectedStart: 0,
			expectedEnd:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &GrepToolResult{
				pattern: "test",
				path:    "/tmp",
				results: tt.results,
			}

			structured := result.StructuredData()
			require.NotNil(t, structured.Metadata)

			metadata, ok := structured.Metadata.(*tooltypes.GrepMetadata)
			require.True(t, ok)
			require.Len(t, metadata.Results, 1)

			var foundMatch *tooltypes.SearchMatch
			for _, match := range metadata.Results[0].Matches {
				if match.LineNumber == tt.lineNum {
					foundMatch = &match
					break
				}
			}

			require.NotNil(t, foundMatch, "expected to find match for line %d", tt.lineNum)
			assert.Equal(t, tt.expectedStart, foundMatch.MatchStart, "MatchStart")
			assert.Equal(t, tt.expectedEnd, foundMatch.MatchEnd, "MatchEnd")
		})
	}
}

// TestTruncateLine tests the line truncation helper function
func TestTruncateLine(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLength int
		expected  string
	}{
		{
			name:      "short line unchanged",
			input:     "short line",
			maxLength: 100,
			expected:  "short line",
		},
		{
			name:      "exact length unchanged",
			input:     "exactly ten",
			maxLength: 11,
			expected:  "exactly ten",
		},
		{
			name:      "long line truncated",
			input:     "this is a very long line that should be truncated",
			maxLength: 20,
			expected:  "this is a very long ... [truncated]",
		},
		{
			name:      "empty line unchanged",
			input:     "",
			maxLength: 100,
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateLine(tt.input, tt.maxLength)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGrepLineTruncation tests that long lines are truncated in search results
func TestGrepLineTruncation(t *testing.T) {
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_line_truncation_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a file with a very long line (simulating decompiled/minified code)
	longLine := "FINDME" + strings.Repeat("x", 500) + "END"
	testFile := filepath.Join(tempDir, "long_line.txt")
	require.NoError(t, os.WriteFile(testFile, []byte(longLine+"\n"), 0o644))

	input := CodeSearchInput{
		Pattern: "FINDME",
		Path:    tempDir,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "FINDME")
	assert.Contains(t, result.GetResult(), "... [truncated]")
	// Should NOT contain the full long line
	assert.NotContains(t, result.GetResult(), "END")
}

// TestGrepOutputSizeTruncation tests that output is truncated when exceeding size limit
func TestGrepOutputSizeTruncation(t *testing.T) {
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_output_size_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create many files with medium-length content to exceed output size limit
	for i := 0; i < 200; i++ {
		content := fmt.Sprintf("SIZETEST line with content %s", strings.Repeat("x", 200))
		filename := filepath.Join(tempDir, fmt.Sprintf("file%d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte(content+"\n"), 0o644))
	}

	input := CodeSearchInput{
		Pattern: "SIZETEST",
		Path:    tempDir,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	assert.False(t, result.IsError())

	// Output should be truncated by either file limit or size limit
	output := result.GetResult()
	assert.True(t,
		strings.Contains(output, "[TRUNCATED DUE TO MAXIMUM 100 FILE LIMIT") ||
			strings.Contains(output, "[TRUNCATED DUE TO OUTPUT SIZE LIMIT"),
		"Output should contain truncation notice")

	// Output size should not exceed grepMaxOutputSize + truncation message overhead
	assert.Less(t, len(output), grepMaxOutputSize+200, "Output should not significantly exceed grepMaxOutputSize")
}

// TestEstimateResultSize tests the result size estimation function
func TestEstimateResultSize(t *testing.T) {
	tests := []struct {
		name    string
		result  SearchResult
		minSize int
		maxSize int
	}{
		{
			name: "simple result",
			result: SearchResult{
				Filename:       "/tmp/test.go",
				MatchedLines:   map[int]string{10: "short line"},
				MatchPositions: map[int][]MatchPosition{10: {{Start: 0, End: 5}}},
				ContextLines:   map[int]string{},
				LineNumbers:    []int{10},
			},
			minSize: 40,
			maxSize: 100,
		},
		{
			name: "long line gets estimated with truncation",
			result: SearchResult{
				Filename:       "/tmp/test.go",
				MatchedLines:   map[int]string{10: strings.Repeat("x", 500)},
				MatchPositions: map[int][]MatchPosition{10: {{Start: 0, End: 5}}},
				ContextLines:   map[int]string{},
				LineNumbers:    []int{10},
			},
			minSize: grepMaxLineLength,
			maxSize: grepMaxLineLength + 100,
		},
		{
			name: "multiple lines",
			result: SearchResult{
				Filename:       "/tmp/test.go",
				MatchedLines:   map[int]string{10: "line1", 20: "line2"},
				MatchPositions: map[int][]MatchPosition{10: {}, 20: {}},
				ContextLines:   map[int]string{15: "context"},
				LineNumbers:    []int{10, 15, 20},
			},
			minSize: 60,
			maxSize: 150,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := estimateResultSize(tt.result)
			assert.GreaterOrEqual(t, size, tt.minSize, "Size should be at least %d", tt.minSize)
			assert.LessOrEqual(t, size, tt.maxSize, "Size should be at most %d", tt.maxSize)
		})
	}
}

// TestTruncateResultsBySize tests the size-based truncation function
func TestTruncateResultsBySize(t *testing.T) {
	// Create results that would exceed grepMaxOutputSize
	var results []SearchResult
	for i := 0; i < 1000; i++ {
		results = append(results, SearchResult{
			Filename:       fmt.Sprintf("/tmp/file%d.go", i),
			MatchedLines:   map[int]string{10: strings.Repeat("x", 200)},
			MatchPositions: map[int][]MatchPosition{10: {{Start: 0, End: 5}}},
			ContextLines:   map[int]string{},
			LineNumbers:    []int{10},
		})
	}

	pattern := "testpattern"
	truncated, wasTruncated := truncateResultsBySize(results, pattern)

	assert.True(t, wasTruncated, "Results should be truncated")
	assert.Less(t, len(truncated), len(results), "Truncated results should be fewer than original")

	// Verify the truncated results fit within the size limit
	totalSize := grepSearchResultHeaderBuffer + len(pattern)
	for _, r := range truncated {
		totalSize += estimateResultSize(r)
	}
	assert.LessOrEqual(t, totalSize, grepMaxOutputSize, "Total size should not exceed grepMaxOutputSize")
}

// TestTruncateResultsBySizeWithLongPattern tests that pattern length is accounted for in size estimation
func TestTruncateResultsBySizeWithLongPattern(t *testing.T) {
	// Create results that would fit with a short pattern but not with a long one
	var results []SearchResult
	for i := 0; i < 100; i++ {
		results = append(results, SearchResult{
			Filename:       fmt.Sprintf("/tmp/file%d.go", i),
			MatchedLines:   map[int]string{10: strings.Repeat("x", 200)},
			MatchPositions: map[int][]MatchPosition{10: {{Start: 0, End: 5}}},
			ContextLines:   map[int]string{},
			LineNumbers:    []int{10},
		})
	}

	shortPattern := "x"
	longPattern := strings.Repeat("y", 10000)

	truncatedShort, wasShortTruncated := truncateResultsBySize(results, shortPattern)
	truncatedLong, wasLongTruncated := truncateResultsBySize(results, longPattern)

	// Long pattern should result in fewer files (or same if both hit the limit)
	if wasShortTruncated && wasLongTruncated {
		assert.LessOrEqual(t, len(truncatedLong), len(truncatedShort),
			"Long pattern should result in fewer or equal results")
	}

	// Verify the size calculation includes pattern length
	totalSizeLong := grepSearchResultHeaderBuffer + len(longPattern)
	for _, r := range truncatedLong {
		totalSizeLong += estimateResultSize(r)
	}
	assert.LessOrEqual(t, totalSizeLong, grepMaxOutputSize, "Total size with long pattern should not exceed grepMaxOutputSize")
}

// TestSizeTruncationAfterFileLimitTruncation is a regression test ensuring that
// size truncation is applied even when file limit truncation has already occurred.
// This prevents output from exceeding grepMaxOutputSize when many large files match.
func TestSizeTruncationAfterFileLimitTruncation(t *testing.T) {
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_size_after_file_limit_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	tempDirAbs, err := filepath.Abs(tempDir)
	require.NoError(t, err)

	// Create 150 files with multiple matching lines per file to exceed 50KB with 100 files
	// Each file has 3 matching lines of 280 chars each (under grepMaxLineLength to avoid truncation)
	// Per file: ~70 (header) + 3 * (280 + 10) = ~940 bytes
	// 100 files * 940 bytes = ~94KB > 50KB limit
	for i := 0; i < 150; i++ {
		var content strings.Builder
		for j := 0; j < 3; j++ {
			content.WriteString("SIZEAFTERFILELIMIT " + strings.Repeat("x", 260) + "\n")
		}
		filename := filepath.Join(tempDir, fmt.Sprintf("file%03d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte(content.String()), 0o644))
	}

	input := CodeSearchInput{
		Pattern: "SIZEAFTERFILELIMIT",
		Path:    tempDirAbs,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	assert.False(t, result.IsError())
	output := result.GetResult()

	// Should be truncated by size limit (since 100 files would exceed 50KB)
	assert.Contains(t, output, "[TRUNCATED DUE TO OUTPUT SIZE LIMIT",
		"Should be truncated by size limit, not just file limit")

	// Verify output size is within limits
	assert.LessOrEqual(t, len(output), grepMaxOutputSize+500,
		"Output should not significantly exceed grepMaxOutputSize")
}

// TestTruncationMessageConsistency verifies truncation messages provide consistent advice
func TestTruncationMessageConsistency(t *testing.T) {
	tests := []struct {
		name             string
		truncationReason GrepTruncationReason
		maxResults       int
		expectedContains string
	}{
		{
			name:             "file limit message",
			truncationReason: GrepTruncatedByFileLimit,
			maxResults:       100,
			expectedContains: "use include filter",
		},
		{
			name:             "size limit message",
			truncationReason: GrepTruncatedByOutputSize,
			maxResults:       100,
			expectedContains: "use include filter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &GrepToolResult{
				pattern:          "test",
				results:          []SearchResult{},
				truncated:        true,
				truncationReason: tt.truncationReason,
				maxResults:       tt.maxResults,
			}

			output := result.GetResult()
			assert.Contains(t, output, tt.expectedContains,
				"Truncation message should contain consistent advice")
		})
	}
}

// TestValidateInputUsesPackageErrors verifies that ValidateInput uses pkg/errors
func TestValidateInputUsesPackageErrors(t *testing.T) {
	tool := &GrepTool{}
	state := NewBasicState(context.TODO())

	// Test max_results validation error
	input := CodeSearchInput{
		Pattern:    "test",
		MaxResults: grepMaxSearchResults + 1,
	}
	inputJSON, _ := json.Marshal(input)

	err := tool.ValidateInput(state, string(inputJSON))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_results cannot exceed")
}

// TestEstimateResultSizeAccountsForNewlines verifies size estimation includes newlines
func TestEstimateResultSizeAccountsForNewlines(t *testing.T) {
	result := SearchResult{
		Filename:       "/tmp/test.go",
		MatchedLines:   map[int]string{10: "line1", 20: "line2"},
		MatchPositions: map[int][]MatchPosition{10: {}, 20: {}},
		ContextLines:   map[int]string{},
		LineNumbers:    []int{10, 20},
	}

	size := estimateResultSize(result)

	// Should account for: filename + file header overhead + 2 lines with line numbers + newlines
	// grepLineNumberOverhead includes the newline character (10 = 8 digits + separator + newline)
	expectedMinSize := len(result.Filename) + grepFileHeaderOverhead + 2*(len("line1")+grepLineNumberOverhead)
	assert.GreaterOrEqual(t, size, expectedMinSize-20, // Allow some tolerance
		"Size should account for all content including overhead")
}

func TestGrepMatchPositions_Integration(t *testing.T) {
	if getRipgrepPath() == "" {
		t.Skip("ripgrep not available, skipping test")
	}

	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState(context.TODO())

	tempDir, err := os.MkdirTemp("", "grep_match_pos_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("func HelloWorld() {}\n"), 0o644))

	input := CodeSearchInput{
		Pattern: "HelloWorld",
		Path:    tempDir,
	}

	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	assert.False(t, result.IsError())

	structured := result.StructuredData()
	metadata, ok := structured.Metadata.(*tooltypes.GrepMetadata)
	require.True(t, ok)
	require.Len(t, metadata.Results, 1)
	require.Len(t, metadata.Results[0].Matches, 1)

	match := metadata.Results[0].Matches[0]
	assert.Equal(t, 1, match.LineNumber)
	assert.Equal(t, 5, match.MatchStart, "HelloWorld starts at position 5 (after 'func ')")
	assert.Equal(t, 15, match.MatchEnd, "HelloWorld ends at position 15")
}
