# ADR 021: Agent Lifecycle Hooks

## Status
Accepted

## Context

Kodelet's agentic architecture processes tool calls and user messages through LLM provider implementations (Anthropic, OpenAI, Google). Currently, there is no extensibility mechanism for users to observe or intercept these agent lifecycle events for:

- **Audit logging**: Recording all tool invocations and user interactions
- **Security controls**: Blocking potentially harmful tool calls or inputs
- **Monitoring & alerting**: Sending notifications to external systems (Slack, webhooks)
- **Input/output modification**: Transforming tool inputs or outputs for specific use cases
- **Compliance**: Enforcing organizational policies on agent behavior

**Problem Statement:**
- Users cannot extend or observe agent behavior without modifying source code
- There is no standardized way to integrate with external systems during agent execution
- Security teams cannot implement guardrails around tool execution
- No mechanism exists for intercepting and modifying tool inputs/outputs

**Goals:**
1. Provide extensibility hooks at key points in the agent lifecycle
2. Support both observation (logging) and interception (blocking/modifying) use cases
3. Allow hooks to be executable scripts/binaries for language-agnostic extensibility
4. Support stacking multiple hooks with clear precedence rules
5. Maintain backward compatibility - hooks are optional with no impact if unused

## Decision

Introduce an **Agent Lifecycle Hooks** system that allows external executables to be invoked at specific points in the agent's lifecycle. Hooks are discovered from standard directories and executed with JSON payloads via stdin, returning JSON results via stdout.

### Hook Types

Four hook types are defined based on agent lifecycle events:

| Hook Type | Trigger Point | Blocking | Can Modify |
|-----------|--------------|----------|------------|
| `before_tool_call` | Before tool execution | Yes | Tool input |
| `after_tool_call` | After tool execution | No | Tool output |
| `user_message_send` | When user sends message | Yes | N/A |
| `agent_stop` | When agent would stop (no tools used) | No | Can return follow-up messages |

### Hook Protocol

Hooks are executable files that implement a simple protocol:

1. **Discovery**: `<hook> hook` - Returns the hook type (stdout)
2. **Execution**: `<hook> run` - Receives JSON payload via stdin, returns JSON result via stdout

#### Payload Structures

**BeforeToolCall:**
```json
{
  "event": "before_tool_call",
  "conv_id": "string",
  "tool_name": "string",
  "tool_input": { ... },
  "tool_user_id": "string",
  "cwd": "string",
  "invoked_by": "main" | "subagent"
}
```

**AfterToolCall:**
```json
{
  "event": "after_tool_call",
  "conv_id": "string",
  "tool_name": "string",
  "tool_input": { ... },
  "tool_output": { ... },
  "tool_user_id": "string",
  "cwd": "string",
  "invoked_by": "main" | "subagent"
}
```

**UserMessageSend:**
```json
{
  "event": "user_message_send",
  "conv_id": "string",
  "message": "string",
  "cwd": "string",
  "invoked_by": "main" | "subagent"
}
```

**AgentStop:**
```json
{
  "event": "agent_stop",
  "conv_id": "string",
  "cwd": "string",
  "messages": [ ... ],
  "invoked_by": "main" | "subagent"
}
```

#### Return Value Structures

**BeforeToolCall:**
```json
{
  "blocked": false,
  "reason": "string",
  "input": { ... }
}
```

Note: `input` is the tool input to be used. Omit or set to `null` to use the original input unchanged.

**AfterToolCall:**
```json
{
  "output": { ... }
}
```

Note: `output` is the tool output to be used. Omit or set to `null` to use the original output unchanged.

**UserMessageSend:**
```json
{
  "blocked": false,
  "reason": "string"
}
```

**AgentStop:**
```json
{
  "follow_up_messages": ["string", "..."]
}
```

