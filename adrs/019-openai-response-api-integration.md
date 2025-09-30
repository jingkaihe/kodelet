# ADR 019: OpenAI Response API Integration

## Status
Proposed

## Context

OpenAI has introduced a new Response API (`POST /responses`) that provides a more powerful and flexible alternative to the Chat Completion API (`POST /chat/completions`). This new API is not just an incremental improvement but a significant architectural change that offers:

- **Stateful conversation management** with automatic context handling via `PreviousResponseID` or `Conversation` parameters
- **Enhanced multimodal support** for text, images, audio, and files
- **Function calling** with strongly-typed arguments and structured outputs
- **Background processing** for long-running tasks
- **Fine-grained streaming** with 30+ event types for detailed response generation tracking
- **Structured output items** instead of simple message objects

More importantly, certain new models like **gpt-5-codex** only support the Response API and cannot be used with the Chat Completion API. This creates a pressing need to integrate Response API support to remain compatible with OpenAI's latest and future model releases.

### Current Architecture

Kodelet currently uses the Chat Completion API through the `github.com/sashabaranov/go-openai` library (v1.41.2). The architecture consists of:

1. **Thread Interface**: Unified abstraction for LLM interactions across providers (Anthropic, OpenAI, Google)
2. **OpenAIThread**: Implementation that uses `client.CreateChatCompletion()`
3. **Message Management**: Manual history tracking with `[]openai.ChatCompletionMessage`
4. **Tool Execution**: Synchronous tool calling with result injection into message history
5. **Conversation Persistence**: Custom implementation using SQLite storage
6. **Web UI Visualization**: React-based conversation viewer that renders message history

### Key Challenges

1. **Library Support**: The current `sashabaranov/go-openai` library does not support the Response API. The official `github.com/openai/openai-go/v2` SDK does support it but would require adding a new dependency.

2. **API Differences**: The Response API has fundamentally different request/response structures:
   - Different input format (flexible input items vs. messages array)
   - Different tool calling approach (structured function calls with typed arguments)
   - Different streaming mechanism (fine-grained events vs. delta-based)
   - Stateful conversation management built-in

3. **Backward Compatibility**: We must maintain support for existing Chat Completion API usage while adding Response API capabilities.

4. **Configuration Complexity**: Need to allow users to choose between APIs without breaking existing configurations.

5. **Conversation Visualization**: The Response API uses a different conversation structure (input items and output items) that needs to be properly persisted and visualized in the web UI. We must store the full conversation history to enable proper rendering of multi-turn interactions.

## Decision

We will integrate OpenAI's Response API into Kodelet with the following approach:

### 1. Dual-Mode Architecture

Implement a configuration-driven approach where users can select which API to use:

```yaml
provider: openai
model: gpt-5-codex

openai:
  responses_api: true  # Use Response API instead of Chat Completion API
  # When false or omitted, use Chat Completion API (default)
```

Environment variable override:
```bash
KODELET_OPENAI_RESPONSES_API=true
```

### 2. Library Strategy

To avoid scope creep and maintain the existing `sashabaranov/go-openai` library, we will:

1. **Keep the existing library** for Chat Completion API support
2. **Add the official OpenAI SDK v2** (`github.com/openai/openai-go/v2`) as an additional dependency specifically for Response API support
3. **Conditionally initialize** the appropriate client based on the `responses_api` configuration

**Rationale**: 
- The official SDK provides comprehensive Response API support with proper type safety
- Implementing the Response API manually would require significant effort and ongoing maintenance
- Having both libraries is acceptable as they serve different purposes and won't conflict
- The official SDK will be imported only when needed, minimizing overhead

### 3. Implementation Architecture

#### Configuration Extension

```go
// pkg/types/llm/config.go

type OpenAIConfig struct {
    Preset        string                  `mapstructure:"preset"`
    BaseURL       string                  `mapstructure:"base_url"`
    APIKeyEnvVar  string                  `mapstructure:"api_key_env_var"`
    Models        *CustomModels           `mapstructure:"models"`
    Pricing       map[string]ModelPricing `mapstructure:"pricing"`
    
    // NEW: Response API configuration
    ResponsesAPI  bool                    `mapstructure:"responses_api"`  // Use Response API instead of Chat Completion
    StoreResponses bool                   `mapstructure:"store_responses"` // Store responses for retrieval (Response API only)
    ConversationID string                 `mapstructure:"conversation_id"` // Persistent conversation ID (Response API only)
}
```

