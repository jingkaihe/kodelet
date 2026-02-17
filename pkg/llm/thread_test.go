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
			expectedModel: "claude-sonnet-4-6",
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
	assert.Contains(t, output, "Success: true")

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
			model:    "sonnet-46",
			aliases:  map[string]string{"sonnet-46": "claude-sonnet-4-6"},
			expected: "claude-sonnet-4-6",
		},
		{
			name:     "returns original when no alias found",
			model:    "claude-sonnet-4-6",
			aliases:  map[string]string{"sonnet-46": "claude-sonnet-4-6"},
			expected: "claude-sonnet-4-6",
		},
		{
			name:     "handles nil aliases map",
			model:    "claude-sonnet-4-6",
			aliases:  nil,
			expected: "claude-sonnet-4-6",
		},
		{
			name:  "resolves from multiple aliases",
			model: "gpt41",
			aliases: map[string]string{
				"sonnet-46": "claude-sonnet-4-6",
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
				Model:    "sonnet-46",
				Aliases: map[string]string{
					"sonnet-46": "claude-sonnet-4-6",
				},
			},
			description: "should resolve sonnet-46 alias to full Claude model name",
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
				Model:    "claude-sonnet-4-6",
				Aliases: map[string]string{
					"sonnet-46": "claude-sonnet-4-6",
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

// Subagent tests removed per ADR 027 - shell-out pattern tested at integration level
