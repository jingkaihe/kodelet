package llm_test

import (
	"context"
	"os"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrossProviderSubagent tests cross-provider subagent functionality
func TestCrossProviderSubagent(t *testing.T) {
	// Skip if API keys are not set
	if os.Getenv("ANTHROPIC_API_KEY") == "" || os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping cross-provider test: both ANTHROPIC_API_KEY and OPENAI_API_KEY must be set")
	}

	// Test Claude main agent with GPT subagent
	t.Run("Claude main with GPT subagent", func(t *testing.T) {
		// Create Claude main agent configuration
		mainConfig := llmtypes.Config{
			Provider:  "anthropic",
			Model:     "claude-3-5-haiku-20241022", // Use faster model for tests
			MaxTokens: 1024,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Provider:  "openai",
				Model:     "gpt-4o-mini",
				MaxTokens: 512,
			},
		}

		// Create main thread
		mainThread, err := llm.NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Test that subagent configuration is properly set
		config := mainThread.GetConfig()
		assert.NotNil(t, config.SubAgent)
		assert.Equal(t, "openai", config.SubAgent.Provider)
		assert.Equal(t, "gpt-4o-mini", config.SubAgent.Model)

		// Create a subagent context
		subagentCtx := llm.NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")
		assert.NotNil(t, subagentConfig.Thread)

		// Verify the subagent is using OpenAI
		assert.Equal(t, "openai", subagentConfig.Thread.Provider())
		
		// Verify the subagent has the correct configuration
		subConfig := subagentConfig.Thread.GetConfig()
		assert.Equal(t, "openai", subConfig.Provider)
		assert.Equal(t, "gpt-4o-mini", subConfig.Model)
		assert.Equal(t, 512, subConfig.MaxTokens)
		assert.True(t, subConfig.IsSubAgent)
	})

	// Test GPT main agent with Claude subagent
	t.Run("GPT main with Claude subagent", func(t *testing.T) {
		// Create GPT main agent configuration
		mainConfig := llmtypes.Config{
			Provider:        "openai",
			Model:           "gpt-4o-mini",
			MaxTokens:       1024,
			ReasoningEffort: "low",
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Provider:       "anthropic",
				Model:          "claude-3-5-haiku-20241022",
				MaxTokens:      512,
				ThinkingBudget: 256,
			},
		}

		// Create main thread
		mainThread, err := llm.NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Test that subagent configuration is properly set
		config := mainThread.GetConfig()
		assert.NotNil(t, config.SubAgent)
		assert.Equal(t, "anthropic", config.SubAgent.Provider)
		assert.Equal(t, "claude-3-5-haiku-20241022", config.SubAgent.Model)

		// Create a subagent context
		subagentCtx := llm.NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")
		assert.NotNil(t, subagentConfig.Thread)

		// Verify the subagent is using Anthropic
		assert.Equal(t, "anthropic", subagentConfig.Thread.Provider())
		
		// Verify the subagent has the correct configuration
		subConfig := subagentConfig.Thread.GetConfig()
		assert.Equal(t, "anthropic", subConfig.Provider)
		assert.Equal(t, "claude-3-5-haiku-20241022", subConfig.Model)
		assert.Equal(t, 512, subConfig.MaxTokens)
		assert.Equal(t, 256, subConfig.ThinkingBudgetTokens)
		assert.True(t, subConfig.IsSubAgent)
	})

	// Test same provider with different models
	t.Run("Same provider different models", func(t *testing.T) {
		// Create Claude main agent with Claude subagent (different model)
		mainConfig := llmtypes.Config{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 2048,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Model:     "claude-3-5-haiku-20241022", // Faster model for subagent
				MaxTokens: 1024,
			},
		}

		// Create main thread
		mainThread, err := llm.NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Create a subagent context
		subagentCtx := llm.NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")

		// Verify the subagent is using the same provider but different model
		assert.Equal(t, "anthropic", subagentConfig.Thread.Provider())
		
		// Verify the subagent has the correct configuration
		subConfig := subagentConfig.Thread.GetConfig()
		assert.Equal(t, "anthropic", subConfig.Provider)
		assert.Equal(t, "claude-3-5-haiku-20241022", subConfig.Model)
		assert.Equal(t, 1024, subConfig.MaxTokens)
		assert.True(t, subConfig.IsSubAgent)
	})

	// Test fallback behavior when cross-provider fails
	t.Run("Fallback to same provider on error", func(t *testing.T) {
		// Create configuration with invalid provider
		mainConfig := llmtypes.Config{
			Provider:  "anthropic",
			Model:     "claude-3-5-haiku-20241022",
			MaxTokens: 1024,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Provider: "invalid-provider",
				Model:    "some-model",
			},
		}

		// Create main thread
		mainThread, err := llm.NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Create a subagent context - should fall back to same provider
		subagentCtx := llm.NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")

		// Should fall back to same provider
		assert.Equal(t, "anthropic", subagentConfig.Thread.Provider())
	})
}

