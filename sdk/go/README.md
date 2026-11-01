# Kodelet Go SDK

Run agent sessions and build extensions in Go. Requires Go 1.24+ on Linux or macOS; the module does not pull in Kodelet's server dependencies.

```bash
go get github.com/jingkaihe/kodelet/sdk/go
```

| Package | Purpose |
| --- | --- |
| [`agent`](agent/) | Daemon-backed sessions, streaming, steering, and inline extensions. |
| [`extension`](extension/) | Tools, commands, shortcuts, profiles, and lifecycle handlers. |
| [`extension/extensiontest`](extension/extensiontest/) | Test extensions with an in-memory host. |
| [`taskprogress`](taskprogress/) | Progress snapshots for long-running tools. |

## Writing an extension

Save as `main.go` in a Go module:

```go
package main

import (
	"context"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

type weatherInput struct {
	Location string `json:"location" jsonschema:"Location to fetch weather for"`
}

func main() {
	ext := extension.New(extension.Metadata{Name: "weather", Version: "0.1.0"})
	extension.RegisterTool(ext, extension.Tool[weatherInput]{
		Name:        "get_weather",
		Description: "Get the current weather for a location",
		Execute: func(ctx context.Context, in weatherInput, tc *extension.ToolContext) (extension.ToolResult, error) {
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

Install globally under `~/.kodelet/extensions/<name>/`, or see [extension discovery](../../docs/MANUAL.md#extension-discovery) for other locations. `ext.Main()` serves stdio and redirects stray stdout prints to stderr.

Schemas are inferred from Go types: `json` tags name fields, `omitempty`/`omitzero` make them optional, and `jsonschema` tags add descriptions. Inputs are validated before handlers run. Register everything before serving; invalid or duplicate registrations panic. Handlers run concurrently, so protect shared state.

## Running agent sessions

The `agent` package requires `kodelet` on `PATH` and a configured daemon and runner. Credentials stay on the daemon; session working directories refer to the runner's machine.

```go
client := agent.NewClient(agent.ClientOptions{})
defer client.Close()

session, err := client.CreateSession(ctx, agent.SessionOptions{})
if err != nil {
	return err
}
response, err := session.RunAndWait(ctx, agent.RunOptions{Message: "Summarize README.md"})
if err != nil {
	return err
}
fmt.Println(response.Content)
```

Use `SessionOptions.Profile` to select a model profile, `Resume` to continue a conversation, and `session.Stream` or `Subscribe` for typed events. Cancelling `ctx` cancels the turn.

Pass `Extensions: []*extension.Extension{ext}` for callbacks in your process, subject to runner policy. When resuming, supply the same extensions in the same order; callbacks are not replayed after disconnects.

## Examples

Run these from `sdk/go`:

| Example | Command |
| --- | --- |
| [Installed extension](examples/weather/main.go) | `go build -o kodelet-extension-weather ./examples/weather` |
| [Single run](examples/basic-session/main.go) | `go run ./examples/basic-session "What does this repository do?"` |
| [Streaming](examples/streaming-session/main.go) | `go run ./examples/streaming-session "List the Go packages"` |
| [Inline extension and UI](examples/inline-extension-session/main.go) | `go run ./examples/inline-extension-session "Use sdk_echo to say hello"` |

Set `KODELET_BIN` to override the executable in these examples. The inline example also accepts `KODELET_SERVER`, `KODELET_RUNNER`, `KODELET_CWD`, and `KODELET_PROFILE`.

See the [Go SDK reference](../../skills/kodelet/references/go-sdk.md) for events, UI, testing, and task progress, or run `go doc github.com/jingkaihe/kodelet/sdk/go/extension` for API details.

## Development

From the repository root:

```bash
mise run sdk-go-test
mise run sdk-go-lint
KODELET_TEST_EXTENSION_SDKS=go mise run sdk-acceptance
```

The module is versioned with subdirectory tags such as `sdk/go/v0.1.0`.
