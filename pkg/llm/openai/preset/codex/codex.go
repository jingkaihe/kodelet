// Package codex provides preset configurations for Codex CLI models.
// It embeds system prompts from the official Codex CLI repository.
package codex

import (
	"embed"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
)

//go:embed prompts/*.md
var embeddedFiles embed.FS

// cachedPrompts is a cache of loaded prompts
var cachedPrompts = make(map[string]string)

// GetPrompt returns the embedded prompt for a given prompt file name.
func GetPrompt(name string) (string, error) {
	if cached, ok := cachedPrompts[name]; ok {
		return cached, nil
	}

	// Try with .md extension if not provided
	fileName := name
	if !strings.HasSuffix(fileName, ".md") {
		fileName = name + ".md"
	}

	data, err := embeddedFiles.ReadFile("prompts/" + fileName)
	if err != nil {
		return "", err
	}

	prompt := string(data)
	cachedPrompts[name] = prompt
	return prompt, nil
}

// GetBasePrompt returns the base Codex prompt.
func GetBasePrompt() (string, error) {
	return GetPrompt("prompt")
}

// GetGPT52Prompt returns the GPT-5.2 specific prompt.
func GetGPT52Prompt() (string, error) {
	return GetPrompt("gpt_5_2_prompt")
}

// GetGPT51CodexMaxPrompt returns the GPT-5.1 Codex Max specific prompt.
func GetGPT51CodexMaxPrompt() (string, error) {
	return GetPrompt("gpt_5_1_codex_max_prompt")
}

// GetHierarchicalAgentsMessage returns the hierarchical agents message.
func GetHierarchicalAgentsMessage() (string, error) {
	return GetPrompt("hierarchical_agents_message")
}

// GetSystemPromptForModel returns the appropriate system prompt for a model.
func GetSystemPromptForModel(modelSlug string) (string, error) {
	switch {
	case strings.Contains(modelSlug, "gpt-5.2"):
		return GetGPT52Prompt()
	case strings.Contains(modelSlug, "gpt-5.1-codex-max"):
		return GetGPT51CodexMaxPrompt()
	default:
		return GetBasePrompt()
	}
}

// Models defines the Codex model categorization for reasoning and non-reasoning models.
var Models = llm.CustomModels{
	Reasoning: []string{
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex-mini",
	},
	NonReasoning: []string{},
}

// Pricing defines the pricing information for Codex models.
// Note: Codex subscription pricing is included in the ChatGPT subscription.
var Pricing = llm.CustomPricing{
	"gpt-5.2-codex": llm.ModelPricing{
		Input:         0.0, // Included in subscription
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.2": llm.ModelPricing{
		Input:         0.0,
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-max": llm.ModelPricing{
		Input:         0.0,
		Output:        0.0,
		ContextWindow: 272_000,
	},
	"gpt-5.1-codex-mini": llm.ModelPricing{
		Input:         0.0,
		Output:        0.0,
		ContextWindow: 272_000,
	},
}

// BaseURL is the API endpoint for Codex models (via ChatGPT backend).
const BaseURL = "https://chatgpt.com/backend-api/codex"

// DefaultModel is the default model for Codex.
const DefaultModel = "gpt-5.1-codex-max"