Note: `follow_up_messages` is optional. If provided, these messages are appended as user messages and the agent continues processing. This enables LLM-based hooks to analyze the conversation and request additional work or clarification. Empty array or omitted field means the agent stops normally.
```

### Error Handling

- Non-zero exit codes indicate hook execution failure
- Hook failures are logged but do not halt agent operation
- Timeout enforcement prevents hung hooks from blocking the agent
- **Empty stdout with exit code 0** is treated as "no action" (not blocked, no modification) - useful for observation-only hooks like audit loggers

## Architecture Overview

### Directory Structure

```
pkg/
└── hooks/
    ├── hooks.go          # Core hook types and interfaces
    ├── discovery.go      # Hook discovery from directories
    ├── executor.go       # Hook execution with timeout
    ├── payload.go        # Payload and result types
    └── hooks_test.go     # Unit tests
```

### Hook Discovery Locations

Hooks are discovered from two locations with the following precedence (earlier directories take precedence):

```
./.kodelet/hooks/           # Repository-local (higher precedence)
~/.kodelet/hooks/           # User-global
```

This follows the same pattern established by the skills system (ADR 020).

### Hook File Structure

Each hook is a single executable file:

```
~/.kodelet/hooks/
├── slack_notify           # Executable hook
├── security_guardrail     # Another hook
└── audit_logger           # Third hook
```

## Implementation Design

### 1. Core Types

```go
// pkg/hooks/hooks.go
package hooks

import (
    "context"
    "time"
)

// HookType represents the type of lifecycle hook
type HookType string

const (
    HookTypeBeforeToolCall  HookType = "before_tool_call"
    HookTypeAfterToolCall   HookType = "after_tool_call"
    HookTypeUserMessageSend HookType = "user_message_send"
    HookTypeAgentStop       HookType = "agent_stop"
)

// InvokedBy indicates whether the hook was triggered by main agent or subagent
type InvokedBy string

const (
    InvokedByMain     InvokedBy = "main"
    InvokedBySubagent InvokedBy = "subagent"
)

// Hook represents a discovered hook executable
type Hook struct {
    Name      string   // Filename of the executable
    Path      string   // Full path to the executable
    HookType  HookType // Type returned by "hook" command
}

// HookManager manages hook discovery and execution
type HookManager struct {
    hooks   map[HookType][]*Hook
    timeout time.Duration
}

// DefaultTimeout is the default execution timeout for hooks
const DefaultTimeout = 30 * time.Second

// NewHookManager creates a new HookManager with discovered hooks
// Returns an empty manager (no-op) if discovery fails
func NewHookManager(opts ...DiscoveryOption) (HookManager, error) {
    discovery, err := NewDiscovery(opts...)
    if err != nil {
        return HookManager{}, err
    }

    hooks, err := discovery.DiscoverHooks()
    if err != nil {
        return HookManager{}, err
    }

    return HookManager{
        hooks:   hooks,
        timeout: DefaultTimeout,
    }, nil
}
```

### 2. Payload Types

```go
// pkg/hooks/payload.go
package hooks

