package llm

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelOptionsScopedCatalogs(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	tests := []struct {
		name     string
		config   llmtypes.Config
		want     []string
		contains []string
		excludes []string
	}{
		{
			name: "anthropic ignores other provider catalogs and aliases",
			config: llmtypes.Config{
				Provider: "anthropic", Model: "custom-claude",
				Aliases: map[string]string{"gpt": "gpt-5", "other": "unrelated-model"},
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "codex",
					Models:   &llmtypes.CustomModels{Reasoning: []string{"openai-custom"}},
					Pricing:  llmtypes.CustomPricing{"priced-openai": {}},
				},
			},
			contains: []string{"claude-sonnet-4-5", "claude-opus-4-6"},
			excludes: []string{"gpt-5", "unrelated-model", "openai-custom", "priced-openai"},
		},
		{
			name: "openai defaults are chat models only",
			config: llmtypes.Config{
				Provider: "openai", Model: "unknown-openai", WeakModel: "gpt-4.1-mini",
				Aliases:   map[string]string{"claude": "claude-sonnet-4-5"},
				Anthropic: &llmtypes.AnthropicConfig{Platform: "copilot"},
			},
			contains: []string{"gpt-5", "gpt-4.1-mini", "gpt-4o"},
			excludes: []string{
				"claude-sonnet-4-5", "gpt-image-1", "gpt-4o-audio-preview", "gpt-4o-realtime-preview",
				"o3-deep-research", "o4-mini-deep-research", "computer-use-preview",
			},
		},
		{
			name: "codex does not inherit general openai catalog",
			config: llmtypes.Config{
				Provider: " OpenAI ", Model: "gpt-5.2-codex",
				OpenAI: &llmtypes.OpenAIConfig{Platform: " CODEX "},
			},
			contains: []string{"gpt-5.3-codex", "gpt-5.4-mini"},
			excludes: []string{"gpt-4o", "gpt-4.1", "o1", "claude-sonnet-4-5"},
		},
		{
			name: "explicit model lists override pricing and defaults with sorting and deduplication",
			config: llmtypes.Config{
				Provider: "openai", Model: " z-current ", WeakModel: " b-weak ",
				OpenAI: &llmtypes.OpenAIConfig{
					Platform: "openai",
					Models: &llmtypes.CustomModels{
						Reasoning:    []string{"z-current", "c-model", " b-weak", ""},
						NonReasoning: []string{"a-model", "c-model", "gpt-image-1", "  "},
					},
					Pricing: llmtypes.CustomPricing{"pricing-only": {}},
				},
			},
			want: []string{"z-current", "a-model", "b-weak", "c-model", "gpt-image-1"},
		},
		{
			name: "explicit empty model lists do not expand",
			config: llmtypes.Config{
				Provider: "openai", Model: "current", WeakModel: "current",
				OpenAI: &llmtypes.OpenAIConfig{Models: &llmtypes.CustomModels{}, Pricing: llmtypes.CustomPricing{"other": {}}},
			},
			want: []string{"current"},
		},
		{
			name: "explicit pricing catalog replaces platform defaults",
			config: llmtypes.Config{
				Provider: "openai", Model: "z-current",
				OpenAI: &llmtypes.OpenAIConfig{Platform: "codex", Pricing: llmtypes.CustomPricing{"priced": {}, "gpt-image-1": {}}},
			},
			want: []string{"z-current", "gpt-image-1", "priced"},
		},
		{
			name: "custom openai endpoint retains only configured models",
			config: llmtypes.Config{
				Provider: "openai", Model: "local-main", WeakModel: "local-weak",
				OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1"},
			},
			want: []string{"local-main", "local-weak"},
		},
		{
			name: "custom endpoint accepts explicit model lists",
			config: llmtypes.Config{
				Provider: "openai", Model: "local-main",
				OpenAI: &llmtypes.OpenAIConfig{
					BaseURL: "https://custom.example/v1",
					Models:  &llmtypes.CustomModels{NonReasoning: []string{"local-other"}},
				},
			},
			want: []string{"local-main", "local-other"},
		},
		{
			name: "custom endpoint accepts explicit pricing catalog",
			config: llmtypes.Config{
				Provider: "openai", Model: "local-main",
				OpenAI: &llmtypes.OpenAIConfig{BaseURL: "https://custom.example/v1", Pricing: llmtypes.CustomPricing{"local-priced": {}}},
			},
			want: []string{"local-main", "local-priced"},
		},
		{
			name: "custom anthropic endpoint has no implicit catalog",
			config: llmtypes.Config{
				Provider: "anthropic", Model: "custom-claude",
				Anthropic: &llmtypes.AnthropicConfig{BaseURL: "https://custom.example"},
			},
			want: []string{"custom-claude"},
		},
		{
			name: "unknown platform has no implicit catalog",
			config: llmtypes.Config{
				Provider: "openai", Model: "unknown",
				OpenAI: &llmtypes.OpenAIConfig{Platform: "custom"},
			},
			want: []string{"unknown"},
		},
		{
			name: "explicit copilot lists skip discovery",
			config: llmtypes.Config{
				Provider: "openai", Model: "configured",
				OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot", Models: &llmtypes.CustomModels{Reasoning: []string{"listed"}}},
			},
			want: []string{"configured", "listed"},
		},
		{
			name: "copilot custom endpoint skips discovery",
			config: llmtypes.Config{
				Provider: "openai", Model: "configured",
				OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot", BaseURL: "https://custom.example/v1"},
			},
			want: []string{"configured"},
		},
		{
			name: "anthropic copilot custom endpoint skips discovery",
			config: llmtypes.Config{
				Provider: "anthropic", Model: "configured",
				Anthropic: &llmtypes.AnthropicConfig{Platform: "copilot", BaseURL: "https://custom.example"},
			},
			want: []string{"configured"},
		},
		{
			name:     "configured non-chat model is retained",
			config:   llmtypes.Config{Provider: "openai", Model: "gpt-image-1", WeakModel: "o3-deep-research"},
			contains: []string{"gpt-image-1", "o3-deep-research"},
		},
		{
			name: "empty config returns empty array",
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := modelOptions(t.Context(), tt.config, func(context.Context) ([]auth.CopilotModelCatalogEntry, error) {
				t.Fatal("this configuration must not perform discovery")
				return nil, nil
			})
			if tt.want != nil {
				assert.Equal(t, tt.want, options)
			}
			for _, model := range tt.contains {
				assert.Contains(t, options, model)
			}
			for _, model := range tt.excludes {
				assert.NotContains(t, options, model)
			}
			if strings.TrimSpace(tt.config.Model) != "" {
				require.NotEmpty(t, options)
				assert.Equal(t, strings.TrimSpace(tt.config.Model), options[0])
				assert.True(t, slices.IsSorted(options[1:]))
			}
			unique := slices.Compact(slices.Sorted(slices.Values(options)))
			assert.Len(t, unique, len(options))
		})
	}
}

