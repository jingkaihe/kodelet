# ADR 018: Google GenAI Integration (Vertex AI and Gemini API)

## Status
Proposed

## Context
Kodelet currently supports Anthropic Claude and OpenAI models as LLM providers. Google offers powerful generative AI capabilities through two distinct APIs:
1. **Gemini API**: Consumer/developer-focused API with simple API key authentication
2. **Vertex AI**: Enterprise-grade platform with advanced features, data residency, and VPC-SC support

Google's Gemini models offer unique capabilities such as:
- Native multi-modal support (images, videos, audio)
- Built-in code execution
- Google Search integration  
- Thinking capability similar to Claude
- Competitive pricing and performance
- Large context windows (up to 2M tokens)

Adding Google GenAI support will provide users with:
- Access to Gemini's unique capabilities
- Choice between consumer (Gemini API) and enterprise (Vertex AI) offerings
- More competitive pricing options
- Better multi-modal capabilities

## Google GenAI SDK Overview

To understand the implementation approach, it's important to understand how the Google GenAI Go SDK works. The SDK provides a unified interface for both Gemini API and Vertex AI with the following key patterns:

### Basic Client Creation and Usage
```go
// Create client with auto-detection from environment
client, err := genai.NewClient(ctx, nil)

// Or explicitly configure backend
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    Backend: genai.BackendGeminiAPI,  // or genai.BackendVertexAI
    APIKey:  "your-api-key",          // for Gemini API
})

// Simple content generation
contents := []*genai.Content{
    genai.NewContentFromText("What is 1+1?", genai.RoleUser),
}
response, err := client.Models.GenerateContent(ctx, "gemini-2.5-pro", contents, nil)
```

### Streaming Pattern
The SDK uses Go's iterator pattern (`iter.Seq2`) for streaming:
```go
for chunk, err := range client.Models.GenerateContentStream(ctx, "gemini-2.5-pro", contents, config) {
    if err != nil {
        // Handle error
        continue
    }
    // Process streaming chunk
    for _, part := range chunk.Candidates[0].Content.Parts {
        fmt.Print(part.Text)
    }
}
```

### Chat Sessions
For multi-turn conversations:
```go
chat, err := client.Chats.Create(ctx, "gemini-2.5-pro", config, history)
for response, err := range chat.SendMessageStream(ctx, genai.Text("Continue our conversation")) {
    // Process response
}
```

### Tool/Function Calling
```go
tools := []*genai.Tool{{
    FunctionDeclarations: []*genai.FunctionDeclaration{{
        Name:        "getWeather",
        Description: "Get current weather",
        Parameters: &genai.Schema{
            Type: genai.TypeObject,
            Properties: map[string]*genai.Schema{
                "location": {Type: genai.TypeString},
            },
        },
    }},
}}

config := &genai.GenerateContentConfig{
    Tools: tools,
    ToolConfig: &genai.ToolConfig{
        FunctionCallingConfig: &genai.FunctionCallingConfig{
            Mode: genai.FunctionCallingConfigModeAuto,
        },
    },
}

// Tool calls appear in response parts as FunctionCall
for _, part := range response.Candidates[0].Content.Parts {
    if part.FunctionCall != nil {
        // Execute tool and return result
        result := executeFunction(part.FunctionCall.Name, part.FunctionCall.Args)
        // Add result back to conversation
    }
}
```

### Multi-modal Content
```go
parts := []*genai.Part{
    genai.NewPartFromText("What's in this image?"),
    genai.NewPartFromBytes(imageData, "image/jpeg"),
}
content := genai.NewContentFromParts(parts, genai.RoleUser)
```

### Provider-Specific Features
```go
// Thinking capability
config := &genai.GenerateContentConfig{
    ThinkingConfig: &genai.ThinkingConfig{
        IncludeThoughts: true,
        ThinkingBudget:  genai.Ptr[int32](8000),
    },
}

// Built-in tools
tools := []*genai.Tool{
    {CodeExecution: &genai.ToolCodeExecution{}},
    {GoogleSearch: &genai.GoogleSearch{}},
}
```

