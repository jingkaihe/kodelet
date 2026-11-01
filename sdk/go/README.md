# Kodelet Go SDK

The Go SDK runs Kodelet agent sessions and builds Kodelet extensions in Go. It lives in its own module, so importing it does not pull in Kodelet's server dependencies:

```bash
go get github.com/jingkaihe/kodelet/sdk/go
```

It requires Go 1.24 or newer and supports Linux and macOS.

| Package | Purpose |
| --- | --- |
| `agent` | Start daemon-backed agent sessions through `kodelet acp`, stream events, steer runs, and attach inline extensions. |
| `extension` | Register tools, commands, shortcuts, model profiles, and lifecycle event handlers, then serve them over stdio or an SDK session. |
| `extension/extensiontest` | Drive an extension through its real JSON-RPC dispatcher with an in-memory host, for unit tests. |
| `taskprogress` | Publish bounded, coalesced progress snapshots for long-running tools, usually ones that drive a child session. |

## Writing an extension

An installed extension is an executable named `kodelet-extension-<name>` in an extension root such as `.kodelet/extensions/<name>/` or `~/.kodelet/extensions/<name>/`:

```go
package main

import (
	"context"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

type weatherInput struct {
	Location string `json:"location" jsonschema:"Location to fetch weather for"`
	Units    string `json:"units,omitempty"`
}

func main() {
	ext := extension.New(extension.Metadata{Name: "weather", Version: "0.1.0"})
	extension.RegisterTool(ext, extension.Tool[weatherInput]{
		Name:        "get_weather",
		Description: "Get the current weather for a location",
		Execute: func(ctx context.Context, in weatherInput, tc *extension.ToolContext) (extension.ToolResult, error) {
			if err := tc.Update(ctx, "Contacting the weather service", nil); err != nil {
				return extension.ToolResult{}, err
			}
			return extension.Text("18°C, cloudy in " + in.Location), nil
		},
	})
	ext.Main()
}
```

```bash
go build -o .kodelet/extensions/weather/kodelet-extension-weather .
kodelet extension inspect weather
```

`ext.Main()` serves the protocol over stdin and stdout and exits when Kodelet disconnects. While it runs, `os.Stdout` is redirected to stderr so stray prints cannot corrupt the protocol stream. SIGTERM triggers a graceful shutdown that runs `session.end` handlers with a one-second budget. Use `ext.Run(ctx)` or `ext.Serve(ctx, in, out)` to manage the process yourself.

Register everything before serving. Like `http.Handle`, the registration functions panic on invalid or duplicate registrations, including input types that cannot be described as JSON Schema. Handlers run concurrently, so protect shared state.

### Input schemas

