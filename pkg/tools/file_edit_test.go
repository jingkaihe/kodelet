package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileEditTool_GenerateSchema(t *testing.T) {
	tool := &FileEditTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/file-edit-input", string(schema.ID))
}

func TestFileEditTool_Name(t *testing.T) {
	tool := &FileEditTool{}
	assert.Equal(t, "file_edit", tool.Name())
}

func TestFileEditTool_Description(t *testing.T) {
	tool := &FileEditTool{}
	desc := tool.Description()
	assert.Contains(t, desc, "Edit a file by replacing old text with new text")
	assert.Contains(t, desc, "file_path")
	assert.Contains(t, desc, "old_text")
	assert.Contains(t, desc, "new_text")
}

func TestFileEditTool_ValidateInput(t *testing.T) {
	// Create a temporary test file
	content := []byte("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 2\n")
	tmpfile, err := os.CreateTemp("", "FileEditTest")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(content)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	mockState := NewBasicState(context.TODO())
	mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

	tool := &FileEditTool{}
	tests := []struct {
		name        string
		input       FileEditInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid input - text exists and is unique",
			input: FileEditInput{
				FilePath: tmpfile.Name(),
				OldText:  "Line 3",
				NewText:  "New Line 3",
			},
			expectError: false,
		},
		{
			name: "file does not exist",
			input: FileEditInput{
				FilePath: "/non/existent/file.txt",
				OldText:  "Line 3",
				NewText:  "New Line 3",
			},
			expectError: true,
			errorMsg:    "does not exist",
		},
		{
			name: "text not found in file",
			input: FileEditInput{
				FilePath: tmpfile.Name(),
				OldText:  "This text doesn't exist",
				NewText:  "New text",
			},
			expectError: true,
			errorMsg:    "old text not found in the file",
		},
		{
			name: "text appears multiple times",
			input: FileEditInput{
				FilePath: tmpfile.Name(),
				OldText:  "Line 2",
				NewText:  "New Line 2",
			},
			expectError: true,
			errorMsg:    "old text appears 2 times",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			err := tool.ValidateInput(mockState, string(input))
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

func TestFileEditTool_Execute(t *testing.T) {
	tool := &FileEditTool{}

	// Test successful edit
	t.Run("successful edit", func(t *testing.T) {
		// Create a temporary test file
		content := []byte("Line 1\nLine 2\nLine 3x\nLine 4\nLine 5\n")
		tmpfile, err := os.CreateTemp("", "FileEditTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

		input := FileEditInput{
			FilePath: tmpfile.Name(),
			OldText:  "Line 3x",
			NewText:  "New Line 3",
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "has been edited successfully")

		// Verify the file was actually edited
		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		assert.Contains(t, string(updatedContent), "New Line 3")
		assert.NotContains(t, string(updatedContent), "Line 3x")
	})

	// Test file open error
	t.Run("file open error", func(t *testing.T) {
		input := FileEditInput{
			FilePath: "/non/existent/file.txt",
			OldText:  "old text",
			NewText:  "new text",
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))

		assert.Contains(t, result.GetError(), "failed to stat the file")
		assert.Empty(t, result.GetResult())
	})

	// Test invalid JSON
	t.Run("invalid JSON", func(t *testing.T) {
		result := tool.Execute(context.Background(), NewBasicState(context.TODO()), "invalid json")
		assert.True(t, result.IsError())
		assert.Empty(t, result.GetResult())
	})
}

func TestFileEditTool_MultipleEdits(t *testing.T) {
	// Create a temporary test file with content that will be edited multiple times
	content := []byte("First line\nSecond line X\nThird line\nFourth line X\nFifth line\n")
	tmpfile, err := os.CreateTemp("", "FileEditMultipleTest")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(content)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())
	mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

	// First edit
	firstInput := FileEditInput{
		FilePath: tmpfile.Name(),
		OldText:  "Second line X",
		NewText:  "MODIFIED Second line",
	}
	firstParams, _ := json.Marshal(firstInput)
	firstResult := tool.Execute(context.Background(), mockState, string(firstParams))
	assert.False(t, firstResult.IsError())

	// Second edit (lastAccessed was updated by first edit)
	secondInput := FileEditInput{
		FilePath: tmpfile.Name(),
		OldText:  "Fourth line X",
		NewText:  "MODIFIED Fourth line",
	}
	secondParams, _ := json.Marshal(secondInput)
	secondResult := tool.Execute(context.Background(), mockState, string(secondParams))
	assert.False(t, secondResult.IsError())

	// Verify both edits were applied
	updatedContent, err := os.ReadFile(tmpfile.Name())
	assert.NoError(t, err)
	updatedContentStr := string(updatedContent)

	assert.True(t, strings.Contains(updatedContentStr, "MODIFIED Second line"))
	assert.True(t, strings.Contains(updatedContentStr, "MODIFIED Fourth line"))
	assert.False(t, strings.Contains(updatedContentStr, "Second line X"))
	assert.False(t, strings.Contains(updatedContentStr, "Fourth line X"))
	assert.True(t, strings.Contains(updatedContentStr, "First line"))
	assert.True(t, strings.Contains(updatedContentStr, "Third line"))
	assert.True(t, strings.Contains(updatedContentStr, "Fifth line"))
}

func TestFormatEditedBlock(t *testing.T) {
	tests := []struct {
		name            string
		originalContent string
		oldText         string
		newText         string
		expected        string
	}{
		{
			name:            "single line edit",
			originalContent: "Line 1\nLine 2\nLine 3\nLine 4\nLine 5",
			oldText:         "Line 3",
			newText:         "Modified Line 3",
			expected:        "3: Modified Line 3\n",
		},
		{
			name:            "multi-line edit",
			originalContent: "Line 1\nLine 2\nLine 3\nLine 4\nLine 5",
			oldText:         "Line 3\nLine 4",
			newText:         "Modified Line 3\nModified Line 4\nAdded Line",
			expected:        "3: Modified Line 3\n4: Modified Line 4\n5: Added Line\n",
		},
		{
			name:            "edit at beginning",
			originalContent: "Line 1\nLine 2\nLine 3\nLine 4\nLine 5",
			oldText:         "Line 1",
			newText:         "Modified Line 1",
			expected:        "1: Modified Line 1\n",
		},
		{
			name:            "edit at end",
			originalContent: "Line 1\nLine 2\nLine 3\nLine 4\nLine 5",
			oldText:         "Line 5",
			newText:         "Modified Line 5",
			expected:        "5: Modified Line 5\n",
		},
		{
			name:            "edit with multiple occurrences of text pattern",
			originalContent: "console.log('test');\nconsole.log(123);\nconsole.log('test again');\nother code;",
			oldText:         "console.log(123);",
			newText:         "console.log('modified');",
			expected:        "2: console.log('modified');\n",
		},
		{
			name:            "text with partial line matches",
			originalContent: "func Test() {\n\treturn nil\n}\n\nfunc Test2() {\n\treturn nil\n}",
			oldText:         "func Test2() {\n\treturn nil\n}",
			newText:         "func Test2() {\n\treturn 123\n}",
			expected:        "5: func Test2() {\n6: \treturn 123\n7: }\n",
		},
		{
			name:            "empty newText",
			originalContent: "Line 1\nLine 2\nLine 3\nLine 4\nLine 5",
			oldText:         "Line 3\n",
			newText:         "",
			expected:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatEditedBlock(tt.originalContent, tt.oldText, tt.newText)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Update existing test to check formatted output
func TestFileEditTool_ExecuteOutputsFormattedEdit(t *testing.T) {
	// Create a temporary test file
	content := []byte("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\n")
	tmpfile, err := os.CreateTemp("", "FileEditFormatTest")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(content)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())
	mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

	// Edit the file
	input := FileEditInput{
		FilePath: tmpfile.Name(),
		OldText:  "Line 3",
		NewText:  "Modified Line 3",
	}
	params, _ := json.Marshal(input)
	result := tool.Execute(context.Background(), mockState, string(params))

	// Check that the result contains formatted output
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "has been edited successfully")
	assert.Contains(t, result.AssistantFacing(), "3: Modified Line 3")
}

func TestFileEditTool_ExecuteWithMultilineEdits(t *testing.T) {
	// Create a temporary test file with a small code snippet
	content := []byte(`package main

import "fmt"

func main() {
	// Print hello world
	fmt.Println("Hello, world!")

	// Process data
	data := []int{1, 2, 3}
	for _, d := range data {
		fmt.Println(d)
	}
}
`)
	tmpfile, err := os.CreateTemp("", "FileEditCodeTest")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(content)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())
	mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

	// Edit the file - replace the data processing loop
	oldText := `	// Process data
	data := []int{1, 2, 3}
	for _, d := range data {
		fmt.Println(d)
	}`

	newText := `	// Process data with sum
	data := []int{1, 2, 3, 4, 5}
	sum := 0
	for _, d := range data {
		sum += d
	}
	fmt.Println("Sum:", sum)`

	input := FileEditInput{
		FilePath: tmpfile.Name(),
		OldText:  oldText,
		NewText:  newText,
	}
	params, _ := json.Marshal(input)
	result := tool.Execute(context.Background(), mockState, string(params))

	// Check that the result contains formatted output with correct line numbers
	assert.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "has been edited successfully")

	// Check for formatted lines with correct line numbers in AssistantFacing
	assistantResult := result.AssistantFacing()
	assert.Contains(t, assistantResult, "9: 	// Process data with sum")
	assert.Contains(t, assistantResult, "10: 	data := []int{1, 2, 3, 4, 5}")
	assert.Contains(t, assistantResult, "11: 	sum := 0")
	assert.Contains(t, assistantResult, "15: 	fmt.Println(\"Sum:\", sum)")

	// Verify the file was actually edited
	updatedContent, err := os.ReadFile(tmpfile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(updatedContent), "// Process data with sum")
	assert.Contains(t, string(updatedContent), "sum := 0")
	assert.NotContains(t, string(updatedContent), "fmt.Println(d)")
}

