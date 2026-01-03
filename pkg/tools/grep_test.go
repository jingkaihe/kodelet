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

	// Should contain truncation notice
	assert.Contains(t, result.GetResult(), "[TRUNCATED DUE TO MAXIMUM 100 RESULT LIMIT]")
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
			results, err := searchDirectory(ctx, tempDir, tt.pattern, tt.includePattern, false, false, 0)
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