This SDK design provides a clean foundation for implementing kodelet's `Thread` interface while supporting both Gemini API and Vertex AI backends transparently.

## Decision
We will implement a new Google GenAI provider using the official `github.com/googleapis/go-genai` SDK that supports both Vertex AI and Gemini API backends through a unified interface. This implementation will:

1. Create a new package `pkg/llm/google` following the existing provider architecture
2. Implement the `Thread` interface for Google GenAI
3. Support both Vertex AI and Gemini API backends with automatic detection
4. Achieve feature parity with Anthropic and OpenAI implementations
5. Support Google-specific features (thinking, code execution, search)
6. Add comprehensive test coverage including unit and integration tests
7. Use Gemini 2.5 Pro as default model and Gemini 2.5 Flash as weak model

## Architecture Details

### Directory Structure
```
pkg/
  └── llm/
      ├── anthropic/     # Existing implementation
      ├── openai/        # Existing implementation  
      └── google/        # New implementation
          ├── google.go      # Main GoogleThread implementation
          ├── google_test.go # Tests for GoogleThread
          ├── persistence.go # Conversation persistence
          ├── persistence_test.go # Tests for persistence
          ├── tools.go       # Tool conversion utilities
          ├── tools_test.go  # Tests for tool conversion
          ├── streaming.go   # Streaming response handler
          ├── streaming_test.go # Tests for streaming
          ├── auth.go        # Authentication helpers
          ├── auth_test.go   # Tests for authentication
          └── models.go      # Model pricing and configuration
          └── models_test.go # Tests for model configuration
```

### Core Components

#### GoogleThread
The `GoogleThread` struct will implement the `llmtypes.Thread` interface, following the exact pattern of existing providers:

```go
type GoogleThread struct {
    client                 *genai.Client
    config                 llmtypes.Config
    backend                string                         // "gemini" or "vertexai"
    state                  tooltypes.State
    messages               []*genai.Content               // Google's message format
    usage                  *llmtypes.Usage
    conversationID         string
    summary                string
    isPersisted            bool
    store                  ConversationStore
    chat                   *genai.ChatSession             // For multi-turn conversations
    thinkingBudget         int32                          // Token budget for thinking
    toolResults            map[string]tooltypes.StructuredToolResult // For structured tool storage
    subagentContextFactory llmtypes.SubagentContextFactory // Cross-provider subagent support
    mu                     sync.Mutex
    conversationMu         sync.Mutex
}
```

#### Factory Pattern Integration
Following the existing factory pattern in `pkg/llm/thread.go`:

```go
func NewThread(config llmtypes.Config) (llmtypes.Thread, error) {
    config.Model = resolveModelAlias(config.Model, config.Aliases)
    
    switch strings.ToLower(config.Provider) {
    case "openai":
        return openai.NewOpenAIThread(config, NewSubagentContext)
    case "anthropic":
        return anthropic.NewAnthropicThread(config, NewSubagentContext)
    case "google":  // New case
        return google.NewGoogleThread(config, NewSubagentContext)
    default:
        return nil, errors.Errorf("unsupported provider: %s", config.Provider)
    }
}
```

#### Google Thread Constructor
```go
func NewGoogleThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (llmtypes.Thread, error) {
    // Auto-detect backend based on config and environment
    backend := detectBackend(config)
    
    clientConfig := &genai.ClientConfig{}
    
    switch backend {
    case "vertexai":
        clientConfig.Backend = genai.BackendVertexAI
        clientConfig.Project = config.Google.Project
        clientConfig.Location = config.Google.Location
        // Use ADC, service account, or API key
    case "gemini":
        clientConfig.Backend = genai.BackendGeminiAPI
        clientConfig.APIKey = config.Google.APIKey
    }
    
    client, err := genai.NewClient(context.Background(), clientConfig)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create Google GenAI client")
    }
    
    return &GoogleThread{
        client:                 client,
        config:                 config,
        backend:                backend,
        usage:                  &llmtypes.Usage{},
        toolResults:            make(map[string]tooltypes.StructuredToolResult),
        subagentContextFactory: subagentContextFactory,
        thinkingBudget:         config.Google.ThinkingBudget,
    }, nil
}
```

