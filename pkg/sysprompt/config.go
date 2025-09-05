package sysprompt

// PromptConfig defines configuration options for prompt generation
type PromptConfig struct {
	// Model identifies the LLM model being used
	Model string

	// EnabledFeatures specifies which features should be enabled
	EnabledFeatures []string
}

func NewDefaultConfig() *PromptConfig {
	return &PromptConfig{
		Model: "claude-sonnet-4-20250514",
		EnabledFeatures: []string{
			"subagent",
			"todoTools",
		},
	}
}

func (c *PromptConfig) WithModel(model string) *PromptConfig {
	c.Model = model
	return c
}

func (c *PromptConfig) WithFeatures(features []string) *PromptConfig {
	c.EnabledFeatures = features
	return c
}

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
}