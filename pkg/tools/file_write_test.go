package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileWriteTool_Name(t *testing.T) {
	tool := &FileWriteTool{}
	assert.Equal(t, "file_write", tool.Name())
}

func TestFileWriteTool_GenerateSchema(t *testing.T) {
	tool := &FileWriteTool{}
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/file-write-input", string(schema.ID))
}

func TestFileWriteTool_Description(t *testing.T) {
	tool := &FileWriteTool{}
	desc := tool.Description()

	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "Writes a file with the given text")
}

func TestFileWriteTool_ValidateInput(t *testing.T) {
	tool := &FileWriteTool{}
	state := NewBasicState(context.TODO())

	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test_file.txt")

	t.Run("valid input for new file", func(t *testing.T) {
		input := FileWriteInput{
			FilePath: testFilePath,
			Text:     "test content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		err = tool.ValidateInput(state, string(inputJSON))
		assert.NoError(t, err)
	})

	t.Run("valid input for existing file", func(t *testing.T) {
		// Create a test file
		err := os.WriteFile(testFilePath, []byte("initial content"), 0644)
		require.NoError(t, err)

		fileInfo, err := os.Stat(testFilePath)
		require.NoError(t, err)

		state.SetFileLastAccessed(testFilePath, fileInfo.ModTime())

		input := FileWriteInput{
			FilePath: testFilePath,
			Text:     "updated content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		err = tool.ValidateInput(state, string(inputJSON))
		assert.NoError(t, err)
	})

	t.Run("invalid input - file modified since last read", func(t *testing.T) {
		// Create a test file
		err := os.WriteFile(testFilePath, []byte("modified content"), 0644)
		require.NoError(t, err)

		state.SetFileLastAccessed(testFilePath, time.Now().Add(-time.Hour))

		input := FileWriteInput{
			FilePath: testFilePath,
			Text:     "updated content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		err = tool.ValidateInput(state, string(inputJSON))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "has been modified since the last read")
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		err := tool.ValidateInput(state, "invalid json")
		assert.Error(t, err)
	})
}

func TestFileWriteTool_Execute(t *testing.T) {
	tool := &FileWriteTool{}
	state := NewBasicState(context.TODO())
	ctx := context.Background()

	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test_file.txt")

	t.Run("successful write", func(t *testing.T) {
		input := FileWriteInput{
			FilePath: testFilePath,
			Text:     "test content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		result := tool.Execute(ctx, state, string(inputJSON))
		assert.False(t, result.IsError())
		assert.Contains(t, result.GetResult(), "has been written successfully")

		// Verify file content
		content, err := os.ReadFile(testFilePath)
		require.NoError(t, err)
		assert.Equal(t, "test content", string(content))

		// Verify that the file's last accessed time was updated in the state
		_, err = state.GetFileLastAccessed(testFilePath)
		assert.NoError(t, err)
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		// Create a file with initial content
		err := os.WriteFile(testFilePath, []byte("initial content"), 0644)
		require.NoError(t, err)

		input := FileWriteInput{
			FilePath: testFilePath,
			Text:     "updated content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		result := tool.Execute(ctx, state, string(inputJSON))
		assert.False(t, result.IsError())

		// Verify file content
		content, err := os.ReadFile(testFilePath)
		require.NoError(t, err)
		assert.Equal(t, "updated content", string(content))
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		result := tool.Execute(ctx, state, "invalid json")
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "invalid input")
	})

	t.Run("non-existent directory", func(t *testing.T) {
		nonExistentPath := filepath.Join(tempDir, "non-existent-dir", "test_file.txt")

		input := FileWriteInput{
			FilePath: nonExistentPath,
			Text:     "test content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		result := tool.Execute(ctx, state, string(inputJSON))
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "failed to write the file")
	})
}