#### Message Conversion
Map between kodelet's message format and Google's Content/Part structure:

```go
func toGoogleContent(msg llmtypes.Message) *genai.Content {
    var parts []*genai.Part
    
    // Handle text content
    if msg.Text != "" {
        parts = append(parts, genai.NewPartFromText(msg.Text))
    }
    
    // Handle images
    for _, image := range msg.Images {
        parts = append(parts, genai.NewPartFromBytes(image.Data, image.MimeType))
    }
    
    // Handle tool calls
    if msg.ToolUse != nil {
        parts = append(parts, &genai.Part{
            FunctionCall: &genai.FunctionCall{
                Name: msg.ToolUse.Name,
                Args: msg.ToolUse.Input,
            },
        })
    }
    
    return genai.NewContentFromParts(parts, toGoogleRole(msg.Role))
}
```

#### Tool Conversion
Convert kodelet's tool format to Google's function declarations:

```go
func toGoogleTools(tools []tooltypes.Tool) []*genai.Tool {
    var googleTools []*genai.Tool
    
    for _, tool := range tools {
        schema := convertToGoogleSchema(tool.Schema)
        googleTools = append(googleTools, &genai.Tool{
            FunctionDeclarations: []*genai.FunctionDeclaration{{
                Name:        tool.Name,
                Description: tool.Description,
                Parameters:  schema,
            }},
        })
    }
    

    
    return googleTools
}
```

#### SendMessage Implementation
Following the established patterns with feedback integration, auto-compaction, and structured tool handling:

```go
func (t *GoogleThread) SendMessage(ctx context.Context, message string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    // Check for pending feedback (following existing pattern)
    if !t.config.IsSubAgent && t.conversationID != "" {
        if err := t.processPendingFeedback(ctx); err != nil {
            return "", errors.Wrap(err, "failed to process pending feedback")
        }
    }
    
    // Add user message to history
    t.AddUserMessage(ctx, message, opt.Images...)
    
    // Auto-compaction check (following existing pattern)
    if !opt.DisableAutoCompact && t.shouldAutoCompact(opt.CompactRatio) {
        if err := t.CompactContext(ctx); err != nil {
            return "", errors.Wrap(err, "failed to compact context")
        }
    }
    
    maxTurns := opt.MaxTurns
    if maxTurns == 0 {
        maxTurns = 10 // Default max turns
    }
    
    var finalOutput strings.Builder
    
    // Message exchange loop (similar to existing providers)
    for turn := 0; turn < maxTurns; turn++ {
        response, err := t.processMessageExchange(ctx, handler, opt)
        if err != nil {
            return "", err
        }
        
        finalOutput.WriteString(response.Text)
        
        // Update usage tracking
        t.updateUsage(response.Usage)
        
        // Check if we need another turn (tool calls present)
        if !t.hasToolCalls(response) {
            break
        }
        
        // Execute tools and add results
        if err := t.executeToolCalls(ctx, response, handler, opt); err != nil {
            return "", err
        }
    }
    
    handler.HandleDone()
    
    // Save conversation if enabled
    if !opt.NoSaveConversation && t.isPersisted {
        if err := t.SaveConversation(ctx, false); err != nil {
            return "", errors.Wrap(err, "failed to save conversation")
        }
    }
    
    return finalOutput.String(), nil
}

func (t *GoogleThread) processMessageExchange(ctx context.Context, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (*GoogleResponse, error) {
    config := &genai.GenerateContentConfig{
        Temperature:     t.config.Temperature,
        MaxOutputTokens: t.config.MaxTokens,
        Tools:          toGoogleTools(t.state.GetTools()),
    }
    
    // Enable thinking if supported and model supports it
    if t.supportsThinking() && !opt.UseWeakModel {
        config.ThinkingConfig = &genai.ThinkingConfig{
            IncludeThoughts: true,
            ThinkingBudget:  genai.Ptr(t.thinkingBudget),
        }
    }
    
    // Get model name (weak model override)
    modelName := t.config.Model
    if opt.UseWeakModel && t.config.WeakModel != "" {
        modelName = t.config.WeakModel
    }
    
    response := &GoogleResponse{}
    
    // Stream response using iterator pattern
    for chunk, err := range t.chat.SendMessageStream(ctx, t.buildContent()) {
        if err != nil {
            return nil, errors.Wrap(err, "streaming failed")
        }
        
        if len(chunk.Candidates) == 0 {
            continue
        }
        
        candidate := chunk.Candidates[0]
        for _, part := range candidate.Content.Parts {
            switch {
            case part.Text != "":
                if part.Thought {
                    handler.HandleThinking(part.Text)
                    response.ThinkingText += part.Text
                } else {
                    handler.HandleText(part.Text)
                    response.Text += part.Text
                }
            case part.FunctionCall != nil:
                toolCall := &GoogleToolCall{
                    ID:   generateToolCallID(),
                    Name: part.FunctionCall.Name,
                    Args: part.FunctionCall.Args,
                }
                response.ToolCalls = append(response.ToolCalls, toolCall)
                handler.HandleToolUse(toolCall.Name, marshalJSON(toolCall.Args))
            }
        }
        
        // Update usage from chunk
        if chunk.UsageMetadata != nil {
            response.Usage = convertUsage(chunk.UsageMetadata)
        }
    }
    
    // Add assistant message to history
    t.addAssistantMessage(response)
    
    return response, nil
}

func (t *GoogleThread) executeToolCalls(ctx context.Context, response *GoogleResponse, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) error {
    for _, toolCall := range response.ToolCalls {
        // Create subagent context (cross-provider support)
        runToolCtx := t.subagentContextFactory(ctx, t, handler, opt.CompactRatio, opt.DisableAutoCompact)
        
        // Execute tool
        output := tools.RunTool(runToolCtx, t.state, toolCall.Name, marshalJSON(toolCall.Args))
        
        // Use renderer registry for consistent output (following existing pattern)
        structuredResult := output.StructuredData()
        registry := renderers.NewRendererRegistry()
        renderedOutput := registry.Render(structuredResult)
        
        handler.HandleToolResult(toolCall.Name, renderedOutput)
        
        // Store structured results for persistence
        t.toolResults[toolCall.ID] = structuredResult
        
        // Add tool result to message history
        t.addToolResult(toolCall.ID, toolCall.Name, renderedOutput)
    }
    
    return nil
}
```