import (
    "encoding/json"

    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// BasePayload contains fields common to all hook payloads
type BasePayload struct {
    Event     HookType  `json:"event"`
    ConvID    string    `json:"conv_id"`
    CWD       string    `json:"cwd"`
    InvokedBy InvokedBy `json:"invoked_by"`
}

// BeforeToolCallPayload is sent to before_tool_call hooks
type BeforeToolCallPayload struct {
    BasePayload
    ToolName   string          `json:"tool_name"`
    ToolInput  json.RawMessage `json:"tool_input"`
    ToolUserID string          `json:"tool_user_id"`
}

// BeforeToolCallResult is returned by before_tool_call hooks
type BeforeToolCallResult struct {
    Blocked bool            `json:"blocked"`
    Reason  string          `json:"reason,omitempty"`
    Input   json.RawMessage `json:"input,omitempty"`
}

// AfterToolCallPayload is sent to after_tool_call hooks
type AfterToolCallPayload struct {
    BasePayload
    ToolName   string                         `json:"tool_name"`
    ToolInput  json.RawMessage                `json:"tool_input"`
    ToolOutput tooltypes.StructuredToolResult `json:"tool_output"`
    ToolUserID string                         `json:"tool_user_id"`
}

// AfterToolCallResult is returned by after_tool_call hooks
type AfterToolCallResult struct {
    Output *tooltypes.StructuredToolResult `json:"output,omitempty"`
}

// UserMessageSendPayload is sent to user_message_send hooks
type UserMessageSendPayload struct {
    BasePayload
    Message string `json:"message"`
}

// UserMessageSendResult is returned by user_message_send hooks
type UserMessageSendResult struct {
    Blocked bool   `json:"blocked"`
    Reason  string `json:"reason,omitempty"`
}

// AgentStopPayload is sent to agent_stop hooks
type AgentStopPayload struct {
    BasePayload
    Messages []llmtypes.Message `json:"messages"`
}

// AgentStopResult is returned by agent_stop hooks
type AgentStopResult struct {
    // FollowUpMessages contains optional messages to append to the conversation.
    // If provided, these are added as user messages and the agent continues.
    FollowUpMessages []string `json:"follow_up_messages,omitempty"`
}
```

### 3. Discovery

```go
// pkg/hooks/discovery.go
package hooks

import (
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "github.com/pkg/errors"
)

// Discovery handles hook discovery from configured directories
type Discovery struct {
    hookDirs []string
}

// DiscoveryOption is a function that configures a Discovery
type DiscoveryOption func(*Discovery) error

// WithDefaultDirs initializes with default hook directories
func WithDefaultDirs() DiscoveryOption {
    return func(d *Discovery) error {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return errors.Wrap(err, "failed to get user home directory")
        }
        d.hookDirs = []string{
            "./.kodelet/hooks",                          // Repo-local (higher precedence)
            filepath.Join(homeDir, ".kodelet", "hooks"), // User-global
        }
        return nil
    }
}

// NewDiscovery creates a new hook discovery instance
func NewDiscovery(opts ...DiscoveryOption) (*Discovery, error) {
    d := &Discovery{}

    if len(opts) == 0 {
        if err := WithDefaultDirs()(d); err != nil {
            return nil, err
        }
    } else {
        for _, opt := range opts {
            if err := opt(d); err != nil {
                return nil, err
            }
        }
    }

    return d, nil
}

// DiscoverHooks finds all available hooks from configured directories
func (d *Discovery) DiscoverHooks() (map[HookType][]*Hook, error) {
    hooks := make(map[HookType][]*Hook)
    seen := make(map[string]bool) // Track seen hook names to maintain precedence

    for _, dir := range d.hookDirs {
        entries, err := os.ReadDir(dir)
        if err != nil {
            if os.IsNotExist(err) {
                continue // Skip non-existent directories
            }
            return nil, errors.Wrapf(err, "failed to read hook directory %s", dir)
        }

        for _, entry := range entries {
            if entry.IsDir() {
                continue // Skip directories
            }

            hookPath := filepath.Join(dir, entry.Name())

            // Check if executable
            info, err := entry.Info()
            if err != nil {
                continue
            }
            if info.Mode()&0111 == 0 {
                continue // Not executable
            }

            // Skip if already discovered (earlier directories have precedence)
            if seen[entry.Name()] {
                continue
            }
            seen[entry.Name()] = true

            // Query hook type
            hookType, err := queryHookType(hookPath)
            if err != nil {
                continue // Skip invalid hooks
            }

            hook := &Hook{
                Name:     entry.Name(),
                Path:     hookPath,
                HookType: hookType,
            }

            hooks[hookType] = append(hooks[hookType], hook)
        }
    }

    return hooks, nil
}

// queryHookType executes the hook with "hook" argument to determine its type
func queryHookType(hookPath string) (HookType, error) {
    cmd := exec.Command(hookPath, "hook")
    output, err := cmd.Output()
    if err != nil {
        return "", errors.Wrap(err, "failed to query hook type")
    }

    hookTypeStr := strings.TrimSpace(string(output))
    hookType := HookType(hookTypeStr)

    // Validate hook type
    switch hookType {
    case HookTypeBeforeToolCall, HookTypeAfterToolCall, HookTypeUserMessageSend, HookTypeAgentStop:
        return hookType, nil
    default:
        return "", errors.Errorf("invalid hook type: %s", hookTypeStr)
    }
}
```

### 4. Executor

```go
// pkg/hooks/executor.go
package hooks

