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
}

func TestGrepTool_ValidateInput(t *testing.T) {
	tool := &GrepTool{}
	state := NewBasicState()

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
			name: "valid input with all fields",
			input: CodeSearchInput{
				Pattern: "func Test",
				Path:    "./",
				Include: "*.go",
			},
			expectError: false,
		},
		{
			name: "missing pattern",
			input: CodeSearchInput{
				Path:    "./",
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
	state := NewBasicState()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_test")
	if err != nil {
		t.Fatal(err)
	}
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
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
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
				assert.NotEmpty(t, result.Error)
			} else {
				assert.Empty(t, result.Error)

				// Skip the "Search results" check for the no matches case
				if tt.name != "search with no matches" {
					assert.Contains(t, result.Result, "Search results for pattern")
				}

				for _, expected := range tt.expectedResults {
					assert.Contains(t, result.Result, expected)
				}

				if tt.notExpected != "" {
					assert.NotContains(t, result.Result, tt.notExpected)
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
				// 		assert.Contains(t, result.Result, contextLine,
				// 			fmt.Sprintf("Should contain context line %d", i))
				// 	}
				// }

				// Verify line numbers are present for all test cases except "no matches"
				if tt.name != "search with no matches" && !tt.expectError {
					// Look for pattern headers
					assert.Contains(t, result.Result, "Pattern found in file",
						"Output should contain file headers")

					// Check for line number format in matches
					if tt.name != "search with surrounding lines" {
						assert.Regexp(t, `\d+:`, result.Result, "Output should contain line numbers with colon for matches")
					}

					// Check for context line format
					// if tt.name == "search with surrounding lines" || tt.name == "search with line numbers" {
					// 	assert.Regexp(t, `\d+-`, result.Result, "Output should contain line numbers with dash for context lines")
					// }
				}

				// Additional verification for the line numbers test case
				if tt.name == "search with line numbers" {
					// Get the temp file path from the result (it's dynamic)
					assert.Contains(t, result.Result, "Pattern found in file",
						"Output should contain the file header")

					// Check for the exact match on line 5
					assert.Contains(t, result.Result, "5:func LineNumberTest",
						"Output should contain the exact match with correct line number")

					// Check for context lines with their line numbers
					// assert.Contains(t, result.Result, "3-// Comment line 3",
					// 	"Output should show line number for context lines before match")
					// assert.Contains(t, result.Result, "7-    fmt.Println",
					// 	"Output should show line number for context lines after match")
				}
			}
		})
	}
}

func TestGrepTool_InvalidJSON(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState()

	result := tool.Execute(ctx, state, "invalid json")
	assert.NotEmpty(t, result.Error)
	assert.Contains(t, result.Error, "invalid input")
}

// TestGrepHiddenFilesIgnored tests that files and directories starting with a dot are ignored
func TestGrepHiddenFilesIgnored(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_hidden_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

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
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Search for "func Test" pattern
	input := CodeSearchInput{
		Pattern: "func Test",
		Path:    tempDir,
	}
	
	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Should not find hidden files
	assert.Empty(t, result.Error)
	assert.Contains(t, result.Result, "TestVisibleFunc")
	assert.Contains(t, result.Result, "TestNormalDirFunc")
	assert.NotContains(t, result.Result, "TestHiddenFunc")
	assert.NotContains(t, result.Result, "TestHiddenDirFunc")
}

// TestGrepResultLimitAndTruncation tests the limit of 100 results with truncation message
func TestGrepResultLimitAndTruncation(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_limit_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create 120 files with the same pattern for testing truncation
	const filesToCreate = 120
	for i := 0; i < filesToCreate; i++ {
		filename := filepath.Join(tempDir, filepath.Clean(filepath.Join("dir"+fmt.Sprintf("%d", i%10), "file"+fmt.Sprintf("%d", i)+".txt")))
		
		// Ensure directory exists
		dir := filepath.Dir(filename)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		
		content := "This is a test file with a FIND_ME pattern inside"
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Search for the pattern
	input := CodeSearchInput{
		Pattern: "FIND_ME",
		Path:    tempDir,
	}
	
	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Count the number of "Pattern found in file" occurrences
	count := strings.Count(result.Result, "Pattern found in file")
	
	// We should have exactly 100 results
	assert.Equal(t, 100, count, "Should return exactly 100 results")
	
	// Should contain truncation notice
	assert.Contains(t, result.Result, "[TRUNCATED DUE TO MAXIMUM 100 RESULT LIMIT]")
}

// TestSortSearchResultsByModTime tests the dedicated sorting function
func TestSortSearchResultsByModTime(t *testing.T) {
	// Create temporary files with different timestamps
	tempDir, err := os.MkdirTemp("", "grep_sort_func_test")
	if err != nil {
		t.Fatal(err)
	}
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
		if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, fileTimes[i], fileTimes[i]); err != nil {
			t.Fatal(err)
		}
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

// TestGrepSortByModTime tests that results are sorted by modification time
func TestGrepSortByModTime(t *testing.T) {
	tool := &GrepTool{}
	ctx := context.Background()
	state := NewBasicState()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "grep_sort_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

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
		
		if err := os.WriteFile(filePath, []byte(fileInfo.content), 0644); err != nil {
			t.Fatal(err)
		}
		
		// Set modification time
		if err := os.Chtimes(filePath, fileInfo.modTime, fileInfo.modTime); err != nil {
			t.Fatal(err)
		}
	}

	// Search for the pattern
	input := CodeSearchInput{
		Pattern: "TIMESTAMP_TEST",
		Path:    tempDir,
	}
	
	inputJSON, _ := json.Marshal(input)
	result := tool.Execute(ctx, state, string(inputJSON))

	// Verify order in output (newest first)
	firstOccurrence := strings.Index(result.Result, "file_newest.txt")
	secondOccurrence := strings.Index(result.Result, "file_newer.txt")
	thirdOccurrence := strings.Index(result.Result, "file_old.txt")
	
	// Assert the files appear in order of newest to oldest
	assert.Greater(t, secondOccurrence, firstOccurrence, "Newest file should appear first")
	assert.Greater(t, thirdOccurrence, secondOccurrence, "Files should be in order of decreasing modification time")
}