#### Enhanced OpenAIThread

```go
// pkg/llm/openai/openai.go

import (
    openai "github.com/sashabaranov/go-openai"
    openai_v2 "github.com/openai/openai-go/v2"
    "github.com/openai/openai-go/v2/responses"
)

type OpenAIThread struct {
    // Existing fields
    client         *openai.Client
    config         llmtypes.Config
    state          tooltypes.State
    messages       []openai.ChatCompletionMessage
    usage          *llmtypes.Usage
    // ... other fields
    
    // NEW: Response API support
    useResponsesAPI    bool                                       // Whether to use Response API
    responsesClient    *openai_v2.Client                          // Official SDK client for Response API
    previousResponseID string                                     // For Response API conversation continuity
    conversationItems  []ConversationItem                         // Unified conversation history (input + output items)
    
    mu sync.Mutex
}

// ConversationItem represents a single item in the Response API conversation
// This can be either an input item (user message, tool result) or output item (assistant message, tool call, reasoning)
type ConversationItem struct {
    Type       string      `json:"type"`        // "input" or "output"
    Item       interface{} `json:"item"`        // The actual item (ResponseInputItemUnionParam or ResponseOutputItemUnion)
    Timestamp  time.Time   `json:"timestamp"`   // When this item was added
}
```

#### Dual-Path Message Processing

```go
// SendMessage will route to the appropriate implementation
func (t *OpenAIThread) SendMessage(
    ctx context.Context,
    message string,
    handler llmtypes.MessageHandler,
    opt llmtypes.MessageOpt,
) (string, error) {
    if t.useResponsesAPI {
        return t.sendMessageResponseAPI(ctx, message, handler, opt)
    }
    return t.sendMessageChatCompletion(ctx, message, handler, opt)
}
```

### 4. Response API Implementation Details

#### Request Construction

The Response API uses a more flexible input structure. We build input items from user messages and tool results, then append them to the conversation history:

```go
func (t *OpenAIThread) buildResponseRequest(
    ctx context.Context,
    message string,
    opt llmtypes.MessageOpt,
) responses.ResponseNewParams {
    // Build current request's input items (text, images, tool outputs)
    currentInputItems := t.buildInputItems(message, opt.Images)
    
    // Append input items to conversation history for persistence
    for _, item := range currentInputItems {
        t.conversationItems = append(t.conversationItems, ConversationItem{
            Type:      "input",
            Item:      item,
            Timestamp: time.Now(),
        })
    }
    
    params := responses.ResponseNewParams{
        Input: responses.ResponseNewParamsInputUnion{
            OfInputItemList: currentInputItems,
        },
        Model: openai_v2.ChatModel(t.config.Model),
        MaxOutputTokens: openai_v2.Int(t.config.MaxTokens),
    }
    
    // Add system instructions
    if systemPrompt := t.getSystemPrompt(ctx); systemPrompt != "" {
        params.Instructions = openai_v2.String(systemPrompt)
    }
    
    // Add conversation continuity
    if t.previousResponseID != "" {
        params.PreviousResponseID = openai_v2.String(t.previousResponseID)
        params.Store = openai_v2.Bool(true) // Required for chaining
    }
    
    // Configure reasoning for reasoning models
    if t.isReasoningModelDynamic(t.config.Model) && t.reasoningEffort != "none" {
        params.Reasoning = shared.ReasoningParam{
            Effort: convertReasoningEffort(t.reasoningEffort),
        }
    }
    
    // Add tools if enabled
    if !opt.NoToolUse {
        params.Tools = t.buildResponseAPITools()
    }
    
    return params
}
```

#### Tool Execution

The Response API has different tool calling semantics. We process output items and append them to the conversation history:

