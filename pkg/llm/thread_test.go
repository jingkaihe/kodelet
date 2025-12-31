package llm

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// skipIfNoOpenAIAPIKey skips the test if OPENAI_API_KEY is not set
func skipIfNoOpenAIAPIKey(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY environment variable not set")
	}
}

func TestNewThread(t *testing.T) {
	tests := []struct {
		name          string
		config        llmtypes.Config
		expectedModel string
		expectedMax   int
	}{
		{
			name: "WithConfigValues",
			config: llmtypes.Config{
				Provider:  "openai",
				Model:     "test-model",
				MaxTokens: 5000,
			},
			expectedModel: "test-model",
			expectedMax:   5000,
		},
		{
			name: "WithDefaultValues",
			config: llmtypes.Config{
				Provider: "anthropic",
			},
			expectedModel: string(anthropic.ModelClaudeSonnet4_5_20250929),
			expectedMax:   8192,
		},
		{
			name: "GoogleProvider",
			config: llmtypes.Config{
				Provider: "google",
			},
			expectedModel: "gemini-2.5-pro",
			expectedMax:   8192,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Skip OpenAI tests if API key is not set
			if tc.config.Provider == "openai" {
				skipIfNoOpenAIAPIKey(t)
			}

			// Cannot type assert with the new structure - need a different approach
			thread, err := NewThread(tc.config)
			assert.NoError(t, err)
			assert.NotNil(t, thread)
		})
	}
}

func TestConsoleMessageHandler(_ *testing.T) {
	// This test mainly ensures the methods don't panic
	// For a more thorough test, we would need to capture stdout
	handler := &llmtypes.ConsoleMessageHandler{Silent: true}

	handler.HandleText("Test text")
	handler.HandleToolUse("call-1", "test-tool", "test-input")
	handler.HandleToolResult("call-1", "test-tool", tooltypes.BaseToolResult{Result: "test-result"})
	handler.HandleDone()

	// With Silent = false, the methods should print to stdout
	// but we're not capturing that output in this test
	handler = &llmtypes.ConsoleMessageHandler{Silent: false}
	handler.HandleText("Test text")
	handler.HandleToolUse("call-1", "test-tool", "test-input")
	handler.HandleToolResult("call-1", "test-tool", tooltypes.BaseToolResult{Result: "test-result"})
	handler.HandleDone()
}

func TestChannelMessageHandler(t *testing.T) {
	ch := make(chan llmtypes.MessageEvent, 4)
	handler := &llmtypes.ChannelMessageHandler{MessageCh: ch}

	handler.HandleText("Test text")
	handler.HandleToolUse("call-1", "test-tool", "test-input")
	handler.HandleToolResult("call-1", "test-tool", tooltypes.BaseToolResult{Result: "test-result"})
	handler.HandleDone()

	// Verify the events sent to the channel
	event := <-ch
	assert.Equal(t, llmtypes.EventTypeText, event.Type)
	assert.Equal(t, "Test text", event.Content)
	assert.False(t, event.Done)

	event = <-ch
	assert.Equal(t, llmtypes.EventTypeToolUse, event.Type)
	assert.Equal(t, "test-tool\n  test-input", event.Content)
	assert.False(t, event.Done)

	event = <-ch
	assert.Equal(t, llmtypes.EventTypeToolResult, event.Type)
	assert.Contains(t, event.Content, "test-result")
	assert.False(t, event.Done)

	event = <-ch
	assert.Equal(t, llmtypes.EventTypeText, event.Type)
	assert.Equal(t, "Done", event.Content)
	assert.True(t, event.Done)
}

