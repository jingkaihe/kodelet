# Agent Lifecycle Hooks

Kodelet supports lifecycle hooks that allow external scripts to observe and control agent behavior. Hooks are language-agnostic executables that receive JSON payloads and can block operations, modify inputs/outputs, or trigger follow-up actions.

## Use Cases

- **Audit logging**: Record all tool invocations and user interactions
- **Security controls**: Block potentially harmful tool calls or inputs
- **Monitoring & alerting**: Send notifications to external systems (Slack, webhooks)
- **Input/output modification**: Transform tool inputs or outputs for specific use cases
- **Compliance**: Enforce organizational policies on agent behavior

## Quick Start

1. Create a hooks directory:
   ```bash
   mkdir -p .kodelet/hooks  # Repository-local hooks
   # OR
   mkdir -p ~/.kodelet/hooks  # Global hooks
   ```

2. Create an executable hook script:
   ```bash
   cat > .kodelet/hooks/audit_logger << 'EOF'
   #!/bin/bash
   if [ "$1" == "hook" ]; then echo "after_tool_call"; exit 0; fi
   if [ "$1" == "run" ]; then
       cat >> ~/.kodelet/audit.log
       exit 0
   fi
   EOF
   chmod +x .kodelet/hooks/audit_logger
   ```

3. Run kodelet - hooks are automatically discovered and executed.

## Hook Types

| Hook Type | Trigger Point | Can Block | Can Modify |
|-----------|--------------|-----------|------------|
| `before_tool_call` | Before tool execution | Yes | Tool input |
| `after_tool_call` | After tool execution | No | Tool output |
| `user_message_send` | When user sends message | Yes | N/A |
| `after_turn` | After each LLM response | No | Messages (mutate), recipe callbacks |
| `agent_stop` | When agent would stop | No | Messages (mutate), recipe callbacks, follow-up messages |

## Hook Protocol

Hooks are executable files that implement a simple protocol:

### Discovery Command
```bash
./my_hook hook
# Output: hook type (e.g., "before_tool_call")
```

### Execution Command
```bash
echo '{"event":"before_tool_call",...}' | ./my_hook run
# Output: JSON result (or empty for no action)
```

## Hook Discovery

Hooks are discovered from two locations (earlier takes precedence):

1. `./.kodelet/hooks/` - Repository-local hooks (higher precedence)
2. `~/.kodelet/hooks/` - User-global hooks

Only executable files are considered. Directories and non-executable files are skipped.

### Disabling Hooks

To temporarily disable a hook without deleting it, rename the file to end with `.disable`:

```bash
# Disable a hook
mv .kodelet/hooks/audit-tool-call .kodelet/hooks/audit-tool-call.disable

# Re-enable a hook
mv .kodelet/hooks/audit-tool-call.disable .kodelet/hooks/audit-tool-call
```

Hooks with names ending in `.disable` are skipped during discovery. This is useful for:
- Temporarily disabling a hook for debugging
- Keeping hook configurations in version control while disabled
- Testing behavior with specific hooks turned off

## Payload Structures

All hook payloads and results are defined as TypeScript interfaces below for clarity.

### Common Types

```typescript
// Hook event types
type HookType = "before_tool_call" | "after_tool_call" | "user_message_send" | "after_turn" | "agent_stop";

// Indicates whether the hook was triggered by main agent or subagent
type InvokedBy = "main" | "subagent";

// Base payload included in all hook events
interface BasePayload {
  event: HookType;
  conv_id: string;
  cwd: string;
  invoked_by: InvokedBy;
}

// Conversation message structure
interface Message {
  role: "user" | "assistant";
  content: string;
}

// Tool execution result (used in after_tool_call)
interface StructuredToolResult {
  toolName: string;
  success: boolean;
  error?: string;
  metadata?: Record<string, unknown>;  // Tool-specific metadata
  timestamp: string;  // ISO 8601 format
}
```

### before_tool_call

```typescript
// Input payload
interface BeforeToolCallPayload extends BasePayload {
  event: "before_tool_call";
  tool_name: string;
  tool_input: Record<string, unknown>;  // Tool-specific input
  tool_user_id: string;
}

// Output result
interface BeforeToolCallResult {
  blocked: boolean;
  reason?: string;  // Required if blocked is true
  input?: Record<string, unknown>;  // Modified input (omit to use original)
}
```

