# ADR 024: OpenAI Responses API Support

## Status
Proposed

## Context

Kodelet currently uses the `sashabaranov/go-openai` library to interact with OpenAI and OpenAI-compatible APIs (e.g., x.ai Grok). This library implements the **Chat Completions API**, which has served well for standard conversational AI tasks.

OpenAI has introduced a newer **Responses API** that offers several advantages over Chat Completions:

1. **Unified Interface**: Single API for text, images, audio, and structured outputs
2. **Rich Streaming Events**: 42+ event types for granular progress tracking (vs basic delta streaming)
3. **Built-in Tools**: Native support for web search, file search, code interpreter, and computer control
4. **Multi-turn Conversations**: Stateless (`previous_response_id`) or stateful (`conversation`) approaches
5. **Prompt Caching**: Explicit cache keys for user-controlled caching strategies
6. **Enhanced Reasoning**: Better support for o-series models with configurable reasoning effort
7. **MCP Integration**: Native Model Context Protocol tool support
8. **Input Token Counting**: Pre-request token estimation

The official `openai-go` SDK (v3) in the `openai-go/` directory provides full Responses API support. In the short term, there is no intention to replace `sashabaranov/go-openai` entirely—both SDKs will coexist.

### SDK Comparison

| Feature | sashabaranov/go-openai | openai-go (official) |
|---------|------------------------|----------------------|
| API Support | Chat Completions | Chat Completions + Responses |
| Streaming | Channel-based | Iterator-based (iter.Seq2) |
| Tool Calling | Function calling | Functions + Built-in tools + MCP |
| Multi-turn | Manual message management | Automatic via `previous_response_id` |
| Prompt Caching | Automatic (server-side) | Automatic + explicit cache keys |
| Generated | Hand-written | Auto-generated from OpenAPI spec |
| Maturity | Battle-tested | Newer, official |

### Responses API Key Concepts

The Responses API differs fundamentally from Chat Completions:

```go
// Chat Completions API (current)
resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
    Model: "gpt-4.1",
    Messages: []openai.ChatCompletionMessage{
        {Role: "user", Content: "Hello"},
    },
})

// Responses API (new)
resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
    Model: "gpt-4.1",
    Input: responses.ResponseNewParamsInputUnion{
        OfString: openai.String("Hello"),
    },
})
```

Key differences:
- **Input**: Union type supporting text, images, files, or structured content
- **Output**: Array of `ResponseOutputItemUnion` (text, tool calls, images, audio)
- **Streaming**: Returns `ResponseStreamEventUnion` with 42+ event types
- **State**: Responses have status (`in_progress`, `completed`, `failed`) and can be retrieved later

## Decision

We will implement Responses API support alongside the existing Chat Completions implementation, allowing gradual migration and feature-specific adoption. The implementation will:

1. **Add a new `responses` sub-package** within `pkg/llm/openai` for Responses API support
2. **Introduce API selection** via configuration to choose between Chat Completions and Responses API
3. **Maintain backward compatibility** with existing Chat Completions-based workflows
4. **Leverage unique Responses API features** for enhanced capabilities (caching, background jobs, built-in tools)
5. **Share common infrastructure** (configuration, pricing, retry logic) between both implementations

## Architecture Details

### Directory Structure

```
pkg/
  └── llm/
      └── openai/
          ├── openai.go              # Chat Completions Thread (existing)
          ├── config.go              # Shared configuration (existing)
          ├── persistence.go         # Shared persistence (existing)
          ├── preset/                # Model presets (existing)
          │   ├── openai/
          │   └── xai/
          └── responses/             # NEW: Responses API implementation
              ├── thread.go          # ResponsesThread implementation
              ├── thread_test.go     # Unit tests
              ├── streaming.go       # Streaming event handling
              ├── streaming_test.go  # Streaming tests
              ├── tools.go           # Tool conversion for Responses API
              ├── tools_test.go      # Tool conversion tests
              ├── input.go           # Input type conversion
              └── input_test.go      # Input conversion tests
```

### Core Components

#### ResponsesThread

A new `ResponsesThread` struct implementing `llmtypes.Thread`:

```go
package responses

import (
    "github.com/openai/openai-go"
    "github.com/openai/openai-go/responses"
)

type ResponsesThread struct {
    *base.Thread                              // Embedded base thread for shared functionality

    client            *openai.Client          // Official openai-go client
    lastResponseID    string                  // For previous_response_id chaining
    messages          []InternalMessage       // Local message history (for persistence/recovery)

    // Responses API specific features
    promptCacheKey    string                  // For prompt caching
    cacheRetention    string                  // "in-memory" or "24h"

    // Model configuration
    customModels      *CustomModels           // Reasoning vs non-reasoning models
    customPricing     CustomPricing           // Per-token pricing
}
```

