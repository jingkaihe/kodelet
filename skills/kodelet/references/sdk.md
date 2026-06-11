# TypeScript SDK

The TypeScript SDK package is imported as `kodelet`. It provides both an agent client API and the extension-authoring API, and re-exports Zod as `z`.

## Agent sessions

Use `Client` to launch Kodelet from Node/TypeScript and run prompts programmatically. By default the client uses the normal `kodelet` executable and the user's default profile/configuration.

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

Streaming sessions emit typed SDK events derived from ACP `session/update` JSON-RPC notifications:

```typescript
import { Client, Profile, defineExtension, z } from "kodelet";

const askQuestionExtension = defineExtension((ext) => {
  ext.setMetadata({ name: "workspace", version: "0.1.0" });

  ext.registerTool({
    name: "ask_user_question",
    description: "Ask the user to choose one option.",
    inputSchema: z.object({
      question: z.string(),
      options: z.array(z.string()).min(2).max(5),
    }),
    async execute(input, ctx) {
      const choice = await ctx.ui.select({
        title: input.question,
        options: input.options,
        submitButtonText: "Select",
      });
      return choice ? `User selected: ${choice}` : "User dismissed the question.";
    },
  });
});

const profile = new Profile({
  name: "openai",
  provider: "openai",
  model: "gpt-5.5",
  max_tokens: 128000,
  reasoning_effort: "xhigh",
  tool_mode: "patch",
  weak_model: "gpt-5.4-mini",
  weak_model_max_tokens: 8192,
  enable_fs_search_tools: false,
  openai: {
    api_mode: "responses",
    platform: "codex",
    service_tier: "fast",
  },
});

const client = new Client();
const session = await client.createSession({
  profile,
  extensions: [askQuestionExtension],
  streaming: true,
  ui: {
    async select(request) {
      console.error(request.title, request.options);
      return request.options[0];
    },
  },
});

session.on("assistant.message_delta", (event) => {
  process.stdout.write(event.data.deltaContent);
});
session.on("tool.call", (event) => {
  console.error(`tool: ${event.data.toolName}`, event.data.input);
});

const response = await session.runAndWait({ message: "help me choose an approach" });
console.log("\nfinal:", response.content);
await client.close();
```

Inline extensions passed to `createSession({ extensions: [...] })` are exposed to Kodelet through a temporary JSON-RPC bridge for that session. The bridge uses a Unix domain socket (or Windows named pipe) by default; set `extensionTransport: "tcp"` to use an ephemeral loopback TCP port instead. Sessions without inline extensions use the normal `.kodelet/extensions` and plugin discovery flow.

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
    };
  },
});
```

Command result actions:

- `pass`: decline handling and continue normal prompt routing.
- `respond`: display a direct terminal/Web UI response; it is not fed into the LLM.
- `runAgent`: replace the prompt and run the normal agent flow; this prompt becomes LLM input.

Recipe-like commands use `kind: "recipe"`, appear in `kodelet recipe list`, can be invoked with `kodelet run -r review --arg target=main`, and can be invoked directly as `/review target=main`.

## Lifecycle events

Subscribe with `ext.on(...)`.

Common events:

- `session.start`, `resources.discover`, `session.end`.
- `user.message`.
- `agent.init`, `agent.start`, `agent.end`.
- `turn.start`, `turn.end`.
- `tool.call`, `tool.result`.

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
- `inline-extension-session.ts` exposes an in-process TypeScript extension with an `sdk_echo` tool for the session.