import (
    "bytes"
    "context"
    "encoding/json"
    "os/exec"
    "time"

    "github.com/jingkaihe/kodelet/pkg/logger"
    "github.com/pkg/errors"
)

// Execute runs all hooks of a given type with the provided payload
func (m HookManager) Execute(ctx context.Context, hookType HookType, payload interface{}) ([]byte, error) {
    hooks := m.hooks[hookType]
    if len(hooks) == 0 {
        return nil, nil
    }

    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        return nil, errors.Wrap(err, "failed to marshal payload")
    }

    var lastResult []byte
    for _, hook := range hooks {
        result, err := m.executeHook(ctx, hook, payloadBytes)
        if err != nil {
            // Log error but continue with other hooks
            logger.G(ctx).WithError(err).WithField("hook", hook.Name).Warn("hook execution failed")
            continue
        }
        lastResult = result
    }

    return lastResult, nil
}

// executeHook runs a single hook with timeout enforcement
func (m HookManager) executeHook(ctx context.Context, hook *Hook, payload []byte) ([]byte, error) {
    timeout := m.timeout
    if timeout == 0 {
        timeout = DefaultTimeout
    }

    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    cmd := exec.CommandContext(ctx, hook.Path, "run")
    cmd.Stdin = bytes.NewReader(payload)

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        if ctx.Err() == context.DeadlineExceeded {
            return nil, errors.Errorf("hook %s timed out after %s", hook.Name, timeout)
        }
        return nil, errors.Wrapf(err, "hook %s failed: %s", hook.Name, stderr.String())
    }

    return stdout.Bytes(), nil
}

// SetTimeout sets the execution timeout for hooks
func (m *HookManager) SetTimeout(timeout time.Duration) {
    m.timeout = timeout
}
```

### 5. Thread Integration

The `HookManager` is a value field on each Thread implementation (not a pointer), so an empty manager simply does nothing:

```go
// In pkg/llm/anthropic/anthropic.go
type Thread struct {
    // ... existing fields ...
    hookManager hooks.HookManager // Hook manager for lifecycle hooks (empty = no-op)
}

// NewAnthropicThread creates a new thread with Anthropic's Claude API
func NewAnthropicThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (*Thread, error) {
    // ... existing initialization ...

    // Initialize hook manager (empty if discovery fails - hooks disabled)
    hookManager := hooks.HookManager{}
    if !config.IsSubAgent {
        // Only main agent discovers hooks; subagents inherit from parent
        hookManager, _ = hooks.NewHookManager()
    }

    return &Thread{
        // ... existing fields ...
        hookManager: hookManager,
    }, nil
}

// NewSubAgent creates a new subagent thread that shares the parent's hook manager
func (t *Thread) NewSubAgent(_ context.Context, config llmtypes.Config) llmtypes.Thread {
    return &Thread{
        // ... existing fields ...
        hookManager: t.hookManager, // Share parent's hook manager
    }
}
```

### 6. Hook Invocation Helpers

Helper methods on Thread to invoke hooks - no nil checks needed since empty manager is a no-op:

```go
// pkg/llm/anthropic/hooks.go (or inline in anthropic.go)

func (t *Thread) invokedBy() hooks.InvokedBy {
    if t.config.IsSubAgent {
        return hooks.InvokedBySubagent
    }
    return hooks.InvokedByMain
}

// triggerUserMessageSend invokes user_message_send hooks
// Returns (blocked, reason)
func (t *Thread) triggerUserMessageSend(ctx context.Context, message string) (bool, string) {
    cwd, _ := os.Getwd()
    payload := hooks.UserMessageSendPayload{
        BasePayload: hooks.BasePayload{
            Event:     hooks.HookTypeUserMessageSend,
            ConvID:    t.conversationID,
            CWD:       cwd,
            InvokedBy: t.invokedBy(),
        },
        Message: message,
    }

    result, err := t.hookManager.ExecuteUserMessageSend(ctx, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("user_message_send hook failed")
        return false, ""
    }
    return result.Blocked, result.Reason
}