**Example Input:**
```json
{
  "event": "before_tool_call",
  "conv_id": "conversation-id",
  "tool_name": "bash",
  "tool_input": {"command": "ls -la"},
  "tool_user_id": "tool-call-id",
  "cwd": "/current/working/dir",
  "invoked_by": "main"
}
```

### after_tool_call

```typescript
// Input payload
interface AfterToolCallPayload extends BasePayload {
  event: "after_tool_call";
  tool_name: string;
  tool_input: Record<string, unknown>;
  tool_output: StructuredToolResult;
  tool_user_id: string;
}

// Output result
interface AfterToolCallResult {
  output?: StructuredToolResult;  // Modified output (omit to use original)
}
```

**Example Input:**
```json
{
  "event": "after_tool_call",
  "conv_id": "conversation-id",
  "tool_name": "bash",
  "tool_input": {"command": "ls -la"},
  "tool_output": {"toolName": "bash", "success": true, "timestamp": "2024-01-15T10:30:00Z"},
  "tool_user_id": "tool-call-id",
  "cwd": "/current/working/dir",
  "invoked_by": "main"
}
```

### user_message_send

```typescript
// Input payload
interface UserMessageSendPayload extends BasePayload {
  event: "user_message_send";
  message: string;
}

// Output result
interface UserMessageSendResult {
  blocked: boolean;
  reason?: string;  // Required if blocked is true
}
```

**Example Input:**
```json
{
  "event": "user_message_send",
  "conv_id": "conversation-id",
  "message": "User's message text",
  "cwd": "/current/working/dir",
  "invoked_by": "main"
}
```

### after_turn

The `after_turn` hook fires after every LLM response, regardless of whether tools were used. This enables:
- **Mid-session context compaction**: Detect high context usage and trigger compaction before it becomes critical
- **Per-turn logging**: Log each LLM turn for debugging or analytics
- **Turn-based rate limiting**: Implement custom turn limits or guardrails

```typescript
// Input payload
interface AfterTurnPayload extends BasePayload {
  event: "after_turn";
  turn_number: number;               // Current turn number (1-indexed)
  tools_used: boolean;               // Whether tools were used in this turn
  usage: UsageInfo;                  // Token usage statistics
  auto_compact_enabled: boolean;     // Whether auto-compact is enabled
  auto_compact_threshold?: number;   // Threshold ratio (e.g., 0.80)
}

// Output result
interface AfterTurnResult {
  result?: HookResult;               // Action to take ("mutate" or "callback")
  messages?: Message[];              // Messages for mutation (result="mutate")
  callback?: string;                 // Recipe to invoke (result="callback")
  callback_args?: Record<string, string>;  // Arguments for callback
}
```

**Example Input:**
```json
{
  "event": "after_turn",
  "conv_id": "conversation-id",
  "cwd": "/current/working/dir",
  "turn_number": 5,
  "tools_used": true,
  "usage": {
    "input_tokens": 80000,
    "output_tokens": 8000,
    "current_context_window": 88000,
    "max_context_window": 128000
  },
  "auto_compact_enabled": true,
  "auto_compact_threshold": 0.80,
  "invoked_by": "main"
}
```

**Result Types:**

1. **No action** (empty result): Continue normally
   ```json
   {}
   ```

2. **Mutate**: Replace conversation history in the current thread
   ```json
   {
     "result": "mutate",
     "messages": [{"role": "user", "content": "## Summary\n\nCompacted context..."}]
   }
   ```

3. **Callback**: Invoke a recipe (e.g., compact)
   ```json
   {
     "result": "callback",
     "callback": "compact"
   }
   ```

**Key Difference from agent_stop:**
- `after_turn` fires on **every turn** (including turns with tool use)
- `agent_stop` only fires when the agent is **completely done** (no more tool calls)
- Both apply mutations to the **current running thread** immediately

### agent_stop

The `agent_stop` hook is the most powerful hook type, supporting multiple result types for different use cases:
- **Continue**: Add follow-up messages to continue the conversation
- **Mutate**: Replace the entire conversation history with new messages
- **Callback**: Invoke a recipe/fragment to generate content