func TestModelOptionsEndpointEnvironmentOverrides(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("OPENAI_API_BASE", "https://custom.example/v1")
			t.Setenv("ANTHROPIC_BASE_URL", "https://custom.example")
			assert.Equal(t, []string{"current"}, ModelOptions(t.Context(), llmtypes.Config{Provider: provider, Model: "current"}))
		})
	}
}

func TestModelOptionsCopilot(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	entries := []auth.CopilotModelCatalogEntry{
		{ID: "gpt-chat", SupportedEndpoints: []string{"/chat/completions"}},
		{ID: "gpt-responses", SupportedEndpoints: []string{"/v1/responses"}},
		{ID: "claude-messages", SupportedEndpoints: []string{"/v1/messages"}},
		{ID: "claude-chat-only", SupportedEndpoints: []string{"/chat/completions"}},
		{ID: "compatible", SupportedEndpoints: []string{"/messages", "/chat/completions"}},
		{ID: "vendor-fallback", Vendor: "Anthropic"},
		{ID: "family-fallback", Capabilities: auth.CopilotModelCapabilities{Family: "claude"}},
		{Version: "claude-version"},
		{Capabilities: auth.CopilotModelCapabilities{Family: "claude-family"}},
		{ID: "gpt-chat"},
		{ID: "gpt-image-1"},
		{ID: "gpt-audio-preview"},
		{ID: "embedding", SupportedEndpoints: []string{"/embeddings"}},
		{},
	}
	for _, tt := range []struct {
		provider string
		want     []string
	}{
		{
			provider: "openai",
			want: []string{
				"configured", "claude-chat-only", "claude-family", "claude-version", "compatible",
				"family-fallback", "gpt-chat", "gpt-responses", "vendor-fallback", "weak",
			},
		},
		{
			provider: "anthropic",
			want: []string{
				"configured", "claude-family", "claude-messages", "claude-version", "compatible",
				"family-fallback", "vendor-fallback", "weak",
			},
		},
	} {
		for _, fail := range []bool{false, true} {
			name := tt.provider
			if fail {
				name += " discovery failure"
			}
			t.Run(name, func(t *testing.T) {
				config := llmtypes.Config{
					Provider: tt.provider, Model: "configured", WeakModel: "weak",
					OpenAI:    &llmtypes.OpenAIConfig{Platform: "copilot"},
					Anthropic: &llmtypes.AnthropicConfig{Platform: "copilot"},
				}
				called := false
				options := modelOptions(t.Context(), config, func(ctx context.Context) ([]auth.CopilotModelCatalogEntry, error) {
					called = true
					deadline, ok := ctx.Deadline()
					assert.True(t, ok)
					assert.Positive(t, time.Until(deadline))
					assert.LessOrEqual(t, time.Until(deadline), modelDiscoveryTimeout)
					if fail {
						return nil, errors.New("discovery unavailable")
					}
					return entries, nil
				})
				assert.True(t, called)
				if fail {
					assert.Equal(t, []string{"configured", "weak"}, options)
				} else {
					assert.Equal(t, tt.want, options)
				}
			})
		}
	}
}

func TestModelOptionsCopilotRespectsRequestContext(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	options := modelOptions(ctx, llmtypes.Config{
		Provider: "openai", Model: "configured",
		OpenAI: &llmtypes.OpenAIConfig{Platform: "copilot"},
	}, func(ctx context.Context) ([]auth.CopilotModelCatalogEntry, error) {
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		return nil, ctx.Err()
	})
	assert.Equal(t, []string{"configured"}, options)
}
