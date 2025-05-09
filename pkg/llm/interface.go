// Package llm provides a provider-agnostic interface for LLM interactions
package llm

import (
	"context"
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/tools"
)

// Message represents a generic message in a conversation
type Message struct {
	Role       string // "user", "assistant", "system" or "tool"
	Content    string // Text content of the message
	ToolCallID string // Optional ID of the tool call this message is responding to
}

// MessageResponse represents a response from the LLM
type MessageResponse struct {
	Content    string
	ToolCalls  []ToolCall
	StopReason string
}

// ToolCall represents a tool call request from the LLM
type ToolCall struct {
	ID         string
	Name       string
	Parameters map[string]interface{}
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	CallID  string
	Content string
	Error   bool
}

// ProviderOptions contains configuration options for LLM providers
type ProviderOptions struct {
	Model      string
	MaxTokens  int
	APIKey     string
	Parameters map[string]interface{} // Additional provider-specific parameters
}

// Provider defines the interface for LLM provider implementations
type Provider interface {
	// SendMessage sends a message to the LLM and returns the response
	SendMessage(ctx context.Context, messages []Message, systemPrompt string, tools []tools.Tool) (MessageResponse, error)

	// AddToolResults adds tool execution results to the conversation
	AddToolResults(toolResults []ToolResult) Message

	// GetAvailableModels returns the list of available models for this provider
	GetAvailableModels() []string

	// ConvertTools converts the standard tools to provider-specific format
	ConvertTools(tools []tools.Tool) interface{}
}

// Factory function to create a provider based on configuration
func NewProvider(providerName string, options ProviderOptions) (Provider, error) {
	switch providerName {
	case "anthropic":
		return NewAnthropicProvider(options)
	case "openai":
		return NewOpenAIProvider(options)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}
