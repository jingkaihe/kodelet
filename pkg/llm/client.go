package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/sirupsen/logrus"
)

// Config holds the configuration for the LLM client
type Config struct {
	Model     string
	MaxTokens int
}

// Client represents an LLM client for asking questions
type Client struct {
	anthropicClient anthropic.Client
	config          Config
}

// NewClient creates a new LLM client with the given configuration
func NewClient(config Config) *Client {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = anthropic.ModelClaude3_7SonnetLatest
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}

	return &Client{
		anthropicClient: anthropic.NewClient(),
		config:          config,
	}
}

// color adds color to terminal output
func color(s string) string {
	return fmt.Sprintf("\033[1;%sm%s\033[0m", "33", s)
}

// Ask sends a query to the LLM and returns the response
func (c *Client) Ask(ctx context.Context, state state.State, query string, silent bool, modelOverride ...string) string {
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(query)),
	}

	for {
		// Determine which model to use
		model := c.config.Model
		if len(modelOverride) > 0 && modelOverride[0] != "" {
			model = modelOverride[0]
		}

		message, err := c.anthropicClient.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: int64(c.config.MaxTokens),
			System: []anthropic.TextBlockParam{
				{
					Text: sysprompt.SystemPrompt(model),
					CacheControl: anthropic.CacheControlEphemeralParam{
						Type: "ephemeral",
					},
				},
			},
			Messages: messages,
			Model:    model,
			Tools:    tools.ToAnthropicTools(tools.Tools),
		})
		if err != nil {
			logrus.WithError(err).Error("error asking")
		}

		textOutput := ""
		for _, block := range message.Content {
			switch block := block.AsAny().(type) {
			case anthropic.TextBlock:
				textOutput += block.Text + "\n"
				if !silent {
					println(block.Text)
					println()
				}
			case anthropic.ToolUseBlock:
				inputJSON, _ := json.Marshal(block.Input)
				if !silent {
					println(block.Name + ": " + string(inputJSON))
					println()
				}
			}
		}

		messages = append(messages, message.ToParam())
		toolResults := []anthropic.ContentBlockParamUnion{}

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				print(color("[user (" + block.Name + ")]: "))

				output := tools.RunTool(ctx, state, block.Name, string(variant.JSON.Input.Raw()))
				println(output.String())

				toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, output.String(), false))
			}
		}
		if len(toolResults) == 0 {
			return textOutput
		}
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}
}
