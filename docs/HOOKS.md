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
| `agent_stop` | When agent would stop | No | Can return follow-up messages |
| `turn_end` | After each assistant response | No | Thread state (via built-in handlers) |

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

Hooks are discovered from four locations (earlier takes precedence):

1. `.kodelet/hooks/` - Repository-local standalone hooks (highest precedence)
2. `.kodelet/plugins/<org@repo>/hooks/` - Repository-local plugin hooks
3. `~/.kodelet/hooks/` - User-global standalone hooks
4. `~/.kodelet/plugins/<org@repo>/hooks/` - User-global plugin hooks (lowest precedence)

Plugin directories use `<org@repo>` on disk, but exposed names always use `org/repo/...` format.

Only executable files are considered. Directories and non-executable files are skipped.

### Plugin-based Hook Naming

Hooks from plugins are prefixed with `org/repo/` to avoid naming collisions:
- Standalone hook: `audit-logger`
- Plugin hook: `jingkaihe/hooks/audit-logger`

This naming convention is consistent with skills and recipes from plugins.

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
type HookType = "before_tool_call" | "after_tool_call" | "user_message_send" | "agent_stop" | "turn_end";

// Indicates whether the hook was triggered by main agent or subagent
type InvokedBy = "main" | "subagent";

// Base payload included in all hook events
interface BasePayload {
  event: HookType;
  conv_id: string;
  cwd: string;
  invoked_by: InvokedBy;
  recipe_name?: string;  // Present when invoked via a recipe (e.g., "compact", "jingkaihe/recipes/init")
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

### agent_stop

```typescript
// Input payload
interface AgentStopPayload extends BasePayload {
  event: "agent_stop";
  messages: Message[];
}

// Output result
interface AgentStopResult {
  follow_up_messages?: string[];  // Optional messages to continue conversation
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
  "invoked_by": "main"
}
```

### turn_end

The `turn_end` hook fires after each assistant response, before the next user message. This hook is particularly useful for post-turn operations like context compaction.

```typescript
// Input payload
interface TurnEndPayload extends BasePayload {
  event: "turn_end";
  response: string;      // The assistant's response text
  turn_number: number;   // Which turn in the conversation (1-indexed)
}

// Output result
interface TurnEndResult {
  // Currently no output fields; reserved for future extensions
}
```

**Example Input:**
```json
{
  "event": "turn_end",
  "conv_id": "conversation-id",
  "cwd": "/current/working/dir",
  "response": "I've completed the analysis...",
  "turn_number": 1,
  "invoked_by": "main"
}
```

## Built-in Hook Handlers

In addition to external hook scripts, Kodelet supports built-in handlers that can be invoked via recipe metadata. These handlers have direct access to the LLM thread state.

### Available Built-in Handlers

| Handler | Hook Event | Description |
|---------|-----------|-------------|
| `swap_context` | `turn_end` | Replaces the conversation history with the assistant's response (used for context compaction) |

### Using Built-in Handlers in Recipes

Built-in handlers are declared in recipe YAML frontmatter:

```markdown
---
name: compact
description: Compact the conversation context
hooks:
  turn_end:
    handler: swap_context
    once: true
allowed_tools: []
---
Your prompt content here...
```

The `once: true` option ensures the handler only executes on the first turn, preventing repeated execution in follow-up conversations.

See [Fragments/Recipes Documentation](./FRAGMENTS.md#recipe-hooks) for more details on recipe hooks.

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

### Recipe-Aware Hook (Bash)

```bash
#!/bin/bash
# Hook that only triggers for a specific recipe
# Demonstrates recipe-aware hook filtering using the recipe_name field

case "$1" in
    hook)
        echo "turn_end"
        ;;
    run)
        payload=$(cat)
        recipe_name=$(echo "$payload" | jq -r '.recipe_name // ""')

        # Only act for the "intro" recipe
        if [ "$recipe_name" != "intro" ]; then
            exit 0
        fi

        # Extract info from payload
        turn_number=$(echo "$payload" | jq -r '.turn_number // 0')
        conv_id=$(echo "$payload" | jq -r '.conv_id // "unknown"')

        # Log to a file
        echo "[$(date -Iseconds)] Recipe: $recipe_name | Turn: $turn_number | ConvID: $conv_id" >> /tmp/intro-hook.log
        exit 0
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

- [ADR 021: Agent Lifecycle Hooks](../adrs/021-agent-lifecycle-hooks.md) - Architecture decision record for core hook system
- [ADR 029: Plugin Hooks](../adrs/029-plugin-hooks.md) - Architecture decision record for plugin-based hooks
- [Tools Reference](./tools.md) - Available tools that can be hooked