// triggerBeforeToolCall invokes before_tool_call hooks
// Returns (blocked, reason, input)
func (t *Thread) triggerBeforeToolCall(ctx context.Context, toolName, toolInput, toolUserID string) (bool, string, string) {
    cwd, _ := os.Getwd()
    payload := hooks.BeforeToolCallPayload{
        BasePayload: hooks.BasePayload{
            Event:     hooks.HookTypeBeforeToolCall,
            ConvID:    t.conversationID,
            CWD:       cwd,
            InvokedBy: t.invokedBy(),
        },
        ToolName:   toolName,
        ToolInput:  json.RawMessage(toolInput),
        ToolUserID: toolUserID,
    }

    result, err := t.hookManager.ExecuteBeforeToolCall(ctx, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("before_tool_call hook failed")
        return false, "", toolInput
    }

    if result.Blocked {
        return true, result.Reason, ""
    }
    if len(result.Input) > 0 {
        return false, "", string(result.Input)
    }
    return false, "", toolInput
}

// triggerAfterToolCall invokes after_tool_call hooks
// Returns modified output or nil to use original
func (t *Thread) triggerAfterToolCall(ctx context.Context, toolName, toolInput, toolUserID string, toolOutput tooltypes.StructuredToolResult) *tooltypes.StructuredToolResult {
    cwd, _ := os.Getwd()
    payload := hooks.AfterToolCallPayload{
        BasePayload: hooks.BasePayload{
            Event:     hooks.HookTypeAfterToolCall,
            ConvID:    t.conversationID,
            CWD:       cwd,
            InvokedBy: t.invokedBy(),
        },
        ToolName:   toolName,
        ToolInput:  json.RawMessage(toolInput),
        ToolOutput: toolOutput,
        ToolUserID: toolUserID,
    }

    result, err := t.hookManager.ExecuteAfterToolCall(ctx, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("after_tool_call hook failed")
        return nil
    }
    return result.Output
}

// triggerAgentStop invokes agent_stop hooks
// Returns follow-up messages that can be appended to the conversation
func (t *Thread) triggerAgentStop(ctx context.Context, messages []llmtypes.Message) []string {
    cwd, _ := os.Getwd()
    payload := hooks.AgentStopPayload{
        BasePayload: hooks.BasePayload{
            Event:     hooks.HookTypeAgentStop,
            ConvID:    t.conversationID,
            CWD:       cwd,
            InvokedBy: t.invokedBy(),
        },
        Messages: messages,
    }

    result, err := t.hookManager.ExecuteAgentStop(ctx, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("agent_stop hook failed")
        return nil
    }
    return result.FollowUpMessages
}
```

### 7. Typed Executor Methods

Add typed executor methods to HookManager for better ergonomics:

```go
// pkg/hooks/executor.go (continued)

// ExecuteUserMessageSend runs user_message_send hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no action" (not blocked).
func (m HookManager) ExecuteUserMessageSend(ctx context.Context, payload UserMessageSendPayload) (*UserMessageSendResult, error) {
    resultBytes, err := m.Execute(ctx, HookTypeUserMessageSend, payload)
    if err != nil {
        return nil, err
    }
    if len(resultBytes) == 0 {
        return &UserMessageSendResult{}, nil // No output = not blocked
    }

    var result UserMessageSendResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        return nil, errors.Wrap(err, "failed to unmarshal result")
    }
    return &result, nil
}

// ExecuteBeforeToolCall runs before_tool_call hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no action" (not blocked, no modification).
func (m HookManager) ExecuteBeforeToolCall(ctx context.Context, payload BeforeToolCallPayload) (*BeforeToolCallResult, error) {
    resultBytes, err := m.Execute(ctx, HookTypeBeforeToolCall, payload)
    if err != nil {
        return nil, err
    }
    if len(resultBytes) == 0 {
        return &BeforeToolCallResult{}, nil // No output = not blocked, use original input
    }

    var result BeforeToolCallResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        return nil, errors.Wrap(err, "failed to unmarshal result")
    }
    return &result, nil
}

