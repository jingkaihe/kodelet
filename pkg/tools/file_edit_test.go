package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	assert.Contains(t, desc, "Edit a file by replacing the UNIQUE old text with the new text")
	assert.Contains(t, desc, "file_path")
	assert.Contains(t, desc, "old_text")
	assert.Contains(t, desc, "new_text")
}

func TestFileEditTool_ValidateInput(t *testing.T) {
	// Create a temporary test file
	content := []byte("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 2\n")
	tmpfile, err := os.CreateTemp("", "FileEditTest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

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
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpfile.Name())

		if _, err := tmpfile.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := tmpfile.Close(); err != nil {
			t.Fatal(err)
		}

		mockState := NewBasicState(context.TODO())

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

		assert.Contains(t, result.GetError(), "failed to read the file")
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
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())

	// First edit
	firstInput := FileEditInput{
		FilePath: tmpfile.Name(),
		OldText:  "Second line X",
		NewText:  "MODIFIED Second line",
	}
	firstParams, _ := json.Marshal(firstInput)
	firstResult := tool.Execute(context.Background(), mockState, string(firstParams))
	assert.False(t, firstResult.IsError())

	// Second edit
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
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())

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
	assert.Contains(t, result.GetResult(), "3: Modified Line 3")
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
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tool := &FileEditTool{}
	mockState := NewBasicState(context.TODO())

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

	// Check for formatted lines with correct line numbers
	assert.Contains(t, result.GetResult(), "9: 	// Process data with sum")
	assert.Contains(t, result.GetResult(), "10: 	data := []int{1, 2, 3, 4, 5}")
	assert.Contains(t, result.GetResult(), "11: 	sum := 0")
	assert.Contains(t, result.GetResult(), "15: 	fmt.Println(\"Sum:\", sum)")

	// Verify the file was actually edited
	updatedContent, err := os.ReadFile(tmpfile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(updatedContent), "// Process data with sum")
	assert.Contains(t, string(updatedContent), "sum := 0")
	assert.NotContains(t, string(updatedContent), "fmt.Println(d)")
}
