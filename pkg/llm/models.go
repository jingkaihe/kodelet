package llm

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/copilotdefaults"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const modelDiscoveryTimeout = 5 * time.Second

// ModelOptions returns model IDs for a resolved profile's provider connection.
// The configured main model is first; the remaining IDs are sorted and unique.
// Explicit model lists take precedence over pricing catalogs and platform defaults.
// Aliases are intentionally not a catalog: they may refer to other connections.
func ModelOptions(ctx context.Context, config llmtypes.Config) []string {
	return modelOptions(ctx, config, auth.LoadCopilotModels)
}

func modelOptions(ctx context.Context, config llmtypes.Config, loadCopilot func(context.Context) ([]auth.CopilotModelCatalogEntry, error)) []string {
	current := strings.TrimSpace(config.Model)
	seen := make(map[string]bool)
	options := make([]string, 0)
	add := func(model string) {
		if model = strings.TrimSpace(model); model != "" && !seen[model] {
			seen[model] = true
			options = append(options, model)
		}
	}
	add(current)
	add(config.WeakModel)
	for _, model := range profileModelCatalog(ctx, config, loadCopilot) {
		add(model)
	}
	if current != "" {
		sort.Strings(options[1:])
	} else {
		sort.Strings(options)
	}
	return options
}

func profileModelCatalog(ctx context.Context, config llmtypes.Config, loadCopilot func(context.Context) ([]auth.CopilotModelCatalogEntry, error)) []string {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	platform := ProviderPlatform(config, provider)
	var models []string
	switch provider {
	case "openai":
		if config.OpenAI != nil {
			if config.OpenAI.Models != nil {
				models = append(models, config.OpenAI.Models.Reasoning...)
				return append(models, config.OpenAI.Models.NonReasoning...)
			}
			if config.OpenAI.Pricing != nil {
				for model := range config.OpenAI.Pricing {
					models = append(models, model)
				}
				return models
			}
		}
		// An endpoint override can expose an entirely different model catalog.
		if openai.GetConfiguredBaseURL(config) != "" {
			return nil
		}
		switch platform {
		case "openai":
			models = append(models, openaipreset.Models.Reasoning...)
			models = append(models, openaipreset.Models.NonReasoning...)
		case "codex":
			models = append(models, codexpreset.Models.Reasoning...)
			models = append(models, codexpreset.Models.NonReasoning...)
		}
	case "anthropic":
		if anthropic.GetConfiguredBaseURL(config) != "" {
			return nil
		}
		if platform == "anthropic" {
			for model := range anthropic.ModelPricingMap {
				models = append(models, model)
			}
		}
	default:
		return nil
	}

	if platform == "copilot" {
		ctx, cancel := context.WithTimeout(ctx, modelDiscoveryTimeout)
		defer cancel()
		entries, err := loadCopilot(ctx)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("profile", config.Profile).WithField("provider", provider).
				Warn("failed to discover Copilot model options; using configured models only")
			return nil
		}
		for _, entry := range entries {
			if copilotModelSupportsProvider(entry, provider) {
				models = append(models, copilotdefaults.CanonicalModelName(entry))
			}
		}
	}

	chatModels := make([]string, 0, len(models))
	for _, model := range models {
		if isChatModelOption(model) {
			chatModels = append(chatModels, model)
		}
	}
	return chatModels
}

func copilotModelSupportsProvider(entry auth.CopilotModelCatalogEntry, provider string) bool {
	// Prefer advertised protocol support. Older cached catalogs may omit it.
	if len(entry.SupportedEndpoints) > 0 {
		for _, endpoint := range entry.SupportedEndpoints {
			endpoint = strings.TrimPrefix(strings.Trim(strings.ToLower(endpoint), "/"), "v1/")
			if provider == "anthropic" && endpoint == "messages" {
				return true
			}
			if provider == "openai" && (endpoint == "chat/completions" || endpoint == "responses") {
				return true
			}
		}
		return false
	}
	if provider == "anthropic" {
		return strings.EqualFold(strings.TrimSpace(entry.Vendor), "anthropic") ||
			strings.HasPrefix(strings.ToLower(copilotdefaults.CanonicalModelName(entry)), "claude") ||
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(entry.Capabilities.Family)), "claude")
	}
	return true
}

func isChatModelOption(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, kind := range []string{"audio", "realtime", "deep-research"} {
		if strings.Contains(model, kind) {
			return false
		}
	}
	for _, prefix := range []string{"gpt-image", "dall-e", "whisper", "tts-", "text-embedding", "computer-use"} {
		if strings.HasPrefix(model, prefix) {
			return false
		}
	}
	return true
}