```typescript
// Token usage statistics
interface UsageInfo {
  input_tokens: number;
  output_tokens: number;
  current_context_window: number;
  max_context_window: number;
}

// Input payload
interface AgentStopPayload extends BasePayload {
  event: "agent_stop";
  messages: Message[];
  usage: UsageInfo;
  invoked_recipe?: string;           // Recipe that triggered this session (if any)
  auto_compact_enabled: boolean;     // Whether auto-compact is enabled
  auto_compact_threshold?: number;   // Threshold ratio (e.g., 0.80)
}

// Hook result types
type HookResult = "" | "continue" | "mutate" | "callback";

// Output result
interface AgentStopResult {
  result?: HookResult;               // Action to take
  follow_up_messages?: string[];     // Messages to continue conversation (result="" or "continue")
  messages?: Message[];              // Messages for mutation (result="mutate")
  callback?: string;                 // Recipe to invoke (result="callback")
  callback_args?: Record<string, string>;  // Arguments for callback
}
```

**Example Input:**
```json
{
  "event": "agent_stop",
  "conv_id": "conversation-id",
  "cwd": "/current/working/dir",
  "messages": [
    {"role": "user", "content": "Please fix the bug in main.go"},
    {"role": "assistant", "content": "I've fixed the bug by..."}
  ],
  "usage": {
    "input_tokens": 50000,
    "output_tokens": 5000,
    "current_context_window": 55000,
    "max_context_window": 128000
  },
  "invoked_recipe": "",
  "auto_compact_enabled": true,
  "auto_compact_threshold": 0.80,
  "invoked_by": "main"
}
```

**Result Types:**

1. **Continue** (default/empty): Add follow-up messages
   ```json
   {"follow_up_messages": ["Please also run the linter"]}
   ```
   
2. **Mutate**: Replace conversation history
   ```json
   {
     "result": "mutate",
     "messages": [{"role": "user", "content": "## Summary\n\nProject context..."}]
   }
   ```

3. **Callback**: Invoke a recipe
   ```json
   {
     "result": "callback",
     "callback": "compact"
   }
   ```

## Built-in Hooks

Kodelet includes built-in hooks for common functionality:

### Compact Hook (`builtin:compact-trigger`)

The built-in compact hook runs on `after_turn` events to detect when context threshold is reached and trigger compaction mid-session:

- Checks `usage.current_context_window / usage.max_context_window` against `auto_compact_threshold`
- When threshold is exceeded, returns `callback: "compact"` to invoke the compact recipe
- The compact recipe generates a summary, which is then applied to the current thread immediately
- The conversation is saved after mutation to persist the compacted context

This enables **proactive compaction** during long multi-turn sessions, preventing context from growing too large.

**Manual Compaction:**
```bash
# Compact the most recent conversation
kodelet run -r compact --follow

# Compact a specific conversation
kodelet run -r compact --resume <conversation-id>
```

**Hook-Triggered Compaction (Custom Hook):**
```bash
#!/bin/bash
# Custom hook to trigger compaction at 70% context utilization
# Note: The built-in after_turn hook already handles this, but you can customize the threshold

case "$1" in
    hook)
        echo "after_turn"
        ;;
    run)
        payload=$(cat)

        current=$(echo "$payload" | jq '.usage.current_context_window')
        max=$(echo "$payload" | jq '.usage.max_context_window')

        # Trigger compact at 70% utilization (before the default 80%)
        if [ "$max" -gt 0 ]; then
            ratio=$(echo "scale=2; $current / $max" | bc)
            if (( $(echo "$ratio > 0.70" | bc -l) )); then
                echo '{"result": "callback", "callback": "compact"}'
            fi
        fi
        ;;
esac
```

## Example Hooks

### Security Guardrail (Python)

```python
#!/usr/bin/env python3
import sys
import json

BLOCKED_COMMANDS = ["rm -rf", "sudo", ":(){:|:&};:"]
BLOCKED_PATTERNS = ["| bash", "| sh"]  # Pipe to shell

if len(sys.argv) < 2:
    sys.exit(1)

if sys.argv[1] == "hook":
    print("before_tool_call")
    sys.exit(0)

if sys.argv[1] == "run":
    payload = json.load(sys.stdin)

    if payload.get("tool_name") == "bash":
        tool_input = payload.get("tool_input", {})
        command = tool_input.get("command", "")

        for blocked in BLOCKED_COMMANDS:
            if blocked in command:
                print(json.dumps({
                    "blocked": True,
                    "reason": f"Security policy: '{blocked}' is not allowed"
                }))
                sys.exit(0)

        for pattern in BLOCKED_PATTERNS:
            if pattern in command:
                print(json.dumps({
                    "blocked": True,
                    "reason": f"Security policy: piping to shell is not allowed"
                }))
                sys.exit(0)

    print(json.dumps({"blocked": False}))
    sys.exit(0)

sys.exit(1)
```

