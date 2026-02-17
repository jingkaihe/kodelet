// Package sysprompt provides system prompt generation and template rendering
// for LLM interactions. It manages prompt configuration, template rendering,
// context data collection (system info, file contexts), and feature toggles
// for different prompt types including main system prompts and subagent prompts.
package sysprompt

// PromptConfig defines configuration options for prompt generation
type PromptConfig struct {
	// Model identifies the LLM model being used
	Model string

	// EnabledFeatures specifies which features should be enabled
	EnabledFeatures []string
}

// NewDefaultConfig creates a new PromptConfig with default settings including
// the default model and enabled features (subagent and todoTools).
func NewDefaultConfig() *PromptConfig {
	return &PromptConfig{
		Model: "claude-sonnet-4-6",
		EnabledFeatures: []string{
			"subagent",
			"todoTools",
		},
	}
}

// WithModel sets the model for the prompt configuration and returns the config for chaining.
func (c *PromptConfig) WithModel(model string) *PromptConfig {
	c.Model = model
	return c
}

// WithFeatures sets the enabled features for the prompt configuration and returns the config for chaining.
func (c *PromptConfig) WithFeatures(features []string) *PromptConfig {
	c.EnabledFeatures = features
	return c
}

// IsFeatureEnabled checks whether a specific feature is enabled in the configuration.
func (c *PromptConfig) IsFeatureEnabled(feature string) bool {
	for _, f := range c.EnabledFeatures {
		if f == feature {
			return true
		}
	}
	return false
}

func updateContextWithConfig(ctx *PromptContext, config *PromptConfig) {
	ctx.Features["subagentEnabled"] = config.IsFeatureEnabled("subagent")
	ctx.Features["todoToolsEnabled"] = config.IsFeatureEnabled("todoTools")
	ctx.Features["isSubagent"] = config.IsFeatureEnabled("isSubagent")
}
