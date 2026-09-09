# TypeScript SDK

The TypeScript SDK package is imported as `kodelet`. It provides both an agent client API and the extension-authoring API, and re-exports Zod as `z`.

## Agent sessions

Use `Client` to launch the thin `kodelet acp` daemon client from Node/TypeScript. Set `server` and `runner` on `Client`, or use the normal daemon selection configuration. Standalone clients authenticate with client credentials (for example `KODELET_AUTH_TOKEN`); provider credentials stay on the daemon. `cwd` is interpreted by the selected runner, not used as the local subprocess directory.

```typescript
import { Client } from "kodelet";

const client = new Client();
const session = await client.createSession();

const response = await session.runAndWait({
  message: "what is the meaning of life",
});

console.log(response.content);
await client.close();
```

ACP accepts individual stdout messages up to 64 MiB, including large saved-conversation replays and live tool results, and preserves UTF-8 across subprocess chunks. This is not a limit on total conversation history. A message above that bound, a pipe error, or unexpected stdout closure rejects pending requests and stops the child. Always await `session.close()` or `client.close()`: closing rejects pending work immediately, waits for process closure, and escalates from SIGTERM to SIGKILL after one second. If closure is still unconfirmed after another second, cleanup rejects instead of claiming success; keep any background lease until cleanup succeeds. Concurrent closes share the same cleanup attempt, and a failed close can be retried.

Streaming sessions emit typed SDK events derived from ACP `session/update` JSON-RPC notifications:

```typescript
import { Client } from "kodelet";

const client = new Client();
const session = await client.createSession({
  profile: "work", // Daemon-owned model profile; optional.
  environmentProfile: "workspace", // Runner-owned environment profile; optional.
  options: { maxTokens: 8192, maxTurns: 4, noSkills: true },
  streaming: true,
});

session.on("assistant.message_delta", (event) => {
  process.stdout.write(event.data.deltaContent);
});
session.on("tool.call", (event) => {
  console.error(`tool: ${event.data.toolName}`, event.data.input);
});
session.on("tool.update", (event) => {
  // Accumulated snapshots replace earlier updates with the same toolCallId.
  console.error(`partial ${event.data.toolCallId}:`, event.data.result);
});
session.on("tool.result", (event) => {
  console.error(`final ${event.data.toolCallId}:`, event.data.result);
});

const response = await session.runAndWait({ message: "help me choose an approach" });
console.log("\nfinal:", response.content);
await client.close();
```

Listeners receive every `tool.update`. To keep completed responses bounded, `response.events` retains only the latest transient snapshot for each `toolCallId`, followed by the authoritative `tool.result`.

`options` accepts the typed `ExecutionOptions` contract: provider/model/weak model, token and turn limits, reasoning effort, weak-model selection, tool/skill/extension restrictions, command allowlists, and filesystem-search enablement. Explicit `false`, `0` where permitted, and empty lists are preserved. Inline `Profile` values accept the same options (legacy snake_case aliases are converted); an inline `name` is only a label, not a required daemon profile. Arbitrary provider configuration, endpoints, credentials, and client-local prompt paths are rejected. Model settings are locked when resuming a persisted conversation.

Temporary SDK config files and the legacy `extensionTransport` selector no longer configure remote execution. `createSession({ extensions, ui })` uses negotiated inline ACP session extensions, while installed executable extensions run on the runner. Use ordinary `Client` sessions with client authentication for delegated model work. `session.close()` detaches; explicit `session.cancel()` cancels the active turn.

### Steering an active session

Use `session.steer(message)` to add guidance to the prompt that is currently running. Wait for a streaming event that confirms ACP has started the prompt before steering:

```typescript
const active = new Promise<void>((resolve) => {
  session.once("assistant.thinking_start", () => resolve());
});
const run = session.runAndWait({
  message: "Review the persistence implementation",
});

await active;
const steering = await session.steer("Also check transaction boundaries");
const response = await run;
```

`steer()` uses the ACP `_session/steering` extension and returns an outcome such as `{ outcome: "injected" }`. It rejects when the session has no active run or the ACP server does not advertise `_meta.steering.supported`. The SDK requests `idleBehavior: "promptRequired"`, so an end-of-turn race returns `{ outcome: "promptRequired", reason: "noRunningTurn" }` rather than silently starting another turn. `injected` means Kodelet queued the message, not that the model consumed it before the prompt ended; guidance left unconsumed remains on the conversation for a later run. Blank messages are rejected locally.

## Extension definitions

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

