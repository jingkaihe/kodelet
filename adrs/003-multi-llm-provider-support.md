# ADR 0001: Multi-LLM Provider Support

## Status

Proposed

## Context

Kodelet currently relies exclusively on Anthropic's Claude API for its LLM capabilities. This dependency on a single LLM provider limits flexibility and introduces potential risks:

1. Vendor lock-in with Anthropic
2. No fallback options if Claude API experiences downtime
3. Inability to leverage strengths of other LLM providers
4. Different pricing structures across providers limit cost optimization

The current implementation directly integrates with Anthropic's SDK throughout the codebase:
- Client initialization happens in multiple places (`cmd/kodelet/main.go` and `pkg/tui/assistant.go`)
- Anthropic-specific types like `anthropic.MessageParam` and `anthropic.ToolUseBlock` are used in core logic
- Model constants like `anthropic.ModelClaude3_7SonnetLatest` are hardcoded in configuration
- Tool definitions are converted specifically to Anthropic's format via `ToAnthropicTools()`

## Decision

We will implement a provider-agnostic LLM interface layer in a new `pkg/llm` package that abstracts away provider-specific implementations. This will allow Kodelet to support multiple LLM providers, starting with OpenAI alongside the existing Anthropic integration.

Key components:

1. **Common Interface**: Create a unified interface for LLM interactions in `pkg/llm/interface.go`
2. **Provider Implementations**: Implement provider-specific adapters in separate files:
   - `pkg/llm/anthropic.go` for Claude
   - `pkg/llm/openai.go` for OpenAI

3. **Unified Message Types**: Define provider-agnostic message and response types to abstract away differences between APIs

4. **Factory Pattern**: Implement a factory function to create appropriate provider implementation based on configuration

## Architecture Details

### Interface and Common Types

```go
// pkg/llm/interface.go
package llm

import "context"

// Message represents a generic message in a conversation
type Message struct {
    Role    string // "user", "assistant", or "system"
    Content string // Text content of the message
}

// MessageResponse represents a response from the LLM
type MessageResponse struct {
    Content     string
    ToolCalls   []ToolCall
    StopReason  string
}

// ToolCall represents a tool call request from the LLM
type ToolCall struct {
    ID          string
    Name        string
    Parameters  map[string]interface{}
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
    CallID  string
    Content string
    Error   bool
}

// Provider defines the interface for LLM provider implementations
type Provider interface {
    // SendMessage sends a message to the LLM and returns the response
    SendMessage(ctx context.Context, messages []Message, systemPrompt string) (MessageResponse, error)

    // AddToolResults adds tool execution results to the conversation
    AddToolResults(toolResults []ToolResult) Message

    // GetAvailableModels returns the list of available models for this provider
    GetAvailableModels() []string

    // ConvertTools converts the standard tools to provider-specific format
    ConvertTools(tools []interface{}) interface{}
}

// Factory function to create a provider based on configuration
func NewProvider(providerName string, options map[string]interface{}) (Provider, error) {
    switch providerName {
    case "anthropic":
        return NewAnthropicProvider(options)
    case "openai":
        return NewOpenAIProvider(options)
    default:
        return nil, fmt.Errorf("unsupported provider: %s", providerName)
    }
}
```

### Anthropic Implementation

```go
// pkg/llm/anthropic.go
package llm

import (
    "context"
    "github.com/anthropics/anthropic-sdk-go"
)

// AnthropicProvider implements the Provider interface for Anthropic Claude
type AnthropicProvider struct {
    client anthropic.Client
    model  string
}

// NewAnthropicProvider creates a new Anthropic provider
func NewAnthropicProvider(options map[string]interface{}) (Provider, error) {
    client := anthropic.NewClient()
    model := options["model"].(string)
    if model == "" {
        model = anthropic.ModelClaude3_7SonnetLatest
    }

    return &AnthropicProvider{
        client: client,
        model:  model,
    }, nil
}

// Implementation of Provider interface methods...
```

### OpenAI Implementation

```go
// pkg/llm/openai.go
package llm

import (
    "context"
    "github.com/sashabaranov/go-openai"
)

// OpenAIProvider implements the Provider interface for OpenAI
type OpenAIProvider struct {
    client openai.Client
    model  string
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(options map[string]interface{}) (Provider, error) {
    apiKey := options["apiKey"].(string)
    model := options["model"].(string)
    if model == "" {
        model = openai.GPT4o
    }

    client := openai.NewClient(apiKey)
    return &OpenAIProvider{
        client: client,
        model:  model,
    }, nil
}

// Implementation of Provider interface methods...
```

## Integration Changes

1. Update `cmd/kodelet/main.go` to use the new LLM interface
2. Modify `pkg/tui/assistant.go` to work with the provider-agnostic interface
3. Update configuration to support provider selection and provider-specific options
4. Create mappings for provider-specific tool implementations

## Configuration Updates

The configuration will be updated to support provider selection:

```yaml
# LLM provider ("anthropic" or "openai")
provider: "anthropic"

# Provider-specific configurations
providers:
  anthropic:
    model: "claude-3-7-sonnet-latest"
    max_tokens: 8192
  openai:
    model: "o4-mini"
    reasoning_effort: medium # or low, medium, high - pending on whether the model is a reasoning model
```

## Consequences

### Positive

1. **Flexibility**: Users can choose which LLM provider to use based on their needs
2. **Resilience**: Can fallback to alternate providers during outages
3. **Cost Optimization**: Choose provider based on budget constraints
4. **Feature Exploration**: Ability to leverage provider-specific capabilities
5. **Future-Proofing**: Easier to integrate new LLM providers as they emerge

### Negative

1. **Implementation Complexity**: Additional abstraction layers and adapters increase code complexity
2. **Feature Parity Challenges**: Different providers have varying capabilities (e.g., tool calling interfaces)
3. **Testing Overhead**: Need to test each provider implementation
4. **Configuration Complexity**: More configuration options required

### Neutral

1. **Migration Path**: Existing users will continue to use Anthropic by default
2. **Provider Selection**: Provider selection could be automatic (based on API key availability) or explicit

## Alternatives Considered

1. **Fork-based Approach**: Create separate builds for each provider
   - Rejected due to code duplication and maintenance overhead

2. **Plugin System**: Implement a more complex plugin system for providers
   - Rejected as overly complex for current needs

3. **Wrapper Libraries**: Use existing multi-provider libraries
   - Rejected due to lack of mature libraries that support tool calling across providers

## Implementation Plan

1. Create the new `pkg/llm` package with interface definitions
2. Implement the Anthropic provider adapter
3. Update the codebase to use the new interface (with Anthropic still as the only provider)
4. Implement the OpenAI provider adapter
5. Update configuration handling to support provider selection
6. Add documentation and tests for the new functionality

## References

- [Anthropic SDK Documentation](https://github.com/anthropics/anthropic-sdk-go)
- [OpenAI Go Client](https://github.com/sashabaranov/go-openai)
- [Tool Calling Specifications](https://platform.openai.com/docs/guides/function-calling)