// ExecuteAfterToolCall runs after_tool_call hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no modification".
func (m HookManager) ExecuteAfterToolCall(ctx context.Context, payload AfterToolCallPayload) (*AfterToolCallResult, error) {
    resultBytes, err := m.Execute(ctx, HookTypeAfterToolCall, payload)
    if err != nil {
        return nil, err
    }
    if len(resultBytes) == 0 {
        return &AfterToolCallResult{}, nil // No output = use original output
    }

    var result AfterToolCallResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        return nil, errors.Wrap(err, "failed to unmarshal result")
    }
    return &result, nil
}

// ExecuteAgentStop runs agent_stop hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no follow-up".
func (m HookManager) ExecuteAgentStop(ctx context.Context, payload AgentStopPayload) (*AgentStopResult, error) {
    resultBytes, err := m.Execute(ctx, HookTypeAgentStop, payload)
    if err != nil {
        return nil, err
    }
    if len(resultBytes) == 0 {
        return &AgentStopResult{}, nil // No output = no follow-up messages
    }

    var result AgentStopResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        return nil, errors.Wrap(err, "failed to unmarshal result")
    }
    return &result, nil
}
```

### 8. LLM Provider Integration

Integration points for each LLM provider using the Thread helper methods:

#### Anthropic (pkg/llm/anthropic/anthropic.go)

```go
// In SendMessage, before AddUserMessage:
if blocked, reason := t.triggerUserMessageSend(ctx, message); blocked {
    return "", errors.Errorf("message blocked by hook: %s", reason)
}

t.AddUserMessage(ctx, message, opt.Images...)

// In the OUTER loop, when no tools are used (instead of breaking immediately):
if !toolsUsed {
    logger.G(ctx).Debug("no tools used, checking agent_stop hook")

    // Trigger agent_stop hook to see if there are follow-up messages
    if messages, err := t.GetMessages(); err == nil {
        if followUps := t.triggerAgentStop(ctx, messages); len(followUps) > 0 {
            logger.G(ctx).WithField("count", len(followUps)).Info("agent_stop hook returned follow-up messages, continuing conversation")
            // Append follow-up messages as user messages and continue
            for _, msg := range followUps {
                t.AddUserMessage(ctx, msg)
                handler.HandleText(fmt.Sprintf("\n📨 Hook follow-up: %s\n", msg))
            }
            continue OUTER
        }
    }

    break OUTER
}

// In executeToolsParallel, around RunTool:
blocked, reason, input := t.triggerBeforeToolCall(runToolCtx, tb.block.Name, tb.variant.JSON.Input.Raw(), tb.block.ID)
if blocked {
    output = tooltypes.NewBlockedToolResult(reason)
} else {
    output = tools.RunTool(runToolCtx, t.state, tb.block.Name, input)
}

structuredResult := output.StructuredData()
if modified := t.triggerAfterToolCall(runToolCtx, tb.block.Name, input, tb.block.ID, structuredResult); modified != nil {
    structuredResult = *modified
}
```

#### OpenAI (pkg/llm/openai/openai.go)

```go
// In SendMessage, before AddUserMessage:
if blocked, reason := t.triggerUserMessageSend(ctx, message); blocked {
    return "", errors.Errorf("message blocked by hook: %s", reason)
}

// In the OUTER loop, when no tools are used:
if !toolsUsed {
    // Trigger agent_stop hook to see if there are follow-up messages
    if messages, err := t.GetMessages(); err == nil {
        if followUps := t.triggerAgentStop(ctx, messages); len(followUps) > 0 {
            for _, msg := range followUps {
                t.AddUserMessage(ctx, msg)
                handler.HandleText(fmt.Sprintf("\n📨 Hook follow-up: %s\n", msg))
            }
            continue OUTER
        }
    }
    break OUTER
}

