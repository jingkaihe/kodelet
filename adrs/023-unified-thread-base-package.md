# ADR 023: Unified Thread Base Package for LLM Providers

## Status

Accepted

## Context

Kodelet supports three LLM providers: Anthropic Claude, OpenAI (including compatible APIs like X.AI Grok), and Google GenAI (Gemini/Vertex AI). Each provider has its own Thread implementation in separate packages (`pkg/llm/anthropic`, `pkg/llm/openai`, `pkg/llm/google`).

Analysis of the three implementations revealed significant code duplication:

1. **Shared Fields** (~11 identical fields):
   - Configuration (config), State, Usage tracking
   - ConversationID, isPersisted, store (persistence)
   - toolResults (structured tool results)
   - subagentContextFactory, hookTrigger
   - Mutexes (mu, conversationMu)

2. **Shared Methods** (~10 identical or near-identical methods):
   - GetState, SetState, GetConfig
   - GetConversationID, SetConversationID
   - IsPersisted, GetUsage
   - SetStructuredToolResult, GetStructuredToolResults, SetStructuredToolResults
   - shouldAutoCompact
   - createMessageSpan, finalizeMessageSpan
   - EnablePersistence

3. **Shared Constants**:
   - MaxImageFileSize (5MB)
   - MaxImageCount (10)

This duplication created several problems:
- Maintenance overhead: Bug fixes or enhancements needed to be applied three times
- Inconsistency risk: Implementations could drift apart over time
- Code review burden: Changes to shared logic required reviewing three files
- Estimated ~300 lines of duplicated code per provider

## Decision

Create a new `pkg/llm/base` package that provides a shared `Thread` struct containing common fields and methods. Provider-specific Thread implementations embed `*base.Thread` to inherit shared functionality while maintaining their own provider-specific fields and methods.

### Design Principles

1. **Composition over inheritance**: Use Go's struct embedding pattern
2. **Preserve provider flexibility**: Allow providers to add custom attributes via variadic parameters
3. **Maintain thread safety**: Keep mutex-protected methods in the base package
4. **Minimal coupling**: Avoid tight coupling to provider-specific types

### Package Structure

```
pkg/llm/
├── base/
│   ├── doc.go       # Package documentation
│   ├── base.go      # Thread struct and shared methods
│   └── base_test.go # Comprehensive unit tests
├── anthropic/
│   └── anthropic.go # Embeds *base.Thread
├── openai/
│   └── openai.go    # Embeds *base.Thread
└── google/
    └── google.go    # Embeds *base.Thread
```

### Shared Components in base.Thread

**Fields:**
- Config (llmtypes.Config)
- State (tooltypes.State)
- Usage (*llmtypes.Usage)
- ConversationID (string)
- Persisted (bool)
- Store (ConversationStore)
- ToolResults (map[string]tooltypes.StructuredToolResult)
- SubagentContextFactory (llmtypes.SubagentContextFactory)
- HookTrigger (hooks.Trigger)
- LoadConversation (callback for provider-specific loading)
- Mu, ConversationMu (sync.Mutex)

**Constants:**
- MaxImageFileSize = 5MB
- MaxImageCount = 10

**Methods:**
- State management: GetState, SetState
- Configuration: GetConfig
- Conversation: GetConversationID, SetConversationID, IsPersisted, EnablePersistence
- Usage: GetUsage (thread-safe)
- Tool results: SetStructuredToolResult, GetStructuredToolResults, SetStructuredToolResults
- Auto-compact: ShouldAutoCompact
- Tracing: CreateMessageSpan, FinalizeMessageSpan (with variadic extraAttributes)

### Provider-Specific Customization

The tracing methods accept variadic `extraAttributes` to allow providers to add their specific attributes without modifying the base package:

```go
// Anthropic-specific attributes
ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
    attribute.Int("thinking_budget_tokens", thinkingBudget),
    attribute.Bool("prompt_cache", enablePromptCache),
)

// OpenAI-specific attributes
ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
    attribute.String("reasoning_effort", reasoningEffort),
    attribute.Bool("use_copilot", useCopilot),
)

// Google-specific attributes
ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt,
    attribute.String("backend", backendName),
)
```

### LoadConversation Callback Pattern

Since conversation loading requires provider-specific message deserialization, the base package uses a callback pattern:

```go
type LoadConversationFunc func(ctx context.Context)

// Provider sets the callback during construction
baseThread.LoadConversation = func(ctx context.Context) {
    t.loadConversation(ctx) // Provider-specific implementation
}
```

This allows `EnablePersistence` to be fully implemented in the base package while delegating the actual loading to the provider.

## Implementation

The implementation was done incrementally:

1. Created `pkg/llm/base` package with Thread struct and constants
2. Added getter/setter methods
3. Added structured tool result methods (thread-safe)
4. Added ShouldAutoCompact method
5. Added tracing methods with extraAttributes pattern
6. Added EnablePersistence with callback pattern
7. Updated Anthropic to embed base.Thread
8. Updated OpenAI to embed base.Thread
9. Updated Google to embed base.Thread
10. Added comprehensive unit tests

## Consequences

### Benefits

- **Reduced duplication**: ~300 lines removed per provider
- **Single source of truth**: Shared logic maintained in one place
- **Consistent behavior**: All providers use the same implementation for common operations
- **Easier maintenance**: Bug fixes and enhancements applied once
- **Better testability**: Shared logic can be unit tested independently

### Trade-offs

- **Additional package**: Introduces a new package to understand
- **Embedding complexity**: Developers need to understand which methods come from base vs provider
- **Migration effort**: Required updating all three providers

### Risks Mitigated

- **Breaking changes**: All existing tests pass after refactoring
- **Interface compliance**: All providers verified to satisfy llmtypes.Thread interface
- **Race conditions**: All thread-safe methods verified with -race flag

## Alternatives Considered

1. **Keep separate implementations**: Continue with duplication
   - Rejected: Maintenance burden too high, inconsistency risk

2. **Interface-based abstraction**: Define interfaces for shared behavior
   - Rejected: Would require passing interface implementations, more boilerplate

3. **Code generation**: Generate common code for each provider
   - Rejected: Adds build complexity, harder to debug

4. **Single unified Thread with provider plugins**: One Thread struct with provider backends
   - Rejected: Too different from existing architecture, higher migration risk

## References

- ADR 004: Replace Client with Thread Abstraction for LLM Interactions
- ADR 010: OpenAI LLM Integration
- ADR 018: Google GenAI Integration
