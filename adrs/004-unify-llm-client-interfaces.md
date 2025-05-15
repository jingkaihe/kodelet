# ADR 004: Replace Client with Thread Abstraction for LLM Interactions

## Status

Proposed

## Context

Currently, Kodelet has two similar implementations for interacting with the Anthropic API:

1. `SendMessage` in `pkg/tui/assistant.go`: Used for the interactive TUI, sending message events through a channel.
2. `Ask` in `pkg/llm/client.go`: Used for one-shot queries, printing to stdout and returning results as a string.

This duplication creates maintenance overhead and potential inconsistencies when evolving either implementation. Additionally, the current `Client` abstraction doesn't properly capture the conversational nature of interactions with Claude, where state and message history need to be maintained.

## Decision

Refactor the codebase by introducing a new `Thread` abstraction in the `pkg/llm` package that represents a conversation thread with Claude. This abstraction will:

1. Completely replace the current `Client` type
2. Support both interactive TUI and one-shot use cases through a flexible interface
3. Provide clear semantics around conversation state and history
4. Use a handler-based pattern for processing responses

## Detailed Implementation Plan

### 1. Create a new `Thread` type in `pkg/llm/thread.go`

```go
// MessageHandler defines how message events should be processed
type MessageHandler interface {
    HandleText(text string)
    HandleToolUse(toolName string, input string)
    HandleToolResult(toolName string, result string)
    HandleDone()
}

// Thread represents a conversation thread with an LLM
type Thread struct {
    client          anthropic.Client
    config          Config
    state           State
    messages        []anthropic.MessageParam
}

// NewThread creates a new conversation thread
func NewThread(config Config) *Thread {
    // Apply defaults if not provided
    if config.Model == "" {
        config.Model = anthropic.ModelClaude3_7SonnetLatest
    }
    if config.MaxTokens == 0 {
        config.MaxTokens = 8192
    }

    return &Thread{
        client: anthropic.NewClient(),
        config: config,
    }
}

// Accessors and setters for Thread
func (t *Thread) SetState(s State) {
    t.state = s
}

func (t *Thread) GetState() State {
    return t.state
}

func (t *Thread) SetMessages(messages []anthropic.MessageParam) {
    t.messages = messages
}

func (t *Thread) GetMessages() []anthropic.MessageParam {
    return t.messages
}

func (t *Thread) AddUserMessage(message string) {
    t.messages = append(t.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))
}
```

### 2. Create default implementations of `MessageHandler`

```go
// ConsoleMessageHandler prints messages to the console
type ConsoleMessageHandler struct {
    silent bool
}

// Implementation of MessageHandler for ConsoleMessageHandler
func (h *ConsoleMessageHandler) HandleText(text string) {
    if !h.silent {
        fmt.Println(text)
        fmt.Println()
    }
}

func (h *ConsoleMessageHandler) HandleToolUse(toolName string, input string) {
    if !h.silent {
        fmt.Printf("🔧 Using tool: %s: %s\n\n", toolName, input)
    }
}

func (h *ConsoleMessageHandler) HandleToolResult(toolName string, result string) {
    if !h.silent {
        fmt.Printf("🔄 Tool result: %s\n\n", result)
    }
}

func (h *ConsoleMessageHandler) HandleDone() {
    // No action needed for console handler
}

// ChannelMessageHandler sends messages through a channel (for TUI)
type ChannelMessageHandler struct {
    messageCh chan MessageEvent
}

// Implementation of MessageHandler for ChannelMessageHandler
func (h *ChannelMessageHandler) HandleText(text string) {
    h.messageCh <- MessageEvent{
        Type:    EventTypeText,
        Content: text,
    }
}

func (h *ChannelMessageHandler) HandleToolUse(toolName string, input string) {
    h.messageCh <- MessageEvent{
        Type:    EventTypeToolUse,
        Content: fmt.Sprintf("%s: %s", toolName, input),
    }
}

func (h *ChannelMessageHandler) HandleToolResult(toolName string, result string) {
    h.messageCh <- MessageEvent{
        Type:    EventTypeToolResult,
        Content: result,
    }
}

func (h *ChannelMessageHandler) HandleDone() {
    h.messageCh <- MessageEvent{
        Type:    EventTypeText,
        Content: "Done",
        Done:    true,
    }
}

// StringCollectorHandler collects text responses into a string
type StringCollectorHandler struct {
    silent bool
    text   strings.Builder
}

// Implementation of MessageHandler for StringCollectorHandler
func (h *StringCollectorHandler) HandleText(text string) {
    h.text.WriteString(text)
    h.text.WriteString("\n")

    if !h.silent {
        fmt.Println(text)
        fmt.Println()
    }
}

func (h *StringCollectorHandler) HandleToolUse(toolName string, input string) {
    if !h.silent {
        fmt.Printf("🔧 Using tool: %s: %s\n\n", toolName, input)
    }
}

func (h *StringCollectorHandler) HandleToolResult(toolName string, result string) {
    if !h.silent {
        fmt.Printf("🔄 Tool result: %s\n\n", result)
    }
}

func (h *StringCollectorHandler) HandleDone() {
    // No action needed for string collector
}

func (h *StringCollectorHandler) CollectedText() string {
    return h.text.String()
}
```

