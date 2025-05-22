# ADR 0001: Adding OpenAI as a LLM Provider

## Status
Proposed

## Context
Kodelet currently supports only Anthropic's Claude as its LLM provider. To increase flexibility and give users more options, we need to add support for OpenAI's models. This will allow users to choose the most suitable model for their specific use cases, considering factors like cost, performance, and capabilities.

## Decision
We will implement a new OpenAI provider that follows the same architecture as the existing Anthropic provider. This will include:

1. Creating a new package `pkg/llm/openai` similar to the existing `pkg/llm/anthropic`
2. Implementing the `Thread` interface for OpenAI
3. Adding pricing and token tracking for OpenAI models
4. Supporting tool calls and conversation persistence
5. Introducing a new configuration parameter `reasoning_effort` for OpenAI to replace `max_tokens`
6. Setting GPT-4.1 as the default OpenAI model and GPT-4.1-mini as the default weak model
7. Maintaining feature parity between providers where applicable

## Architecture Details

### Directory Structure
```
pkg/
  └── llm/
      ├── anthropic/     # Existing implementation
      └── openai/        # New implementation
          ├── openai.go  # Main OpenAI thread implementation
          └── persistence.go  # Conversation persistence for OpenAI
```

### Core Components

#### OpenAIThread
The `OpenAIThread` struct will implement the `llm.Thread` interface and handle interactions with OpenAI's API:

```go
// OpenAIThread implements the Thread interface using OpenAI's API
type OpenAIThread struct {
    client         *openai.Client
    config         llmtypes.Config
    reasoningEffort string     // low, medium, high to determine token allocation
    state          tooltypes.State
    messages       []openai.ChatCompletionMessage
    usage          *llmtypes.Usage
    conversationID string
    summary        string
    isPersisted    bool
    store          ConversationStore
    mu             sync.Mutex
    conversationMu sync.Mutex
}
```

#### Model Pricing
Similar to the Anthropic implementation, we'll maintain a pricing structure for OpenAI models:

```go
// ModelPricing holds the per-token pricing for different operations
type ModelPricing struct {
    Input         float64
    Output        float64
    ContextWindow int
}

// ModelPricingMap maps model names to their pricing information
var ModelPricingMap = map[string]ModelPricing{
    "gpt-4.1": {
        Input:         0.00001,  // $0.01 per 1K tokens
        Output:        0.00003,  // $0.03 per 1K tokens
        ContextWindow: 128_000,
    },
    "gpt-4.1-mini": {
        Input:         0.000003, // $0.003 per 1K tokens
        Output:        0.000009, // $0.009 per 1K tokens
        ContextWindow: 128_000,
    },
    "gpt-4o": {
        Input:         0.000005, // $0.005 per 1K tokens
        Output:        0.000015, // $0.015 per 1K tokens
        ContextWindow: 128_000,
    },
    "gpt-3.5-turbo": {
        Input:         0.0000005, // $0.0005 per 1K tokens
        Output:        0.0000015, // $0.0015 per 1K tokens
        ContextWindow: 16_000,
    },
    // Additional models...
}
```

#### Tool Conversion
We'll need to convert Kodelet's internal tool representations to OpenAI's format:

```go
func ToOpenAITools(tools []tooltypes.Tool) []openai.Tool {
    // Convert internal tool format to OpenAI's format
}
```

### Key Implementation Considerations

1. **API Differences**:
   - Handle differences in API structures between Anthropic and OpenAI
   - Map Kodelet's unified interface to OpenAI-specific concepts

2. **Token Usage Tracking**:
   - Implement OpenAI-specific token counting and cost tracking
   - Handle differences in how OpenAI reports token usage

3. **Tool Execution**:
   - Adapt the tool execution flow to work with OpenAI's different tool calling format
   - Handle multiple tool calls in a single response (OpenAI can return multiple tool calls at once)

4. **Conversation Persistence**:
   - Ensure the conversation format can be serialized/deserialized properly
   - Maintain compatibility with the existing conversation store

5. **System Prompts**:
   - Create OpenAI-specific system prompts that work well with their models
   - Support the different prompt format requirements

6. **Reasoning Effort Configuration**:
   - Implement the new `reasoning_effort` parameter to replace `max_tokens`
   - Map reasoning effort levels (low, medium, high) to appropriate token allocation strategies
   - Example mapping:
     - low: Optimize for minimal token usage
     - medium: Balance between token usage and thoroughness
     - high: Allow for more extensive reasoning and detailed responses

## Configuration
Users will be able to configure OpenAI through similar mechanisms as Anthropic:

```yaml
# Environment variables
OPENAI_API_KEY=sk-...
KODELET_PROVIDER=openai  # To select OpenAI as the provider
KODELET_MODEL=gpt-4.1
KODELET_REASONING_EFFORT=medium  # low, medium, high

# Config file
provider: openai
model: gpt-4.1
reasoning_effort: medium  # low, medium, high
weak_model: gpt-4.1-mini
```

## Compatibility
We will maintain the same interface and features across providers where possible:
- Tool execution
- Conversation persistence
- Token usage tracking
- Thinking capability (implemented differently based on provider capabilities)

## Alternatives Considered

1. **Using a third-party LLM gateway**:
   - Rejected because it would add unnecessary complexity and dependencies
   - Direct integration gives us more control over implementation details

2. **Complete rewrite of the LLM architecture**:
   - Rejected in favor of maintaining the current architecture and extending it
   - The current design already separates provider-specific code effectively

## Consequences

### Positive
- Users have more options for LLM providers
- Different cost structures and capabilities give more flexibility
- Reduced dependency on a single provider

### Negative
- Increased maintenance burden with multiple provider implementations
- Need to ensure feature parity across providers
- May need to handle provider-specific limitations differently

## Implementation Plan

1. Create the basic `pkg/llm/openai` package structure
2. Update the configuration types to support the `reasoning_effort` parameter
3. Implement the core `OpenAIThread` type with the required interfaces
4. Add support for token tracking - use placeholder values for now
5. Implement tool conversion and execution for OpenAI's tool calling format
6. Add conversation persistence compatible with the existing store
7. Update configuration handling to support provider selection and reasoning effort
8. Add tests for the OpenAI implementation
9. Update documentation with OpenAI-specific guidance
10. Create examples showing how to configure and use the OpenAI provider
