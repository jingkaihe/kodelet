# ADR 025: Recipe Hooks for Context Compaction

## Status
Proposed

## Context

Kodelet currently supports automatic context compaction triggered when the context window utilization exceeds a configurable threshold (default 80%). However, there is no way to manually trigger compaction via:
- CLI command (`kodelet run -r compact`)
- ACP slash command (`/compact`)

**Current Implementation:**
- Auto-compact is checked in each provider's `SendMessage` loop (`ShouldAutoCompact()`)
- `CompactContext()` creates a summary thread, generates a summary, and **replaces the message history**
- The compaction logic is tightly coupled to provider implementations (Anthropic, OpenAI, Google)
- Kodelet has an existing **hooks system** (ADR 021) for lifecycle events: `before_tool_call`, `after_tool_call`, `user_message_send`, `agent_stop`

**Problem Statement:**
1. Users cannot manually compact context when they know it would be beneficial
2. The compaction prompt (`prompts.CompactPrompt`) is hardcoded, not customizable
3. Recipes/fragments currently only produce prompts—they cannot perform state mutations like message swapping
4. Existing hooks don't have a suitable event for post-turn operations that allow conversation continuation

**Goals:**
1. Enable manual context compaction via `kodelet run -r compact` and `/compact` slash command
2. Extract the compaction prompt into a recipe for customizability
3. Maintain the existing auto-compact behavior
4. Leverage and extend the existing hooks system rather than introducing a new abstraction

## Decision

Extend the **Recipe Metadata** to support **built-in hook handlers** that execute at lifecycle events. This requires introducing a new hook event `turn_end` that fires after each LLM response, allowing post-processing before the conversation continues.

### Key Design Decisions

1. **New hook event `turn_end`**: Fires after each assistant response, before the next user message
2. **Hook declaration in recipe metadata**: Recipes declare hooks via YAML frontmatter field `hooks`
3. **Built-in handlers complement external hooks**: Internal handlers (like `swap_context`) run alongside external hook scripts
4. **Handler receives assistant response**: The built-in handler receives the final assistant message as input
5. **`once` parameter**: Hooks can be configured to execute only on the first turn, preventing repeated execution in follow-up conversations
6. **Same mechanism for CLI and ACP**: Both `kodelet run -r compact` and `/compact` use the same hook system

### Why `turn_end` Instead of `agent_stop`

| Hook Event | When it fires | Can continue? | Use case |
|------------|---------------|---------------|----------|
| `agent_stop` | End of conversation | No | Cleanup, logging, final actions |
| `turn_end` | After each LLM response | **Yes** | Context manipulation, validation |

For compaction, we need:
1. LLM generates summary
2. Hook fires and swaps context
3. **Conversation continues** with compacted history

`agent_stop` fires too late—the conversation is already ending. `turn_end` fires at the right moment.

### Recipe-Based Compact Flow

```
User triggers compact (CLI or ACP)
    ↓
Load compact recipe (contains CompactPrompt + hooks config)
    ↓
LLM generates comprehensive summary
    ↓
turn_end lifecycle event fires:
  - External hooks execute (if any)
  - Built-in handler `swap_context` executes:
    - Takes assistant response (the summary)
    - Replaces thread messages with summary as single user message
    - Clears stale tool results and file access tracking
    ↓
Conversation continues with compacted context
```

## Architecture Overview

### New Hook Type

Extend the hooks system with `turn_end`:

```go
// pkg/hooks/hooks.go
const (
    HookTypeBeforeToolCall  HookType = "before_tool_call"
    HookTypeAfterToolCall   HookType = "after_tool_call"
    HookTypeUserMessageSend HookType = "user_message_send"
    HookTypeAgentStop       HookType = "agent_stop"
    HookTypeTurnEnd         HookType = "turn_end"  // NEW
)
```

### Turn End Payload

```go
// pkg/hooks/payload.go

// TurnEndPayload is sent when an assistant turn completes
type TurnEndPayload struct {
    BasePayload
    Response string `json:"response"` // The assistant's response text
    TurnNumber int   `json:"turn_number"` // Which turn in the conversation
}

// TurnEndResult allows hooks to modify behavior
type TurnEndResult struct {
    // Future: could support response modification, turn cancellation, etc.
}
```