#### Factory Pattern Update

Update `pkg/llm/openai/openai.go` to support API selection:

```go
// Config addition
type OpenAIConfig struct {
    // ... existing fields ...

    // API selection
    UseResponsesAPI bool `yaml:"use_responses_api" mapstructure:"use_responses_api"`

    // Responses API specific
    PromptCacheKey    string `yaml:"prompt_cache_key" mapstructure:"prompt_cache_key"`
    CacheRetention    string `yaml:"cache_retention" mapstructure:"cache_retention"`
}

// Updated factory
func NewOpenAIThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (llmtypes.Thread, error) {
    if config.OpenAI.UseResponsesAPI {
        return responses.NewResponsesThread(config, subagentContextFactory)
    }
    // Existing Chat Completions implementation
    return newChatCompletionsThread(config, subagentContextFactory)
}
```

#### ResponsesThread Constructor

```go
func NewResponsesThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (*ResponsesThread, error) {
    // Apply defaults
    if config.Model == "" {
        config.Model = "gpt-4.1"
    }

    // Create official openai-go client
    opts := []option.RequestOption{}

    if config.OpenAI.BaseURL != "" {
        opts = append(opts, option.WithBaseURL(config.OpenAI.BaseURL))
    }

    apiKey := os.Getenv(config.OpenAI.APIKeyEnvVar)
    if apiKey == "" {
        apiKey = os.Getenv("OPENAI_API_KEY")
    }
    opts = append(opts, option.WithAPIKey(apiKey))

    client := openai.NewClient(opts...)

    // Create base thread
    baseThread, err := base.NewThread(config, subagentContextFactory)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create base thread")
    }

    return &ResponsesThread{
        Thread:         baseThread,
        client:         client,
        messages:       make([]InternalMessage, 0),
        promptCacheKey: config.OpenAI.PromptCacheKey,
        cacheRetention: config.OpenAI.CacheRetention,
        customModels:   loadCustomModels(config),
        customPricing:  loadCustomPricing(config),
    }, nil
}
```

### Message Exchange Flow

#### SendMessage Implementation

```go
func (t *ResponsesThread) SendMessage(ctx context.Context, message string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
    // Trigger hook: user_message_send
    if err := t.TriggerHook(ctx, "user_message_send", message); err != nil {
        return "", err
    }

    // Add user message
    t.AddUserMessage(ctx, message, opt.Images...)

    // Auto-compact check
    if !opt.DisableAutoCompact && t.ShouldAutoCompact(opt.CompactRatio) {
        if err := t.CompactContext(ctx); err != nil {
            return "", errors.Wrap(err, "failed to compact context")
        }
    }

    maxTurns := opt.MaxTurns
    if maxTurns == 0 {
        maxTurns = 10
    }

    var finalOutput strings.Builder

    for turn := 0; turn < maxTurns; turn++ {
        response, err := t.processMessageExchange(ctx, handler, opt)
        if err != nil {
            return "", err
        }

        finalOutput.WriteString(response.Text)

        // Check for tool calls
        if len(response.ToolCalls) == 0 {
            break
        }

        // Execute tools
        if err := t.executeToolCalls(ctx, response.ToolCalls, handler, opt); err != nil {
            return "", err
        }
    }

    handler.HandleDone()

    // Save conversation
    if t.IsPersisted() && !opt.NoSaveConversation {
        if err := t.SaveConversation(ctx, false); err != nil {
            return "", errors.Wrap(err, "failed to save conversation")
        }
    }

    return finalOutput.String(), nil
}
```

#### Process Message Exchange

```go
func (t *ResponsesThread) processMessageExchange(ctx context.Context, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (*ResponseResult, error) {
    // Build request parameters
    params := t.buildRequestParams(opt)

    // Check for streaming handler
    if streamHandler, ok := handler.(llmtypes.StreamingMessageHandler); ok {
        return t.processStreamingResponse(ctx, params, streamHandler)
    }

    return t.processNonStreamingResponse(ctx, params, handler)
}

func (t *ResponsesThread) buildRequestParams(opt llmtypes.MessageOpt) responses.ResponseNewParams {
    modelName := t.GetConfig().Model
    if opt.UseWeakModel && t.GetConfig().WeakModel != "" {
        modelName = t.GetConfig().WeakModel
    }

    params := responses.ResponseNewParams{
        Model: modelName,
        Input: t.buildInput(),
    }

    // Multi-turn support via previous_response_id
    if t.lastResponseID != "" {
        params.PreviousResponseID = t.lastResponseID
    }

    // Add tools if not disabled
    if !opt.DisableTools {
        params.Tools = t.convertTools()
        params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
            OfAuto: &responses.ResponseNewParamsToolChoiceAuto{},
        }
    }

    // Reasoning models
    if t.isReasoningModel(modelName) {
        params.Reasoning = shared.ReasoningParam{
            Effort: t.mapReasoningEffort(t.GetConfig().ReasoningEffort),
        }
    } else {
        params.MaxOutputTokens = openai.Int(int64(t.GetConfig().MaxTokens))
    }

    // Prompt caching
    if t.promptCacheKey != "" {
        params.PromptCacheKey = t.promptCacheKey
        params.PromptCacheRetention = t.cacheRetention
    }

    return params
}
```

