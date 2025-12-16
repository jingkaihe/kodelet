# ADR 021: Agent Lifecycle Hooks

## Status
Proposed

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
| `agent_stop` | When agent completes/stops | No | N/A |

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
{}
```

### Error Handling

- Non-zero exit codes indicate hook execution failure
- Hook failures are logged but do not halt agent operation
- Timeout enforcement prevents hung hooks from blocking the agent

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
    Output interface{} `json:"output,omitempty"`
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

// AgentStopResult is returned by agent_stop hooks (empty for now)
type AgentStopResult struct{}
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

// NewHookManager creates a new HookManager with discovered hooks
func NewHookManager(opts ...DiscoveryOption) (*HookManager, error) {
    discovery, err := NewDiscovery(opts...)
    if err != nil {
        return nil, err
    }

    hooks, err := discovery.DiscoverHooks()
    if err != nil {
        return nil, err
    }

    return &HookManager{
        hooks:   hooks,
        timeout: DefaultTimeout,
    }, nil
}

// Execute runs all hooks of a given type with the provided payload
func (m *HookManager) Execute(ctx context.Context, hookType HookType, payload interface{}) ([]byte, error) {
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
func (m *HookManager) executeHook(ctx context.Context, hook *Hook, payload []byte) ([]byte, error) {
    ctx, cancel := context.WithTimeout(ctx, m.timeout)
    defer cancel()

    cmd := exec.CommandContext(ctx, hook.Path, "run")
    cmd.Stdin = bytes.NewReader(payload)

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        if ctx.Err() == context.DeadlineExceeded {
            return nil, errors.Errorf("hook %s timed out after %s", hook.Name, m.timeout)
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

### 5. Public API Functions

```go
// pkg/hooks/api.go
package hooks

import (
    "context"
    "encoding/json"
    "os"
    "sync"

    "github.com/jingkaihe/kodelet/pkg/logger"
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

var (
    globalManager     *HookManager
    globalManagerOnce sync.Once
    globalManagerErr  error
)

// GetManager returns the global hook manager, initializing it on first call
func GetManager() (*HookManager, error) {
    globalManagerOnce.Do(func() {
        globalManager, globalManagerErr = NewHookManager()
    })
    return globalManager, globalManagerErr
}

// getInvokedBy determines if this is a main agent or subagent from context/config
func getInvokedBy(config llmtypes.Config) InvokedBy {
    if config.IsSubAgent {
        return InvokedBySubagent
    }
    return InvokedByMain
}

// SendUserMessage triggers user_message_send hooks
// Returns (blocked, reason) where blocked=true means the message should not be processed
func SendUserMessage(ctx context.Context, thread llmtypes.Thread, message string) (bool, string) {
    manager, err := GetManager()
    if err != nil {
        logger.G(ctx).WithError(err).Debug("failed to get hook manager")
        return false, ""
    }

    config := thread.GetConfig()
    cwd, _ := os.Getwd()

    payload := UserMessageSendPayload{
        BasePayload: BasePayload{
            Event:     HookTypeUserMessageSend,
            ConvID:    thread.GetConversationID(),
            CWD:       cwd,
            InvokedBy: getInvokedBy(config),
        },
        Message: message,
    }

    resultBytes, err := manager.Execute(ctx, HookTypeUserMessageSend, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("user_message_send hook execution failed")
        return false, ""
    }

    if resultBytes == nil {
        return false, ""
    }

    var result UserMessageSendResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        logger.G(ctx).WithError(err).Debug("failed to unmarshal user_message_send result")
        return false, ""
    }

    return result.Blocked, result.Reason
}

