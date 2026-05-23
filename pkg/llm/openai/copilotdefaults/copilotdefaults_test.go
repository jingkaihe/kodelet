package copilotdefaults

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/auth"
)

func TestBuildPlatformDefaults(t *testing.T) {
	entries := []auth.CopilotModelCatalogEntry{
		{
			ID: "z-non-reasoning",
			Capabilities: auth.CopilotModelCapabilities{
				Limits: auth.CopilotModelLimits{MaxContextWindowTokens: 64000},
			},
		},
		{
			ID: "a-reasoning",
			Capabilities: auth.CopilotModelCapabilities{
				Limits:   auth.CopilotModelLimits{MaxContextWindowTokens: 256000},
				Supports: auth.CopilotModelSupport{ReasoningEffort: []string{"low", "high"}},
			},
		},
		{
			ID: "a-reasoning",
			Capabilities: auth.CopilotModelCapabilities{
				Limits:   auth.CopilotModelLimits{MaxContextWindowTokens: 256000},
				Supports: auth.CopilotModelSupport{ReasoningEffort: []string{"medium"}},
			},
		},
		{
			Version: "version-fallback",
		},
		{
			Capabilities: auth.CopilotModelCapabilities{Family: "family-fallback"},
		},
		{
			ID:      "   ",
			Version: "  ",
			Capabilities: auth.CopilotModelCapabilities{
				Family: "   ",
			},
		},
	}

	models, pricing := BuildPlatformDefaults(entries)

	require.NotNil(t, models)
	assert.Equal(t, []string{"a-reasoning"}, models.Reasoning)
	assert.Equal(t, []string{"family-fallback", "version-fallback", "z-non-reasoning"}, models.NonReasoning)
	assert.Equal(t, 256000, pricing["a-reasoning"].ContextWindow)
	assert.Equal(t, 64000, pricing["z-non-reasoning"].ContextWindow)
	assert.Equal(t, 128000, pricing["version-fallback"].ContextWindow)
	_, exists := pricing[""]
	assert.False(t, exists)
}

func TestCanonicalModelName(t *testing.T) {
	tests := []struct {
		name  string
		entry auth.CopilotModelCatalogEntry
		want  string
	}{
		{
			name: "prefers id",
			entry: auth.CopilotModelCatalogEntry{
				ID:      " gpt-id ",
				Version: " gpt-version ",
				Capabilities: auth.CopilotModelCapabilities{
					Family: "gpt-family",
				},
			},
			want: "gpt-id",
		},
		{
			name: "falls back to version",
			entry: auth.CopilotModelCatalogEntry{
				Version: " gpt-version ",
				Capabilities: auth.CopilotModelCapabilities{
					Family: "gpt-family",
				},
			},
			want: "gpt-version",
		},
		{
			name: "falls back to family",
			entry: auth.CopilotModelCatalogEntry{
				Capabilities: auth.CopilotModelCapabilities{
					Family: " gpt-family ",
				},
			},
			want: "gpt-family",
		},
		{
			name: "blank",
			entry: auth.CopilotModelCatalogEntry{
				ID:      " ",
				Version: "\t",
				Capabilities: auth.CopilotModelCapabilities{
					Family: "\n",
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CanonicalModelName(tt.entry))
		})
	}
}

func TestSupportsReasoning(t *testing.T) {
	assert.False(t, SupportsReasoning(auth.CopilotModelCatalogEntry{}))
	assert.True(t, SupportsReasoning(auth.CopilotModelCatalogEntry{
		Capabilities: auth.CopilotModelCapabilities{
			Supports: auth.CopilotModelSupport{ReasoningEffort: []string{"low"}},
		},
	}))
}

func TestContextWindow(t *testing.T) {
	assert.Equal(t, 128000, ContextWindow(auth.CopilotModelCatalogEntry{}))
	assert.Equal(t, 200000, ContextWindow(auth.CopilotModelCatalogEntry{
		Capabilities: auth.CopilotModelCapabilities{
			Limits: auth.CopilotModelLimits{MaxContextWindowTokens: 200000},
		},
	}))
}

func TestLoadPlatformDefaultsUsesFreshCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cachePath := filepath.Join(home, ".kodelet", "copilot-models.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(cachePath), 0o755))
	fetchedAt := time.Now().UTC().Format(time.RFC3339Nano)
	cache := fmt.Sprintf(`{
  "providers": {
    "copilot": {
      "fetched_at": %q,
      "models": [
        {
          "id": "cached-reasoning",
          "capabilities": {
            "limits": {"max_context_window_tokens": 111},
            "supports": {"reasoning_effort": ["low"]}
          }
        },
        {
          "id": "cached-basic",
          "capabilities": {}
        }
      ]
    }
  }
}`, fetchedAt)
	require.NoError(t, os.WriteFile(cachePath, []byte(cache), 0o644))

	models, pricing, err := LoadPlatformDefaults(context.Background())
	require.NoError(t, err)
	require.NotNil(t, models)
	assert.Equal(t, []string{"cached-reasoning"}, models.Reasoning)
	assert.Equal(t, []string{"cached-basic"}, models.NonReasoning)
	assert.Equal(t, 111, pricing["cached-reasoning"].ContextWindow)
}
