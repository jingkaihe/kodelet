package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileMultiEditTool_ValidateInput(t *testing.T) {
	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test.txt")

	// Create test file with repeated text
	err := os.WriteFile(testFilePath, []byte("Hello World\nHello World\nHello World\n"), 0644)
	require.NoError(t, err)

	// Make sure we can get the file stat time
	fileInfo, err := os.Stat(testFilePath)
	require.NoError(t, err)
	modTime := fileInfo.ModTime()

	// Create state with the current file modification time
	state := NewBasicState()
	state.SetFileLastAccessed(testFilePath, modTime)

	tool := &FileMultiEditTool{}

	tests := []struct {
		name    string
		input   FileMultiEditInput
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid input",
			input: FileMultiEditInput{
				FilePath:   testFilePath,
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 2,
			},
			wantErr: false,
		},
		{
			name: "invalid occurrence - zero",
			input: FileMultiEditInput{
				FilePath:   testFilePath,
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 0,
			},
			wantErr: true,
			errMsg:  "occurrence must be greater than 0",
		},
		{
			name: "invalid occurrence - too many",
			input: FileMultiEditInput{
				FilePath:   testFilePath,
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 10,
			},
			wantErr: true,
			errMsg:  "old text appears 3 times in the file, but 10 occurrences were requested",
		},
		{
			name: "text not found",
			input: FileMultiEditInput{
				FilePath:   testFilePath,
				OldText:    "Non-existent Text",
				NewText:    "New Text",
				Occurrence: 1,
			},
			wantErr: true,
			errMsg:  "old text not found in the file",
		},
		{
			name: "file does not exist",
			input: FileMultiEditInput{
				FilePath:   filepath.Join(tempDir, "nonexistent.txt"),
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 1,
			},
			wantErr: true,
			errMsg:  "does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputBytes, err := json.Marshal(tt.input)
			require.NoError(t, err)

			err = tool.ValidateInput(state, string(inputBytes))

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFileMultiEditTool_Execute(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name           string
		fileContent    string
		input          FileMultiEditInput
		expectedResult string
		expectedFile   string
		expectedCount  int
	}{
		{
			name:        "replace single occurrence",
			fileContent: "Hello World\nHello World\nHello World\n",
			input: FileMultiEditInput{
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 1,
			},
			expectedFile:  "New Text\nHello World\nHello World\n",
			expectedCount: 1,
		},
		{
			name:        "replace multiple occurrences",
			fileContent: "Hello World\nHello World\nHello World\n",
			input: FileMultiEditInput{
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 2,
			},
			expectedFile:  "New Text\nNew Text\nHello World\n",
			expectedCount: 2,
		},
		{
			name:        "replace all occurrences",
			fileContent: "Hello World\nHello World\nHello World\n",
			input: FileMultiEditInput{
				OldText:    "Hello World",
				NewText:    "New Text",
				Occurrence: 3,
			},
			expectedFile:  "New Text\nNew Text\nNew Text\n",
			expectedCount: 3,
		},
		{
			name:        "multiline text replacement",
			fileContent: "function test() {\n  console.log('test');\n}\n\nfunction test() {\n  console.log('test');\n}\n",
			input: FileMultiEditInput{
				OldText:    "function test() {\n  console.log('test');\n}",
				NewText:    "function newTest() {\n  console.log('new test');\n}",
				Occurrence: 2,
			},
			expectedFile:  "function newTest() {\n  console.log('new test');\n}\n\nfunction newTest() {\n  console.log('new test');\n}\n",
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFile := filepath.Join(tempDir, tt.name+".txt")
			err := os.WriteFile(testFile, []byte(tt.fileContent), 0644)
			require.NoError(t, err)

			// Set the file path in the input
			tt.input.FilePath = testFile

			// Make sure we can get the file stat time
			fileInfo, err := os.Stat(testFile)
			require.NoError(t, err)
			modTime := fileInfo.ModTime()

			// Create state with the current file modification time
			state := NewBasicState()
			state.SetFileLastAccessed(testFile, modTime)

			tool := &FileMultiEditTool{}

			// First validate the input
			inputBytes, err := json.Marshal(tt.input)
			require.NoError(t, err)

			err = tool.ValidateInput(state, string(inputBytes))
			require.NoError(t, err)

			// Execute the tool
			result := tool.Execute(context.Background(), state, string(inputBytes))

			// Check that there was no error
			assert.Empty(t, result.Error)

			// Check that the result contains the expected count
			assert.Contains(t, result.Result,
				"Replaced "+string("0123456789"[tt.expectedCount])+" occurrence(s)")

			// Check that the file was modified correctly
			content, err := os.ReadFile(testFile)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedFile, string(content))
		})
	}
}

func TestFileMultiEditTool_Name(t *testing.T) {
	tool := &FileMultiEditTool{}
	assert.Equal(t, "file_multi_edit", tool.Name())
}

func TestFileMultiEditTool_Description(t *testing.T) {
	tool := &FileMultiEditTool{}
	assert.Contains(t, tool.Description(), "Edit a file by replacing multiple occurrences of old text with the new text")
}

func TestFileMultiEditTool_TracingKVs(t *testing.T) {
	tool := &FileMultiEditTool{}
	input := FileMultiEditInput{
		FilePath:   "/path/to/file.txt",
		OldText:    "old",
		NewText:    "new",
		Occurrence: 3,
	}

	inputBytes, err := json.Marshal(input)
	require.NoError(t, err)

	kvs, err := tool.TracingKVs(string(inputBytes))
	require.NoError(t, err)

	assert.Len(t, kvs, 4)
}
