package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Ensure we're using the same constant defined in code_search.go
var surroundingLinesCount = CodeSearchSurroundingLines

func TestCodeSearchTool_GenerateSchema(t *testing.T) {
	tool := &CodeSearchTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/code-search-input", string(schema.ID))
}

func TestCodeSearchTool_Name(t *testing.T) {
	tool := &CodeSearchTool{}
	assert.Equal(t, "code_search", tool.Name())
}

func TestCodeSearchTool_Description(t *testing.T) {
	tool := &CodeSearchTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Search for a pattern in the codebase using regex")
	assert.Contains(t, desc, "pattern")
	assert.Contains(t, desc, "path")
	assert.Contains(t, desc, "include")
}

func TestCodeSearchTool_ValidateInput(t *testing.T) {
	tool := &CodeSearchTool{}
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

func TestCodeSearchTool_Execute(t *testing.T) {
	tool := &CodeSearchTool{}
	ctx := context.Background()
	state := NewBasicState()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "code_search_test")
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
				if tt.name == "search with surrounding lines" {
					// Verify we get the exact number of surrounding lines we expect
					for i := 1; i <= surroundingLinesCount*2+1; i++ {
						if i == surroundingLinesCount+1 {
							// This is the target line, already verified
							continue
						}

						contextLine := fmt.Sprintf("Line %d - Context line", i)
						assert.Contains(t, result.Result, contextLine,
							fmt.Sprintf("Should contain context line %d", i))
					}
				}

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
					if tt.name == "search with surrounding lines" || tt.name == "search with line numbers" {
						assert.Regexp(t, `\d+-`, result.Result, "Output should contain line numbers with dash for context lines")
					}
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
					assert.Contains(t, result.Result, "3-// Comment line 3",
						"Output should show line number for context lines before match")
					assert.Contains(t, result.Result, "7-    fmt.Println",
						"Output should show line number for context lines after match")
				}
			}
		})
	}
}

func TestCodeSearchTool_InvalidJSON(t *testing.T) {
	tool := &CodeSearchTool{}
	ctx := context.Background()
	state := NewBasicState()

	result := tool.Execute(ctx, state, "invalid json")
	assert.NotEmpty(t, result.Error)
	assert.Contains(t, result.Error, "invalid input")
}
