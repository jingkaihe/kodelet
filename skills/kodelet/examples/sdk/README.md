# TypeScript Agent SDK examples

These examples use the `kodelet` TypeScript SDK to drive daemon-backed sessions or define a runner-installed delegated search extension.

From `skills/kodelet/examples/sdk`, install dependencies and run them with npm:

```bash
npm install
npm run basic -- "what is the meaning of life?"
npm run streaming -- "explain this repository in one paragraph"
```

The example package requires the SDK with `registerProfile` and `ctx.children` support. During development, install the local `sdk/` package after building it.

Useful environment variables:

- `KODELET_BIN` — thin Kodelet client executable to launch; defaults to `kodelet` from `PATH`.
- `KODELET_SERVER` / `KODELET_AUTH_TOKEN` — daemon endpoint and client authentication. Provider credentials remain on the daemon.
- `KODELET_PROFILE` — optional daemon-owned model profile.

## Examples

- `basic-agent-session.ts` runs one prompt and prints the final response.
- `streaming-agent-session.ts` streams assistant deltas as they arrive.
- `delegated-code-search.ts` replaces the old inline executable example with a runner-owned preset and scoped child execution. Install a `kodelet-extension-*` wrapper on the runner that executes this file with `tsx` (or build it first). Do not pass the callback into `createSession`; the daemon cannot execute client-local callbacks. The parent must permit filesystem search; the child permits only `file_read`, `grep_tool`, and `glob_tool`, with extensions and skills disabled.