### 3. Implement the `SendMessage` method on `Thread`

```go
// SendMessage sends a user message to the thread and processes the response
func (t *Thread) SendMessage(
    ctx context.Context,
    message string,
    handler MessageHandler,
    modelOverride ...string,
) error {
    // Add the user message to history
    t.messages = append(t.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message)))

    // Main interaction loop for handling tool calls
    for {
        // Determine which model to use
        model := t.config.Model
        if len(modelOverride) > 0 && modelOverride[0] != "" {
            model = modelOverride[0]
        }

        // Send request to Anthropic API
        response, err := t.client.Messages.New(ctx, anthropic.MessageNewParams{
            MaxTokens: int64(t.config.MaxTokens),
            System: []anthropic.TextBlockParam{
                {
                    Text: sysprompt.SystemPrompt(model),
                    CacheControl: anthropic.CacheControlEphemeralParam{
                        Type: "ephemeral",
                    },
                },
            },
            Messages: t.messages,
            Model:    model,
            Tools:    tools.ToAnthropicTools(tools.Tools),
        })
        if err != nil {
            return fmt.Errorf("error sending message to Anthropic: %w", err)
        }

        // Add the assistant response to history
        t.messages = append(t.messages, response.ToParam())

        // Process the response content blocks
        toolUseCount := 0
        for _, block := range response.Content {
            switch variant := block.AsAny().(type) {
            case anthropic.TextBlock:
                handler.HandleText(variant.Text)
            case anthropic.ToolUseBlock:
                toolUseCount++
                inputJSON, _ := json.Marshal(variant.JSON.Input.Raw())
                handler.HandleToolUse(block.Name, string(inputJSON))

                // Run the tool
                output := tools.RunTool(ctx, t.state, block.Name, string(variant.JSON.Input.Raw()))
                handler.HandleToolResult(block.Name, output.String())

                // Add tool result to messages for next API call
                t.messages = append(t.messages, anthropic.NewUserMessage(
                    anthropic.NewToolResultBlock(block.ID, output.String(), false),
                ))
            }
        }

        // If no tools were used, we're done
        if toolUseCount == 0 {
            break
        }
    }

    handler.HandleDone()
    return nil
}
```

### 4. Create a convenience method for one-shot queries

```go
// SendMessageAndGetText is a convenience method for one-shot queries that returns the response as a string
func SendMessageAndGetText(ctx context.Context, state tooltypes.State, query string, config Config, silent bool, modelOverride ...string) string {
    thread := NewThread(config)
    thread.SetState(state)

    handler := &StringCollectorHandler{silent: silent}
    thread.SendMessage(ctx, query, handler, modelOverride...)
    return handler.CollectedText()
}
```

### 5. Update `AssistantClient` in TUI package to use the new `Thread`

```go
// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan MessageEvent) error {
    handler := &ChannelMessageHandler{messageCh: messageCh}

    thread := llm.NewThread(llm.Config{
        Model:     viper.GetString("model"),
        MaxTokens: viper.GetInt("max_tokens"),
    })
    thread.SetState(a.state)
    thread.SetMessages(a.messages)

    err := thread.SendMessage(ctx, message, handler)
    a.messages = thread.GetMessages() // Update messages after the call
    return err
}
```

## Migration Strategy

1. Create the new `Thread` type with the `MessageHandler` interface and default implementations
2. Implement the `SendMessage` method on `Thread` and necessary accessor methods
3. Create a convenience function `SendMessageAndGetText` for one-shot queries
4. Update all code that currently uses `Client.Ask` to use the new `Thread` or convenience function
5. Update `pkg/tui/assistant.go` to use the new `Thread` type
6. Run tests to ensure all functionality works correctly
7. Remove the deprecated `Client` type entirely

## Benefits

- **Reduced code duplication**: Core LLM interaction logic exists in one place
- **Consistency**: Ensures both interfaces behave consistently
- **Flexibility**: The handler-based approach allows for different output strategies
- **Maintainability**: Easier to update and evolve the LLM interaction logic
- **Testability**: Handlers can be mocked for better unit testing

## Consequences

- **Short-term complexity**: The refactoring will temporarily increase complexity
- **API changes**: All code using the current `Client` will need to be updated
- **Learning curve**: New pattern may require developers to understand the handler approach
- **Improved semantics**: The `Thread` name better reflects the conversation-oriented nature of the API

## Alternatives Considered

1. **Keep separate implementations**: Continue with two separate implementations
2. **Keep Client but enhance it**: Enhance the existing `Client` type instead of replacing it
3. **Use inheritance/embedding**: Have `AssistantClient` embed a common base type
4. **Event-based system**: Implement a more complex event system with registerable listeners

The proposed solution offers the cleanest approach by introducing a single, well-named abstraction that properly represents the conversational nature of LLM interactions. By completely replacing the `Client` type with `Thread`, we avoid the confusion of having two similar concepts and provide a clearer mental model for developers.