#### Required Thread Interface Methods
All methods from `llmtypes.Thread` interface must be implemented:

```go
// Core interface methods
func (t *GoogleThread) SetState(s tooltypes.State)                    { t.state = s }
func (t *GoogleThread) GetState() tooltypes.State                     { return t.state }
func (t *GoogleThread) Provider() string                              { return "google" }
func (t *GoogleThread) GetConfig() llmtypes.Config                    { return t.config }
func (t *GoogleThread) GetUsage() llmtypes.Usage                      { return *t.usage }

// Conversation management
func (t *GoogleThread) GetConversationID() string                     { return t.conversationID }
func (t *GoogleThread) SetConversationID(id string)                   { t.conversationID = id }
func (t *GoogleThread) IsPersisted() bool                            { return t.isPersisted }
func (t *GoogleThread) EnablePersistence(ctx context.Context, enabled bool) {
    t.isPersisted = enabled
    if enabled {
        t.store = conversations.NewSQLiteStore() // Following existing pattern
    }
}

// Message handling
func (t *GoogleThread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
    // Convert to Google's Content format and add to t.messages
    content := t.buildUserContent(message, imagePaths)
    t.messages = append(t.messages, content)
}

func (t *GoogleThread) GetMessages() ([]llmtypes.Message, error) {
    // Convert Google Content format back to llmtypes.Message
    return t.convertToStandardMessages(), nil
}

// Subagent support (cross-provider capability)
func (t *GoogleThread) NewSubAgent(ctx context.Context, config llmtypes.Config) llmtypes.Thread {
    subagentThread, err := NewGoogleThread(config, t.subagentContextFactory)
    if err != nil {
        // Handle error appropriately
        return nil
    }
    subagentThread.SetState(t.state)
    return subagentThread
}

// Auto-compaction support (following existing pattern)
func (t *GoogleThread) shouldAutoCompact(compactRatio float64) bool {
    if t.usage.MaxContextWindow == 0 {
        return false
    }
    utilizationRatio := float64(t.usage.CurrentContextWindow) / float64(t.usage.MaxContextWindow)
    return utilizationRatio >= compactRatio
}

func (t *GoogleThread) CompactContext(ctx context.Context) error {
    // 1. Generate summary using CompactPrompt
    // 2. Replace message history with summary
    // 3. Clear stale tool results
    // 4. Reset usage counters
    return t.performContextCompaction(ctx)
}

// Conversation persistence (following existing SQLite pattern)
func (t *GoogleThread) SaveConversation(ctx context.Context, summarise bool) error {
    if !t.isPersisted || t.store == nil {
        return nil
    }
    
    conv := &conversations.Conversation{
        ID:       t.conversationID,
        Messages: t.convertToStandardMessages(),
        Summary:  t.summary,
        Usage:    *t.usage,
    }
    
    if summarise {
        // Generate summary using weak model
        conv.Summary = t.generateSummary(ctx)
    }
    
    return t.store.SaveConversation(ctx, conv)
}

// Structured tool results (following existing pattern)
func (t *GoogleThread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
    t.toolResults[toolCallID] = result
}

func (t *GoogleThread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
    return t.toolResults
}
```

