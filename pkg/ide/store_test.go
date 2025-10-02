package ide

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIDEStore(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "kodelet-ide-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Override the home directory for testing
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	t.Run("NewIDEStore creates directory", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)
		assert.NotNil(t, store)

		ideDir := filepath.Join(tmpDir, ".kodelet", "ide")
		_, err = os.Stat(ideDir)
		assert.NoError(t, err, "IDE directory should exist")
	})

	t.Run("WriteContext and ReadContext", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)

		conversationID := "test-conversation-123"
		context := &Context{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file1.go", Language: "go"},
				{Path: "/path/to/file2.go", Language: "go"},
			},
			Diagnostics: []DiagnosticInfo{
				{
					FilePath: "/path/to/file1.go",
					Line:     10,
					Column:   5,
					Severity: "error",
					Message:  "undefined variable",
					Source:   "gopls",
					Code:     "UndeclaredName",
				},
			},
		}

		// Write context
		err = store.WriteContext(conversationID, context)
		require.NoError(t, err)

		// Read context
		readContext, err := store.ReadContext(conversationID)
		require.NoError(t, err)
		assert.NotNil(t, readContext)
		assert.Len(t, readContext.OpenFiles, 2)
		assert.Equal(t, "/path/to/file1.go", readContext.OpenFiles[0].Path)
		assert.Equal(t, "go", readContext.OpenFiles[0].Language)
		assert.Len(t, readContext.Diagnostics, 1)
		assert.Equal(t, "error", readContext.Diagnostics[0].Severity)
		assert.Equal(t, "undefined variable", readContext.Diagnostics[0].Message)
	})

	t.Run("WriteContext with selection", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)

		conversationID := "test-conversation-456"
		context := &Context{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
			Selection: &SelectionInfo{
				FilePath:  "/path/to/file.go",
				StartLine: 10,
				EndLine:   20,
				Content:   "func TestExample() {\n\t// test code\n}",
			},
		}

		err = store.WriteContext(conversationID, context)
		require.NoError(t, err)

		readContext, err := store.ReadContext(conversationID)
		require.NoError(t, err)
		assert.NotNil(t, readContext)
		assert.NotNil(t, readContext.Selection)
		assert.Equal(t, "/path/to/file.go", readContext.Selection.FilePath)
		assert.Equal(t, 10, readContext.Selection.StartLine)
		assert.Equal(t, 20, readContext.Selection.EndLine)
		assert.Contains(t, readContext.Selection.Content, "func TestExample()")
	})

	t.Run("ReadContext non-existent", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)

		context, err := store.ReadContext("non-existent-id")
		assert.ErrorIs(t, err, ErrContextNotFound)
		assert.Nil(t, context)
	})

	t.Run("ClearContext", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)

		conversationID := "test-conversation-789"
		context := &Context{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
		}

		// Write context
		err = store.WriteContext(conversationID, context)
		require.NoError(t, err)

		// Verify it exists
		assert.True(t, store.HasContext(conversationID))

		// Clear context
		err = store.ClearContext(conversationID)
		require.NoError(t, err)

		// Verify it's gone
		assert.False(t, store.HasContext(conversationID))
	})

	t.Run("HasContext", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)

		conversationID := "test-conversation-101"

		// Should not exist initially
		assert.False(t, store.HasContext(conversationID))

		// Write context
		context := &Context{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
		}
		err = store.WriteContext(conversationID, context)
		require.NoError(t, err)

		// Should exist now
		assert.True(t, store.HasContext(conversationID))
	})

	t.Run("UpdatedAt timestamp", func(t *testing.T) {
		store, err := NewIDEStore()
		require.NoError(t, err)

		conversationID := "test-conversation-202"
		context := &Context{
			OpenFiles: []FileInfo{
				{Path: "/path/to/file.go", Language: "go"},
			},
		}

		beforeWrite := time.Now()
		err = store.WriteContext(conversationID, context)
		require.NoError(t, err)
		afterWrite := time.Now()

		readContext, err := store.ReadContext(conversationID)
		require.NoError(t, err)
		assert.NotNil(t, readContext)

		// Check that UpdatedAt was set
		assert.False(t, readContext.UpdatedAt.IsZero())
		assert.True(t, readContext.UpdatedAt.After(beforeWrite) || readContext.UpdatedAt.Equal(beforeWrite))
		assert.True(t, readContext.UpdatedAt.Before(afterWrite) || readContext.UpdatedAt.Equal(afterWrite))
	})
}