### Recipe Metadata Extension

Extend `fragments.Metadata` to support hooks with configuration:

```go
// pkg/fragments/fragments.go
type Metadata struct {
    Name            string            `yaml:"name,omitempty"`
    Description     string            `yaml:"description,omitempty"`
    AllowedTools    []string          `yaml:"allowed_tools,omitempty"`
    AllowedCommands []string          `yaml:"allowed_commands,omitempty"`
    Defaults        map[string]string `yaml:"defaults,omitempty"`
    Hooks           map[string]HookConfig `yaml:"hooks,omitempty"` // NEW: Lifecycle hooks -> handler config
}

// HookConfig defines configuration for a recipe hook
type HookConfig struct {
    Handler string `yaml:"handler"`          // Built-in handler name (e.g., "swap_context")
    Once    bool   `yaml:"once,omitempty"`   // If true, only execute on the first turn
}
```

### Compact Recipe

Create a built-in `compact` recipe at `pkg/fragments/recipes/compact.md`:

```markdown
---
name: compact
description: Compact the conversation context into a comprehensive summary
hooks:
  turn_end:
    handler: swap_context
    once: true
allowed_tools: []
---
Create a comprehensive summary of the conversation history that preserves all essential context for continued development work.

Please create a conversation summary following the steps below:

1. Review the entire conversation history thoroughly to understand:
   - The user's primary objectives and detailed requirements
   - All technical decisions and implementations discussed
   - Problems encountered and solutions applied
   - Current state of work and what remains to be done

[... rest of CompactPrompt ...]
```

**Notes**:
- `once: true` ensures the handler only executes on the first turn, not on subsequent turns in the follow-up conversation
- `allowed_tools: []` ensures the model generates the summary in a single turn without tool calls

### Built-in Hook Handler Interface

Extend the hooks package to support built-in handlers:

```go
// pkg/hooks/builtin.go
package hooks

import (
    "context"
    
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// BuiltinHandler defines the interface for built-in hook handlers
// These are internal handlers that can be referenced by recipes
type BuiltinHandler interface {
    // Name returns the handler identifier used in recipe metadata
    Name() string
    
    // HandleTurnEnd is called when turn_end event fires
    // - ctx: context for cancellation
    // - thread: the LLM thread to operate on
    // - response: the assistant's response for this turn
    HandleTurnEnd(ctx context.Context, thread llmtypes.Thread, response string) error
}

// BuiltinRegistry holds registered built-in handlers
type BuiltinRegistry struct {
    handlers map[string]BuiltinHandler
}

// DefaultBuiltinRegistry returns registry with default handlers
func DefaultBuiltinRegistry() *BuiltinRegistry {
    r := &BuiltinRegistry{
        handlers: make(map[string]BuiltinHandler),
    }
    r.Register(&SwapContextHandler{})
    return r
}

// Register adds a handler to the registry
func (r *BuiltinRegistry) Register(h BuiltinHandler) {
    r.handlers[h.Name()] = h
}

// Get retrieves a handler by name
func (r *BuiltinRegistry) Get(name string) (BuiltinHandler, bool) {
    h, ok := r.handlers[name]
    return h, ok
}
```

### SwapContext Handler Implementation

```go
// pkg/hooks/swap_context.go
package hooks

import (
    "context"

    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    "github.com/pkg/errors"
)

// SwapContextHandler replaces thread messages with the provided summary
type SwapContextHandler struct{}

func (h *SwapContextHandler) Name() string {
    return "swap_context"
}

func (h *SwapContextHandler) HandleTurnEnd(ctx context.Context, thread llmtypes.Thread, response string) error {
    // Thread must implement ContextSwapper interface
    swapper, ok := thread.(ContextSwapper)
    if !ok {
        return errors.New("thread does not support context swapping")
    }
    
    return swapper.SwapContext(ctx, response)
}

// ContextSwapper is implemented by threads that support context replacement
// Defined in pkg/hooks/, implemented by each provider
type ContextSwapper interface {
    // SwapContext replaces the current message history with a summary
    SwapContext(ctx context.Context, summary string) error
}
```