### Model Pricing and Configuration

Based on current Vertex AI pricing for Gemini 2.5 models:

```go
type ModelPricing struct {
    Input            float64 // Per token cost for input
    InputHigh        float64 // Per token cost for input >200K tokens (Pro only)
    Output           float64 // Per token cost for output
    OutputHigh       float64 // Per token cost for output >200K tokens (Pro only)
    AudioInput       float64 // Per token cost for audio input (Flash/Flash Lite)
    ContextWindow    int     // Maximum context window size
    HasThinking      bool    // Supports thinking capability
    TieredPricing    bool    // Has different pricing tiers
    HighTierThreshold int    // Token threshold for high tier pricing
}

var ModelPricingMap = map[string]ModelPricing{
    // Gemini 2.5 Pro - Tiered pricing based on input tokens
    "gemini-2.5-pro": {
        Input:             0.00125,  // $1.25 per 1M tokens (<=200K input)
        InputHigh:         0.0025,   // $2.50 per 1M tokens (>200K input)
        Output:            0.01,     // $10 per 1M tokens (<=200K input)
        OutputHigh:        0.015,    // $15 per 1M tokens (>200K input)
        ContextWindow:     2_097_152, // 2M tokens
        TieredPricing:     true,
        HighTierThreshold: 200_000,  // 200K tokens
    },
    
    // Gemini 2.5 Flash - Standard multi-modal model
    "gemini-2.5-flash": {
        Input:         0.0003,   // $0.30 per 1M tokens (text, image, video)
        AudioInput:    0.001,    // $1 per 1M tokens (audio)
        Output:        0.0025,   // $2.50 per 1M tokens
        ContextWindow: 1_048_576, // 1M tokens
    },
    
    // Gemini 2.5 Flash Lite - Lightweight model
    "gemini-2.5-flash-lite": {
        Input:         0.0001,   // $0.10 per 1M tokens (text, image, video)
        AudioInput:    0.0003,   // $0.30 per 1M tokens (audio)
        Output:        0.0004,   // $0.40 per 1M tokens
        ContextWindow: 1_048_576, // 1M tokens
    },
}

// Calculate actual cost based on usage and model pricing
func (t *GoogleThread) calculateCost(inputTokens, outputTokens int, hasAudio bool) (inputCost, outputCost float64) {
    pricing, exists := ModelPricingMap[t.config.Model]
    if !exists {
        return 0, 0
    }
    
    // Handle tiered pricing for Pro models
    if pricing.TieredPricing && inputTokens > pricing.HighTierThreshold {
        lowTierTokens := pricing.HighTierThreshold
        highTierTokens := inputTokens - pricing.HighTierThreshold
        
        inputCost = (float64(lowTierTokens) * pricing.Input) + (float64(highTierTokens) * pricing.InputHigh)
        
        // Output pricing also depends on input token count
        if inputTokens <= pricing.HighTierThreshold {
            outputCost = float64(outputTokens) * pricing.Output
        } else {
            outputCost = float64(outputTokens) * pricing.OutputHigh
        }
    } else {
        // Standard pricing
        if hasAudio && pricing.AudioInput > 0 {
            inputCost = float64(inputTokens) * pricing.AudioInput
        } else {
            inputCost = float64(inputTokens) * pricing.Input
        }
        outputCost = float64(outputTokens) * pricing.Output
    }
    
    return inputCost, outputCost
}

// Additional Google-specific pricing considerations:
// - Batch API: 50% of standard pricing
// - Grounding with Google Search: 1,500 free grounded prompts/day, then $35 per 1,000 prompts
// - Grounding with Google Maps: 1,500 free grounded prompts/day, then $25 per 1,000 prompts
```

