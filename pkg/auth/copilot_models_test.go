package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCachedCopilotModels(t *testing.T) {
	t.Run("returns fresh cached models", func(t *testing.T) {
		dir := t.TempDir()
		path := dir + "/copilot-models.json"
		fetchedAt := time.Date(2026, 4, 12, 17, 0, 0, 0, time.UTC)

		err := saveCachedCopilotModels(path, []CopilotModelCatalogEntry{{ID: "gpt-5.4"}}, fetchedAt)
		require.NoError(t, err)

		models, expiresAt, err := loadCachedCopilotModels(path, fetchedAt.Add(2*time.Minute))
		require.NoError(t, err)
		require.Len(t, models, 1)
		assert.Equal(t, "gpt-5.4", models[0].ID)
		assert.Equal(t, fetchedAt.Add(copilotModelsCacheTTL), expiresAt)
	})

	t.Run("treats stale cache as miss", func(t *testing.T) {
		dir := t.TempDir()
		path := dir + "/copilot-models.json"
		fetchedAt := time.Date(2026, 4, 12, 17, 0, 0, 0, time.UTC)

		err := saveCachedCopilotModels(path, []CopilotModelCatalogEntry{{ID: "gpt-5.4"}}, fetchedAt)
		require.NoError(t, err)

		models, expiresAt, err := loadCachedCopilotModels(path, fetchedAt.Add(copilotModelsCacheTTL+time.Second))
		require.NoError(t, err)
		assert.Nil(t, models)
		assert.Equal(t, fetchedAt.Add(copilotModelsCacheTTL), expiresAt)
	})
}