### Streaming Implementation

The Responses API provides rich streaming events. Map them to existing handler methods:

```go
func (t *ResponsesThread) processStreamingResponse(ctx context.Context, params responses.ResponseNewParams, handler llmtypes.StreamingMessageHandler) (*ResponseResult, error) {
    stream := t.client.Responses.NewStreaming(ctx, params)

    result := &ResponseResult{
        ToolCalls: make([]ToolCall, 0),
    }

    var textBuilder strings.Builder
    var reasoningBuilder strings.Builder
    toolCallMap := make(map[int]*ToolCall) // Index -> ToolCall

    for stream.Next() {
        event := stream.Current()

        switch event.Type {
        // Text generation events
        case "response.text.delta":
            handler.HandleTextDelta(event.Delta)
            textBuilder.WriteString(event.Delta)

        case "response.text.done":
            handler.HandleContentBlockEnd()

        // Reasoning events (o-series models)
        case "response.reasoning.text.delta":
            if reasoningBuilder.Len() == 0 {
                handler.HandleThinkingStart()
            }
            handler.HandleThinkingDelta(event.Delta)
            reasoningBuilder.WriteString(event.Delta)

        case "response.reasoning.text.done":
            handler.HandleThinkingEnd()

        // Tool call events
        case "response.function_call.arguments.delta":
            tc := t.getOrCreateToolCall(toolCallMap, int(event.OutputIndex))
            tc.Arguments += event.Delta

        case "response.function_call.arguments.done":
            tc := toolCallMap[int(event.OutputIndex)]
            if tc != nil {
                handler.HandleToolUse(tc.ID, tc.Name, tc.Arguments)
                result.ToolCalls = append(result.ToolCalls, *tc)
            }

        // Output item events (for tool call metadata)
        case "response.output_item.added":
            if event.Item.Type == "function_call" {
                tc := t.getOrCreateToolCall(toolCallMap, int(event.OutputIndex))
                tc.ID = event.Item.ID
                tc.Name = event.Item.Name
            }

        // Response lifecycle events
        case "response.completed":
            t.lastResponseID = event.Response.ID
            if event.Response.Usage != nil {
                result.Usage = t.convertUsage(event.Response.Usage)
            }

        case "response.failed":
            return nil, errors.Errorf("response failed: %s", event.Response.Error.Message)

        // Web search events (built-in tool)
        case "response.web_search.searching":
            handler.HandleToolUse("", "web_search", `{"status":"searching"}`)

        case "response.web_search.completed":
            handler.HandleToolResult("web_search", "Search completed")

        // Code interpreter events (built-in tool)
        case "response.code_interpreter.code.delta":
            // Could emit as tool progress

        case "response.code_interpreter.completed":
            // Handle code execution result
        }
    }

    if err := stream.Err(); err != nil {
        return nil, errors.Wrap(err, "streaming failed")
    }

    result.Text = textBuilder.String()
    result.ReasoningText = reasoningBuilder.String()

    // Update usage tracking
    t.updateUsage(result.Usage)

    return result, nil
}
```

### Tool Conversion

Convert Kodelet tools to Responses API format:

```go
func (t *ResponsesThread) convertTools() []responses.ToolUnionParam {
    kodeletTools := t.GetState().GetTools()
    result := make([]responses.ToolUnionParam, 0, len(kodeletTools))

    for _, tool := range kodeletTools {
        result = append(result, responses.ToolUnionParam{
            OfFunction: &responses.FunctionToolParam{
                Type: responses.F(responses.FunctionToolTypeFunction),
                Function: responses.FunctionDefinitionParam{
                    Name:        tool.Name,
                    Description: openai.String(tool.Description),
                    Parameters:  t.convertSchema(tool.Schema),
                },
            },
        })
    }

    return result
}

func (t *ResponsesThread) convertSchema(schema tooltypes.ToolSchema) responses.FunctionParametersUnionParam {
    // Convert Kodelet's tool schema to Responses API JSON schema format
    return responses.FunctionParametersUnionParam{
        OfJSONSchema: &responses.FunctionParametersJSONSchemaParam{
            Type:       openai.String("object"),
            Properties: t.convertProperties(schema.Properties),
            Required:   schema.Required,
        },
    }
}
```

### Input Building