// In processMessageExchange, around tools.RunTool:
blocked, reason, input := t.triggerBeforeToolCall(runToolCtx, toolCall.Function.Name, toolCall.Function.Arguments, toolCall.ID)
if blocked {
    output = tooltypes.NewBlockedToolResult(reason)
} else {
    output = tools.RunTool(runToolCtx, t.state, toolCall.Function.Name, input)
}

structuredResult := output.StructuredData()
if modified := t.triggerAfterToolCall(runToolCtx, toolCall.Function.Name, input, toolCall.ID, structuredResult); modified != nil {
    structuredResult = *modified
}
```

#### Google (pkg/llm/google/google.go)

```go
// In SendMessage, before AddUserMessage:
if blocked, reason := t.triggerUserMessageSend(ctx, message); blocked {
    return "", errors.Errorf("message blocked by hook: %s", reason)
}

// In the OUTER loop, when no tools are used:
if !toolsUsed {
    // Trigger agent_stop hook to see if there are follow-up messages
    if messages, err := t.GetMessages(); err == nil {
        if followUps := t.triggerAgentStop(ctx, messages); len(followUps) > 0 {
            for _, msg := range followUps {
                t.AddUserMessage(ctx, msg)
                handler.HandleText(fmt.Sprintf("\n📨 Hook follow-up: %s\n", msg))
            }
            continue OUTER
        }
    }
    break OUTER
}

// In executeToolCalls, around tools.RunTool:
blocked, reason, input := t.triggerBeforeToolCall(runToolCtx, toolCall.Name, string(argsJSON), toolCall.ID)
if blocked {
    output = tooltypes.NewBlockedToolResult(reason)
} else {
    output = tools.RunTool(runToolCtx, t.state, toolCall.Name, input)
}

structuredResult := output.StructuredData()
if modified := t.triggerAfterToolCall(runToolCtx, toolCall.Name, input, toolCall.ID, structuredResult); modified != nil {
    structuredResult = *modified
}
```

## Implementation Phases

### Phase 1: Core Infrastructure (Week 1)
- [x] Create `pkg/hooks/` package with core types
- [x] Implement hook discovery from `.kodelet/hooks/` directories
- [x] Implement hook executor with timeout enforcement
- [x] Write unit tests for discovery and execution

### Phase 2: LLM Provider Integration (Week 1-2)
- [x] Add `hooks.SendUserMessage` to all three providers
- [x] Add `hooks.BeforeToolCall` to all three providers
- [x] Add `hooks.AfterToolCall` to all three providers
- [x] Add `hooks.AgentStop` to all three providers
- [x] Add `NewBlockedToolResult` helper function

### Phase 3: Testing & Documentation (Week 2)
- [x] Write integration tests with sample hook executables
- [ ] Create example hooks (audit logger, slack notifier)
- [ ] Add `docs/HOOKS.md` documentation
- [ ] Update `AGENTS.md` with hooks overview
- [ ] Add hook examples to `examples/hooks/` directory

### Phase 4: CLI (Week 2-3, Optional)
- [x] Add `--no-hooks` CLI flag to disable hooks
- [ ] Add `kodelet hooks list` command to show discovered hooks

## Example Hook Implementations

### Audit Logger (Bash)

```bash
#!/bin/bash
# ~/.kodelet/hooks/audit_logger

if [ "$1" == "hook" ]; then
    echo "after_tool_call"
    exit 0
fi

if [ "$1" == "run" ]; then
    # Read JSON payload from stdin
    payload=$(cat)

    # Extract fields and log to file
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) | $payload" >> ~/.kodelet/audit.log

    # No output needed - empty stdout with exit 0 means "no modification"
    exit 0
fi

exit 1
```

### Security Guardrail (Python)

```python
#!/usr/bin/env python3
# ~/.kodelet/hooks/security_guardrail

import sys
import json

BLOCKED_COMMANDS = ["rm -rf", "sudo", "curl | bash", "wget | sh"]

if len(sys.argv) < 2:
    sys.exit(1)

if sys.argv[1] == "hook":
    print("before_tool_call")
    sys.exit(0)