func TestFileEditTool_ReplaceAll(t *testing.T) {
	tool := &FileEditTool{}

	t.Run("replace all occurrences", func(t *testing.T) {
		// Create a test file with multiple occurrences
		content := []byte("Hello world\nHello everyone\nGoodbye world\nHello again\n")
		tmpfile, err := os.CreateTemp("", "FileEditReplaceAllTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "Hello",
			NewText:    "Hi",
			ReplaceAll: true,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Replaced 3 occurrences")

		// Verify the file was edited correctly
		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		updatedStr := string(updatedContent)

		// All "Hello" should be replaced with "Hi"
		assert.Contains(t, updatedStr, "Hi world")
		assert.Contains(t, updatedStr, "Hi everyone")
		assert.Contains(t, updatedStr, "Hi again")
		assert.NotContains(t, updatedStr, "Hello")
		assert.Contains(t, updatedStr, "Goodbye world") // Should remain unchanged
	})

	t.Run("replace all multiline occurrences", func(t *testing.T) {
		content := []byte(`func test() {
    return "test"
}

func main() {
    test()
}

func test() {
    return "test"
}`)
		tmpfile, err := os.CreateTemp("", "FileEditReplaceAllMultilineTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

		input := FileEditInput{
			FilePath: tmpfile.Name(),
			OldText: `func test() {
    return "test"
}`,
			NewText: `func test() {
    return "modified"
}`,
			ReplaceAll: true,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Replaced 2 occurrences")

		// Verify the file was edited correctly
		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		updatedStr := string(updatedContent)

		// Count occurrences of "modified"
		modifiedCount := strings.Count(updatedStr, `return "modified"`)
		assert.Equal(t, 2, modifiedCount)
		assert.NotContains(t, updatedStr, `return "test"`)
		assert.Contains(t, updatedStr, "func main()") // Should remain unchanged
	})

	t.Run("replace all with zero occurrences", func(t *testing.T) {
		content := []byte("No matching text here\n")
		tmpfile, err := os.CreateTemp("", "FileEditReplaceAllZeroTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "nonexistent",
			NewText:    "replacement",
			ReplaceAll: true,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Replaced 0 occurrences")

		// Verify the file was not changed
		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		assert.Equal(t, string(content), string(updatedContent))
	})

	t.Run("single occurrence with replace_all true", func(t *testing.T) {
		content := []byte("Single occurrence of unique text\n")
		tmpfile, err := os.CreateTemp("", "FileEditReplaceAllSingleTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "unique",
			NewText:    "special",
			ReplaceAll: true,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Replaced 1 occurrences")

		// Verify the file was edited correctly
		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		assert.Contains(t, string(updatedContent), "special")
		assert.NotContains(t, string(updatedContent), "unique")
	})
}

func TestFileEditTool_ValidateInputReplaceAll(t *testing.T) {
	// Create a temporary test file with multiple occurrences
	content := []byte("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 2\n")
	tmpfile, err := os.CreateTemp("", "FileEditValidateReplaceAllTest")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(content)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	mockState := NewBasicState(context.TODO())
	mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

	tool := &FileEditTool{}

	t.Run("multiple occurrences with replace_all true - should pass", func(t *testing.T) {
		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "Line 2",
			NewText:    "New Line 2",
			ReplaceAll: true,
		}
		inputJSON, _ := json.Marshal(input)
		err := tool.ValidateInput(mockState, string(inputJSON))
		assert.NoError(t, err)
	})

	t.Run("multiple occurrences with replace_all false - should fail", func(t *testing.T) {
		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "Line 2",
			NewText:    "New Line 2",
			ReplaceAll: false,
		}
		inputJSON, _ := json.Marshal(input)
		err := tool.ValidateInput(mockState, string(inputJSON))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "old text appears 2 times")
		assert.Contains(t, err.Error(), "set replace_all to true")
	})

	t.Run("unique occurrence with replace_all false - should pass", func(t *testing.T) {
		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "Line 3",
			NewText:    "New Line 3",
			ReplaceAll: false,
		}
		inputJSON, _ := json.Marshal(input)
		err := tool.ValidateInput(mockState, string(inputJSON))
		assert.NoError(t, err)
	})

	t.Run("replaceAll=false without prior read - should fail validation", func(t *testing.T) {
		// Create a fresh file that hasn't been read
		freshFile, err := os.CreateTemp("", "FileEditNoPriorReadTest")
		require.NoError(t, err)
		defer os.Remove(freshFile.Name())

		_, err = freshFile.WriteString("some content\n")
		require.NoError(t, err)
		err = freshFile.Close()
		require.NoError(t, err)

		freshState := NewBasicState(context.TODO())
		// Don't set any last accessed time

		input := FileEditInput{
			FilePath:   freshFile.Name(),
			OldText:    "some",
			NewText:    "new",
			ReplaceAll: false,
		}
		inputJSON, _ := json.Marshal(input)
		err = tool.ValidateInput(freshState, string(inputJSON))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "last access time")
	})

	t.Run("replaceAll=true without prior read - should pass validation", func(t *testing.T) {
		// Create a fresh file that hasn't been read
		freshFile, err := os.CreateTemp("", "FileEditNoPriorReadReplaceAllTest")
		require.NoError(t, err)
		defer os.Remove(freshFile.Name())

		_, err = freshFile.WriteString("foo bar foo\n")
		require.NoError(t, err)
		err = freshFile.Close()
		require.NoError(t, err)

		freshState := NewBasicState(context.TODO())
		// Don't set any last accessed time

		input := FileEditInput{
			FilePath:   freshFile.Name(),
			OldText:    "foo",
			NewText:    "qux",
			ReplaceAll: true,
		}
		inputJSON, _ := json.Marshal(input)
		err = tool.ValidateInput(freshState, string(inputJSON))
		assert.NoError(t, err)
	})
}

