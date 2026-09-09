# TypeScript Agent SDK examples

These examples use the `kodelet` TypeScript SDK to drive daemon-backed ACP sessions.

From `skills/kodelet/examples/sdk`, install dependencies and run them with npm:

```bash
npm install
npm run basic -- "what is the meaning of life?"
npm run streaming -- "explain this repository in one paragraph"
```

During development, build the local `sdk/` package, then run `npm install --no-save --package-lock=false --ignore-scripts ../../../../sdk` from this example directory to use that build without running the SDK's global MCP plugin installer.

Useful environment variables:

- `KODELET_BIN` — thin Kodelet client executable to launch; defaults to `kodelet` from `PATH`.
- `KODELET_SERVER` / `KODELET_AUTH_TOKEN` — daemon endpoint and client authentication. Provider credentials remain on the daemon.
- `KODELET_RUNNER` — registered runner to target; set this to the owning runner when using a standalone or remote runner.
- `KODELET_PROFILE` — optional daemon-owned model profile.

## Examples

- `basic-agent-session.ts` runs one prompt and prints the final response.
- `streaming-agent-session.ts` streams assistant deltas as they arrive.
