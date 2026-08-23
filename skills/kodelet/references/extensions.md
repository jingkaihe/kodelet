# Extensions

Extensions are Kodelet's unified external extensibility primitive. They are long-running subprocess that can register model tools, prompt commands, dynamic recipes, native TUI shortcuts, and lifecycle event handlers.

Extensions communicate over stdio JSON-RPC using `Content-Length` framing. `stdout` is reserved for protocol messages; `stderr` is used for logs.

## What extensions can provide

- **Model tools**: tools exposed alongside built-in and MCP tools.
- **Prompt commands**: slash-style or named commands checked before the LLM sees the prompt.
- **Dynamic recipes**: command registrations with `kind: "recipe"` that appear in recipe listings and can be run with `kodelet run -r`.
- **Native TUI shortcuts**: direct keyboard handlers registered with `ext.registerShortcut(...)`.
- **Lifecycle event handlers**: observers/mutators/blockers for session, user, agent, turn, and tool events.
- **Persistent TUI UI**: passive styled widgets above/below the composer and interactive overlay surfaces with focus, keyboard, mouse, and resize notifications.

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

  ext.registerShortcut("ctrl+alt+r", {
    description: "Refresh project context",
    async handler(ctx) {
      await ctx.ui.notify({ title: "Project context", message: `Refreshed ${ctx.cwd}` });
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
- Native TUI shortcuts use `ext.registerShortcut(key, { description?, handler })` and execute the handler directly.
- `runAgent` results may set `display` when the visible and persisted user message should differ from the model-facing `prompt`.
- Recipe-like commands use `kind: "recipe"`, appear in `kodelet recipe list`, and can be invoked through `kodelet run -r` or directly as `/name`.
- Lifecycle handlers use `ext.on(...)` for events like `session.start`, `user.message`, `agent.init`, `turn.start`, `tool.call`, `tool.update`, `tool.result`, and `agent.end`.
- Tool and event contexts can call host UI helpers such as `ctx.ui.input`, `ctx.ui.confirm`, `ctx.ui.select`, and `ctx.ui.notify`.
- Native TUI contexts can call `ctx.ui.setWidget(...)` and `ctx.ui.openSurface(...)` when the host advertises `ui.widgets` and `ui.surfaces`; multi-line widgets use their first line as a foldable summary in the TUI.

Mutating/blocking event handlers run sequentially by priority, discovery order, then registration order. The first blocking handler stops the operation. Events use SDK `timeoutInSec` or the built-in 30 second default.

Extensions that sanitize `tool.result` should apply the same policy in `tool.update`; Kodelet suppresses streaming snapshots when a result-subscribing extension does not also subscribe to updates. If an update sanitizer errors or returns invalid output, Kodelet drops that transient snapshot rather than exposing unsanitized output.

Legacy hook mapping:

| Old concept | Extension event |
| --- | --- |
| `before_tool_call` | `tool.call` |
| `after_tool_call` | `tool.result` |

## Runtime protocol

Extension subprocesses communicate with Kodelet over stdio JSON-RPC using `Content-Length` framing. Kodelet sends initialization, tool execution, command execution, and lifecycle event requests to the extension process. Extension code should reserve `stdout` for protocol messages and write logs to `stderr`.

The TypeScript SDK's `runExtension(...)` helper implements this protocol for extensions. Non-SDK extensions can implement the same JSON-RPC methods directly. Shortcut handlers are invoked through `extension.shortcut.execute`. Kodelet supplies an opaque `context.uiScopeId` for conversation-scoped calls, and persistent UI messages carry it as `scopeId`, including an explicit empty string for host-global objects, parentless frame updates, and later surface close requests. Host input and resize arrive as `extension.ui.surface.input` and `extension.ui.surface.resize` notifications with the same scope. Positive sequence numbers reject stale frames/events per scoped object, and the TUI coalesces pending presentation frames to the latest snapshot.

Shortcut keys are case-insensitive ASCII single chords. Supported forms are `ctrl+<ASCII letter>`, `alt+<ASCII letter-or-digit>`, `ctrl+alt+<ASCII letter>`, and an unmodified `f1` through `f12`; `control` and `option` are accepted aliases. `ctrl+i` and `ctrl+m`, including Ctrl+Alt variants, are rejected because terminals report them as Tab and Enter. Shift, Command/Meta/Super, modified function keys, punctuation, spaces, non-ASCII characters, and navigation-key combinations are unsupported. Invalid registrations are not advertised. The native TUI skips conflicts with reserved host keys, allows other built-in composer bindings to be overridden with a warning, and uses the later-loaded extension when two extensions register the same shortcut. Shortcuts currently run only in local native `kodelet chat` sessions.

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

Extension-provided tools, commands, dynamic recipes, and native TUI shortcuts load through the extension runtime when extensions are enabled.

## Example project

See `examples/extensions/workspace/` in this skill. It includes:

- `src/index.ts` — registers `ask_user_question`, shows UI prompts, handles `agent.start`, and intercepts `tool.call` for bash approval.
- `kodelet-extension-workspace` — executable wrapper.
- `package.json` / `tsconfig.json` — TypeScript development setup.