Build Responses API input from message history:

```go
func (t *ResponsesThread) buildInput() responses.ResponseNewParamsInputUnion {
    // For multi-turn with previous_response_id, only send new message
    if t.lastResponseID != "" {
        return t.buildLatestInput()
    }

    // Otherwise, build full input array
    items := make([]responses.ResponseInputItemUnionParam, 0)

    // Add system prompt as instructions (handled separately in params)
    // Build message items from history
    for _, msg := range t.getMessageHistory() {
        item := t.convertMessageToInputItem(msg)
        items = append(items, item)
    }

    return responses.ResponseNewParamsInputUnion{
        OfResponseInputItemUnionParamArray: items,
    }
}

func (t *ResponsesThread) convertMessageToInputItem(msg InternalMessage) responses.ResponseInputItemUnionParam {
    switch msg.Role {
    case "user":
        parts := make([]responses.ResponseInputContentUnionParam, 0)

        // Add images
        for _, img := range msg.Images {
            parts = append(parts, responses.ResponseInputContentUnionParam{
                OfInputImage: &responses.ResponseInputImageParam{
                    Type:   responses.F(responses.ResponseInputImageTypeInputImage),
                    Source: t.buildImageSource(img),
                },
            })
        }

        // Add text
        parts = append(parts, responses.ResponseInputContentUnionParam{
            OfInputText: &responses.ResponseInputTextParam{
                Type: responses.F(responses.ResponseInputTextTypeInputText),
                Text: msg.Content,
            },
        })

        return responses.ResponseInputItemUnionParam{
            OfMessage: &responses.ResponseInputMessageItemParam{
                Type:    responses.F(responses.ResponseInputMessageItemTypeMessage),
                Role:    responses.F(responses.ResponseInputMessageItemRoleUser),
                Content: parts,
            },
        }

    case "assistant":
        return responses.ResponseInputItemUnionParam{
            OfMessage: &responses.ResponseInputMessageItemParam{
                Type:    responses.F(responses.ResponseInputMessageItemTypeMessage),
                Role:    responses.F(responses.ResponseInputMessageItemRoleAssistant),
                Content: []responses.ResponseInputContentUnionParam{
                    {
                        OfInputText: &responses.ResponseInputTextParam{
                            Type: responses.F(responses.ResponseInputTextTypeInputText),
                            Text: msg.Content,
                        },
                    },
                },
            },
        }

    case "tool":
        return responses.ResponseInputItemUnionParam{
            OfFunctionCallOutput: &responses.ResponseInputFunctionCallOutputParam{
                Type:   responses.F(responses.ResponseInputFunctionCallOutputTypeFunctionCallOutput),
                CallID: msg.ToolCallID,
                Output: msg.Content,
            },
        }
    }

    return responses.ResponseInputItemUnionParam{}
}
```

### Usage Tracking

```go
func (t *ResponsesThread) convertUsage(usage *responses.ResponseUsage) llmtypes.Usage {
    return llmtypes.Usage{
        InputTokens:  int(usage.InputTokens),
        OutputTokens: int(usage.OutputTokens),
        // Responses API provides more granular tracking
        CachedInputTokens: int(usage.CacheReadInputTokens),
        ReasoningTokens:   int(usage.ReasoningTokens),
    }
}

func (t *ResponsesThread) updateUsage(usage llmtypes.Usage) {
    t.Mu.Lock()
    defer t.Mu.Unlock()

    t.Usage.InputTokens += usage.InputTokens
    t.Usage.OutputTokens += usage.OutputTokens

    // Calculate costs using pricing
    pricing := t.getPricing(t.GetConfig().Model)

    // Account for cached tokens (reduced cost)
    regularInputTokens := usage.InputTokens - usage.CachedInputTokens
    t.Usage.InputCost += float64(regularInputTokens) * pricing.Input
    t.Usage.InputCost += float64(usage.CachedInputTokens) * pricing.CachedInput
    t.Usage.OutputCost += float64(usage.OutputTokens) * pricing.Output

    // Update context window
    t.Usage.CurrentContextWindow = usage.InputTokens + usage.OutputTokens
    t.Usage.MaxContextWindow = pricing.ContextWindow
}
```

### Persistence

Extend existing persistence to store Responses API specific state:

```go
type ResponsesConversationState struct {
    // Existing fields
    Messages        []InternalMessage `json:"messages"`
    Usage           llmtypes.Usage    `json:"usage"`
    Summary         string            `json:"summary"`

    // Responses API specific
    LastResponseID  string            `json:"last_response_id,omitempty"`
    ConversationID  string            `json:"conversation_id,omitempty"`
    ResponseHistory []string          `json:"response_history,omitempty"`
    PromptCacheKey  string            `json:"prompt_cache_key,omitempty"`
}

func (t *ResponsesThread) SaveConversation(ctx context.Context, summarize bool) error {
    state := ResponsesConversationState{
        Messages:        t.getMessageHistory(),
        Usage:           t.GetUsage(),
        Summary:         t.summary,
        LastResponseID:  t.lastResponseID,
        ConversationID:  t.conversationID,
        ResponseHistory: t.responseHistory,
        PromptCacheKey:  t.promptCacheKey,
    }

    if summarize {
        state.Summary = t.generateSummary(ctx)
    }

    return t.store.SaveConversation(ctx, t.GetConversationID(), state)
}

func (t *ResponsesThread) loadConversation(ctx context.Context) error {
    state, err := t.store.LoadConversation(ctx, t.GetConversationID())
    if err != nil {
        return err
    }

    // Restore state
    t.setMessageHistory(state.Messages)
    t.SetUsage(state.Usage)
    t.summary = state.Summary
    t.lastResponseID = state.LastResponseID
    t.conversationID = state.ConversationID
    t.responseHistory = state.ResponseHistory

    return nil
}
```

### Configuration

#### Environment Variables

```bash
# Enable Responses API
KODELET_OPENAI_USE_RESPONSES_API=true

# Responses API specific
KODELET_OPENAI_PROMPT_CACHE_KEY=user-123
KODELET_OPENAI_CACHE_RETENTION=24h
```

#### Config File

```yaml
provider: openai
model: gpt-4.1
weak_model: gpt-4.1-mini
max_tokens: 8192
reasoning_effort: medium

openai:
  # API selection
  use_responses_api: true

  # Responses API features
  prompt_cache_key: "project-kodelet"
  cache_retention: "24h"           # "in-memory" or "24h"

  # Existing configuration
  preset: openai
  base_url: ""
  api_key_env_var: OPENAI_API_KEY
```

### Feature Comparison

| Feature | Chat Completions Thread | Responses Thread |
|---------|------------------------|------------------|
| Basic chat | ✓ | ✓ |
| Streaming | ✓ (channel-based) | ✓ (iterator + rich events) |
| Tool calling | ✓ | ✓ (+ built-in tools) |
| Multi-turn | Manual message management | `previous_response_id` |
| Reasoning models | ✓ | ✓ (better support) |
| Image input | ✓ | ✓ |
| Prompt caching | ✓ (automatic, server-side) | ✓ (automatic + explicit cache keys) |
| Token counting | Post-hoc only | Pre-request available |
| Web search | ✗ | ✓ (built-in) |
| Code interpreter | ✗ | ✓ (built-in) |
| MCP tools | ✗ | ✓ |

### Prompt Caching Clarification

Both APIs benefit from OpenAI's automatic prompt caching (server-side, for prompts >1024 tokens). The key difference:

- **Chat Completions**: Automatic caching only—no user control over cache keys or retention
- **Responses API**: Automatic caching PLUS explicit `prompt_cache_key` and `prompt_cache_retention` parameters for user-controlled caching strategies

The Responses API's explicit caching is useful for:
- Sharing cached prompts across different conversations with the same cache key
- Controlling cache retention (`in-memory` for session-scoped, `24h` for persistent)
- Optimizing costs for batch operations with identical system prompts

### Multi-turn via `previous_response_id`

The Responses API provides `previous_response_id` for efficient multi-turn conversations. Instead of sending the full message history each turn, we reference the previous response and only send new content.

#### How It Works

```
Turn 1 (Fresh start):
┌─────────────────────────────────────────────────────────────┐
│ Request:                                                    │
│   input: [user message: "Read config.yaml"]                 │
│   previous_response_id: (none)                              │
│                                                             │
│ Response:                                                   │
│   id: "resp_abc123"                                         │
│   output: [tool_call: read_file("config.yaml")]             │
└─────────────────────────────────────────────────────────────┘
                              ↓
Turn 2 (Tool result):
┌─────────────────────────────────────────────────────────────┐
│ Request:                                                    │
│   input: [tool_result: "contents of config.yaml..."]        │
│   previous_response_id: "resp_abc123"  ← references turn 1  │
│                                                             │
│ Response:                                                   │
│   id: "resp_def456"                                         │
│   output: [text: "The config file contains..."]             │
└─────────────────────────────────────────────────────────────┘
                              ↓
Turn 3 (Follow-up):
┌─────────────────────────────────────────────────────────────┐
│ Request:                                                    │
│   input: [user message: "Now update the port to 8080"]      │
│   previous_response_id: "resp_def456"  ← references turn 2  │
│                                                             │
│ Response:                                                   │
│   id: "resp_ghi789"                                         │
│   output: [tool_call: edit_file(...)]                       │
└─────────────────────────────────────────────────────────────┘
```