### Audit Logger (Bash)

```bash
#!/bin/bash
if [ "$1" == "hook" ]; then
    echo "after_tool_call"
    exit 0
fi

if [ "$1" == "run" ]; then
    payload=$(cat)
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) | $payload" >> ~/.kodelet/audit.log
    # Empty output = no modification
    exit 0
fi

exit 1
```

### Follow-up Message Hook (Bash)

```bash
#!/bin/bash
# Asks the agent to clean up if foo.txt exists

case "$1" in
    hook)
        echo "agent_stop"
        ;;
    run)
        payload=$(cat)
        invoked_by=$(echo "$payload" | jq -r '.invoked_by')

        # Only act when invoked by the main agent
        if [ "$invoked_by" != "main" ]; then
            exit 0
        fi

        if [ -f "./foo.txt" ]; then
            echo '{"follow_up_messages":["I noticed foo.txt exists. Please remove it."]}'
        fi
        ;;
    *)
        exit 1
        ;;
esac
```

### Turn Logger Hook (Bash)

```bash
#!/bin/bash
# Logs context utilization after each turn

case "$1" in
    hook)
        echo "after_turn"
        ;;
    run)
        payload=$(cat)
        turn=$(echo "$payload" | jq '.turn_number')
        current=$(echo "$payload" | jq '.usage.current_context_window')
        max=$(echo "$payload" | jq '.usage.max_context_window')
        tools_used=$(echo "$payload" | jq '.tools_used')

        ratio=$(echo "scale=2; $current * 100 / $max" | bc)
        echo "Turn $turn: context ${ratio}% (tools_used: $tools_used)" >&2

        # No action needed
        exit 0
        ;;
    *)
        exit 1
        ;;
esac
```

### Custom Compaction Threshold Hook (Bash)

```bash
#!/bin/bash
# Triggers compact at a custom threshold (e.g., 70%) before the default 80%

case "$1" in
    hook)
        echo "after_turn"
        ;;
    run)
        payload=$(cat)

        current=$(echo "$payload" | jq '.usage.current_context_window')
        max=$(echo "$payload" | jq '.usage.max_context_window')

        # Trigger compact at 70% utilization
        if [ "$max" -gt 0 ]; then
            ratio=$(echo "scale=2; $current / $max" | bc)
            if (( $(echo "$ratio > 0.70" | bc -l) )); then
                echo '{"result": "callback", "callback": "compact"}'
            fi
        fi
        ;;
    *)
        exit 1
        ;;
esac
```

## Hook Behavior

### Error Handling

- Non-zero exit codes indicate hook failure
- Hook failures are logged but do not halt agent operation
- 30-second timeout prevents hung hooks from blocking the agent
- **Empty stdout with exit code 0** is treated as "no action" (not blocked, no modification)

### Deny-Fast Semantics

For blocking hooks (`before_tool_call`, `user_message_send`):
- If any hook blocks, execution stops immediately
- Subsequent hooks are not executed
- The first blocking reason is returned

### Message Accumulation

For `agent_stop` hooks:
- Follow-up messages from all hooks are accumulated
- Agent continues with all combined follow-up messages

## CLI Options

- `--no-hooks`: Disable all lifecycle hooks for a session

```bash
kodelet run --no-hooks "your query"
kodelet chat --no-hooks
```

## Debugging Hooks

1. Test hook discovery:
   ```bash
   ./your_hook hook  # Should output the hook type
   ```

2. Test hook execution:
   ```bash
   echo '{"event":"before_tool_call","tool_name":"bash","tool_input":{"command":"ls"}}' | ./your_hook run
   ```

3. Check logs for hook warnings (set `KODELET_LOG_LEVEL=debug`)

## Security Considerations

- Hooks have access to the current working directory and message content
- Hooks run with the same permissions as kodelet
- Repository-local hooks take precedence over global hooks
- Only executable files are considered hooks
- Hooks are not executed for `kodelet commit` and `kodelet pr` commands by default

## Related Documentation

- [ADR 021: Agent Lifecycle Hooks](../adrs/021-agent-lifecycle-hooks.md) - Original hook system design
- [ADR 025: Enhanced Hook System](../adrs/025-enhanced-hook-system.md) - Enhanced hooks with message mutation and callbacks
- [Fragments Guide](./FRAGMENTS.md) - Recipe/fragment template system
- [Tools Reference](./tools.md) - Available tools that can be hooked