### Configuration Support

#### Environment Variables
```bash
# Backend selection
GOOGLE_GENAI_USE_VERTEXAI=true  # Use Vertex AI backend

# Gemini API
GOOGLE_API_KEY=your-api-key
# or
GEMINI_API_KEY=your-api-key

# Vertex AI
GOOGLE_CLOUD_PROJECT=your-project
GOOGLE_CLOUD_LOCATION=us-central1
GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json

# Provider selection
KODELET_PROVIDER=google
KODELET_MODEL=gemini-2.5-pro
```

#### Config File
Following the existing configuration structure in `llmtypes.Config`:

```yaml
provider: google
model: gemini-2.5-pro
weak_model: gemini-2.5-flash
max_tokens: 8192
weak_model_max_tokens: 4096
temperature: 0.7

# Google-specific settings (added to llmtypes.Config)
google:
  backend: gemini                   # or vertexai (auto-detected if not specified)
  api_key: ${GOOGLE_API_KEY}        # For Gemini API
  project: your-project             # For Vertex AI
  location: us-central1             # For Vertex AI
  thinking_budget: 8000             # Token budget for thinking


# Subagent configuration (existing pattern)
subagent:
  provider: google                  # Can be different from main provider
  model: gemini-2.5-flash
  max_tokens: 4096

# Model aliases (existing pattern)
aliases:
  smart: gemini-2.5-pro
  fast: gemini-2.5-flash
  
# Retry configuration (following OpenAI pattern)
retry:
  max_attempts: 3
  delay: 1s
  backoff: exponential
```

### Feature Parity Matrix

| Feature | Anthropic | OpenAI | Google GenAI |
|---------|-----------|--------|--------------|
| **Text Generation** | ✓ | ✓ | ✓ |
| **Streaming** | ✓ | ✓ | ✓ (iter.Seq2) |
| **Tool Calling** | ✓ | ✓ | ✓ |
| **Multi-modal** | ✓ (images) | ✓ (images) | ✓ (images, video, audio) |
| **Conversation Persistence** | ✓ | ✓ | ✓ |
| **Token Tracking** | ✓ | ✓ | ✓ |
| **Thinking/Reasoning** | ✓ (thinking) | ✓ (reasoning effort) | ✓ (thinking) |
| **Context Caching** | ✓ | - | ✓ |
| **Subagent Support** | ✓ | ✓ | ✓ |
| **Code Execution** | - | - | ✓ (unique) |
| **Web Search** | - | - | ✓ (unique) |

### Testing Strategy

#### Unit Tests

**google_test.go**: Core GoogleThread functionality
```go
func TestGoogleThread_Creation(t *testing.T) {
    // Test thread creation with different backends
}

func TestGoogleThread_SendMessage_Basic(t *testing.T) {
    // Test basic message sending
}

func TestGoogleThread_InterfaceCompliance(t *testing.T) {
    // Test that GoogleThread implements llmtypes.Thread interface
}

func TestGoogleThread_UsageTracking(t *testing.T) {
    // Test token usage tracking and cost calculation
}
```