func TestFileEditTool_StructuredDataReplaceAll(t *testing.T) {
	result := &FileEditToolResult{
		filename:      "/test/file.go",
		oldText:       "old",
		newText:       "new",
		oldContent:    "old content old",
		newContent:    "new content new",
		startLine:     1,
		endLine:       1,
		replaceAll:    true,
		replacedCount: 2,
		edits: []EditInfo{
			{StartLine: 1, EndLine: 1, OldContent: "old", NewContent: "new"},
			{StartLine: 1, EndLine: 1, OldContent: "old", NewContent: "new"},
		},
	}

	structuredData := result.StructuredData()

	assert.Equal(t, "file_edit", structuredData.ToolName)
	assert.True(t, structuredData.Success)

	meta, ok := structuredData.Metadata.(*tooltypes.FileEditMetadata)
	require.True(t, ok)

	assert.Equal(t, "/test/file.go", meta.FilePath)
	assert.True(t, meta.ReplaceAll)
	assert.Equal(t, 2, meta.ReplacedCount)
	assert.Len(t, meta.Edits, 2)
}

func TestFindAllOccurrences(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		oldText     string
		expectedLen int
	}{
		{
			name:        "single occurrence",
			content:     "Hello world\nHow are you\n",
			oldText:     "Hello",
			expectedLen: 1,
		},
		{
			name:        "multiple occurrences same line",
			content:     "Hello Hello world\n",
			oldText:     "Hello",
			expectedLen: 2,
		},
		{
			name:        "multiple occurrences different lines",
			content:     "Hello world\nHello everyone\nGoodbye Hello\n",
			oldText:     "Hello",
			expectedLen: 3,
		},
		{
			name:        "multiline text",
			content:     "func test() {\n    return 1\n}\n\nfunc test() {\n    return 1\n}\n",
			oldText:     "func test() {\n    return 1\n}",
			expectedLen: 2,
		},
		{
			name:        "no occurrences",
			content:     "Hello world\n",
			oldText:     "goodbye",
			expectedLen: 0,
		},
		{
			name:        "overlapping pattern",
			content:     "aaaa\n",
			oldText:     "aa",
			expectedLen: 2, // Non-overlapping: should find 2 occurrences
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edits := findAllOccurrences(tt.content, tt.oldText)
			assert.Len(t, edits, tt.expectedLen)

			// Verify each edit has proper line numbers
			for _, edit := range edits {
				assert.Greater(t, edit.StartLine, 0)
				assert.GreaterOrEqual(t, edit.EndLine, edit.StartLine)
				assert.Equal(t, tt.oldText, edit.OldContent)
			}
		})
	}
}