Tool and command input schemas are inferred from the Go type with [github.com/google/jsonschema-go](https://github.com/google/jsonschema-go). Exported fields use their JSON names. Fields without `omitempty` or `omitzero` are required, and a `jsonschema` tag sets the description. Structs reject unknown properties. Nullable unions that are generated for slices and pointers are simplified to the plain type, so schemas read like hand-written ones. Input is validated against the schema before the handler runs.

Set `Tool.InputSchema` to advertise a raw JSON Schema unchanged. The input is then decoded into the handler's type without validation, just as raw schemas behave in the TypeScript SDK. Tools typed as `map[string]any`, `json.RawMessage`, or `any` accept any object. For commands, struct inputs that fail validation pass the prompt on to the next route; use `map[string]any` to receive the raw input without advertising a schema.

### Events, commands, shortcuts, and profiles

Event keys are typed, so each handler receives a decoded payload. A zero `EventResult` means no change:

```go
extension.On(ext, extension.EventToolCall, func(ctx context.Context, event extension.ToolCallEvent, ec *extension.EventContext) (extension.EventResult, error) {
	var input struct {
		Command string `json:"command"`
	}
	if event.Tool.Name == "bash" && event.Tool.DecodeInput(&input) == nil && strings.Contains(input.Command, "rm -rf /") {
		return extension.EventResult{Block: &extension.EventBlock{Reason: "Refusing dangerous shell command"}}, nil
	}
	return extension.EventResult{}, nil
}, extension.WithPriority(10), extension.WithTimeout(5*time.Second))
```

Handlers for one event run by descending priority and then registration order. A block stops the chain. `Input` and `Output` results replace what later handlers and the host see; for `tool.result` and `tool.update`, a replaced object output loses its machine `data` copy. `SystemPromptPatch` fields are pointers, so `Replace: extension.Ptr("")` clears the prompt while nil fields leave it alone. Use `extension.CustomEvent(name)` for events the SDK does not model, and `extension.NoTimeout` to disable a host timeout.

`RegisterCommand` returns `extension.Respond(text)`, `extension.RunAgent(prompt)`, or the zero result to pass. `ext.RegisterShortcut("ctrl+g", ...)` accepts `ctrl+letter`, `alt+letter-or-digit`, `ctrl+alt+letter`, and `f1` to `f12`; return `extension.Submit(message)` to submit a message. `ext.RegisterProfile` declares a self-contained model profile with native snake_case `Settings` and returns its name for `agent.SessionOptions.Profile`.

### Call context

Every handler receives a call context with the conversation and workspace metadata (`CWD`, `ConversationID`, `RunnerID`, and so on) plus these helpers:

| Helper | Behavior |
| --- | --- |
| `Log` | A `*slog.Logger` that writes JSON lines to stderr. Interactive hosts may show `warn` and `error` entries as notifications. |
| `Storage` | Reads and writes text or JSON under the extension data directory and rejects paths that escape it. |
| `ResolveWorkspacePath`, `RelativeToWorkspace` | Resolve paths against the workspace and reject escapes. |
| `UI.Input`, `Confirm`, `Select`, `Notify` | Ask the user. When no interactive UI exists, answers come back as unavailable rather than as errors. |
| `UI.AppendTranscript`, `SetWidget`, `RemoveWidget`, `OpenSurface` | Persistent UI that can outlive the call. Transcript and widget calls are no-ops when unsupported; opening a surface returns an error. |
| `ToolContext.Update` | Publish a transient progress snapshot; this is a no-op when the host lacks tool updates. |
| `ToolContext.ForkConversation` | Fork the live conversation, optionally as a child. Errors match `extension.ErrConversationForkUnavailable`. |
| `ToolContext.AcquireBrowser` | Raw CDP access to the conversation's shared browser for trusted installed extensions. |
| `AcquireBackgroundTask` | Keep the process alive after the call returns until the lease is closed. |

The `ctx` argument is cancelled when Kodelet cancels the request or the connection closes. Host requests issued after the call ends fail with `extension.ErrRequestInactive`. Use the standard library for files, processes, and environment variables; the TypeScript SDK's `fs`, `process`, and `env` helpers have no Go equivalent.

### Testing

`extensiontest` runs an extension through the real dispatcher, including schema validation and JSON encoding:

```go
h := extensiontest.New(ext, &extensiontest.Options{Host: func(ctx context.Context, call extensiontest.HostCall) (any, error) {
	return map[string]any{"status": "submitted", "confirmed": true}, nil
}})
defer h.Close()
result, err := h.ExecuteTool(ctx, "get_weather", map[string]any{"location": "London"})
```

Use `extensiontest.DefaultInitializeParams()` with `h.Initialize` to test capability-dependent behavior, and `h.HostCalls()` to assert host requests.

## Running agent sessions

`agent.Client` starts `kodelet acp` as a thin client of the Kodelet daemon. The daemon owns the model loop, credentials, and conversation history; the selected runner owns workspace execution and tool policy. Session working directories refer to the runner's machine.

```go
client := agent.NewClient(agent.ClientOptions{}) // Or set Server and Runner explicitly.
defer client.Close()

session, err := client.CreateSession(ctx, agent.SessionOptions{
	Profile: "coding",
	Options: agent.ExecutionOptions{MaxTurns: agent.Ptr(10), AllowedTools: []string{"file_read", "grep_tool"}},
})
if err != nil {
	return err
}
response, err := session.RunAndWait(ctx, agent.RunOptions{Message: "Summarize README.md"})
```

`ExecutionOptions` uses pointer and slice fields so that unset values keep daemon defaults while `false`, `0`, and empty lists are sent explicitly. Set `SessionOptions.Resume` to continue a conversation, or `ParentConversationID` to create a child conversation.

Stream a run with a range loop, or subscribe to every run of a session:

```go
for event, err := range session.Stream(ctx, agent.RunOptions{Message: "List the packages"}) {
	if err != nil {
		return err
	}
	if delta, ok := event.Data.(agent.AssistantMessageDelta); ok {
		fmt.Print(delta.DeltaContent)
	}
}
```

Event data is typed: `AssistantMessageDelta`, `ToolCall`, `ToolResult` (for both `tool.update` and `tool.result`), `ContextCompacted`, `Response` (for `agent.end`), and so on. Subscribers run in order on a dedicated goroutine, and `RunAndWait` returns only after they have seen the run's events. Cancelling `ctx` asks Kodelet to cancel the turn and returns the context error. `session.Steer(ctx, message)` sends guidance to the running turn.

### Inline extensions

Pass extensions to `CreateSession` to expose callbacks from your process without installing executables on the runner:

```go
session, err := client.CreateSession(ctx, agent.SessionOptions{
	Extensions: []*extension.Extension{echo},
	UI: &agent.UIHandlers{
		Confirm: func(ctx context.Context, request extension.ConfirmRequest) (bool, error) { return true, nil },
	},
})
```

- Callbacks run in your process and may capture application state. Each runner run gets a fresh runtime for the same `*extension.Extension`, so captured state persists across runs while the process is alive.
- Inline tools remain subject to daemon and runner restrictions, including `AllowedTools`, `NoTools`, and extension policy.
- `UIHandlers` answer `Input`, `Confirm`, `Select`, and `Notify` locally; a nil handler reports that interaction as unavailable. Other host requests go to the runner.
- Extensions are identified as `inline-1`, `inline-2`, and so on. Callbacks are never restarted or replayed after a disconnect; to resume, pass the same extensions in the same order with `Resume`.
- Inline extensions cannot acquire the runner-local browser.

The negotiation, relay, and failure semantics are the same as in the [TypeScript SDK](../typescript/README.md#inline-extensions).

### Task progress

`taskprogress` turns a child session's tool activity into bounded `data.taskRun` snapshots, which Kodelet renders as a live task card:

```go
progress := taskprogress.New(tc, taskprogress.Options{
	Kind: "code_search", Task: task, CWD: tc.CWD,
	RunningTitle: "Searching code", CompletedTitle: "Searched code", FailedTitle: "Code search failed",
	RespondingDetail: "writing summary",
})
_ = progress.Start(ctx)
progress.Attach(session)
response, err := session.RunAndWait(ctx, agent.RunOptions{Message: task})
snapshot := progress.Finish(ctx, err == nil, "")
```

Use `StartActivity` and `FinishActivity` directly to track work that does not involve an agent session.

## Differences from the TypeScript SDK

| TypeScript | Go |
| --- | --- |
| Zod schemas | Go types with `json` and `jsonschema` struct tags, or a raw `InputSchema` |
| `ctx.signal` | The handler's `context.Context` |
| `session.on(...)` | `session.Subscribe` or `session.Stream` |
| Optional results (`undefined`) | Zero values: `EventResult{}`, `CommandResult{}`, `ShortcutResult{}` |
| `ctx.fs`, `ctx.process`, `ctx.env` | The standard library |
| `renderTemplate` (Mustache) | Not included |
| MCP adapter | Not included; install the `kodelet@mcp` extension |

## Examples

Run these from `sdk/go`:

| Example | Command |
| --- | --- |
| Installed extension | `go build -o kodelet-extension-weather ./examples/weather` |
| Single run | `go run ./examples/basic-session "What does this repository do?"` |
| Streaming | `go run ./examples/streaming-session "List the Go packages"` |
| Inline extension with captured state, progress, local UI, and Ctrl+C cancellation | `go run ./examples/inline-extension-session "Use sdk_echo to say hello"` |

The session examples use the `kodelet` executable on `PATH`, or `KODELET_BIN`. The inline example also reads `KODELET_SERVER`, `KODELET_RUNNER`, `KODELET_CWD`, and `KODELET_PROFILE`. Model credentials stay on the daemon.

## Development

From the repository root, `mise run sdk-go-test` runs vet and race-enabled tests, and `mise run sdk-go-lint` runs golangci-lint with the repository configuration. The real SDK, ACP, daemon, and runner acceptance gates compile Go fixtures against this module; run them with `KODELET_TEST_EXTENSION_SDKS=go mise run sdk-acceptance`. CI runs all three, and also runs vet and tests with the minimum Go version declared in `go.mod`.

The module is versioned with subdirectory tags such as `sdk/go/v0.1.0`.
