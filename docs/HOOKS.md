# Agent Lifecycle Hooks

Agent lifecycle hooks have been removed. Kodelet now uses **Extensions** as the single external extensibility primitive for model tools, prompt commands, dynamic recipes, and lifecycle event handlers.

## Replacement

Use an extension executable named `kodelet-extension-*` under one of the extension roots:

```text
./.kodelet/extensions/kodelet-extension-xxx
./.kodelet/extensions/*/kodelet-extension-xxx
~/.kodelet/extensions/kodelet-extension-xxx
~/.kodelet/extensions/*/kodelet-extension-xxx
```

Extensions are long-running subprocesses that communicate with Kodelet over stdio JSON-RPC. TypeScript extensions can use the `@jingkaihe/kodelet` SDK:

```typescript
import { z, defineExtension } from "@jingkaihe/kodelet";

const Input = z.object({ location: z.string() });

export default defineExtension((ext) => {
  ext.registerTool({
    name: "get_weather",
    description: "Get weather for a location",
    inputSchema: Input,
    async execute(input) {
      return { content: `Weather for ${input.location}: cloudy` };
    },
  });

  ext.on("tool.call", async (event) => {
    if (event.tool.name === "bash" && String(event.tool.input).includes("rm -rf /")) {
      return { block: { reason: "Dangerous command denied" } };
    }
  });
});
```

## Hook migration map

| Removed hook | Extension event |
|---|---|
| `before_tool_call` | `tool.call` |
| `after_tool_call` | `tool.result` |
| `user_message_send` | `user.message` |
| `agent_stop` | `agent.end` |
| `turn_end` | `turn.end` |

Use `kodelet run --no-extensions` or `extensions.enabled: false` to disable extension loading. See [`extension-design.md`](extension-design.md) for the protocol, SDK, discovery, configuration, and event semantics.
