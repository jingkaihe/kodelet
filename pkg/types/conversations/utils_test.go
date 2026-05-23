package conversations

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDefaultBasePathUsesEnvironmentOverride(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "kodelet-base")
	t.Setenv("KODELET_BASE_PATH", basePath)

	got, err := GetDefaultBasePath()
	require.NoError(t, err)
	assert.Equal(t, basePath, got)
	assert.DirExists(t, basePath)
}