The API automatically maintains context from the referenced response, so we only transmit new content each turn.

#### Implementation Strategy

We use a **hybrid approach** that combines `previous_response_id` for efficiency with local message storage for persistence:

```go
type ResponsesThread struct {
    // ...
    lastResponseID string            // Current response chain
    messages       []InternalMessage // Local copy for persistence/recovery
}

// Within a session: use previous_response_id for efficiency
func (t *ResponsesThread) buildInput() responses.ResponseNewParamsInputUnion {
    if t.lastResponseID != "" {
        // Only send new content, API has context from previous response
        return t.buildIncrementalInput()
    }
    // Fresh start or recovery: send full history
    return t.buildFullInput()
}
```

**Key behaviors:**

1. **Within a session**: Use `previous_response_id` to chain responses
2. **On recovery from persistence**: Rebuild full context from stored messages (no `previous_response_id`)
3. **After context compaction**: Clear `lastResponseID` and start fresh chain with compacted summary

#### Input Building Logic

```go
func (t *ResponsesThread) buildIncrementalInput() responses.ResponseNewParamsInputUnion {
    // When we have previous_response_id, only send:
    // - New user message (if this is a user turn)
    // - Tool results (if this is a tool result turn)

    lastMsg := t.messages[len(t.messages)-1]

    switch lastMsg.Role {
    case "user":
        return responses.ResponseNewParamsInputUnion{
            OfString: openai.String(lastMsg.Content),
        }

    case "tool":
        // Send all pending tool results
        return t.buildToolResultsInput()
    }

    return responses.ResponseNewParamsInputUnion{}
}

func (t *ResponsesThread) buildToolResultsInput() responses.ResponseNewParamsInputUnion {
    var items []responses.ResponseInputItemUnionParam

    // Collect all tool results since last assistant message
    for i := len(t.messages) - 1; i >= 0; i-- {
        msg := t.messages[i]
        if msg.Role == "assistant" {
            break
        }
        if msg.Role == "tool" {
            items = append([]responses.ResponseInputItemUnionParam{
                {
                    OfFunctionCallOutput: &responses.ResponseInputFunctionCallOutputParam{
                        Type:   responses.F(responses.ResponseInputFunctionCallOutputTypeFunctionCallOutput),
                        CallID: msg.ToolCallID,
                        Output: msg.Content,
                    },
                },
            }, items...)
        }
    }

    return responses.ResponseNewParamsInputUnion{
        OfResponseInputItemUnionParamArray: items,
    }
}

func (t *ResponsesThread) buildFullInput() responses.ResponseNewParamsInputUnion {
    // Recovery path: rebuild full context from stored messages
    items := make([]responses.ResponseInputItemUnionParam, 0, len(t.messages))

    for _, msg := range t.messages {
        items = append(items, t.convertMessageToInputItem(msg))
    }

    return responses.ResponseNewParamsInputUnion{
        OfResponseInputItemUnionParamArray: items,
    }
}
```

#### Complete Message Exchange Flow

```go
func (t *ResponsesThread) processMessageExchange(ctx context.Context, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (*ResponseResult, error) {
    params := t.buildRequestParams(opt)

    // Make API call
    result, err := t.callAPI(ctx, params, handler)
    if err != nil {
        return nil, err
    }

    // Update response chain
    t.lastResponseID = result.ResponseID

    // Store assistant message locally (for persistence)
    t.messages = append(t.messages, InternalMessage{
        Role:    "assistant",
        Content: result.Text,
    })

    // Store tool calls if any
    for _, tc := range result.ToolCalls {
        t.messages = append(t.messages, InternalMessage{
            Role:       "assistant",
            ToolCallID: tc.ID,
            ToolName:   tc.Name,
            ToolArgs:   tc.Arguments,
        })
    }

    return result, nil
}

func (t *ResponsesThread) executeToolCalls(ctx context.Context, toolCalls []ToolCall, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) error {
    for _, tc := range toolCalls {
        // Execute tool
        result := tools.RunTool(ctx, t.GetState(), tc.Name, tc.Arguments)

        // Store result locally
        t.messages = append(t.messages, InternalMessage{
            Role:       "tool",
            ToolCallID: tc.ID,
            Content:    result.AssistantFacing(),
        })

        handler.HandleToolResult(tc.ID, tc.Name, result.UserFacing())
    }

    // Note: lastResponseID remains set, next request will use it
    return nil
}
```

#### Context Compaction and Summary Generation

Summary generation requires special handling because the summary thread does NOT have access to the original thread's `previous_response_id` chain. We must pass the full message history.

**CompactContext** (detailed summary for context reduction):