During local development, a wrapper can also run `tsx` against `src/index.ts` as shown in `examples/extensions/workspace/kodelet-extension-workspace`.

## Extension tools

Register tools with `ext.registerTool(...)`, provide a Zod `inputSchema`, and return either a string or an object like:

```typescript
return {
  content: "Assistant-facing result",
  data: { structured: true },
  error: undefined,
};
```

Per-tool enablement lives under `extensions.tools.<tool-name>.enabled`. Tool timeouts use SDK `timeoutInSec` or the built-in 10 minute fallback.

### Live conversation forks

Create a named child conversation from an active tool context:

```typescript
const conversationId = await ctx.forkConversation({ name: "Investigate authentication" });
```

Omit `name` to preserve the source title. Unavailable forks raise `ConversationForkUnavailableError`.

### ACP subagents and code search

Use ordinary `Client` sessions with normal client credentials and the intended server/runner. Pass typed `options` directly; `profile` selects a configured or registered daemon profile, and `options.provider` must match it. Scoped `ctx.children` and `get_profile` remain removed.

Use inline `agent.init` hooks for instructions, keeping extensions enabled. Explicitly allowlist search tools and disable skills; runner policy still applies, but the caller's presentation-only tool list is not inherited.

For inherited context, create a named fork inside the tool handler, then load it with `createSession({ resume: conversationId })`. ACP preserves normal history and streaming; `TaskProgress.attach(session)` tracks activity. Steering never starts a new turn. Own cancellation and client cleanup before releasing any background lease. Runner-targeted credentials remain phase two.

### Registered model profiles

Declare a profile, then use its returned name in a later tool handler's `client.createSession({ profile: searchProfile })`:

```typescript
const searchProfile = ext.registerProfile({
  name: "code-search",
  provider: "openai",
  model: "gpt-5.6-luna",
  reasoningEffort: "none",
  hidden: true,
});
```

For Claude subscriptions, use `provider: "anthropic"` and `anthropicAPIAccess: "subscription"` (Python: `anthropic_api_access`). Provider credentials stay on the daemon.

### Background extension work

Acquire a host lifetime lease before starting work that can continue after the current handler returns, and release it only after the worker and its final state or UI updates finish:

```typescript
const lease = await ctx.acquireBackgroundTask("index repository");
try {
  await runBackgroundWorker();
} finally {
  await lease.close();
}
```

Persistent local runtimes return a no-op lease. Runner-backed runtimes use the lease to retain the conversation's extension process and execution instance after the parent run closes. The lease is not task persistence: extensions that need crash recovery should store their own state beneath `ctx.storage.dataDir` and reconcile it when the process starts again.

## User input from extensions

Tool and event contexts can request UI input from the host:

```typescript
const choice = await ctx.ui.select({
  title: "Choose an option",
  message: "Pick one approach.",
  options: ["Fast", "Safe", "Skip"],
  submitButtonText: "Select",
});

const confirmed = await ctx.ui.confirm({
  title: "Allow?",
  message: "A tool call is about to run.",
});
```

The workspace example uses this to ask users whether to allow or deny bash commands, and can remember exact command decisions in extension storage.

## Persistent TUI widgets and surfaces

The native `kodelet chat` host advertises `capabilities.ui.transcript`, `capabilities.ui.widgets`, and `capabilities.ui.surfaces`. Other hosts currently do not: `ctx.ui.appendTranscript(...)` and `ctx.ui.setWidget(...)` become no-ops without support, and `ctx.ui.openSurface(...)` rejects without surface support.

Use `appendTranscript` for durable informational text that should appear in the transcript without being represented as a user or assistant message.

```typescript
await ctx.ui.appendTranscript({ title: "Drawing saved", message: "./drawing.png" });
```

Use `setWidget` for passive text or styled-line content above or below the composer. For multi-line widgets, make the first line a useful summary: the native TUI keeps it visible while the user folds or unfolds the remaining lines by clicking it or pressing `Ctrl+O`. Widgets start expanded, and reusing an ID updates the widget without resetting its fold state; passing `undefined` removes it.

```typescript
await ctx.ui.setWidget("status", [
  "Extension state",
  { spans: [{ text: " ready", style: { foreground: "#00ff00", bold: true } }] },
]);
await ctx.ui.setWidget("status", ["Moved"], { placement: "belowComposer" });
await ctx.ui.setWidget("status", undefined);
```

Use `openSurface` for an overlay that persists after its opening handler returns. Sizes are cell counts or percentage strings; anchors include all corners, edges, and center. A `nonCapturing` surface does not take keyboard focus.

