# Go SDK

The Go SDK is the module `github.com/jingkaihe/kodelet/sdk/go` (Go 1.24+, Linux and macOS). It mirrors the TypeScript SDK in idiomatic Go:

- `agent` — daemon-backed sessions through `kodelet acp`, typed events, steering, and inline extensions.
- `extension` — tools, commands, shortcuts, model profiles, and lifecycle events served over stdio or an SDK session.
- `extension/extensiontest` — an in-memory host for unit tests.
- `taskprogress` — bounded `data.taskRun` progress snapshots.

```bash
go get github.com/jingkaihe/kodelet/sdk/go
```

## Installed extensions

Build the program as `kodelet-extension-<name>` in an extension root, for example `.kodelet/extensions/weather/kodelet-extension-weather`:

```go
type weatherInput struct {
	Location string `json:"location" jsonschema:"Location to fetch weather for"`
	Units    string `json:"units,omitempty"`
}

func main() {
	ext := extension.New(extension.Metadata{Name: "weather", Version: "0.1.0"})
	extension.RegisterTool(ext, extension.Tool[weatherInput]{
		Name:        "get_weather",
		Description: "Get the current weather for a location",
		Timeout:     30 * time.Second, // extension.NoTimeout disables the host timeout
		Execute: func(ctx context.Context, in weatherInput, tc *extension.ToolContext) (extension.ToolResult, error) {
			_ = tc.Update(ctx, "Contacting the weather service", nil)
			return extension.ToolResult{
				Content:           "18°C, cloudy",
				StructuredContent: map[string]any{"temperature": 18},
				Data:              map[string]any{"presentation": extension.Presentation{Summary: "Weather for " + in.Location}},
			}, nil
		},
	})
	ext.Main() // serves stdio; os.Stdout is redirected to stderr while serving
}
```

Schemas come from Go types: JSON field names, required unless `omitempty`/`omitzero`, `jsonschema` tags for descriptions, and no unknown properties. Input is validated before `Execute`. `Tool.InputSchema` advertises a raw schema without validation; `map[string]any`, `json.RawMessage`, and `any` accept any object. Registration functions panic on invalid or duplicate registrations. Handlers run concurrently.

Other registrations:

```go
extension.RegisterCommand(ext, extension.Command[map[string]any]{
	Name: "weather", Description: "Weather briefing",
	Execute: func(ctx context.Context, _ map[string]any, cc *extension.CommandContext) (extension.CommandResult, error) {
		return extension.RunAgent("Brief me on the weather in " + strings.Join(cc.Invocation.Args, " ")), nil
	},
})
ext.RegisterShortcut("ctrl+g", extension.Shortcut{Description: "Greet", Handler: func(ctx context.Context, sc *extension.ShortcutContext) (extension.ShortcutResult, error) {
	return extension.Submit("hello"), nil
}})
profile := ext.RegisterProfile(extension.ProfileRegistration{Name: "code-search", Provider: "openai", Model: "gpt-5", Hidden: true, Settings: map[string]any{"max_tokens": 512}})
extension.On(ext, extension.EventAgentInit, func(ctx context.Context, ev extension.AgentInitEvent, ec *extension.EventContext) (extension.EventResult, error) {
	return extension.EventResult{SystemPrompt: &extension.SystemPromptPatch{Append: "Be concise."}}, nil
}, extension.WithPriority(10))
```

Command results are `Respond`, `RunAgent`, or the zero value to pass; struct inputs that fail validation also pass. Event keys (`EventToolCall`, `EventToolResult`, `EventUserMessage`, `EventAgentFinished`, ...) decode typed payloads; `CustomEvent(name)` covers others. A zero `EventResult` means no change; `Block` stops the chain; `Input`/`Output` replace values for later handlers.

Call contexts carry `CWD`, `ConversationID`, `RunnerID`, and other metadata, plus `Log` (`*slog.Logger` writing JSON to stderr), `Storage`, `ResolveWorkspacePath`, `UI` (`Input`, `Confirm`, `Select`, `Notify`, `AppendTranscript`, `SetWidget`, `RemoveWidget`, `OpenSurface`), `AcquireBackgroundTask`, and on tools `Update`, `ForkConversation`, and `AcquireBrowser`. The handler `ctx` is cancelled with the request. Use the standard library for files and processes.

Test with the real dispatcher:

```go
h := extensiontest.New(ext, nil) // Options.Host answers host requests
defer h.Close()
result, err := h.ExecuteTool(ctx, "get_weather", map[string]any{"location": "London"})
```

## Agent sessions

```go
client := agent.NewClient(agent.ClientOptions{Server: server, Runner: runner}) // empty values use CLI config
defer client.Close()
session, err := client.CreateSession(ctx, agent.SessionOptions{
	CWD:     "/runner/workspace",
	Profile: "coding",
	Options: agent.ExecutionOptions{NoTools: agent.Ptr(true), MaxTurns: agent.Ptr(3)},
})
response, err := session.RunAndWait(ctx, agent.RunOptions{Message: "Summarize README.md"})
for event, err := range session.Stream(ctx, agent.RunOptions{Message: "Go on"}) { /* typed event.Data */ }
unsubscribe := session.Subscribe(func(event agent.Event) { /* every run */ })
result, err := session.Steer(ctx, "Focus on tests") // only during a run
```

`ExecutionOptions` pointer and slice fields send explicit `false`, `0`, and empty lists; unset values keep daemon defaults. `Resume` continues a conversation; `ParentConversationID` creates a child and cannot be combined with `Resume`. Cancelling `ctx` cancels the turn and returns the context error. Event data types include `AssistantMessageDelta`, `ToolCall`, `ToolResult`, `ContextCompacted`, and `Response` (for `agent.end`).

Inline extensions run callbacks in your process:

```go
session, err := client.CreateSession(ctx, agent.SessionOptions{
	Extensions: []*extension.Extension{ext},
	UI: &agent.UIHandlers{Confirm: func(ctx context.Context, r extension.ConfirmRequest) (bool, error) { return true, nil }},
})
```

They are identified as `inline-1`, `inline-2`, and so on, stay subject to tool and extension policy, are never replayed after a disconnect, and must be passed again in the same order when resuming. Nil UI handlers report the interaction as unavailable. Inline extensions cannot acquire the runner-local browser.

## Task progress

```go
progress := taskprogress.New(tc, taskprogress.Options{Kind: "code_search", Task: task, CWD: tc.CWD, RunningTitle: "Searching code", CompletedTitle: "Searched code", FailedTitle: "Code search failed", RespondingDetail: "writing summary"})
_ = progress.Start(ctx)
progress.Attach(session)
_, err := session.RunAndWait(ctx, agent.RunOptions{Message: task})
snapshot := progress.Finish(ctx, err == nil, "")
```

Not included in Go: Mustache `renderTemplate` and the MCP adapter (install the `kodelet@mcp` extension instead).