// BeforeToolCall triggers before_tool_call hooks
// Returns (blocked, reason, modifiedInput) where:
// - blocked=true means the tool call should not execute
// - modifiedInput (if non-empty) replaces the original input as JSON string
func BeforeToolCall(ctx context.Context, thread llmtypes.Thread, toolName, toolInput, toolUserID string) (bool, string, string) {
    manager, err := GetManager()
    if err != nil {
        logger.G(ctx).WithError(err).Debug("failed to get hook manager")
        return false, "", toolInput
    }

    config := thread.GetConfig()
    cwd, _ := os.Getwd()

    payload := BeforeToolCallPayload{
        BasePayload: BasePayload{
            Event:     HookTypeBeforeToolCall,
            ConvID:    thread.GetConversationID(),
            CWD:       cwd,
            InvokedBy: getInvokedBy(config),
        },
        ToolName:   toolName,
        ToolInput:  json.RawMessage(toolInput), // toolInput is already JSON string from LLM
        ToolUserID: toolUserID,
    }

    resultBytes, err := manager.Execute(ctx, HookTypeBeforeToolCall, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("before_tool_call hook execution failed")
        return false, "", toolInput
    }

    if resultBytes == nil {
        return false, "", toolInput
    }

    var result BeforeToolCallResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        logger.G(ctx).WithError(err).Debug("failed to unmarshal before_tool_call result")
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

// AfterToolCall triggers after_tool_call hooks
// Returns modifiedOutput if the hook wants to replace the output
func AfterToolCall(ctx context.Context, thread llmtypes.Thread, toolName, toolInput, toolUserID string, toolOutput tooltypes.StructuredToolResult) *tooltypes.StructuredToolResult {
    manager, err := GetManager()
    if err != nil {
        logger.G(ctx).WithError(err).Debug("failed to get hook manager")
        return nil
    }

    config := thread.GetConfig()
    cwd, _ := os.Getwd()

    payload := AfterToolCallPayload{
        BasePayload: BasePayload{
            Event:     HookTypeAfterToolCall,
            ConvID:    thread.GetConversationID(),
            CWD:       cwd,
            InvokedBy: getInvokedBy(config),
        },
        ToolName:   toolName,
        ToolInput:  json.RawMessage(toolInput), // toolInput is already JSON string from LLM
        ToolOutput: toolOutput,
        ToolUserID: toolUserID,
    }

    resultBytes, err := manager.Execute(ctx, HookTypeAfterToolCall, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("after_tool_call hook execution failed")
        return nil
    }

    if resultBytes == nil {
        return nil
    }

    var result AfterToolCallResult
    if err := json.Unmarshal(resultBytes, &result); err != nil {
        logger.G(ctx).WithError(err).Debug("failed to unmarshal after_tool_call result")
        return nil
    }

    if result.Output != nil {
        modifiedBytes, err := json.Marshal(result.Output)
        if err != nil {
            logger.G(ctx).WithError(err).Debug("failed to marshal output")
            return nil
        }
        var modifiedResult tooltypes.StructuredToolResult
        if err := json.Unmarshal(modifiedBytes, &modifiedResult); err != nil {
            logger.G(ctx).WithError(err).Debug("failed to unmarshal output to StructuredToolResult")
            return nil
        }
        return &modifiedResult
    }

    return nil
}

// AgentStop triggers agent_stop hooks
func AgentStop(ctx context.Context, thread llmtypes.Thread, messages []llmtypes.Message) {
    manager, err := GetManager()
    if err != nil {
        logger.G(ctx).WithError(err).Debug("failed to get hook manager")
        return
    }

    config := thread.GetConfig()
    cwd, _ := os.Getwd()

    payload := AgentStopPayload{
        BasePayload: BasePayload{
            Event:     HookTypeAgentStop,
            ConvID:    thread.GetConversationID(),
            CWD:       cwd,
            InvokedBy: getInvokedBy(config),
        },
        Messages: messages,
    }

    _, err = manager.Execute(ctx, HookTypeAgentStop, payload)
    if err != nil {
        logger.G(ctx).WithError(err).Debug("agent_stop hook execution failed")
    }
}
```

### 6. LLM Provider Integration

Integration points for each LLM provider, following the pattern shown in the git diff:

#### Anthropic (pkg/llm/anthropic/anthropic.go)

```go
// In SendMessage, before AddUserMessage:
blocked, reason := hooks.SendUserMessage(ctx, t, message)
if blocked {
    return "", errors.Errorf("message blocked by hook: %s", reason)
}

t.AddUserMessage(ctx, message, opt.Images...)

// In SendMessage, after the OUTER loop (before return):
messages, _ := t.GetMessages()
hooks.AgentStop(ctx, t, messages)

// In executeToolsParallel, around RunTool:
blocked, reason, modifiedInput := hooks.BeforeToolCall(runToolCtx, t, tb.block.Name, tb.variant.JSON.Input.Raw(), tb.block.ID)
if blocked {
    // Create a blocked result and continue
    output = tooltypes.NewBlockedToolResult(reason)
} else {
    output = tools.RunTool(runToolCtx, t.state, tb.block.Name, modifiedInput)
}

structuredResult := output.StructuredData()
if modified := hooks.AfterToolCall(runToolCtx, t, tb.block.Name, modifiedInput, tb.block.ID, structuredResult); modified != nil {
    structuredResult = *modified
}
```

#### OpenAI (pkg/llm/openai/openai.go)

```go
// In SendMessage, before AddUserMessage:
blocked, reason := hooks.SendUserMessage(ctx, t, message)
if blocked {
    return "", errors.Errorf("message blocked by hook: %s", reason)
}

// After handler.HandleDone():
messages, _ := t.GetMessages()
hooks.AgentStop(ctx, t, messages)

// In processMessageExchange, around tools.RunTool:
blocked, reason, modifiedInput := hooks.BeforeToolCall(runToolCtx, t, toolCall.Function.Name, toolCall.Function.Arguments, toolCall.ID)
if blocked {
    output = tooltypes.NewBlockedToolResult(reason)
} else {
    output = tools.RunTool(runToolCtx, t.state, toolCall.Function.Name, modifiedInput)
}

structuredResult := output.StructuredData()
if modified := hooks.AfterToolCall(runToolCtx, t, toolCall.Function.Name, modifiedInput, toolCall.ID, structuredResult); modified != nil {
    structuredResult = *modified
}
```

#### Google (pkg/llm/google/google.go)

```go
// In SendMessage, before AddUserMessage:
blocked, reason := hooks.SendUserMessage(ctx, t, message)
if blocked {
    return "", errors.Errorf("message blocked by hook: %s", reason)
}

// After handler.HandleDone():
messages, _ := t.GetMessages()
hooks.AgentStop(ctx, t, messages)

// In executeToolCalls, around tools.RunTool:
blocked, reason, modifiedInput := hooks.BeforeToolCall(runToolCtx, t, toolCall.Name, string(argsJSON), toolCall.ID)
if blocked {
    output = tooltypes.NewBlockedToolResult(reason)
} else {
    output = tools.RunTool(runToolCtx, t.state, toolCall.Name, modifiedInput)
}

structuredResult := output.StructuredData()
if modified := hooks.AfterToolCall(runToolCtx, t, toolCall.Name, modifiedInput, toolCall.ID, structuredResult); modified != nil {
    structuredResult = *modified
}
```

## Implementation Phases

### Phase 1: Core Infrastructure (Week 1)
- [ ] Create `pkg/hooks/` package with core types
- [ ] Implement hook discovery from `.kodelet/hooks/` directories
- [ ] Implement hook executor with timeout enforcement
- [ ] Write unit tests for discovery and execution

### Phase 2: LLM Provider Integration (Week 1-2)
- [ ] Add `hooks.SendUserMessage` to all three providers
- [ ] Add `hooks.BeforeToolCall` to all three providers
- [ ] Add `hooks.AfterToolCall` to all three providers
- [ ] Add `hooks.AgentStop` to all three providers
- [ ] Add `NewBlockedToolResult` helper function

### Phase 3: Testing & Documentation (Week 2)
- [ ] Write integration tests with sample hook executables
- [ ] Create example hooks (audit logger, slack notifier)
- [ ] Add `docs/HOOKS.md` documentation
- [ ] Update `AGENTS.md` with hooks overview
- [ ] Add hook examples to `examples/hooks/` directory

### Phase 4: CLI (Week 2-3, Optional)
- [ ] Add `--no-hooks` CLI flag to disable hooks
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
    
    # Return empty result (no modification)
    echo '{}'
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

        fmt.Println("{}")
        os.Exit(0)
    }

    os.Exit(1)
}
```

## Testing Strategy

### Unit Tests
1. **Discovery tests**: Verify hook discovery from multiple directories with precedence
2. **Executor tests**: Test hook execution with timeout enforcement
3. **Payload tests**: Verify JSON serialization/deserialization of payloads

### Integration Tests
1. **End-to-end tests**: Test hook invocation through actual LLM provider flows
2. **Blocking tests**: Verify that blocked hooks prevent tool execution
3. **Modification tests**: Verify that hooks can modify inputs/outputs

### Test Fixtures
Create test hook executables that:
- Return specific hook types
- Block specific tool calls
- Modify inputs/outputs in predictable ways
- Simulate timeouts and failures

## Consequences

### Positive
1. **Extensibility**: Users can observe and control agent behavior without code changes
2. **Language-agnostic**: Hooks can be written in any language
3. **Security**: Security teams can implement guardrails around tool execution
4. **Compliance**: Audit logging and policy enforcement become possible
5. **Integration**: Easy integration with external systems (Slack, webhooks, etc.)

### Negative
1. **Performance overhead**: Each hook invocation adds latency (mitigated by timeout)
2. **Complexity**: More moving parts to debug when things go wrong
3. **Security risk**: Malicious hooks could intercept sensitive data (mitigated by directory permissions)

### Risks & Mitigations
| Risk | Mitigation |
|------|------------|
| Hook hangs indefinitely | Timeout enforcement (default 30s) |
| Hook fails and blocks agent | Log errors, continue operation |
| Malicious hook in repo | User must explicitly trust repo hooks |
| Performance degradation | Optional hooks, async where possible |

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