func TestFileEditTool_ModifiedSinceLastRead(t *testing.T) {
	tool := &FileEditTool{}

	t.Run("replaceAll=false should fail when file modified since last read", func(t *testing.T) {
		content := []byte("Hello world\nHello everyone\n")
		tmpfile, err := os.CreateTemp("", "FileEditModifiedTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		// Set last accessed time to 1 hour ago to simulate stale read
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now().Add(-time.Hour))

		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "Hello world",
			NewText:    "Hi world",
			ReplaceAll: false,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "has been modified since the last read")
	})

	t.Run("replaceAll=true should succeed even when file modified since last read", func(t *testing.T) {
		content := []byte("Hello world\nHello everyone\n")
		tmpfile, err := os.CreateTemp("", "FileEditModifiedReplaceAllTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		// Set last accessed time to 1 hour ago to simulate stale read
		mockState.SetFileLastAccessed(tmpfile.Name(), time.Now().Add(-time.Hour))

		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "Hello",
			NewText:    "Hi",
			ReplaceAll: true,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		// Should succeed despite stale last access time
		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Replaced 2 occurrences")

		// Verify the file was edited correctly
		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		assert.Contains(t, string(updatedContent), "Hi world")
		assert.Contains(t, string(updatedContent), "Hi everyone")
		assert.NotContains(t, string(updatedContent), "Hello")
	})

	t.Run("replaceAll=true should work without any prior file read", func(t *testing.T) {
		content := []byte("foo bar foo baz foo\n")
		tmpfile, err := os.CreateTemp("", "FileEditNoReadTest")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write(content)
		require.NoError(t, err)
		err = tmpfile.Close()
		require.NoError(t, err)

		mockState := NewBasicState(context.TODO())
		// Don't set any last accessed time - simulating no prior read

		input := FileEditInput{
			FilePath:   tmpfile.Name(),
			OldText:    "foo",
			NewText:    "qux",
			ReplaceAll: true,
		}
		params, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), mockState, string(params))

		// Should succeed even without prior read
		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "Replaced 3 occurrences")

		updatedContent, err := os.ReadFile(tmpfile.Name())
		assert.NoError(t, err)
		assert.Equal(t, "qux bar qux baz qux\n", string(updatedContent))
	})
}

