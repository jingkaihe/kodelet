# Kodelet TypeScript SDK

The SDK starts `kodelet acp` as a client of the Kodelet daemon. The daemon owns the model loop and conversation history; the selected embedded or standalone runner owns workspace execution and tool policy. Configure credentials and model profiles on the daemon, not in temporary SDK launch files.

## Extension model profiles

`ext.registerProfile({ name, provider, model, ...configuration })` declares a self-contained profile using ordinary snake_case configuration keys and built-in defaults. See the [registration example](../skills/kodelet/references/sdk.md#registered-model-profiles). Session `ExecutionOptions` remains a separate camelCase API.

## Inline extensions

Pass extension entrypoints to `Client.createSession({ extensions })` to expose callbacks from your TypeScript process without installing extension executables on the runner:

```ts
import { Client, defineExtension, z } from "kodelet";

let calls = 0;
const echo = defineExtension((ext) => {
  ext.registerTool({
    name: "sdk_echo",
    description: "Echo text using an SDK callback",
    inputSchema: z.object({ text: z.string() }),
    async execute(input, ctx) {
      ctx.signal.throwIfAborted();
      await ctx.update("Echoing text");
      return `${++calls}: ${input.text}`;
    },
  });
});

const client = new Client(); // Or select an explicit server and runner.
try {
  const session = await client.createSession({ extensions: [echo] });
  const response = await session.runAndWait({ message: "Use sdk_echo to say hello" });
  console.log(response.content);
  await session.close();
} finally {
  await client.close();
}
```

Callbacks remain in the SDK process and may capture application state. Each runner run and extension gets a separate extension host, initialized using the original entrypoint. State declared outside the entrypoint, such as `calls` above, survives across runs while your process is alive. Inline tools remain subject to daemon and runner restrictions, including `allowedTools`, `noTools`, and extension policy; an attachment cannot grant extra permissions or leak its tools into other sessions.

The bridge relays the existing extension protocol through an authenticated session-scoped ACP connection. It preserves tool updates, reverse context RPC such as `ctx.forkConversation()`, notifications, and request cancellation. Methods implemented locally by the SDK, including filesystem and process helpers, still run in the SDK process; a runner-provided `ctx.cwd` does not make its filesystem locally accessible. Install an executable extension on the runner when the callback itself needs runner-local resources.

### Runnable example

`sdk/examples/inline-extension-session.ts` demonstrates an inline `sdk_echo` tool, captured application state, progress updates, a local notification handler, and Ctrl+C cancellation with session cleanup. From the repository root:

```bash
npm --prefix sdk ci
npm --prefix sdk run build
npm --prefix sdk run example:inline
# Or supply your own prompt:
npm --prefix sdk run example:inline -- "Use sdk_echo to say hello"
```

Use a compatible `kodelet` executable on `PATH`, or set `KODELET_BIN` to its path. The example uses the normal local daemon by default; set `KODELET_SERVER` and `KODELET_RUNNER` to target a remote daemon and registered runner, using the CLI's usual authentication configuration. `KODELET_CWD` selects the runner workspace directory and `KODELET_PROFILE` selects a daemon model profile. Model credentials stay on the daemon. The effective extension/tool policy must permit `sdk_echo`.

### UI handlers

Supply `ui` alongside inline extensions to handle `ctx.ui.input`, `confirm`, `select`, and `notify` locally:

```ts
const session = await client.createSession({
  extensions: [echo],
  ui: {
    confirm: async (request, signal) => {
      signal?.throwIfAborted();
      return request.message === "Allow callback?";
    },
    notify: (request) => { console.log(request.message); },
  },
});
```

Handlers receive an abort signal and should stop pending interactions when it is aborted. An omitted handler in a supplied `ui` object reports that interaction as unavailable. Other host RPC methods continue to the runner, and local UI handlers do not enable unrelated capabilities such as persistent widgets or background tasks.

### Lifecycle and compatibility

- Both sides negotiate `_meta.sessionExtensions.version: 1`. If the ACP executable lacks support, session creation fails with a compatibility error and closes the child process. The daemon and selected runner must also support session extensions.
- Entry points are assigned deterministic attachment IDs (`inline-1`, `inline-2`, and so on). The runner may use a different namespaced identity inside the raw extension protocol.
- A frame is acknowledged as accepted before callbacks execute. Nested host requests therefore do not block the relay from delivering their responses.
- Closing a runtime or session aborts active callbacks and rejects pending host requests. Cleanup is bounded, and callbacks that ignore cancellation cannot return late results or issue new scoped host requests. Arbitrary JavaScript cannot be forcibly stopped; callbacks should honor `ctx.signal` and release their own resources.
- Disconnected callbacks are never automatically restarted or replayed. To resume a conversation, explicitly provide `extensions` again in `createSession({ resume: conversationId, extensions: [echo] })`, retaining the same extension order. Callbacks and captured application state are not serialized into history.
- The deprecated `extensionTransport: "unix" | "tcp"` option is accepted as a compatibility no-op. Neither choice creates a socket, temporary executable, or configuration file.

## Development

From the repository root, run `mise run sdk-test` for TypeScript checking, build, SDK tests, and package dry-run. The real SDK/ACP/daemon/runner acceptance gate is `KODELET_TEST_EXTENSION_SDK=typescript mise exec -- go test ./cmd/kodelet -run '^TestSessionExtensionsAcrossProcessBoundary$' -count=1 -timeout=3m`.
