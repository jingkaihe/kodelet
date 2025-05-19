# ADR 0005: ToolResult Interface Refactoring

## Status

Proposed

## Context

Currently, Kodelet uses a concrete `ToolResult` struct to represent the result of tool executions:

```go
type ToolResult struct {
    Result string `json:"result"`
    Error  string `json:"error"`
}
```

This struct has a `String()` method that formats the output with result and error tags. The current implementation has several limitations:

1. It uses the same string representation for both LLM communication and user display, which may not be optimal for both use cases
2. Tools cannot provide specialized formatting for different consumers (LLM vs user)
3. The error detection is implicit (checking if the Error field is non-empty)
4. JSON marshaling/unmarshaling is handled generically

As the project evolves, we need more flexibility in how tool results are represented, displayed, and processed.

## Decision

We will refactor `ToolResult` to be an interface instead of a concrete struct, allowing different tools to implement custom result types while maintaining a consistent interface:

```go
type ToolResult interface {
    LLMMessage() string           // String representation for LLM consumption
    UserMessage() string          // String representation for user display
    IsError() bool                // Whether the result represents an error

    JSONMarshal() ([]byte, error) // Custom JSON marshaling
    JSONUnmarshal(data []byte) error // Custom JSON unmarshaling
}
```

We will also create a default implementation (`DefaultToolResult`) that maintains backward compatibility with existing code:

```go
type DefaultToolResult struct {
    Result string `json:"result"`
    Error  string `json:"error"`
}
```

## Consequences

### Positive

1. **Separation of concerns**: Different representations for LLM and user display
2. **Flexibility**: Tools can provide specialized result formatting
3. **Explicit error handling**: The `IsError()` method makes error detection explicit
4. **Better serialization control**: Custom JSON methods allow for more flexible serialization strategies
5. **Extensibility**: New tool result types can be added without modifying core interfaces

### Negative

1. **Increased complexity**: The interface approach is more complex than a simple struct
2. **Backward compatibility**: Need to ensure existing serialized conversations still work
3. **Implementation effort**: All tools need to be updated to return the new interface
4. **Potential performance impact**: Interface method calls are slightly less efficient than direct struct access

### Neutral

1. The existing tooling ecosystem will need to be updated to use the new interface
2. Serialization and deserialization logic will need to accommodate both the new interface and legacy format

## Implementation Plan

1. **Define the new interface**: Create the `ToolResult` interface in `pkg/types/tools/types.go`
2. **Create the default implementation**: Implement `DefaultToolResult` that maintains the current behavior
3. **Update tool interface**: Modify the `Tool` interface to return the new `ToolResult` interface
4. **Update core tool runner**: Modify `RunTool` function to work with the interface
5. **Update message handlers**: Update all message handlers to use the new methods
6. **Update serialization**: Ensure the conversation storage can handle the new interface
7. **Update tools one by one**: Convert each tool to return the new interface
8. **Add tests**: Add comprehensive tests for the new interface and implementations
9. **Update documentation**: Update all relevant documentation

## Migration Strategy

To ensure backward compatibility:
1. The `DefaultToolResult` implementation will closely mirror the current behavior
2. We'll add deserialization logic to handle both old and new formats
3. We'll implement the refactoring in phases, starting with core components and then updating individual tools
4. Existing serialized conversations will continue to work with the new implementation

## Alternatives Considered

1. **Extending the existing struct**: Add methods to the existing struct rather than creating an interface. Rejected because it doesn't allow for custom implementations.
2. **Creating separate types for LLM and user display**: Considered having completely separate types for different consumers. Rejected due to increased complexity in the tool execution flow.
3. **Using embedded structs**: Using embedded structs for specialization. Rejected in favor of interfaces for better polymorphism.