// TestCrossProviderWithOpenAICompatible tests cross-provider with OpenAI-compatible APIs
func TestCrossProviderWithOpenAICompatible(t *testing.T) {
	// Skip if API keys are not set
	if os.Getenv("ANTHROPIC_API_KEY") == "" || os.Getenv("XAI_API_KEY") == "" {
		t.Skip("Skipping OpenAI-compatible test: both ANTHROPIC_API_KEY and XAI_API_KEY must be set")
	}

	t.Run("Claude main with xAI Grok subagent", func(t *testing.T) {
		// Create Claude main agent with xAI Grok subagent
		mainConfig := llmtypes.Config{
			Provider:  "anthropic",
			Model:     "claude-3-5-haiku-20241022",
			MaxTokens: 1024,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Provider:  "openai",
				Model:     "grok-3",
				MaxTokens: 512,
				OpenAI: &llmtypes.OpenAIConfig{
					Preset: "xai",
				},
			},
		}

		// Create main thread
		mainThread, err := llm.NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Create a subagent context
		subagentCtx := llm.NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")

		// Verify the subagent is using OpenAI provider
		assert.Equal(t, "openai", subagentConfig.Thread.Provider())
		
		// Verify the subagent has the correct configuration
		subConfig := subagentConfig.Thread.GetConfig()
		assert.Equal(t, "openai", subConfig.Provider)
		assert.Equal(t, "grok-3", subConfig.Model)
		assert.Equal(t, 512, subConfig.MaxTokens)
		assert.NotNil(t, subConfig.OpenAI)
		assert.Equal(t, "xai", subConfig.OpenAI.Preset)
		assert.True(t, subConfig.IsSubAgent)
	})
}

// TestSubagentCompactConfigInheritance tests that compact configuration is properly inherited
func TestSubagentCompactConfigInheritance(t *testing.T) {
	// Create a simple configuration
	mainConfig := llmtypes.Config{
		Provider:  "anthropic",
		Model:     "claude-3-5-haiku-20241022",
		MaxTokens: 1024,
	}

	// Create main thread
	mainThread, err := llm.NewThread(mainConfig)
	require.NoError(t, err)

	// Set up state
	ctx := context.Background()
	state := tools.NewBasicState(ctx)
	mainThread.SetState(state)

	// Test different compact configurations
	testCases := []struct {
		name               string
		compactRatio       float64
		disableAutoCompact bool
	}{
		{"Default compact settings", 0.0, false},
		{"High compact ratio", 0.9, false},
		{"Compact disabled", 0.8, true},
		{"Edge case ratio 1.0", 1.0, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a subagent context with specific compact settings
			subagentCtx := llm.NewSubagentContext(
				ctx, 
				mainThread, 
				&llmtypes.StringCollectorHandler{Silent: true}, 
				tc.compactRatio, 
				tc.disableAutoCompact,
			)

			// Verify compact configuration is properly set
			subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
			require.True(t, ok, "SubAgentConfig should be in context")
			
			assert.Equal(t, tc.compactRatio, subagentConfig.CompactRatio)
			assert.Equal(t, tc.disableAutoCompact, subagentConfig.DisableAutoCompact)
		})
	}
}