```typescript
const surface = await ctx.ui.openSurface({
  id: "game",
  initialLines: ["Loading…"],
  width: "75%",
  height: "80%",
  anchor: "center",
  margin: { top: 1, right: 1, bottom: 1, left: 1 },
});

surface.onResize(({ width, height }) => {
  surface.update([`Surface size: ${width}×${height}`]);
});

surface.onInput((event) => {
  if (event.kind === "mouse") console.error(event.mouse?.x, event.mouse?.y);
  if (event.kind === "key" && event.key === "q") void surface.close();
});
```

`surface.update(...)` is intentionally synchronous and replace-in-place. Persistent UI is scoped to the originating conversation, so one extension can use the same widget or surface ID independently in multiple conversations. The SDK keeps one frame in flight and one replaceable latest pending frame per surface, waiting for transport writes before sending the next snapshot; the Bubble Tea host independently keeps only the newest pending sequence per scoped surface. Input, focus, blur, and resize events use a separate ordered sequence, and stale events are discarded. A failed `surface.close()` keeps the handle owned and retryable; successful close releases the ID. If the extension process fails, Kodelet removes its widgets/surfaces and restores focus automatically.

Use `surface.onClose(handler)` to clean up recording, timers, or input waits when the host closes the UI or `surface.close()` succeeds. It returns an unsubscribe function and calls the handler once, immediately if already closed. Surface closure does not cancel the daemon execution.

## Commands and dynamic recipes

Prompt commands are checked before the LLM receives user input.

```typescript
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
      display: `Please review ${input.target}`,
    };
  },
});
```

Command result actions:

- `pass`: decline handling and continue normal prompt routing.
- `respond`: display a direct terminal/Web UI response; it is not fed into the LLM.
- `runAgent`: replace the prompt and run the normal agent flow; this prompt becomes LLM input. Set optional `display` to replace the slash command with different visible and persisted user text.

Recipe-like commands use `kind: "recipe"`, appear in `kodelet recipe list` for the selected workspace, can be invoked with `kodelet run -r review --arg target=main`, and can be invoked directly as `/review target=main`. Recipe listing starts discovery extensions on the runner; recipe rendering can execute template commands in the selected directory.

## Native TUI shortcuts

Register a direct keyboard handler with `ext.registerShortcut(...)`:

```typescript
ext.registerShortcut("ctrl+alt+r", {
  description: "Refresh project context",
  async handler(ctx) {
    await ctx.ui.notify({ title: "Project context", message: `Refreshed ${ctx.cwd}` });
  },
});
```

The handler executes directly with a `ShortcutContext`. With `capabilities.shortcuts.submit`, it may return `{ action: "submit", message }`; unsupported submit results fail explicitly. Effective shortcuts appear in the native TUI's `?` dialog. Supported case-insensitive ASCII forms are `ctrl+<ASCII letter>`, `alt+<ASCII letter-or-digit>`, `ctrl+alt+<ASCII letter>`, and unmodified `f1` through `f12`; `control` and `option` are aliases. `ctrl+i` and `ctrl+m`, including Ctrl+Alt variants, are rejected because terminals report them as Tab and Enter. Shift, Command/Meta/Super, modified function keys, punctuation, spaces, non-ASCII characters, and navigation-key combinations are unsupported, and registration fails rather than advertising an unreachable binding. Reserved host bindings are skipped, other built-in composer bindings may be overridden with a warning, and later-loaded extensions win extension-to-extension conflicts.

Shortcuts currently execute only in local native `kodelet chat` sessions, not Web UI, ACP, or runner-backed hosts.

## Lifecycle events

Subscribe with `ext.on(...)`.

Common events:

- `session.start`, `resources.discover`, `session.end`.
- `user.message`.
- `agent.init`, `agent.start`, `agent.end`.
- `turn.start`, `turn.end`.
- `tool.call`, `tool.update`, `tool.result`.

Mutating/blocking events run sequentially by priority, discovery order, then registration order. The first blocking handler stops the operation. Events use SDK `timeoutInSec` or the built-in 30 second default.

Legacy mapping:

| Old concept | Extension event |
| --- | --- |
| `before_tool_call` | `tool.call` |
| `after_tool_call` | `tool.result` |

## Examples

Runnable TypeScript SDK examples live in `skills/kodelet/examples/sdk/`:

- `basic-agent-session.ts` runs one prompt and prints the final response.
- `streaming-agent-session.ts` streams assistant deltas as they arrive.
