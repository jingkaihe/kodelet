// Package extension builds Kodelet extensions in Go.
//
// An extension registers tools, slash commands, TUI shortcuts, model profiles,
// and lifecycle event handlers on an [Extension], then serves them either as an
// installed executable over stdio ([Extension.Main] or [Extension.Run]) or as an
// inline callback attached to an SDK session (see the agent package).
//
//	ext := extension.New(extension.Metadata{Name: "weather", Version: "0.1.0"})
//
//	type WeatherInput struct {
//		Location string `json:"location" jsonschema:"Location to fetch weather for"`
//	}
//
//	extension.RegisterTool(ext, extension.Tool[WeatherInput]{
//		Name:        "get_weather",
//		Description: "Get the current weather for a location",
//		Execute: func(ctx context.Context, in WeatherInput, tc *extension.ToolContext) (extension.ToolResult, error) {
//			return extension.Text("18°C, cloudy in " + in.Location), nil
//		},
//	})
//
//	func main() { ext.Main() }
//
// Tool and command input schemas are inferred from Go types with
// github.com/google/jsonschema-go: exported fields use their JSON names, fields
// without omitempty or omitzero are required, and a jsonschema struct tag
// supplies the description. Inputs are validated against the inferred schema
// before handlers run. Register everything before serving; registration
// functions panic on invalid or duplicate registrations, like http.Handle.
//
// Handlers receive a context.Context that is cancelled when Kodelet cancels the
// request or the runtime closes, and a call context with workspace metadata,
// storage, structured logging, UI, and host RPC helpers. Handlers run
// concurrently; protect shared state accordingly.
package extension
