package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/diffview"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
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
		err := os.WriteFile(testFilePath, []byte("initial content"), 0o644)
		require.NoError(t, err)

		input := FileWriteInput{
			FilePath: testFilePath,
			Text:     "updated content",
		}

		inputJSON, err := json.Marshal(input)
		require.NoError(t, err)

		err = tool.ValidateInput(state, string(inputJSON))
		assert.NoError(t, err)
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
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		// Create a file with initial content
		err := os.WriteFile(testFilePath, []byte("initial content"), 0o644)
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
		meta := result.StructuredData().Metadata.(*tooltypes.FileWriteMetadata)
		assert.Equal(t, nonExistentPath, meta.FilePath)
		assert.Empty(t, meta.UnifiedDiff)
	})

	t.Run("cannot read existing path", func(t *testing.T) {
		inputJSON, err := json.Marshal(FileWriteInput{FilePath: tempDir, Text: "new content"})
		require.NoError(t, err)

		result := tool.Execute(ctx, state, string(inputJSON))
		assert.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "failed to read the file before writing")
		assert.Empty(t, result.StructuredData().Metadata.(*tooltypes.FileWriteMetadata).UnifiedDiff)
		info, err := os.Stat(tempDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})
}

func TestFileWriteTool_ExecuteUnifiedDiff(t *testing.T) {
	for _, tt := range []struct {
		name     string
		exists   bool
		oldText  string
		text     string
		added    int
		removed  int
		contains []string
	}{
		{
			name: "new file", text: "one\ntwo\n", added: 2,
			contains: []string{"+one\n+two\n"},
		},
		{
			name: "overwrite", exists: true,
			oldText: "before\nold\nafter\n", text: "before\nnew\nafter\n", added: 1, removed: 1,
			contains: []string{" before\n-old\n+new\n after\n"},
		},
		{
			name: "unchanged", exists: true, oldText: "same\n", text: "same\n",
		},
		{
			name: "final newline change", exists: true,
			oldText: "value", text: "value\n", added: 1, removed: 1,
			contains: []string{`\ No newline at end of file`},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file.txt")
			if tt.exists {
				require.NoError(t, os.WriteFile(path, []byte(tt.oldText), 0o644))
			}
			inputJSON, err := json.Marshal(FileWriteInput{FilePath: path, Text: tt.text})
			require.NoError(t, err)

			result := (&FileWriteTool{}).Execute(t.Context(), nil, string(inputJSON))
			require.False(t, result.IsError(), result.GetError())
			meta := result.StructuredData().Metadata.(*tooltypes.FileWriteMetadata)
			assert.Equal(t, path, meta.FilePath)
			assert.Equal(t, tt.text, meta.Content)
			for _, content := range tt.contains {
				assert.Contains(t, meta.UnifiedDiff, content)
			}
			if tt.oldText == tt.text {
				assert.Empty(t, meta.UnifiedDiff)
			}
			summary := diffview.FromFileWriteMetadata(*meta)
			assert.Equal(t, tt.added, summary.Added)
			assert.Equal(t, tt.removed, summary.Removed)

			content, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.text, string(content))
		})
	}
}