```go
func (t *ResponsesThread) CompactContext(ctx context.Context) error {
    // Create a SEPARATE thread for summary generation
    // This thread has NO access to our previous_response_id chain
    summaryThread, err := NewResponsesThread(t.GetConfig(), nil)
    if err != nil {
        return errors.Wrap(err, "failed to create summary thread")
    }

    // Copy our local messages to the summary thread
    // This is why we maintain local messages even when using previous_response_id
    summaryThread.messages = t.messages
    summaryThread.EnablePersistence(ctx, false)

    handler := &llmtypes.StringCollectorHandler{Silent: true}

    // Summary thread makes a FRESH request (no previous_response_id)
    // It sends the full message history to generate the summary
    _, err = summaryThread.SendMessage(ctx, prompts.CompactPrompt, handler, llmtypes.MessageOpt{
        UseWeakModel:       false, // Use main model for detailed summary
        NoToolUse:          true,  // Focus on summarization
        DisableAutoCompact: true,  // Prevent infinite recursion
        DisableUsageLog:    true,
        NoSaveConversation: true,
    })
    if err != nil {
        return errors.Wrap(err, "failed to generate compact summary")
    }

    compactSummary := handler.CollectedText()

    // Reset our state
    t.Mu.Lock()
    defer t.Mu.Unlock()

    t.messages = []InternalMessage{
        {Role: "user", Content: compactSummary},
    }
    t.lastResponseID = "" // Break chain, next request rebuilds context
    t.ToolResults = make(map[string]tooltypes.StructuredToolResult)

    if t.GetState() != nil {
        t.GetState().SetFileLastAccess(make(map[string]time.Time))
    }

    return nil
}
```

**ShortSummary** (brief summary for conversation list):

```go
func (t *ResponsesThread) ShortSummary(ctx context.Context) string {
    // Create a SEPARATE thread - no access to previous_response_id
    summaryThread, err := NewResponsesThread(t.GetConfig(), nil)
    if err != nil {
        logger.G(ctx).WithError(err).Error("failed to create summary thread")
        return "Could not generate summary."
    }

    // Copy local messages (full history needed for summary)
    summaryThread.messages = t.messages
    summaryThread.EnablePersistence(ctx, false)

    handler := &llmtypes.StringCollectorHandler{Silent: true}
    _, err = summaryThread.SendMessage(ctx, prompts.ShortSummaryPrompt, handler, llmtypes.MessageOpt{
        UseWeakModel:       true,  // Use WEAK model for speed/cost
        NoToolUse:          true,
        DisableAutoCompact: true,
        DisableUsageLog:    true,
        NoSaveConversation: true,
    })
    if err != nil {
        logger.G(ctx).WithError(err).Error("failed to generate short summary")
        return "Could not generate summary."
    }

    return handler.CollectedText()
}
```

**Key insight**: This is why we maintain a local `messages` array even when using `previous_response_id`:

1. **Within-session efficiency**: Use `previous_response_id` for incremental requests
2. **Summary generation**: Copy local `messages` to a fresh thread (no `previous_response_id`)
3. **Persistence recovery**: Rebuild context from local `messages` when resuming

The summary threads start fresh because:
- They're separate API conversations
- They don't inherit the `previous_response_id` chain
- They receive full message history via the copied `messages` array

#### Persistence and Recovery

```go
type ResponsesConversationState struct {
    Messages       []InternalMessage `json:"messages"`
    LastResponseID string            `json:"last_response_id,omitempty"`
    Usage          llmtypes.Usage    `json:"usage"`
    Summary        string            `json:"summary"`
}

func (t *ResponsesThread) SaveConversation(ctx context.Context, summarize bool) error {
    state := ResponsesConversationState{
        Messages:       t.messages,
        LastResponseID: t.lastResponseID, // May be stale after long time
        Usage:          t.GetUsage(),
    }
    // ...
}

func (t *ResponsesThread) loadConversation(ctx context.Context) error {
    state, err := t.store.LoadConversation(ctx, t.GetConversationID())
    if err != nil {
        return err
    }

    t.messages = state.Messages

    // Note: We intentionally DON'T restore lastResponseID
    // Response IDs may expire or become invalid after time
    // Instead, we'll rebuild context from messages on first request
    t.lastResponseID = ""

    return nil
}
```

#### Advantages Over Chat Completions

| Aspect | Chat Completions | Responses API |
|--------|-----------------|---------------|
| **Per-request payload** | Full message history | Only new content |
| **Token billing** | Re-bill all input tokens | Only new tokens (context is server-side) |
| **Latency** | Scales with history | Constant for incremental |
| **Context management** | Client-side | Server-side with client fallback |

#### Edge Cases

1. **Response ID expiration**: OpenAI may invalidate old response IDs. Solution: Fall back to full context rebuild.

2. **Tool call ordering**: Multiple tool calls must be returned in the same order they were requested. The API handles this via `call_id` matching.