func TestFileEditTool_ConcurrentEdits(t *testing.T) {
	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())

	tmpfile, err := os.CreateTemp("", "FileEditConcurrentTest")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	initialContent := "MARKER_A=value1\nMARKER_B=value2\nMARKER_C=value3\nMARKER_D=value4\n"
	_, err = tmpfile.WriteString(initialContent)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	// Simulate reading the file first
	mockState.SetFileLastAccessed(tmpfile.Name(), time.Now())

	done := make(chan bool, 4)
	errors := make(chan error, 4)

	edits := []struct {
		oldText string
		newText string
	}{
		{"MARKER_A=value1", "MARKER_A=new_value_A"},
		{"MARKER_B=value2", "MARKER_B=new_value_B"},
		{"MARKER_C=value3", "MARKER_C=new_value_C"},
		{"MARKER_D=value4", "MARKER_D=new_value_D"},
	}

	for _, edit := range edits {
		go func(oldText, newText string) {
			input := FileEditInput{
				FilePath: tmpfile.Name(),
				OldText:  oldText,
				NewText:  newText,
			}
			params, _ := json.Marshal(input)
			result := tool.Execute(context.Background(), mockState, string(params))
			if result.IsError() {
				errors <- assert.AnError
			}
			done <- true
		}(edit.oldText, edit.newText)
	}

	for i := 0; i < len(edits); i++ {
		<-done
	}
	close(errors)

	for err := range errors {
		t.Errorf("concurrent edit failed: %v", err)
	}

	finalContent, err := os.ReadFile(tmpfile.Name())
	require.NoError(t, err)

	content := string(finalContent)
	assert.Contains(t, content, "MARKER_A=new_value_A")
	assert.Contains(t, content, "MARKER_B=new_value_B")
	assert.Contains(t, content, "MARKER_C=new_value_C")
	assert.Contains(t, content, "MARKER_D=new_value_D")

	assert.NotContains(t, content, "MARKER_A=value1")
	assert.NotContains(t, content, "MARKER_B=value2")
	assert.NotContains(t, content, "MARKER_C=value3")
	assert.NotContains(t, content, "MARKER_D=value4")
}