func TestStringCollectorHandler(t *testing.T) {
	handler := &llmtypes.StringCollectorHandler{Silent: true}

	handler.HandleText("Line 1")
	handler.HandleText("Line 2")
	handler.HandleToolUse("call-1", "test-tool", "test-input")
	handler.HandleToolResult("call-1", "test-tool", tooltypes.BaseToolResult{Result: "test-result"})
	handler.HandleDone()

	expected := "Line 1\nLine 2\n"
	assert.Equal(t, expected, handler.CollectedText())

	// Test with Silent = false (just for coverage)
	handler = &llmtypes.StringCollectorHandler{Silent: false}
	handler.HandleToolUse("call-1", "test-tool", "test-input")
	handler.HandleToolResult("call-1", "test-tool", tooltypes.BaseToolResult{Result: "test-result"})
}

// Integration test for SendMessageAndGetText with real Anthropic client
func TestSendMessageAndGetText(t *testing.T) {
	// Skip if no API key is available
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY environment variable not set")
	}

	ctx := context.Background()

	// We won't set expectations since these may not be called in a simple text response
	// The state methods would only be called if tools are used

	// Use a simple query that should return a predictable response
	query := "Respond with exactly these words: 'Hello from test'"

	// Test with real client
	result := SendMessageAndGetText(ctx,
		tools.NewBasicState(ctx),
		query,
		llmtypes.Config{
			Provider:  "anthropic",
			Model:     string(anthropic.ModelClaudeHaiku4_5_20251001),
			MaxTokens: 100,
		},
		true,
		llmtypes.MessageOpt{NoSaveConversation: true},
	)

	// Verify we got a non-error response
	assert.False(t, strings.HasPrefix(result, "Error:"), "Response should not contain an error")
	// Verify the result contains expected response
	assert.Contains(t, result, "Hello from test", "Response should contain the requested text")
}

// MockMessageHandler is a mock implementation of MessageHandler
type MockMessageHandler struct {
	mock.Mock
}

func (m *MockMessageHandler) HandleText(text string) {
	m.Called(text)
}

func (m *MockMessageHandler) HandleToolUse(toolCallID string, toolName string, input string) {
	m.Called(toolCallID, toolName, input)
}

func (m *MockMessageHandler) HandleToolResult(toolCallID string, toolName string, result tooltypes.ToolResult) {
	m.Called(toolCallID, toolName, result)
}

func (m *MockMessageHandler) HandleThinking(thinking string) {
	m.Called(thinking)
}

func (m *MockMessageHandler) HandleDone() {
	m.Called()
}

// TestSendMessageRealClient tests the SendMessage method with the real Anthropic client
func TestSendMessageRealClient(t *testing.T) {
	// Skip if no API key is available
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY environment variable not set")
	}

	ctx := context.Background()
	mockHandler := new(MockMessageHandler)

	// Set up expectations for the handler - allow optional tool use
	mockHandler.On("HandleText", mock.Anything).Return()
	mockHandler.On("HandleToolUse", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockHandler.On("HandleToolResult", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	mockHandler.On("HandleThinking", mock.Anything).Return().Maybe()
	mockHandler.On("HandleDone").Return()

	// Create a real thread
	thread, err := NewThread(llmtypes.Config{
		Provider:  "anthropic",
		Model:     string(anthropic.ModelClaudeHaiku4_5_20251001), // Using a real model
		MaxTokens: 100,
	})
	assert.NoError(t, err)
	thread.SetState(tools.NewBasicState(context.TODO()))

	// Send a simple message that should not trigger tool use
	_, err = thread.SendMessage(ctx, "Say hello world", mockHandler, llmtypes.MessageOpt{})

	// Verify
	assert.NoError(t, err)
	mockHandler.AssertExpectations(t)
}

// TestStringCollectorHandlerCapture tests that StringCollectorHandler captures stdout correctly
func TestStringCollectorHandlerCapture(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Create handler
	handler := &llmtypes.StringCollectorHandler{Silent: false}

	// Run methods
	handler.HandleText("Test text")
	handler.HandleToolUse("call-1", "test-tool", "test-input")
	handler.HandleToolResult("call-1", "test-tool", tooltypes.BaseToolResult{Result: "test-result"})

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read captured output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output contains expected text
	assert.Contains(t, output, "Test text")
	assert.Contains(t, output, "Using tool: test-tool")
	assert.Contains(t, output, "test-result")

	// Verify collected text
	assert.Equal(t, "Test text\n", handler.CollectedText())
}

// TestSendMessageWithToolUse tests the tool-using capability of the Thread with real API
func TestSendMessageWithToolUse(t *testing.T) {
	// Skip if no API key is available
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY environment variable not set")
	}

	// Add timeout for API calls
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set up handler that will collect the response
	handler := &llmtypes.StringCollectorHandler{Silent: true}

	// Create thread
	thread, err := NewThread(llmtypes.Config{
		Provider:  "anthropic",
		Model:     string(anthropic.ModelClaudeHaiku4_5_20251001),
		MaxTokens: 1000,
	})
	assert.NoError(t, err)
	thread.SetState(tools.NewBasicState(context.TODO()))

	// Send message that should trigger thinking tool use
	_, err = thread.SendMessage(ctx, "Use the thinking tool to calculate 25 * 32", handler, llmtypes.MessageOpt{})

	// Verify response
	assert.NoError(t, err)
	responseText := handler.CollectedText()

	// The response should contain the calculation result (800)
	assert.Contains(t, responseText, "800", "Response should include the calculation result")
}

