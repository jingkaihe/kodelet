package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetCopilotModelsCache(t *testing.T) {
	t.Helper()

	copilotModelsMu.Lock()
	originalMemoryCache := append([]CopilotModelCatalogEntry(nil), copilotModelsMemoryCache...)
	originalMemoryExpiry := copilotModelsMemoryExpiry
	copilotModelsMemoryCache = nil
	copilotModelsMemoryExpiry = time.Time{}
	copilotModelsMu.Unlock()

	t.Cleanup(func() {
		copilotModelsMu.Lock()
		copilotModelsMemoryCache = originalMemoryCache
		copilotModelsMemoryExpiry = originalMemoryExpiry
		copilotModelsMu.Unlock()
	})
}

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

func TestSaveCachedCopilotModels(t *testing.T) {
	t.Run("preserves other providers and copies model slice", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "copilot-models.json")
		fetchedAt := time.Date(2026, 4, 12, 17, 0, 0, 0, time.UTC)

		initialCache := copilotModelsCacheFile{Providers: map[string]copilotModelsCacheEntry{
			"other": {
				FetchedAt: fetchedAt.Add(-time.Hour).Format(time.RFC3339Nano),
				Models:    []CopilotModelCatalogEntry{{ID: "other-model"}},
			},
		}}
		initialData, err := json.Marshal(initialCache)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, initialData, 0o644))

		models := []CopilotModelCatalogEntry{{ID: "copilot-model"}}
		require.NoError(t, saveCachedCopilotModels(path, models, fetchedAt))
		models[0].ID = "mutated-after-save"

		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(string(raw), "\n"))

		var saved copilotModelsCacheFile
		require.NoError(t, json.Unmarshal(raw, &saved))
		require.Contains(t, saved.Providers, "other")
		require.Contains(t, saved.Providers, "copilot")
		assert.Equal(t, "other-model", saved.Providers["other"].Models[0].ID)
		assert.Equal(t, "copilot-model", saved.Providers["copilot"].Models[0].ID)
		assert.Equal(t, fetchedAt.UTC().Format(time.RFC3339Nano), saved.Providers["copilot"].FetchedAt)
	})
}

func TestCopilotModelsCachePath(t *testing.T) {
	setTestHome(t)

	path, err := copilotModelsCachePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(os.Getenv("HOME"), ".kodelet", "copilot-models.json"), path)
}

func TestLoadCopilotModels(t *testing.T) {
	t.Run("fetches models then serves memory and disk cache copies", func(t *testing.T) {
		setTestHome(t)
		resetCopilotModelsCache(t)

		_, err := SaveCopilotCredentials(&CopilotCredentials{
			AccessToken:    "github-oauth-token",
			CopilotToken:   "copilot-api-token",
			Scope:          "copilot",
			CopilotExpires: time.Now().Add(time.Hour).Unix(),
		})
		require.NoError(t, err)

		var calls int32
		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			assert.Equal(t, http.MethodGet, req.Method)
			assert.Equal(t, CopilotBaseURL+"/models", req.URL.String())
			assert.Equal(t, "Bearer copilot-api-token", req.Header.Get("Authorization"))
			assert.Equal(t, "application/json", req.Header.Get("Accept"))
			assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
			assert.Equal(t, copilotChatUserAgent, req.Header.Get("User-Agent"))
			assert.Equal(t, copilotEditorVersion, req.Header.Get("Editor-Version"))
			assert.Equal(t, copilotPluginVersion, req.Header.Get("Editor-Plugin-Version"))
			assert.Equal(t, copilotIntegrationID, req.Header.Get("Copilot-Integration-Id"))
			assert.Equal(t, "conversation-panel", req.Header.Get("OpenAI-Intent"))
			assert.Equal(t, copilotGitHubAPIVer, req.Header.Get("X-GitHub-Api-Version"))
			assert.Equal(t, copilotFetchLibrary, req.Header.Get("X-Vscode-User-Agent-Library-Version"))

			body := `{"data":[{"id":"gpt-5-copilot","version":"2026-05-01","vendor":"openai","supported_endpoints":["chat"],"capabilities":{"family":"gpt-5","limits":{"max_context_window_tokens":200000},"supports":{"reasoning_effort":["low","medium"]}}}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})})

		models, err := LoadCopilotModels(context.Background())
		require.NoError(t, err)
		require.Len(t, models, 1)
		assert.Equal(t, "gpt-5-copilot", models[0].ID)
		assert.Equal(t, "gpt-5", models[0].Capabilities.Family)
		assert.Equal(t, 200000, models[0].Capabilities.Limits.MaxContextWindowTokens)
		assert.Equal(t, []string{"low", "medium"}, models[0].Capabilities.Supports.ReasoningEffort)
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

		models[0].ID = "mutated-return-value"
		memoryModels, err := LoadCopilotModels(context.Background())
		require.NoError(t, err)
		require.Len(t, memoryModels, 1)
		assert.Equal(t, "gpt-5-copilot", memoryModels[0].ID)
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "second call should use memory cache")

		copilotModelsMu.Lock()
		copilotModelsMemoryCache = nil
		copilotModelsMemoryExpiry = time.Time{}
		copilotModelsMu.Unlock()

		diskModels, err := LoadCopilotModels(context.Background())
		require.NoError(t, err)
		require.Len(t, diskModels, 1)
		assert.Equal(t, "gpt-5-copilot", diskModels[0].ID)
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "third call should use disk cache")
	})

	t.Run("returns fetch errors when cache is missing", func(t *testing.T) {
		setTestHome(t)
		resetCopilotModelsCache(t)

		_, err := SaveCopilotCredentials(&CopilotCredentials{
			AccessToken:    "github-oauth-token",
			CopilotToken:   "copilot-api-token",
			Scope:          "copilot",
			CopilotExpires: time.Now().Add(time.Hour).Unix(),
		})
		require.NoError(t, err)

		setDefaultHTTPClient(t, &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
			}, nil
		})})

		models, err := LoadCopilotModels(context.Background())
		assert.Nil(t, models)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "copilot models request failed with status 502")
	})
}
