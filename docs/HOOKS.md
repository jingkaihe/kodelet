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

## Payload Structures

### before_tool_call

**Input:**
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

**Output:**
```json
{
  "blocked": false,
  "reason": "optional reason if blocked",
  "input": {"modified": "input"}
}
```

- `blocked`: Set to `true` to prevent tool execution
- `reason`: Explanation shown to the agent (required if blocked)
- `input`: Modified tool input (omit or null to use original)

### after_tool_call

**Input:**
```json
{
  "event": "after_tool_call",
  "conv_id": "conversation-id",
  "tool_name": "bash",
  "tool_input": {"command": "ls -la"},
  "tool_output": {"toolName": "bash", "success": true, ...},
  "tool_user_id": "tool-call-id",
  "cwd": "/current/working/dir",
  "invoked_by": "main"
}
```

**Output:**
```json
{
  "output": {"toolName": "bash", "success": true, "error": "[REDACTED]", ...}
}
```

- `output`: Modified tool output (omit or null to use original)

### user_message_send

**Input:**
```json
{
  "event": "user_message_send",
  "conv_id": "conversation-id",
  "message": "User's message text",
  "cwd": "/current/working/dir",
  "invoked_by": "main"
}
```

**Output:**
```json
{
  "blocked": false,
  "reason": "optional reason if blocked"
}
```

### agent_stop

**Input:**
```json
{
  "event": "agent_stop",
  "conv_id": "conversation-id",
  "cwd": "/current/working/dir",
  "messages": [...],
  "invoked_by": "main"
}
```

**Output:**
```json
{
  "follow_up_messages": ["Please also run the tests.", "Check the documentation."]
}
```

- `follow_up_messages`: Optional messages to continue the conversation

## Example Hooks

### Security Guardrail (Python)

```python
#!/usr/bin/env python3
import sys
import json

BLOCKED_COMMANDS = ["rm -rf", "sudo", "curl | bash"]

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
        if [ -f "./foo.txt" ]; then
            echo '{"follow_up_messages":["I noticed foo.txt exists. Please remove it."]}'
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

- [ADR 021: Agent Lifecycle Hooks](../adrs/021-agent-lifecycle-hooks.md) - Architecture decision record
- [Tools Reference](./tools.md) - Available tools that can be hooked