**auth_test.go**: Authentication and backend detection
```go
func TestBackendDetection(t *testing.T) {
    // Test automatic backend detection logic
}

func TestGeminiAPIAuth(t *testing.T) {
    // Test Gemini API authentication
}

func TestVertexAIAuth(t *testing.T) {
    // Test Vertex AI authentication (ADC, service account)
}
```

**tools_test.go**: Tool conversion and execution
```go
func TestToolConversion(t *testing.T) {
    // Test tool schema conversion to Google format
}

func TestGoogleSpecificTools(t *testing.T) {
    // Test code execution and search tool integration
}

func TestToolExecution(t *testing.T) {
    // Test tool calling and result handling
}
```

**streaming_test.go**: Streaming and message handling
```go
func TestStreamingResponse(t *testing.T) {
    // Test iterator-based streaming
}

func TestMessageConversion(t *testing.T) {
    // Test bidirectional message format conversion
}

func TestThinkingCapability(t *testing.T) {
    // Test thinking feature integration
}
```

**persistence_test.go**: Conversation management
```go
func TestConversationPersistence(t *testing.T) {
    // Test conversation saving and loading
}

func TestContextCompaction(t *testing.T) {
    // Test auto-compaction functionality
}

func TestSubagentCreation(t *testing.T) {
    // Test cross-provider subagent support
}
```

**models_test.go**: Model configuration and pricing
```go
func TestModelPricing(t *testing.T) {
    // Test pricing calculation including tiered pricing
}

func TestTieredPricingCalculation(t *testing.T) {
    // Test complex pricing for Pro models
}

func TestAudioPricingCalculation(t *testing.T) {
    // Test audio input pricing calculation
}
```

#### Integration Tests
```go
func TestGoogleThread_SendMessage_Gemini(t *testing.T) {
    // Test with real Gemini API backend
}

func TestGoogleThread_SendMessage_VertexAI(t *testing.T) {
    // Test with real Vertex AI backend
}

func TestGoogleThread_MultiModal(t *testing.T) {
    // Test image, video, audio input handling
}

func TestGoogleThread_CrossProviderSubagent(t *testing.T) {
    // Test subagent creation with different providers
}
```

#### Acceptance Tests
Add Google provider tests to existing acceptance test suite:
- Basic conversation flow
- Tool execution
- Multi-turn conversations
- Conversation persistence and resume
- Subagent creation
- Error handling

### Implementation Challenges & Solutions

1. **Iterator-based Streaming API**
   - Challenge: Google uses Go's new `iter.Seq2` pattern instead of channels
   - Solution: Wrap iterator in handler dispatch loop

2. **Dual Backend Support**
   - Challenge: Supporting both Vertex AI and Gemini API
   - Solution: Abstract backend differences behind unified interface

3. **Message Format Differences**
   - Challenge: Google uses Content/Part structure vs flat messages
   - Solution: Bidirectional conversion utilities

4. **Authentication Complexity**
   - Challenge: Multiple auth methods (API key, ADC, service account)
   - Solution: Auto-detection with explicit override options

5. **Provider-Specific Features**
   - Challenge: Features like code execution not available in other providers
   - Solution: Optional configuration flags with graceful fallback

## Consequences

### Positive
- Users gain access to Google's competitive models
- Choice between consumer and enterprise offerings
- Unique features like code execution and search
- Better multi-modal support (video, audio)
- More competitive pricing options
- Improved model diversity reduces provider lock-in

### Negative
- Increased complexity with three provider implementations
- More configuration options to document and support
- Need to handle provider-specific feature differences
- Additional testing burden
- Potential for feature fragmentation

## Implementation Plan

### Phase 1: Foundation & Core Implementation (Week 1)
1. **Configuration Updates**:
   - Add `Google` struct to `llmtypes.Config`
   - Update factory pattern in `pkg/llm/thread.go`
   - Add environment variable support
   - Update configuration validation

2. **Basic GoogleThread Implementation**:
   - Create `pkg/llm/google` package structure with organized file layout
   - `google.go`: Implement `GoogleThread` struct with all required fields
   - `auth.go`: Add backend detection and client initialization
   - `models.go`: Model pricing configuration and cost calculation
   - Add constructor `NewGoogleThread` with dependency injection

