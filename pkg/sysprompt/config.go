package sysprompt

// PromptConfig defines configuration options for prompt generation
type PromptConfig struct {
	// Model identifies the LLM model being used
	Model string

	// EnabledFeatures specifies which features should be enabled
	EnabledFeatures []string
}

// NewDefaultConfig creates a default configuration
func NewDefaultConfig() *PromptConfig {
	return &PromptConfig{
		Model: "claude-sonnet-4-20250514",
		EnabledFeatures: []string{
			"subagent",
			"todoTools",
			"batchTool",
		},
	}
}

// WithModel sets the model in the configuration
func (c *PromptConfig) WithModel(model string) *PromptConfig {
	c.Model = model
	return c
}

// WithFeatures sets the enabled features in the configuration
func (c *PromptConfig) WithFeatures(features []string) *PromptConfig {
	c.EnabledFeatures = features
	return c
}

// IsFeatureEnabled checks if a feature is enabled
func (c *PromptConfig) IsFeatureEnabled(feature string) bool {
	for _, f := range c.EnabledFeatures {
		if f == feature {
			return true
		}
	}
	return false
}

// updateContextWithConfig updates a PromptContext with configuration settings
func updateContextWithConfig(ctx *PromptContext, config *PromptConfig) {
	// Update feature flags based on config
	ctx.Features["subagentEnabled"] = config.IsFeatureEnabled("subagent")
	ctx.Features["todoToolsEnabled"] = config.IsFeatureEnabled("todoTools")
	ctx.Features["batchToolEnabled"] = config.IsFeatureEnabled("batchTool")
}