```go
func (t *OpenAIThread) processResponseOutput(
    ctx context.Context,
    resp *responses.Response,
    handler llmtypes.MessageHandler,
    opt llmtypes.MessageOpt,
) (string, bool, error) {
    var textOutput string
    var toolsUsed bool
    
    // Append all output items to conversation history for persistence
    for _, item := range resp.Output {
        t.conversationItems = append(t.conversationItems, ConversationItem{
            Type:      "output",
            Item:      item,
            Timestamp: time.Now(),
        })
        
        switch item.Type {
        case "message":
            // Extract text content
            msg := item.AsOutputMessage()
            for _, content := range msg.Content {
                if content.Type == "output_text" {
                    text := content.Text
                    handler.HandleText(text)
                    textOutput += text
                }
            }
            
        case "function_call":
            // Execute function tool
            toolsUsed = true
            call := item.AsFunctionToolCall()
            
            handler.HandleToolUse(call.Name, call.Arguments)
            
            runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)
            output := tools.RunTool(runToolCtx, t.state, call.Name, call.Arguments)
            
            structuredResult := output.StructuredData()
            renderedOutput := t.renderToolResult(structuredResult)
            handler.HandleToolResult(call.Name, renderedOutput)
            
            // Create tool result input item for next request
            toolResultItem := responses.ResponseInputItemParamOfFunctionCallOutput(
                call.CallID,
                output.AssistantFacing(),
            )
            
            // Append tool result to conversation history
            t.conversationItems = append(t.conversationItems, ConversationItem{
                Type:      "input",
                Item:      toolResultItem,
                Timestamp: time.Now(),
            })
            
        case "reasoning":
            // Handle reasoning content (for o-series models)
            reasoning := item.AsReasoningItem()
            if len(reasoning.Content) > 0 {
                handler.HandleThinking(reasoning.Content[0].Text)
            }
        }
    }
    
    return textOutput, toolsUsed, nil
}
```

#### Streaming Support

The Response API provides fine-grained streaming events:

```go
func (t *OpenAIThread) sendMessageResponseAPIStreaming(
    ctx context.Context,
    params responses.ResponseNewParams,
    handler llmtypes.MessageHandler,
) (*responses.Response, error) {
    stream := t.responsesClient.Responses.NewStreaming(ctx, params)
    
    var fullText string
    var toolCalls []responses.FunctionToolCall
    
    for stream.Next() {
        event := stream.Current()
        
        switch e := event.AsAny().(type) {
        case responses.ResponseTextDeltaEvent:
            // Stream text as it arrives
            handler.HandleText(e.Delta)
            fullText += e.Delta
            
        case responses.ResponseReasoningTextDeltaEvent:
            // Stream reasoning tokens
            handler.HandleThinking(e.Delta)
            
        case responses.ResponseFunctionCallArgumentsDeltaEvent:
            // Accumulate function arguments
            // Will handle after completion
            
        case responses.ResponseFunctionCallArgumentsDoneEvent:
            // Function call complete
            toolCalls = append(toolCalls, responses.FunctionToolCall{
                Name: e.Name,
                Arguments: e.Arguments,
                CallID: e.CallID,
            })
            
        case responses.ResponseCompletedEvent:
            // Response complete, return final response object
            return &e.Response, nil
            
        case responses.ResponseErrorEvent:
            return nil, errors.New(e.Error.Message)
        }
    }
    
    if err := stream.Err(); err != nil {
        return nil, err
    }
    
    return nil, errors.New("stream ended without completion event")
}
```

#### Conversation State Management

Response API has built-in conversation management. We integrate this with the existing persistence.go architecture by adapting the SaveConversation and loadConversation methods to handle both APIs:

```go
// SaveConversation saves the current thread to the conversation store
// This method is already implemented in persistence.go and needs to be extended
func (t *OpenAIThread) SaveConversation(ctx context.Context, summarize bool) error {
    t.conversationMu.Lock()
    defer t.conversationMu.Unlock()

    if !t.isPersisted || t.store == nil {
        return nil
    }

    // Generate a new summary if requested
    if summarize {
        t.summary = t.ShortSummary(ctx)
    }

    var messagesJSON []byte
    var err error
    var metadata map[string]interface{}

    if t.useResponsesAPI {
        // For Response API, serialize the unified conversation history
        // conversationItems contains both input and output items in chronological order
        conversationData := map[string]interface{}{
            "api_type": "responses",  // Discriminator for Response API
            "previous_response_id": t.previousResponseID,
            "conversation_items": t.conversationItems,  // Unified history for visualization
        }
        messagesJSON, err = json.Marshal(conversationData)
        if err != nil {
            return errors.Wrap(err, "error marshaling response items")
        }
        metadata = map[string]interface{}{
            "model": t.config.Model,
            "api_type": "responses",
        }
    } else {
        // For Chat Completion API, use existing logic
        t.cleanupOrphanedMessages()
        messagesJSON, err = json.Marshal(t.messages)
        if err != nil {
            return errors.Wrap(err, "error marshaling messages")
        }
        metadata = map[string]interface{}{
            "model": t.config.Model,
            "api_type": "chat_completion",
        }
    }

    // Build the conversation record (same structure for both APIs)
    record := convtypes.ConversationRecord{
        ID:                  t.conversationID,
        RawMessages:         messagesJSON,
        Provider:            "openai",
        Usage:               *t.usage,
        Metadata:            metadata,
        Summary:             t.summary,
        CreatedAt:           time.Now(),
        UpdatedAt:           time.Now(),
        FileLastAccess:      t.state.FileLastAccess(),
        ToolResults:         t.GetStructuredToolResults(),
        BackgroundProcesses: t.state.GetBackgroundProcesses(),
    }

    return t.store.Save(ctx, record)
}

// loadConversation loads a conversation from the store
// This method is already implemented in persistence.go and needs to be extended
func (t *OpenAIThread) loadConversation(ctx context.Context) error {
    t.conversationMu.Lock()
    defer t.conversationMu.Unlock()

    if !t.isPersisted || t.store == nil || t.conversationID == "" {
        return nil
    }

    record, err := t.store.Load(ctx, t.conversationID)
    if err != nil {
        return errors.Wrap(err, "failed to load conversation")
    }

    // Check provider compatibility
    if record.Provider != "" && record.Provider != "openai" {
        return errors.Errorf("incompatible provider: %s", record.Provider)
    }

    // Check which API type was used
    apiType := "chat_completion" // default
    if metadata, ok := record.Metadata["api_type"].(string); ok {
        apiType = metadata
    }

    if apiType == "responses" && t.useResponsesAPI {
        // Load Response API conversation
        var conversationData struct {
            APIType            string              `json:"api_type"`
            PreviousResponseID string              `json:"previous_response_id"`
            ConversationItems  []ConversationItem  `json:"conversation_items"`
        }
        
        if err := json.Unmarshal(record.RawMessages, &conversationData); err != nil {
            return errors.Wrap(err, "error unmarshaling response items")
        }

        t.previousResponseID = conversationData.PreviousResponseID
        t.conversationItems = conversationData.ConversationItems
    } else if apiType == "chat_completion" && !t.useResponsesAPI {
        // Load Chat Completion API conversation (existing logic)
        var messages []openai.ChatCompletionMessage
        if err := json.Unmarshal(record.RawMessages, &messages); err != nil {
            return errors.Wrap(err, "error unmarshaling messages")
        }

        t.cleanupOrphanedMessages()
        t.messages = messages
    } else {
        return errors.Errorf("API type mismatch: conversation uses %s but thread is configured for %s",
            apiType, map[bool]string{true: "responses", false: "chat_completion"}[t.useResponsesAPI])
    }

    // Common restoration for both APIs
    t.usage = &record.Usage
    t.summary = record.Summary
    t.state.SetFileLastAccess(record.FileLastAccess)
    t.SetStructuredToolResults(record.ToolResults)
    t.restoreBackgroundProcesses(record.BackgroundProcesses)

    return nil
}
```

#### Streaming and Visualization for Response API

To support web UI visualization, we need to extend the existing StreamMessages function to handle Response API conversations:

