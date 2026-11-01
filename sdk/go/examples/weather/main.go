// Command weather is an installable Kodelet extension. Build it as
// kodelet-extension-weather inside an extension root, for example:
//
//	go build -o ~/.kodelet/extensions/weather/kodelet-extension-weather ./examples/weather
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

type weatherInput struct {
	Location string `json:"location" jsonschema:"Location to fetch weather for"`
	Units    string `json:"units,omitempty" jsonschema:"celsius (default) or fahrenheit"`
}

type weatherReport struct {
	Location     string  `json:"location"`
	Temperature  float64 `json:"temperature"`
	Units        string  `json:"units"`
	Condition    string  `json:"condition"`
	ObservedTime string  `json:"observedTime"`
}

func main() {
	ext := extension.New(extension.Metadata{Name: "weather", Version: "0.1.0"})

	extension.RegisterTool(ext, extension.Tool[weatherInput]{
		Name:        "get_weather",
		Description: "Get the current weather for a location",
		Timeout:     30 * time.Second,
		Execute: func(ctx context.Context, in weatherInput, tc *extension.ToolContext) (extension.ToolResult, error) {
			tc.Log.Info("fetching weather", "location", in.Location)
			if err := tc.Update(ctx, "Contacting the weather service", nil); err != nil {
				return extension.ToolResult{}, err
			}
			report := weatherReport{Location: in.Location, Temperature: 18, Units: "celsius", Condition: "cloudy", ObservedTime: time.Now().UTC().Format(time.RFC3339)}
			if in.Units == "fahrenheit" {
				report.Temperature, report.Units = 64.4, "fahrenheit"
			}
			summary := fmt.Sprintf("%s: %.1f° %s, %s", report.Location, report.Temperature, report.Units, report.Condition)
			return extension.ToolResult{
				Content:           summary,
				StructuredContent: report,
				Data:              map[string]any{"presentation": extension.Presentation{Summary: "Weather for " + in.Location}},
			}, nil
		},
	})

	extension.RegisterCommand(ext, extension.Command[map[string]any]{
		Name:        "weather",
		Description: "Ask the agent for a weather briefing",
		Execute: func(_ context.Context, _ map[string]any, cc *extension.CommandContext) (extension.CommandResult, error) {
			location := strings.Join(cc.Invocation.Args, " ")
			if location == "" {
				return extension.Respond("Usage: /weather <location>"), nil
			}
			return extension.RunAgent("Use get_weather to brief me on the weather in " + location), nil
		},
	})

	extension.On(ext, extension.EventToolCall, func(_ context.Context, event extension.ToolCallEvent, _ *extension.EventContext) (extension.EventResult, error) {
		var input struct {
			Command string `json:"command"`
		}
		if event.Tool.Name == "bash" && event.Tool.DecodeInput(&input) == nil && strings.Contains(input.Command, "rm -rf /") {
			return extension.EventResult{Block: &extension.EventBlock{Reason: "Refusing dangerous shell command"}}, nil
		}
		return extension.EventResult{}, nil
	}, extension.WithTimeout(5*time.Second))

	extension.On(ext, extension.EventAgentInit, func(context.Context, extension.AgentInitEvent, *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{SystemPrompt: &extension.SystemPromptPatch{Append: extension.Ptr("Report temperatures with their units.")}}, nil
	})

	ext.Main()
}
