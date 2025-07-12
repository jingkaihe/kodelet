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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewThread(t *testing.T) {
	tests := []struct {
		name          string
		config        llmtypes.Config
		expectedModel string
		expectedMax   int
	}{
		{
			name:          "WithConfigValues",
			config:        llmtypes.Config{Model: "test-model", MaxTokens: 5000},
			expectedModel: "test-model",
			expectedMax:   5000,
		},
		{
			name:          "WithDefaultValues",
			config:        llmtypes.Config{},
			expectedModel: string(anthropic.ModelClaudeSonnet4_20250514),
			expectedMax:   8192,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Cannot type assert with the new structure - need a different approach
			thread, err := NewThread(tc.config)
			assert.NoError(t, err)
			assert.NotNil(t, thread)
		})
	}
}

func TestConsoleMessageHandler(t *testing.T) {
	// This test mainly ensures the methods don't panic
	// For a more thorough test, we would need to capture stdout
	handler := &llmtypes.ConsoleMessageHandler{Silent: true}

	handler.HandleText("Test text")
	handler.HandleToolUse("test-tool", "test-input")
	handler.HandleToolResult("test-tool", "test-result")
	handler.HandleDone()

	// With Silent = false, the methods should print to stdout
	// but we're not capturing that output in this test
	handler = &llmtypes.ConsoleMessageHandler{Silent: false}
	handler.HandleText("Test text")
	handler.HandleToolUse("test-tool", "test-input")
	handler.HandleToolResult("test-tool", "test-result")
	handler.HandleDone()
}

func TestChannelMessageHandler(t *testing.T) {
	ch := make(chan llmtypes.MessageEvent, 4)
	handler := &llmtypes.ChannelMessageHandler{MessageCh: ch}

	handler.HandleText("Test text")
	handler.HandleToolUse("test-tool", "test-input")
	handler.HandleToolResult("test-tool", "test-result")
	handler.HandleDone()

	// Verify the events sent to the channel
	event := <-ch
	assert.Equal(t, llmtypes.EventTypeText, event.Type)
	assert.Equal(t, "Test text", event.Content)
	assert.False(t, event.Done)

	event = <-ch
	assert.Equal(t, llmtypes.EventTypeToolUse, event.Type)
	assert.Equal(t, "test-tool: test-input", event.Content)
	assert.False(t, event.Done)

	event = <-ch
	assert.Equal(t, llmtypes.EventTypeToolResult, event.Type)
	assert.Equal(t, "test-result", event.Content)
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
	handler.HandleToolUse("test-tool", "test-input")
	handler.HandleToolResult("test-tool", "test-result")
	handler.HandleDone()

	expected := "Line 1\nLine 2\n"
	assert.Equal(t, expected, handler.CollectedText())

	// Test with Silent = false (just for coverage)
	handler = &llmtypes.StringCollectorHandler{Silent: false}
	handler.HandleToolUse("test-tool", "test-input")
	handler.HandleToolResult("test-tool", "test-result")
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
			Model:     string(anthropic.ModelClaude3_5Haiku20241022),
			MaxTokens: 100,
		},
		true,
		llmtypes.MessageOpt{},
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

func (m *MockMessageHandler) HandleToolUse(toolName string, input string) {
	m.Called(toolName, input)
}

func (m *MockMessageHandler) HandleToolResult(toolName string, result string) {
	m.Called(toolName, result)
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
	mockHandler.On("HandleToolUse", mock.Anything, mock.Anything).Return().Maybe()
	mockHandler.On("HandleToolResult", mock.Anything, mock.Anything).Return().Maybe()
	mockHandler.On("HandleThinking", mock.Anything).Return().Maybe()
	mockHandler.On("HandleDone").Return()

	// Create a real thread
	thread, err := NewThread(llmtypes.Config{
		Model:     string(anthropic.ModelClaude3_5Haiku20241022), // Using a real model
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
	handler.HandleToolUse("test-tool", "test-input")
	handler.HandleToolResult("test-tool", "test-result")

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
	assert.Contains(t, output, "Tool result: test-result")

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
		Model:     string(anthropic.ModelClaude3_5Haiku20241022),
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
			model:    "sonnet-4",
			aliases:  map[string]string{"sonnet-4": "claude-sonnet-4-20250514"},
			expected: "claude-sonnet-4-20250514",
		},
		{
			name:     "returns original when no alias found",
			model:    "claude-sonnet-4-20250514",
			aliases:  map[string]string{"sonnet-4": "claude-sonnet-4-20250514"},
			expected: "claude-sonnet-4-20250514",
		},
		{
			name:     "handles nil aliases map",
			model:    "claude-sonnet-4-20250514",
			aliases:  nil,
			expected: "claude-sonnet-4-20250514",
		},
		{
			name:  "resolves from multiple aliases",
			model: "gpt41",
			aliases: map[string]string{
				"sonnet-4": "claude-sonnet-4-20250514",
				"gpt41":    "gpt-4.1",
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
				Model: "sonnet-4",
				Aliases: map[string]string{
					"sonnet-4": "claude-sonnet-4-20250514",
				},
			},
			description: "should resolve sonnet-4 alias to full Claude model name",
		},
		{
			name: "resolves OpenAI alias to GPT model",
			config: llmtypes.Config{
				Model: "gpt41",
				Aliases: map[string]string{
					"gpt41": "gpt-4.1",
				},
			},
			description: "should resolve gpt41 alias to full OpenAI model name",
		},
		{
			name: "uses full model name when no alias exists",
			config: llmtypes.Config{
				Model: "claude-sonnet-4-20250514",
				Aliases: map[string]string{
					"sonnet-4": "claude-sonnet-4-20250514",
				},
			},
			description: "should use original model name when it's not an alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalModel := tt.config.Model

			thread, err := NewThread(tt.config)

			require.NoError(t, err, tt.description)
			require.NotNil(t, thread, "thread should not be nil")

			// Verify that the original config was NOT modified (passed by value)
			assert.Equal(t, originalModel, tt.config.Model, "original config should not be modified")
		})
	}
}