if sys.argv[1] == "run":
    payload = json.load(sys.stdin)

    if payload.get("tool_name") == "bash":
        # tool_input is already a parsed JSON object
        tool_input = payload.get("tool_input", {})
        command = tool_input.get("command", "")

        for blocked in BLOCKED_COMMANDS:
            if blocked in command:
                result = {
                    "blocked": True,
                    "reason": f"Security policy: '{blocked}' is not allowed"
                }
                print(json.dumps(result))
                sys.exit(0)

    print(json.dumps({"blocked": False}))
    sys.exit(0)

sys.exit(1)
```

### Slack Notifier (Go)

```go
// ~/.kodelet/hooks/slack_notify (compiled binary)
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
)

type AgentStopPayload struct {
    Event     string    `json:"event"`
    ConvID    string    `json:"conv_id"`
    CWD       string    `json:"cwd"`
    InvokedBy string    `json:"invoked_by"`
    Messages  []Message `json:"messages"`
}

type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

func main() {
    if len(os.Args) < 2 {
        os.Exit(1)
    }

    switch os.Args[1] {
    case "hook":
        fmt.Println("agent_stop")
        os.Exit(0)
    case "run":
        var payload AgentStopPayload
        if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
            os.Exit(1)
        }

        webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
        if webhookURL == "" {
            os.Exit(0) // Silently skip if not configured
        }

        slackMsg := map[string]string{
            "text": fmt.Sprintf("Agent completed conversation %s (%d messages)",
                payload.ConvID, len(payload.Messages)),
        }
        body, _ := json.Marshal(slackMsg)
        http.Post(webhookURL, "application/json", bytes.NewReader(body))

        // No output needed - empty stdout with exit 0 is valid for agent_stop
        os.Exit(0)
    }

    os.Exit(1)
}
```

### Follow-up Messages Hook (Bash)

This example demonstrates an `agent_stop` hook that returns follow-up messages to continue the conversation:

```bash
#!/bin/bash
# .kodelet/hooks/foo-txt-remover
# Asks the agent to remove foo.txt if it exists

case "$1" in
    hook)
        echo "agent_stop"
        ;;
    run)
        # Check if foo.txt exists in current directory
        if [ -f "./foo.txt" ]; then
            echo '{"follow_up_messages":["I noticed foo.txt exists. Please remove it."]}'
        fi
        # Empty output = no follow-up, agent stops normally
        ;;
    *)
        exit 1
        ;;
esac
```

## Documentation

### docs/HOOKS.md

Create comprehensive documentation covering:
- Hook types and their use cases
- Payload and result structures
- Example hooks in multiple languages
- Security considerations
- Debugging tips

### AGENTS.md Update

Add section:
```markdown
## Agent Lifecycle Hooks

Kodelet supports lifecycle hooks that allow external scripts to observe and control agent behavior:

- **Location**: `.kodelet/hooks/` (repo) or `~/.kodelet/hooks/` (global)
- **Hook types**: `before_tool_call`, `after_tool_call`, `user_message_send`, `agent_stop`
- **Protocol**: Executables responding to `hook` (type) and `run` (execution) commands

See [docs/HOOKS.md](docs/HOOKS.md) for creating custom hooks.
```

## Related ADRs

- **ADR 020: Agentic Skills** - Similar discovery pattern for `.kodelet/` directories
- **ADR 006: Conversation Persistence** - ConversationID used in hook payloads
- **ADR 015: Structured Tool Result Storage** - Tool result format used in payloads

## Conclusion

The Agent Lifecycle Hooks system provides a powerful extensibility mechanism that:

1. **Follows existing patterns**: Uses the same directory discovery approach as skills
2. **Maintains backward compatibility**: Hooks are optional with zero impact if unused
3. **Enables security controls**: Blocking hooks can prevent dangerous operations
4. **Supports observability**: All agent actions can be logged and monitored
5. **Language-agnostic**: Any executable can be a hook

The implementation adds minimal overhead to the core agent loop while providing maximum flexibility for users to customize agent behavior.
