# Extensions

Extensions are Kodelet's unified external extensibility primitive. They are long-running subprocess that can register model tools, prompt commands, dynamic recipes, and lifecycle event handlers.

Extensions communicate over stdio JSON-RPC using `Content-Length` framing. `stdout` is reserved for protocol messages; `stderr` is used for logs.

## What extensions can provide

- **Model tools**: tools exposed alongside built-in and MCP tools.
- **Prompt commands**: slash-style or named commands checked before the LLM sees the prompt.
- **Dynamic recipes**: command registrations with `kind: "recipe"` that appear in recipe listings and can be run with `kodelet run -r`.
- **Lifecycle event handlers**: observers/mutators/blockers for session, user, agent, turn, and tool events.

Use the TypeScript SDK to author extension subprocesses, or implement the JSON-RPC protocol directly. See `references/sdk.md` for SDK agent sessions and the full SDK API surface.

## Authoring a standalone extension

Most TypeScript extensions use `defineExtension(...)` and `runExtension(...)` from the `kodelet` package:

```typescript
import { z, defineExtension } from "kodelet";
import { runExtension } from "kodelet/runtime";

const WeatherInput = z.object({
  location: z.string().describe("Location to fetch weather for"),
});

const extension = defineExtension((ext) => {
  ext.setMetadata({ name: "weather", version: "0.1.0" });

  ext.registerTool({
    name: "get_weather",
    description: "Get weather for a location",
    inputSchema: WeatherInput,
    timeoutInSec: 600,
    async execute(input, ctx) {
      ctx.log.info(`Fetching weather for ${input.location}`);
      return {
        content: `Weather for ${input.location}: cloudy`,
        data: { location: input.location, condition: "cloudy" },
      };
    },
  });

  ext.registerCommand({
    name: "review",
    aliases: ["/review"],
    description: "Run an extension-provided review recipe",
    kind: "recipe",
    inputSchema: z.object({ target: z.string().default("HEAD") }),
    timeoutInSec: 1800,
    async execute(input) {
      return {
        action: "runAgent",
        recipeName: "review",
        prompt: `Review ${input.target}. Focus on correctness, simplicity, and tests.`,
      };
    },
  });

  ext.on("tool.call", { priority: 100, timeoutInSec: 5 }, async (event) => {
    if (
      event.tool.name === "bash" &&
      JSON.stringify(event.tool.input).includes("rm -rf /")
    ) {
      return { block: { reason: "Dangerous command denied" } };
    }
  });
});

await runExtension(extension);
```

Recommended extension layout:

```text
.kodelet/extensions/weather/
  package.json
  src/index.ts
  dist/index.js
  kodelet-extension-weather
```

Wrapper example:

```bash
#!/usr/bin/env bash
exec kodelet-extension-node ./dist/index.js
```

During local development, a wrapper can run `tsx` against `src/index.ts`, as shown in `examples/extensions/workspace/kodelet-extension-workspace`.

### Tools, commands, events, and UI helpers

- Tools use `ext.registerTool(...)`, a Zod `inputSchema`, and return either a string or `{ content, data?, error? }`.
- Prompt commands use `ext.registerCommand(...)` and return `pass`, `respond`, or `runAgent` actions.
- Recipe-like commands use `kind: "recipe"`, appear in `kodelet recipe list`, and can be invoked through `kodelet run -r` or directly as `/name`.
- Lifecycle handlers use `ext.on(...)` for events like `session.start`, `user.message`, `agent.init`, `turn.start`, `tool.call`, `tool.result`, and `agent.end`.
- Tool and event contexts can call host UI helpers such as `ctx.ui.input`, `ctx.ui.confirm`, `ctx.ui.select`, and `ctx.ui.notify`.

Mutating/blocking event handlers run sequentially by priority, discovery order, then registration order. The first blocking handler stops the operation. Events use SDK `timeoutInSec` or the built-in 30 second default.

Legacy hook mapping:

| Old concept | Extension event |
| --- | --- |
| `before_tool_call` | `tool.call` |
| `after_tool_call` | `tool.result` |

## Runtime protocol

Extension subprocesses communicate with Kodelet over stdio JSON-RPC using `Content-Length` framing. Kodelet sends initialization, tool execution, command execution, and lifecycle event requests to the extension process. Extension code should reserve `stdout` for protocol messages and write logs to `stderr`.

The TypeScript SDK's `runExtension(...)` helper implements this protocol for extensions. Non-SDK extensions can implement the same JSON-RPC methods directly.

## Discovery

Kodelet discovers executable files named `kodelet-extension-*` in this precedence order:

1. `./.kodelet/extensions`.
2. `./.kodelet/plugins/<org@repo>/extensions`.
3. `~/.kodelet/extensions`.
4. `~/.kodelet/plugins/<org@repo>/extensions`.

Within each root, both forms are supported:

```text
<extension-root>/kodelet-extension-xxx
<extension-root>/*/kodelet-extension-xxx
```

The executable filename must be `kodelet-extension-xxx`. Kodelet derives the extension ID/name as `xxx` for a direct executable, or as the parent directory name for a nested executable. Plugin extension IDs are addressed as `org@repo/extension`; standalone extensions are matched by directory or executable path in allow/deny config.

Inspect extensions:

```bash
kodelet extension list
kodelet extension list --json
kodelet extension inspect weather
kodelet extension inspect org@repo/weather --json
```

## Configuration

```yaml
extensions:
  enabled: true
  global_dir: ~/.kodelet/extensions
  local_dir: ./.kodelet/extensions
  max_output_size: 102400

  allow:
    - org@repo/security
    - ./.kodelet/extensions/weather
    - ~/.kodelet/extensions/kodelet-extension-gh

  deny:
    - org@repo/experimental-extension
    - /absolute/path/to/kodelet-extension-experimental

  tools:
    get_weather:
      enabled: true

  processes:
    weather:
      env:
        WEATHER_API_KEY: null
```

Config semantics:

- `enabled`: disables all extension discovery and execution when false.
- `global_dir` / `local_dir`: standalone extension roots.
- `max_output_size`: maximum assistant-facing extension tool output.
- `allow` / `deny`: extension allow/deny lists; deny wins when both match.
- `tools`: per-tool enablement.
- `processes`: per-extension process config, including env injection/inheritance.

Disable for one run:

```bash
kodelet run --no-extensions "query"
kodelet acp --no-extensions
```

## Plugin extensions

Plugins can provide extension executables under `extensions/`:

```bash
kodelet plugin add orgname/extensions
kodelet plugin list
kodelet plugin show orgname/extensions
```

Extension-provided tools, commands, and dynamic recipes load through the extension runtime when extensions are enabled.

## Example project

See `examples/extensions/workspace/` in this skill. It includes:

- `src/index.ts` — registers `ask_user_question`, shows UI prompts, handles `agent.start`, and intercepts `tool.call` for bash approval.
- `kodelet-extension-workspace` — executable wrapper.
- `package.json` / `tsconfig.json` — TypeScript development setup.