```go
// StreamMessages is extended to handle both Chat Completion and Response API formats
func StreamMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
    // Try to determine the API type
    var typeCheck struct {
        APIType string `json:"api_type"`
    }
    if err := json.Unmarshal(rawMessages, &typeCheck); err == nil && typeCheck.APIType == "responses" {
        return streamResponseAPIMessages(rawMessages, toolResults)
    }
    
    // Fall back to Chat Completion API format (existing implementation)
    return streamChatCompletionMessages(rawMessages, toolResults)
}

func streamResponseAPIMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
    var conversationData struct {
        ConversationItems []ConversationItem `json:"conversation_items"`
    }
    
    if err := json.Unmarshal(rawMessages, &conversationData); err != nil {
        return nil, errors.Wrap(err, "error unmarshaling response API messages")
    }

    var streamable []StreamableMessage

    // Process conversation items in chronological order
    // Each item is already tagged as input or output, making visualization straightforward
    for _, convItem := range conversationData.ConversationItems {
        if convItem.Type == "input" {
            // Process input items (user messages, tool results, etc.)
            // Convert to appropriate streamable format
            // Implementation details depend on input item structure
        } else if convItem.Type == "output" {
            // Process output items (assistant messages, tool calls, reasoning)
            outputItem := convItem.Item.(responses.ResponseOutputItemUnion)
            switch outputItem.Type {
            case "message":
                msg := outputItem.AsOutputMessage()
                for _, content := range msg.Content {
                    if content.Type == "output_text" {
                        streamable = append(streamable, StreamableMessage{
                            Kind:    "text",
                            Role:    "assistant",
                            Content: content.Text,
                        })
                    }
                }
                
            case "function_call":
                call := outputItem.AsFunctionToolCall()
                streamable = append(streamable, StreamableMessage{
                    Kind:       "tool-use",
                    Role:       "assistant",
                    ToolName:   call.Name,
                    ToolCallID: call.CallID,
                    Input:      call.Arguments,
                })
                
            case "reasoning":
                reasoning := outputItem.AsReasoningItem()
                if len(reasoning.Content) > 0 {
                    streamable = append(streamable, StreamableMessage{
                        Kind:    "thinking",
                        Role:    "assistant",
                        Content: reasoning.Content[0].Text,
                    })
                }
            }
        }
    }

    return streamable, nil
}

func streamChatCompletionMessages(rawMessages json.RawMessage, toolResults map[string]tooltypes.StructuredToolResult) ([]StreamableMessage, error) {
    // Existing implementation from persistence.go
    // ... (keep existing code)
}
```

### 5. Migration and Compatibility

#### Model Detection

Automatically use Response API for models that require it:

```go
func needsResponsesAPI(model string) bool {
    responsesOnlyModels := []string{
        "gpt-5-codex",
        // Add other Response API-only models as they're released
    }
    return slices.Contains(responsesOnlyModels, model)
}

func NewOpenAIThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (*OpenAIThread, error) {
    // Auto-enable Response API for models that require it
    useResponsesAPI := config.OpenAI != nil && config.OpenAI.ResponsesAPI
    if needsResponsesAPI(config.Model) {
        useResponsesAPI = true
        logger.G(context.Background()).
            WithField("model", config.Model).
            Info("automatically enabling Response API for model that requires it")
    }
    
    thread := &OpenAIThread{
        config: config,
        useResponsesAPI: useResponsesAPI,
        // ... other initialization
    }
    
    if useResponsesAPI {
        // Initialize Response API client
        thread.responsesClient = openai_v2.NewClient()
        // Configure base URL if custom
        if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
            // Configure custom base URL
        }
    } else {
        // Initialize Chat Completion client (existing code)
        thread.client = openai.NewClientWithConfig(clientConfig)
    }
    
    return thread, nil
}
```

**Note**: gpt-5-codex pricing needs to be added to `pricing.go` (same pricing as gpt-5) for proper token usage tracking and cost calculation.

#### Backward Compatibility

Existing configurations will continue to work without changes:

```yaml
# Existing configuration - continues to use Chat Completion API
provider: openai
model: gpt-4.1
```

```yaml
# New configuration - explicitly uses Response API
provider: openai
model: gpt-4.1
openai:
  responses_api: true
```

```yaml
# Automatic Response API usage for models that require it
provider: openai
model: gpt-5-codex  # Response API automatically enabled
```

