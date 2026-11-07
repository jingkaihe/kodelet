package base

import (
	"path/filepath"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnablePersistenceRequiresInjectedStorage(t *testing.T) {
	basePath := t.TempDir()
	t.Setenv("KODELET_BASE_PATH", basePath)
	thread := NewThread(llmtypes.Config{}, "conversation")

	require.ErrorIs(t, thread.EnablePersistence(t.Context(), true), errConversationStorageUnavailable)
	assert.False(t, thread.IsPersisted())
	assert.NoFileExists(t, filepath.Join(basePath, "storage.db"), "a thread must not open a database of its own")
	require.NoError(t, thread.EnablePersistence(t.Context(), false))
}
