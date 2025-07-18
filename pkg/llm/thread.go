// Package llm provides a unified interface for Large Language Model providers.
// It abstracts different LLM providers (Anthropic Claude, OpenAI GPT) behind
// a common Thread interface for consistent interaction patterns.
package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/llm/anthropic"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// resolveModelAlias resolves a model name through the configured aliases
// If the model name exists as an alias, returns the mapped full name
// Otherwise returns the original model name unchanged
func resolveModelAlias(modelName string, aliases map[string]string) string {
	logger.G(context.TODO()).
		WithField("modelName", modelName).
		WithField("aliases", aliases).Debug("Resolving model alias")

	if aliases == nil {
		return modelName
	}

	if resolvedName, exists := aliases[modelName]; exists {
		return resolvedName
	}

	return modelName
}

// NewThread creates a new thread based on the model specified in the config
func NewThread(config llmtypes.Config) (llmtypes.Thread, error) {
	config.Model = resolveModelAlias(config.Model, config.Aliases)

	// Create thread based on provider
	switch strings.ToLower(config.Provider) {
	case "openai":
		return openai.NewOpenAIThread(config, NewSubagentContext)
	case "anthropic":
		return anthropic.NewAnthropicThread(config, NewSubagentContext)
	default:
		return nil, errors.Errorf("unsupported provider: %s", config.Provider)
	}
}

// NewSubagentThread creates a new subagent thread based on the parent thread's configuration
// This function handles cross-provider subagent creation at the centralized level
func NewSubagentThread(ctx context.Context, parentThread llmtypes.Thread, state tooltypes.State) llmtypes.Thread {
	// Get the parent's configuration
	parentConfig := getThreadConfig(parentThread)
	config := parentConfig
	config.IsSubAgent = true

	// Apply subagent configuration if specified
	if parentConfig.SubAgent != nil {
		subConfig := parentConfig.SubAgent

		// Check if we need a different provider
		if subConfig.Provider != "" && subConfig.Provider != parentConfig.Provider {
			// Create a new thread with different provider
			newConfig := config
			newConfig.Provider = subConfig.Provider
			newConfig.Model = subConfig.Model
			if subConfig.MaxTokens > 0 {
				newConfig.MaxTokens = subConfig.MaxTokens
			}
			if subConfig.ReasoningEffort != "" {
				newConfig.ReasoningEffort = subConfig.ReasoningEffort
			}
			if subConfig.ThinkingBudget > 0 {
				newConfig.ThinkingBudgetTokens = subConfig.ThinkingBudget
			}
			if subConfig.OpenAI != nil {
				newConfig.OpenAI = subConfig.OpenAI
			}

			// Create new thread with different provider using central NewThread function
			newThread, err := NewThread(newConfig)
			if err != nil {
				logger.G(ctx).WithError(err).Error("Failed to create subagent with different provider, falling back to same provider")
				// Fall back to same provider
			} else {
				// Set up the state for the new thread
				newThread.SetState(state)
				return newThread
			}
		}

		// Same provider or fallback - apply subagent settings
		if subConfig.Model != "" {
			config.Model = subConfig.Model
		}
		if subConfig.MaxTokens > 0 {
			config.MaxTokens = subConfig.MaxTokens
		}
		if subConfig.ReasoningEffort != "" {
			config.ReasoningEffort = subConfig.ReasoningEffort
		}
		if subConfig.ThinkingBudget > 0 {
			config.ThinkingBudgetTokens = subConfig.ThinkingBudget
		}
	}

	// Create same-provider subagent using the parent's NewSubAgent method
	subagent := parentThread.NewSubAgent(ctx, config)
	subagent.SetState(state)
	return subagent
}

// getThreadConfig extracts configuration from a thread
// This is a helper function to get config from different thread types
func getThreadConfig(thread llmtypes.Thread) llmtypes.Config {
	switch t := thread.(type) {
	case *anthropic.AnthropicThread:
		return t.GetConfig()
	case *openai.OpenAIThread:
		return t.GetConfig()
	default:
		// Return a default config if we can't determine the type
		logger.G(context.Background()).Warn("Unknown thread type, using default config")
		return llmtypes.Config{
			Provider: thread.Provider(),
		}
	}
}

// NewSubagentContext creates a subagent context for the given thread
// This centralized function handles cross-provider subagent creation
func NewSubagentContext(ctx context.Context, parentThread llmtypes.Thread, handler llmtypes.MessageHandler, compactRatio float64, disableAutoCompact bool) context.Context {
	state := tools.NewBasicState(ctx, tools.WithSubAgentTools(), tools.WithExtraMCPTools(parentThread.GetState().MCPTools()))
	subAgent := NewSubagentThread(ctx, parentThread, state)
	ctx = context.WithValue(ctx, llmtypes.SubAgentConfig{}, llmtypes.SubAgentConfig{
		Thread:             subAgent,
		MessageHandler:     handler,
		CompactRatio:       compactRatio,
		DisableAutoCompact: disableAutoCompact,
	})
	return ctx
}

// SendMessageAndGetTextWithUsage is a convenience method for one-shot queries that returns the response as a string and usage information
func SendMessageAndGetTextWithUsage(ctx context.Context, state tooltypes.State, query string, config llmtypes.Config, silent bool, opt llmtypes.MessageOpt) (string, llmtypes.Usage) {
	thread, err := NewThread(config)
	if err != nil {
		return fmt.Sprintf("Error creating thread: %v", err), llmtypes.Usage{}
	}
	thread.SetState(state)

	handler := &llmtypes.StringCollectorHandler{Silent: silent}
	_, err = thread.SendMessage(ctx, query, handler, opt)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), llmtypes.Usage{}
	}
	return handler.CollectedText(), thread.GetUsage()
}

// SendMessageAndGetText is a convenience method for one-shot queries that returns the response as a string
func SendMessageAndGetText(ctx context.Context, state tooltypes.State, query string, config llmtypes.Config, silent bool, opt llmtypes.MessageOpt) string {
	text, _ := SendMessageAndGetTextWithUsage(ctx, state, query, config, silent, opt)
	return text
}

// ExtractMessages parses the raw messages from a conversation record
func ExtractMessages(provider string, rawMessages []byte, toolResults map[string]tooltypes.StructuredToolResult) ([]llmtypes.Message, error) {
	switch provider {
	case "anthropic":
		return anthropic.ExtractMessages(rawMessages, toolResults)
	case "openai":
		return openai.ExtractMessages(rawMessages, toolResults)
	default:
		return nil, errors.Errorf("unsupported provider: %s", provider)
	}
}