### 6. Testing Strategy

1. **Unit Tests**: Test Response API request/response conversion logic
2. **Integration Tests**: Test actual API calls with mocked responses
3. **E2E Tests**: Test complete workflows with both APIs
4. **Compatibility Tests**: Ensure Chat Completion API continues to work
5. **Model-Specific Tests**: Test Response API-only models

### 7. Documentation Updates

1. Update `AGENTS.md` with Response API configuration examples
2. Update `config.sample.yaml` with new Response API options
3. Create migration guide for users wanting to switch to Response API
4. Document model compatibility matrix (which models support which API)
5. Document conversation persistence format for Response API (input items + output items)
6. Update web UI documentation to explain Response API conversation visualization

## Consequences

### Positive

1. **Future-Proof**: Support for gpt-5-codex and future Response API-only models
2. **Enhanced Capabilities**: Access to improved function calling, background processing, and fine-grained streaming
3. **Backward Compatible**: Existing configurations and workflows continue to work unchanged
4. **Flexible**: Users can choose the API that best fits their needs
5. **Well-Supported**: Official SDK provides proper type safety and maintenance

### Negative

1. **Additional Dependency**: Adding `github.com/openai/openai-go/v2` increases dependency footprint
2. **Code Complexity**: Dual-mode implementation adds conditional logic and maintenance burden
3. **Learning Curve**: Users need to understand when to use which API
4. **Testing Overhead**: Need to test both API paths comprehensively
5. **Binary Size**: Additional dependency slightly increases binary size
6. **Visualization Complexity**: Web UI needs to handle both Chat Completion and Response API conversation formats

### Neutral

1. **Configuration Migration**: Users wanting Response API features need to update their config
2. **Documentation Burden**: Need to document both APIs and their differences
3. **Performance**: Response API may have different performance characteristics

## Implementation Plan

### Phase 1: Foundation (Week 1)

1. Add `github.com/openai/openai-go/v2` dependency
2. Extend `OpenAIConfig` with `responses_api` field
3. Update `OpenAIThread` struct with Response API fields
4. Implement model detection logic for automatic Response API enablement
5. Add gpt-5-codex to pricing.go (same pricing as gpt-5)

### Phase 2: Core Implementation (Week 2)

1. Implement `sendMessageResponseAPI()` method
2. Implement request builder for Response API
3. Implement response parser for Response API
4. Implement tool execution for Response API
5. Add usage tracking for Response API

### Phase 3: Streaming Support (Week 3)

1. Implement streaming for Response API
2. Handle fine-grained streaming events
3. Map events to existing handler interface

### Phase 4: Conversation Management (Week 4)

1. Implement conversation state management with response items persistence
2. Implement conversation persistence for Response API (input items + output items)
3. Add conversation loading/resuming support with full history restoration
4. Test multi-turn conversations with proper visualization data
5. Ensure web UI can properly render Response API conversations

### Phase 5: Testing & Documentation (Week 5)

1. Write unit tests for all Response API code
2. Write integration tests
3. Update documentation (AGENTS.md, config.sample.yaml)
4. Create migration guide
5. Test with gpt-5-codex and other Response API models

## Success Criteria

1. ✅ Users can configure Response API usage via configuration
2. ✅ gpt-5-codex and other Response API-only models work correctly
3. ✅ Existing Chat Completion API usage continues to work unchanged
4. ✅ Tool execution works with both APIs
5. ✅ Streaming works with both APIs
6. ✅ Conversation persistence works with both APIs (including response items for visualization)
7. ✅ Web UI properly visualizes Response API conversations
8. ✅ All tests pass for both API paths
9. ✅ Documentation is clear and comprehensive
10. ✅ Zero breaking changes for existing users

## References

- [OpenAI Response API Documentation](https://platform.openai.com/docs/guides/text)
- [OpenAI Go SDK v2](https://github.com/openai/openai-go)
- [ADR 010: OpenAI LLM Integration](./010-openai-llm-integration.md)
- [ADR 016: OpenAI Compatible API Support](./016-openai-compatible-api-support.md)
- [OPENAI_RESPONSE_API.md](../OPENAI_RESPONSE_API.md)