### SwapContext Provider Implementation

Each provider implements the `ContextSwapper` interface:

```go
// pkg/llm/anthropic/anthropic.go

// SwapContext replaces the conversation history with a summary message
func (t *Thread) SwapContext(ctx context.Context, summary string) error {
    t.Mu.Lock()
    defer t.Mu.Unlock()

    t.messages = []anthropic.MessageParam{
        {
            Role: anthropic.MessageParamRoleUser,
            Content: []anthropic.ContentBlockParamUnion{
                anthropic.NewTextBlock(summary),
            },
        },
    }

    // Clear stale tool results - they reference tool calls that no longer exist
    t.ToolResults = make(map[string]tooltypes.StructuredToolResult)

    // Clear file access tracking to start fresh with context retrieval
    if t.State != nil {
        t.State.SetFileLastAccess(make(map[string]time.Time))
    }

    return nil
}
```

Similar implementations for OpenAI and Google providers.

### Trigger Integration

The `turn_end` hook is triggered from within each provider's `SendMessage` loop, consistent with other hooks:

```go
// pkg/llm/anthropic/anthropic.go (inside SendMessage loop)

// After assistant response is complete, trigger turn_end
if finalOutput != "" {
    t.HookTrigger.TriggerTurnEnd(ctx, t, finalOutput, turnNumber, t.RecipeHooks)
}
```

The provider tracks `turnNumber` and increments it after each assistant response. The `RecipeHooks` field stores the hooks configuration set via `SetRecipeHooks()`.

The `Trigger` method handles both external hooks and built-in handlers:

```go
// pkg/hooks/trigger.go

// TriggerTurnEnd invokes turn_end hooks including built-in handlers
func (t Trigger) TriggerTurnEnd(
    ctx context.Context,
    thread llmtypes.Thread,
    response string,
    turnNumber int,
    recipeHooks map[string]fragments.HookConfig,
) {
    // First, execute external hooks (if any)
    if t.Manager.HasHooks(HookTypeTurnEnd) {
        payload := TurnEndPayload{
            BasePayload: BasePayload{
                Event:     HookTypeTurnEnd,
                ConvID:    t.ConversationID,
                CWD:       t.getCwd(ctx),
                InvokedBy: t.invokedBy(),
            },
            Response:   response,
            TurnNumber: turnNumber,
        }
        t.Manager.ExecuteTurnEnd(ctx, payload)
    }
    
    // Then, execute built-in handler if specified in recipe
    if hookConfig, ok := recipeHooks["turn_end"]; ok {
        // Skip if once=true and not the first turn
        if hookConfig.Once && turnNumber > 1 {
            return
        }
        
        registry := DefaultBuiltinRegistry()
        if handler, exists := registry.Get(hookConfig.Handler); exists {
            if err := handler.HandleTurnEnd(ctx, thread, response); err != nil {
                logger.G(ctx).WithError(err).Error("built-in handler failed")
            }
        }
    }
}
```

### Passing Recipe Hooks to Thread

Recipe hooks metadata is passed to the thread so providers can trigger built-in handlers:

```go
// pkg/types/llm/thread.go - extend Thread interface or base struct
type Thread interface {
    // ... existing methods ...
    SetRecipeHooks(hooks map[string]fragments.HookConfig)
}

// Set by run/chat/ACP before SendMessage
thread.SetRecipeHooks(fragmentMetadata.Hooks)
```

### Refactoring CompactContext

The existing `CompactContext()` method should load the prompt from the compact recipe to stay DRY:

```go
// pkg/llm/anthropic/anthropic.go
func (t *Thread) CompactContext(ctx context.Context) error {
    // Load compact prompt from recipe (single source of truth)
    compactPrompt, err := fragments.LoadCompactPrompt(ctx)
    if err != nil {
        return errors.Wrap(err, "failed to load compact prompt")
    }

    // Create summary thread (existing logic)
    summaryThread, err := NewAnthropicThread(t.GetConfig(), nil)
    if err != nil {
        return errors.Wrap(err, "failed to create summary thread")
    }
    summaryThread.messages = t.messages
    summaryThread.EnablePersistence(ctx, false)
    summaryThread.HookTrigger = hooks.Trigger{}

    // Generate summary using recipe prompt
    handler := &llmtypes.StringCollectorHandler{Silent: true}
    _, err = summaryThread.SendMessage(ctx, compactPrompt, handler, llmtypes.MessageOpt{
        UseWeakModel:       false,
        NoToolUse:          true,
        DisableAutoCompact: true,
        DisableUsageLog:    true,
        NoSaveConversation: true,
    })
    if err != nil {
        return errors.Wrap(err, "failed to generate compact summary")
    }

    // Use SwapContext for the actual replacement
    return t.SwapContext(ctx, handler.CollectedText())
}
```

### Helper to Load Compact Prompt

Add a helper function in the fragments package:

```go
// pkg/fragments/fragments.go

// LoadCompactPrompt loads the compact recipe and returns just the prompt content.
// This allows CompactContext() to reuse the same prompt as manual compaction.
func LoadCompactPrompt(ctx context.Context) (string, error) {
    processor, err := NewFragmentProcessor()
    if err != nil {
        return "", err
    }
    
    fragment, err := processor.LoadFragment(ctx, &Config{
        FragmentName: "compact",
    })
    if err != nil {
        return "", err
    }
    
    return fragment.Content, nil
}
```

This ensures:
1. **Single source of truth**: The compact prompt lives only in the recipe file
2. **Consistency**: Both auto-compact and manual compact use the same prompt
3. **Customizability**: Users can override the compact recipe to change both behaviors

## Implementation Phases

### Phase 1: New Hook Type
- [ ] Add `HookTypeTurnEnd` constant to `pkg/hooks/hooks.go`
- [ ] Add `TurnEndPayload` and `TurnEndResult` to `pkg/hooks/payload.go`
- [ ] Add `ExecuteTurnEnd()` to `HookManager`
- [ ] Add `TriggerTurnEnd()` to `hooks.Trigger` with `once` support
- [ ] Add `turn_end` trigger call inside each provider's `SendMessage` loop
- [ ] Track `turnNumber` in providers for `once` functionality
- [ ] Update `docs/HOOKS.md` with new hook type documentation

### Phase 2: Core Infrastructure
- [ ] Add `ContextSwapper` interface to `pkg/hooks/`
- [ ] Add `SetRecipeHooks(map[string]HookConfig)` to Thread interface
- [ ] Add `RecipeHooks` field to base thread struct
- [ ] Implement `SwapContext()` for all providers (Anthropic, OpenAI, Google)
- [ ] Refactor `CompactContext()` to use `SwapContext()`
- [ ] Add `BuiltinHandler` interface and `BuiltinRegistry` to `pkg/hooks/`
- [ ] Implement `SwapContextHandler`
- [ ] Add unit tests for swap context and built-in handlers

### Phase 3: Recipe Integration
- [ ] Add `HookConfig` struct to `pkg/fragments/fragments.go`
- [ ] Extend `fragments.Metadata` with `Hooks` field (`map[string]HookConfig`)
- [ ] Update frontmatter parsing to handle nested hook config
- [ ] Create built-in `compact` recipe in `pkg/fragments/recipes/`
- [ ] Add `LoadCompactPrompt()` helper function to fragments package
- [ ] Remove `CompactPrompt` from `pkg/llm/prompts/` (now in recipe)
- [ ] Update `CompactContext()` to use `fragments.LoadCompactPrompt()`
- [ ] Add integration tests for recipe with hooks

### Phase 4: CLI Integration
- [ ] Call `thread.SetRecipeHooks()` in run/chat commands when using recipes
- [ ] Update CLI help and documentation

### Phase 5: ACP Integration
- [ ] Modify `pkg/acp/server.go` to support recipe hooks for slash commands
- [ ] Pass fragment metadata through session handling
- [ ] Add `/compact` to advertised slash commands

### Phase 6: Documentation
- [ ] Update `docs/FRAGMENTS.md` with recipe hooks documentation
- [ ] Update `docs/HOOKS.md` with `turn_end` and built-in handler documentation
- [ ] Update `docs/MANUAL.md` with compact command usage
- [ ] Add examples for custom compact recipes