3. **Parallel tool execution**: When tools are executed in parallel, all results are sent together in a single request with the same `previous_response_id`.

4. **Subagents**: Subagents start fresh without `previous_response_id` (they have their own context).

### Migration Strategy

1. **Phase 1: Parallel Implementation**
   - Add `responses/` sub-package
   - Implement `ResponsesThread` with feature parity to Chat Completions
   - Gate behind `use_responses_api` configuration flag
   - Default to Chat Completions (existing behavior)

2. **Phase 2: Feature Enhancement**
   - Add explicit prompt caching support
   - Add pre-request token counting
   - Integrate built-in tools (web search, code interpreter) - optional

3. **Phase 3: Evaluation**
   - Run parallel testing with both implementations
   - Gather metrics on reliability, latency, cost
   - Document feature differences and trade-offs

4. **Phase 4: Default Migration (Future)**
   - Once Responses API proves stable, consider making it the default
   - Maintain Chat Completions for backward compatibility
   - Support OpenAI-compatible providers that don't implement Responses API

## Consequences

### Positive

- **Enhanced Features**: Access to explicit prompt caching, built-in tools, MCP integration
- **Better Multi-turn**: Simpler multi-turn via `previous_response_id` vs manual message management
- **Rich Streaming**: 42+ event types enable better progress tracking and UI
- **Cost Optimization**: Explicit prompt caching can reduce costs for repetitive workloads
- **Future-Proof**: Official SDK auto-generated from OpenAPI spec, always up-to-date
- **Better Reasoning**: Native support for o-series models with configurable effort
- **MCP Support**: Native Model Context Protocol integration

### Negative

- **Increased Complexity**: Two implementations to maintain
- **SDK Dependency**: Additional dependency on `openai-go` SDK
- **API Differences**: Subtle differences in behavior between APIs
- **Testing Burden**: Need tests for both implementations
- **Documentation**: Users need to understand when to use which API
- **Compatibility**: OpenAI-compatible providers (x.ai, etc.) may not support Responses API

### Risks

1. **API Stability**: Responses API is newer, potential for breaking changes
2. **Provider Compatibility**: x.ai and other compatible providers may not implement Responses API
3. **Feature Drift**: Features may diverge between the two APIs over time

### Mitigations

1. **Configuration-Driven**: Easy to switch between APIs via config
2. **Feature Detection**: Gracefully fall back to Chat Completions if Responses API unavailable
3. **Abstraction**: Share code where possible (config, pricing, persistence patterns)

## Testing Strategy

### Unit Tests

```go
// thread_test.go
func TestResponsesThread_Creation(t *testing.T)
func TestResponsesThread_SendMessage_Basic(t *testing.T)
func TestResponsesThread_SendMessage_WithTools(t *testing.T)
func TestResponsesThread_MultiTurn_PreviousResponseID(t *testing.T)
func TestResponsesThread_MultiTurn_Conversation(t *testing.T)
func TestResponsesThread_Reasoning(t *testing.T)

// streaming_test.go
func TestStreamingEventHandling(t *testing.T)
func TestStreamingToolCalls(t *testing.T)
func TestStreamingReasoningEvents(t *testing.T)

// tools_test.go
func TestToolConversion(t *testing.T)
func TestBuiltInToolIntegration(t *testing.T)

// input_test.go
func TestInputBuilding(t *testing.T)
func TestImageInputHandling(t *testing.T)
```

### Integration Tests

- Test with real OpenAI API (gated by environment variable)
- Test multi-turn conversations with `previous_response_id`
- Test prompt caching behavior
- Test background job creation and polling

## Implementation Plan

### Phase 1: Foundation
1. Add `openai-go` SDK to `go.mod`
2. Create `pkg/llm/openai/responses/` package structure
3. Implement basic `ResponsesThread` with `SendMessage`
4. Add configuration support for API selection
5. Write unit tests for core functionality

### Phase 2: Feature Parity
6. Implement streaming with rich event handling
7. Add tool calling support
8. Implement multi-turn conversations
9. Add reasoning model support
10. Implement persistence

### Phase 3: Enhanced Features
11. Add explicit prompt caching support
12. Add pre-request token counting
13. Integrate built-in tools (optional)

### Phase 4: Integration
15. Update documentation
16. Add integration tests
17. Update examples and guides
18. Performance benchmarking

## References

- [OpenAI Responses API Documentation](https://platform.openai.com/docs/api-reference/responses)
- [openai-go SDK](https://github.com/openai/openai-go)
- [OpenAI API Migration Guide](https://platform.openai.com/docs/guides/responses-vs-chat-completions)
- [ADR-010: OpenAI LLM Integration](./010-openai-llm-integration.md)
- [ADR-016: OpenAI Compatible API Support](./016-openai-compatible-api-support.md)
