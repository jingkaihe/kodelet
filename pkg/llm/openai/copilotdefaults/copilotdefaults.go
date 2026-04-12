// Package copilotdefaults builds OpenAI-compatible model defaults from the Copilot model catalog.
package copilotdefaults

import (
	"context"
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/auth"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func LoadPlatformDefaults(ctx context.Context) (*llmtypes.CustomModels, llmtypes.CustomPricing, error) {
	entries, err := auth.LoadCopilotModels(ctx)
	if err != nil {
		return nil, nil, err
	}

	models, pricing := BuildPlatformDefaults(entries)
	return models, pricing, nil
}

func BuildPlatformDefaults(entries []auth.CopilotModelCatalogEntry) (*llmtypes.CustomModels, llmtypes.CustomPricing) {
	models := &llmtypes.CustomModels{}
	pricing := make(llmtypes.CustomPricing)
	seenReasoning := map[string]bool{}
	seenNonReasoning := map[string]bool{}

	for _, entry := range entries {
		name := CanonicalModelName(entry)
		if name == "" {
			continue
		}

		pricing[name] = llmtypes.ModelPricing{
			Input:         0,
			CachedInput:   0,
			Output:        0,
			ContextWindow: ContextWindow(entry),
		}

		if SupportsReasoning(entry) {
			if !seenReasoning[name] {
				models.Reasoning = append(models.Reasoning, name)
				seenReasoning[name] = true
			}
			continue
		}

		if !seenNonReasoning[name] {
			models.NonReasoning = append(models.NonReasoning, name)
			seenNonReasoning[name] = true
		}
	}

	sort.Strings(models.Reasoning)
	sort.Strings(models.NonReasoning)

	return models, pricing
}

func CanonicalModelName(entry auth.CopilotModelCatalogEntry) string {
	if normalized := strings.TrimSpace(entry.ID); normalized != "" {
		return normalized
	}

	if normalized := strings.TrimSpace(entry.Version); normalized != "" {
		return normalized
	}

	return strings.TrimSpace(entry.Capabilities.Family)
}

func SupportsReasoning(entry auth.CopilotModelCatalogEntry) bool {
	return len(entry.Capabilities.Supports.ReasoningEffort) > 0
}

func ContextWindow(entry auth.CopilotModelCatalogEntry) int {
	if entry.Capabilities.Limits.MaxContextWindowTokens > 0 {
		return entry.Capabilities.Limits.MaxContextWindowTokens
	}

	return 128000
}