func TestResolveModelAlias(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		aliases  map[string]string
		expected string
	}{
		{
			name:     "resolves existing alias",
			model:    "sonnet-45",
			aliases:  map[string]string{"sonnet-45": "claude-sonnet-4-5-20250929"},
			expected: "claude-sonnet-4-5-20250929",
		},
		{
			name:     "returns original when no alias found",
			model:    "claude-sonnet-4-5-20250929",
			aliases:  map[string]string{"sonnet-45": "claude-sonnet-4-5-20250929"},
			expected: "claude-sonnet-4-5-20250929",
		},
		{
			name:     "handles nil aliases map",
			model:    "claude-sonnet-4-5-20250929",
			aliases:  nil,
			expected: "claude-sonnet-4-5-20250929",
		},
		{
			name:  "resolves from multiple aliases",
			model: "gpt41",
			aliases: map[string]string{
				"sonnet-45": "claude-sonnet-4-5-20250929",
				"gpt41":     "gpt-4.1",
			},
			expected: "gpt-4.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveModelAlias(tt.model, tt.aliases)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewThreadWithAliases(t *testing.T) {
	tests := []struct {
		name        string
		config      llmtypes.Config
		description string
	}{
		{
			name: "resolves Anthropic alias to Claude model",
			config: llmtypes.Config{
				Provider: "anthropic",
				Model:    "sonnet-45",
				Aliases: map[string]string{
					"sonnet-45": "claude-sonnet-4-5-20250929",
				},
			},
			description: "should resolve sonnet-45 alias to full Claude model name",
		},
		{
			name: "resolves OpenAI alias to GPT model",
			config: llmtypes.Config{
				Provider: "openai",
				Model:    "gpt41",
				Aliases: map[string]string{
					"gpt41": "gpt-4.1",
				},
			},
			description: "should resolve gpt41 alias to full OpenAI model name",
		},
		{
			name: "uses full model name when no alias exists",
			config: llmtypes.Config{
				Provider: "anthropic",
				Model:    "claude-sonnet-4-5-20250929",
				Aliases: map[string]string{
					"sonnet-45": "claude-sonnet-4-5-20250929",
				},
			},
			description: "should use original model name when it's not an alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip OpenAI tests if API key is not set
			if tt.config.Provider == "openai" {
				skipIfNoOpenAIAPIKey(t)
			}

			originalModel := tt.config.Model

			thread, err := NewThread(tt.config)

			require.NoError(t, err, tt.description)
			require.NotNil(t, thread, "thread should not be nil")

			// Verify that the original config was NOT modified (passed by value)
			assert.Equal(t, originalModel, tt.config.Model, "original config should not be modified")
		})
	}
}

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
			Model:     "claude-haiku-4-5-20251001", // Use faster model for tests
			MaxTokens: 1024,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Provider:  "openai",
				Model:     "gpt-4o-mini",
				MaxTokens: 512,
			},
		}

		// Create main thread
		mainThread, err := NewThread(mainConfig)
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
		subagentCtx := NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

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
				Model:          "claude-haiku-4-5-20251001",
				MaxTokens:      512,
				ThinkingBudget: 256,
			},
		}

		// Create main thread
		mainThread, err := NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Test that subagent configuration is properly set
		config := mainThread.GetConfig()
		assert.NotNil(t, config.SubAgent)
		assert.Equal(t, "anthropic", config.SubAgent.Provider)
		assert.Equal(t, "claude-haiku-4-5-20251001", config.SubAgent.Model)

		// Create a subagent context
		subagentCtx := NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")
		assert.NotNil(t, subagentConfig.Thread)

		// Verify the subagent is using Anthropic
		assert.Equal(t, "anthropic", subagentConfig.Thread.Provider())

		// Verify the subagent has the correct configuration
		subConfig := subagentConfig.Thread.GetConfig()
		assert.Equal(t, "anthropic", subConfig.Provider)
		assert.Equal(t, "claude-haiku-4-5-20251001", subConfig.Model)
		assert.Equal(t, 512, subConfig.MaxTokens)
		assert.Equal(t, 256, subConfig.ThinkingBudgetTokens)
		assert.True(t, subConfig.IsSubAgent)
	})

	// Test same provider with different models
	t.Run("Same provider different models", func(t *testing.T) {
		// Create Claude main agent with Claude subagent (different model)
		mainConfig := llmtypes.Config{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-5-20250929",
			MaxTokens: 2048,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Model:     "claude-haiku-4-5-20251001", // Faster model for subagent
				MaxTokens: 1024,
			},
		}

		// Create main thread
		mainThread, err := NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Create a subagent context
		subagentCtx := NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

		// Verify subagent context was created
		subagentConfig, ok := subagentCtx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
		require.True(t, ok, "SubAgentConfig should be in context")

		// Verify the subagent is using the same provider but different model
		assert.Equal(t, "anthropic", subagentConfig.Thread.Provider())

		// Verify the subagent has the correct configuration
		subConfig := subagentConfig.Thread.GetConfig()
		assert.Equal(t, "anthropic", subConfig.Provider)
		assert.Equal(t, "claude-haiku-4-5-20251001", subConfig.Model)
		assert.Equal(t, 1024, subConfig.MaxTokens)
		assert.True(t, subConfig.IsSubAgent)
	})

	// Test fallback behavior when cross-provider fails
	t.Run("Fallback to same provider on error", func(t *testing.T) {
		// Create configuration with invalid provider
		mainConfig := llmtypes.Config{
			Provider:  "anthropic",
			Model:     "claude-haiku-4-5-20251001",
			MaxTokens: 1024,
			SubAgent: &llmtypes.SubAgentConfigSettings{
				Provider: "invalid-provider",
				Model:    "some-model",
			},
		}

		// Create main thread
		mainThread, err := NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Create a subagent context - should fall back to same provider
		subagentCtx := NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

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
			Model:     "claude-haiku-4-5-20251001",
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
		mainThread, err := NewThread(mainConfig)
		require.NoError(t, err)

		// Set up state
		ctx := context.Background()
		state := tools.NewBasicState(ctx)
		mainThread.SetState(state)

		// Create a subagent context
		subagentCtx := NewSubagentContext(ctx, mainThread, &llmtypes.StringCollectorHandler{Silent: true}, 0.8, false)

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
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 1024,
	}

	// Create main thread
	mainThread, err := NewThread(mainConfig)
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
			subagentCtx := NewSubagentContext(
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
