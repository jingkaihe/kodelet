# TypeScript Agent SDK examples

These examples show how to use `Client` from the `kodelet` TypeScript SDK to
launch and drive Kodelet agent sessions from Node/TypeScript.

From `skills/kodelet/examples/sdk`, install dependencies and run them with npm:

```bash
npm install
npm run basic -- "what is the meaning of life?"
npm run streaming -- "explain this repository in one paragraph"
npm run inline-extension
```

The example package depends on the latest published `kodelet` package.

Useful environment variables:

- `KODELET_BIN` — Kodelet executable to launch. Defaults to `kodelet` from
  `PATH`.
- `KODELET_PROFILE` — optional named Kodelet profile to use for the session.

## Examples

- `basic-agent-session.ts` runs one prompt and prints the final response.
- `streaming-agent-session.ts` streams assistant deltas as they arrive.
- `inline-extension-session.ts` exposes an in-process TypeScript extension with
  an `sdk_echo` tool for the session.
