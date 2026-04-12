package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const copilotModelsCacheTTL = 5 * time.Minute

type CopilotModelCatalogEntry struct {
	ID                 string                   `json:"id"`
	Version            string                   `json:"version"`
	Vendor             string                   `json:"vendor"`
	SupportedEndpoints []string                 `json:"supported_endpoints"`
	Capabilities       CopilotModelCapabilities `json:"capabilities"`
}

type CopilotModelCapabilities struct {
	Family   string              `json:"family"`
	Limits   CopilotModelLimits  `json:"limits"`
	Supports CopilotModelSupport `json:"supports"`
}

type CopilotModelLimits struct {
	MaxContextWindowTokens int `json:"max_context_window_tokens"`
}

type CopilotModelSupport struct {
	ReasoningEffort []string `json:"reasoning_effort"`
}

type copilotModelsCacheFile struct {
	Providers map[string]copilotModelsCacheEntry `json:"providers"`
}

type copilotModelsCacheEntry struct {
	FetchedAt string                     `json:"fetched_at"`
	Models    []CopilotModelCatalogEntry `json:"models"`
}

var (
	copilotModelsMu           sync.Mutex
	copilotModelsMemoryCache  []CopilotModelCatalogEntry
	copilotModelsMemoryExpiry time.Time
)

func copilotModelsCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}

	return filepath.Join(home, ".kodelet", "copilot-models.json"), nil
}

func loadCachedCopilotModels(path string, now time.Time) ([]CopilotModelCatalogEntry, time.Time, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, time.Time{}, nil
		}
		return nil, time.Time{}, errors.Wrap(err, "failed to read copilot models cache")
	}

	var cacheFile copilotModelsCacheFile
	if err := json.Unmarshal(raw, &cacheFile); err != nil {
		return nil, time.Time{}, nil
	}

	entry, ok := cacheFile.Providers["copilot"]
	if !ok {
		return nil, time.Time{}, nil
	}

	fetchedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.FetchedAt))
	if err != nil {
		return nil, time.Time{}, nil
	}

	expiresAt := fetchedAt.Add(copilotModelsCacheTTL)
	if !now.Before(expiresAt) {
		return nil, expiresAt, nil
	}

	return append([]CopilotModelCatalogEntry(nil), entry.Models...), expiresAt, nil
}

func saveCachedCopilotModels(path string, models []CopilotModelCatalogEntry, fetchedAt time.Time) error {
	var cacheFile copilotModelsCacheFile

	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &cacheFile)
	} else if !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to read existing copilot models cache")
	}

	if cacheFile.Providers == nil {
		cacheFile.Providers = make(map[string]copilotModelsCacheEntry)
	}

	cacheFile.Providers["copilot"] = copilotModelsCacheEntry{
		FetchedAt: fetchedAt.UTC().Format(time.RFC3339Nano),
		Models:    append([]CopilotModelCatalogEntry(nil), models...),
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.Wrap(err, "failed to create copilot models cache directory")
	}

	data, err := json.MarshalIndent(cacheFile, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to encode copilot models cache")
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return errors.Wrap(err, "failed to write copilot models cache")
	}

	return nil
}

func fetchCopilotModels(ctx context.Context) ([]CopilotModelCatalogEntry, error) {
	token, err := CopilotAccessToken(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get copilot access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CopilotBaseURL+"/models", nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create copilot models request")
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", copilotChatUserAgent)
	req.Header.Set("Editor-Version", copilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", copilotPluginVersion)
	req.Header.Set("Copilot-Integration-Id", copilotIntegrationID)
	req.Header.Set("OpenAI-Intent", "conversation-panel")
	req.Header.Set("X-GitHub-Api-Version", copilotGitHubAPIVer)
	req.Header.Set("X-Vscode-User-Agent-Library-Version", copilotFetchLibrary)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch copilot models")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("copilot models request failed with status %d", resp.StatusCode)
	}

	var payload struct {
		Data []CopilotModelCatalogEntry `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, errors.Wrap(err, "failed to decode copilot models response")
	}

	return payload.Data, nil
}

// LoadCopilotModels returns the cached Copilot model catalog, refreshing it when stale.
func LoadCopilotModels(ctx context.Context) ([]CopilotModelCatalogEntry, error) {
	now := time.Now()

	copilotModelsMu.Lock()
	if len(copilotModelsMemoryCache) > 0 && now.Before(copilotModelsMemoryExpiry) {
		models := append([]CopilotModelCatalogEntry(nil), copilotModelsMemoryCache...)
		copilotModelsMu.Unlock()
		return models, nil
	}
	copilotModelsMu.Unlock()

	path, err := copilotModelsCachePath()
	if err != nil {
		return nil, err
	}

	copilotModelsMu.Lock()
	defer copilotModelsMu.Unlock()

	if len(copilotModelsMemoryCache) > 0 && now.Before(copilotModelsMemoryExpiry) {
		return append([]CopilotModelCatalogEntry(nil), copilotModelsMemoryCache...), nil
	}

	if cached, expiresAt, err := loadCachedCopilotModels(path, now); err == nil && len(cached) > 0 {
		copilotModelsMemoryCache = append([]CopilotModelCatalogEntry(nil), cached...)
		copilotModelsMemoryExpiry = expiresAt
		return append([]CopilotModelCatalogEntry(nil), cached...), nil
	}

	models, err := fetchCopilotModels(ctx)
	if err != nil {
		return nil, err
	}

	fetchedAt := time.Now()
	if err := saveCachedCopilotModels(path, models, fetchedAt); err != nil {
		return nil, err
	}

	copilotModelsMemoryCache = append([]CopilotModelCatalogEntry(nil), models...)
	copilotModelsMemoryExpiry = fetchedAt.Add(copilotModelsCacheTTL)

	return append([]CopilotModelCatalogEntry(nil), models...), nil
}