## Usage Examples

### CLI Usage

```bash
# Manual compact with default prompt
kodelet run -r compact --follow

# Manual compact with custom instructions
kodelet run -r compact --follow "Focus on the kubernetes deployment changes"

# Continue conversation after compact
kodelet run --follow "Now implement the next feature"
```

### ACP Slash Command

```
/compact
```

The IDE will execute the compact recipe and swap the context automatically.

### Custom Compact Recipe

Users can create custom compact recipes with different summarization strategies:

```markdown
---
name: compact-brief
description: Brief context compaction for quick summaries
hooks:
  turn_end:
    handler: swap_context
    once: true
allowed_tools: []
---
Create a brief summary of this conversation in 3-5 bullet points:
- What was the user's main goal?
- What key actions were taken?
- What is the current state?

Keep it concise and actionable.
```

## Future Considerations

### Additional Built-in Handlers

The built-in handler system can be extended for other use cases:

| Handler | Hook Event | Use Case |
|---------|-----------|----------|
| `swap_context` | `turn_end` | Replace message history with summary |
| `append_context` | `turn_end` | Add structured context to the conversation |
| `export_markdown` | `turn_end` | Export conversation to markdown file |
| `validate_output` | `turn_end` | Validate assistant response against schema |

### Handlers for Other Hook Events

Future enhancement could support handlers for other lifecycle events:

```yaml
hooks:
  before_tool_call:
    handler: log_tool_usage
  after_tool_call:
    handler: validate_tool_output
  turn_end:
    handler: swap_context
    once: true
```

### Handler Parameters

Handlers could accept additional parameters from recipe metadata:

```yaml
hooks:
  turn_end:
    handler: swap_context
    once: true
    params:
      preserve_system_prompt: true
      max_summary_tokens: 4000
```

### External + Built-in Hook Composition

The current design runs external hooks first, then built-in handlers. Future enhancement could allow explicit ordering:

```yaml
hooks:
  turn_end:
    handlers:
      - external  # Run external hooks first
      - swap_context  # Then built-in handler
    once: true
```

## Testing Strategy

### Unit Tests
1. **TurnEnd hook type**: Payload serialization, execution flow
2. **BuiltinRegistry**: Register, retrieve, unknown handler handling
3. **SwapContext**: Message replacement, tool result clearing, state cleanup
4. **HookConfig parsing**: Handler and once field extraction from frontmatter
5. **TriggerTurnEnd**: External hooks + built-in handler execution order
6. **Once parameter**: Handler skipped on turn > 1 when once=true

### Integration Tests
1. **Recipe with hooks**: End-to-end compact recipe execution
2. **CLI hook flow**: `kodelet run -r compact` full flow
3. **ACP hook flow**: `/compact` slash command handling
4. **External + built-in combination**: Both hook types work together
5. **Conversation continuation**: After compact, subsequent messages work correctly
6. **Once behavior**: Hook only fires on first turn, not subsequent turns

### Acceptance Tests
1. **Manual compact preserves intent**: After compact, model can continue coherently
2. **Custom compact recipes**: User-defined recipes work correctly
3. **Cross-provider consistency**: Built-in handlers work identically across Anthropic/OpenAI/Google

## Conclusion

The Recipe Hooks approach provides a clean, extensible mechanism for context compaction that:

1. **Introduces `turn_end` hook**: New lifecycle event that fires after each turn, allowing conversation to continue
2. **Leverages existing infrastructure**: Extends the hooks system rather than creating parallel abstractions
3. **Separates concerns**: Summarization prompt (recipe) vs. state mutation (built-in handler)
4. **Enables customization**: Users can create custom compact recipes with different prompts
5. **Unifies entry points**: CLI and ACP use the same mechanism
6. **Maintains determinism**: Built-in handlers always execute when declared
7. **Future-proof**: Handler system can support additional operations and hook events

The implementation extends existing infrastructure (hooks, recipes, providers) rather than introducing parallel concepts, making it a natural extension of Kodelet's architecture.