3. **Message Conversion Layer**:
   - `streaming.go`: Implement bidirectional conversion between `llmtypes.Message` and `*genai.Content`
   - Handle multi-modal content (images, text, audio)
   - Add proper role mapping and iterator-based streaming

4. **Basic Unit Tests** (following `foo.go`/`foo_test.go` convention):
   - `google_test.go`: Thread creation and basic functionality
   - `auth_test.go`: Backend detection and authentication
   - `models_test.go`: Model configuration and pricing logic

### Phase 2: Feature Implementation & Advanced Features (Week 2-3)
5. **SendMessage Implementation**:
   - Implement complete message exchange loop
   - Add streaming response handling with iterator pattern
   - Integrate feedback system check (following existing pattern)
   - Add auto-compaction logic

6. **Tool Integration**:
   - `tools.go`: Implement tool schema conversion to Google format
   - Add tool execution with structured results storage
   - Integrate renderer registry for consistent output
   - Support Google-specific tools (code execution, search)

7. **Thread Interface Completion**:
   - `google.go`: Implement all required `llmtypes.Thread` methods
   - `persistence.go`: Add conversation persistence with SQLite integration
   - Implement subagent creation with cross-provider support
   - Add usage tracking and context window management

8. **Vertex AI Backend Support**:
   - Add authentication helpers for ADC and service accounts
   - Implement backend-specific client configuration
   - Add error handling for different auth methods

9. **Provider-Specific Features**:
   - Implement thinking capability support
   - Add context caching integration
   - Support optional code execution and search tools
   - Handle model-specific capabilities

10. **Context Management**:
    - Implement auto-compaction following existing patterns
    - Add proper context window tracking
    - Handle large context windows (2M+ tokens)

11. **Error Handling & Resilience**:
    - Add comprehensive error handling
    - Implement retry logic with backoff
    - Handle API rate limits and quota issues
    - Add graceful degradation for optional features

12. **Testing Infrastructure**:
    - Complete unit test coverage following `foo.go`/`foo_test.go` pattern
    - `streaming_test.go`: Test iterator-based streaming and message handling
    - `tools_test.go`: Test tool conversion and Google-specific tools
    - `persistence_test.go`: Test conversation management and compaction
    - Add integration tests for both Gemini and Vertex AI backends
    - Test cross-provider subagent functionality
    - Test conversation management features
    - Add mock implementations for testing

### Phase 3: Documentation & Integration (Week 4)
13. **Documentation & Examples**:
    - Update user documentation with Google setup instructions
    - Add configuration examples for both Gemini API and Vertex AI backends
    - Document unique Google features (thinking, code execution, search)
    - Update CLI help text and error messages
    - Add troubleshooting guide for common authentication issues

14. **Integration Updates**:
    - Update AGENTS.md with Google-specific patterns and commands
    - Add migration examples from other providers
    - Update build system dependencies (`go.mod`, `go.sum`)
    - Validate existing kodelet commands work with Google provider

15. **Final Validation**:
    - Test with existing kodelet commands (run, chat, etc.)
    - Validate fragment/recipe compatibility
    - Verify cost tracking accuracy

## Success Criteria

1. **Feature Parity**: All existing features work with Google provider
2. **Test Coverage**: >80% test coverage for new code
3. **Performance**: Response latency comparable to other providers
4. **Documentation**: Complete user and developer documentation
5. **Backend Support**: Both Vertex AI and Gemini API fully functional
6. **Unique Features**: At least thinking capability implemented
7. **Error Handling**: Graceful handling of provider-specific errors

## References

- [Google GenAI Go SDK Documentation](https://github.com/googleapis/go-genai)
- [Gemini API Documentation](https://ai.google.dev/gemini-api/docs)
- [Vertex AI Documentation](https://cloud.google.com/vertex-ai/generative-ai/docs)
- [Gemini Models Overview](https://ai.google.dev/gemini-api/docs/models/gemini)
- [Vertex AI Pricing](https://cloud.google.com/vertex-ai/generative-ai/pricing)