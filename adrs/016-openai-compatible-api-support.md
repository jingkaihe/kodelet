# ADR 016: Support for OpenAI-Compatible APIs

## Status
Proposed

## Context
Kodelet currently supports OpenAI's API with a hardcoded endpoint and a fixed set of models and pricing. Many LLM providers now offer OpenAI-compatible APIs (e.g., xAI, Groq, Together AI, Perplexity), which could be supported with minimal changes. Users would benefit from being able to use these alternative providers while maintaining the same interface and functionality.

The current limitations are:
1. The OpenAI client is initialized without the ability to specify a custom base URL
2. Models and pricing information are hardcoded in the source code
3. No way to dynamically configure provider-specific models and pricing

## Decision
We will extend the existing OpenAI implementation to support any OpenAI-compatible API by:

1. Making the API endpoint configurable via the `OPENAI_API_BASE` environment variable
2. Moving model and pricing configuration to `kodelet-config.yaml`
3. Implementing a fallback mechanism that uses hardcoded defaults when custom configuration is not provided
4. Keeping all compatibility logic within the existing `pkg/llm/openai` package

## Architecture Details

### Configuration Structure

#### Environment Variables
```bash
# Existing
OPENAI_API_KEY=sk-...

# New
OPENAI_API_BASE=https://api.x.ai/v1  # Optional, defaults to OpenAI's endpoint
```

#### kodelet-config.yaml Structure
```yaml
# Provider configuration
provider: openai
model: grok-3

# OpenAI-compatible provider configuration
openai:
  # Option 1: Use a built-in preset (recommended for popular providers)
  preset: "xai"  # Built-in preset with all xAI models and current pricing

  # Option 2: Custom configuration (overrides preset if both are specified)
  base_url: https://api.x.ai/v1
  models:
    # Only these Grok models support reasoning capabilities (per xAI docs)
    reasoning:
      - grok-4-0709
      - grok-3-mini
      - grok-3-mini-fast
    # non_reasoning is optional - if not specified, it will be auto-populated
    # with all models from pricing that are not in the reasoning list
  pricing:
    grok-4-0709:
      input: 0.000003         # $3 per million tokens
      output: 0.000015        # $15 per million tokens
      context_window: 256000  # 256k tokens
    # ... other models
```

### Implementation Changes

#### 1. Client Initialization Enhancement
```go
// pkg/llm/openai/openai.go

func NewOpenAIThread(config llmtypes.Config, store conversations.Store) (*OpenAIThread, error) {
    // Get API key
    apiKey := os.Getenv("OPENAI_API_KEY")
    if apiKey == "" {
        return nil, errors.New("OPENAI_API_KEY environment variable is required")
    }

    // Initialize client configuration
    clientConfig := openai.DefaultConfig(apiKey)

    // Check for custom base URL (environment variable takes precedence)
    if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
        clientConfig.BaseURL = baseURL
    } else if config.OpenAI != nil {
        // Check preset first, then custom base URL
        if config.OpenAI.Preset != "" {
            if presetBaseURL := getPresetBaseURL(config.OpenAI.Preset); presetBaseURL != "" {
                clientConfig.BaseURL = presetBaseURL
            }
        }
        if config.OpenAI.BaseURL != "" {
            clientConfig.BaseURL = config.OpenAI.BaseURL  // Override preset
        }
    }

    client := openai.NewClientWithConfig(clientConfig)

    // Load custom models and pricing if available
    models, pricing := loadCustomConfiguration(config)

    return &OpenAIThread{
        client:         client,
        config:         config,
        customModels:   models,
        customPricing:  pricing,
        // ... other fields
    }, nil
}
```

#### 2. Dynamic Model and Pricing Loading
```go
// pkg/llm/openai/config.go (new file)

type CustomModels struct {
    Reasoning    []string
    NonReasoning []string
}

type CustomPricing map[string]ModelPricing

func loadCustomConfiguration(config llmtypes.Config) (*CustomModels, CustomPricing) {
    if config.OpenAI == nil {
        return nil, nil
    }

    var models *CustomModels
    var pricing CustomPricing

    // Load preset if specified
    if config.OpenAI.Preset != "" {
        presetModels, presetPricing := loadPreset(config.OpenAI.Preset)
        models = presetModels
        pricing = presetPricing
    }

    // Override with custom configuration if provided
    if config.OpenAI.Models != nil {
        if models == nil {
            models = &CustomModels{}
        }
        models.Reasoning = config.OpenAI.Models.Reasoning
        models.NonReasoning = config.OpenAI.Models.NonReasoning
    }

    if config.OpenAI.Pricing != nil {
        if pricing == nil {
            pricing = make(CustomPricing)
        }
        for model, p := range config.OpenAI.Pricing {
            pricing[model] = ModelPricing{
                Input:         p.Input,
                CachedInput:   p.CachedInput,
                Output:        p.Output,
                ContextWindow: p.ContextWindow,
            }
        }
    }

    // Auto-populate NonReasoning if not explicitly set
    if models != nil && len(models.NonReasoning) == 0 && len(models.Reasoning) > 0 && pricing != nil {
        reasoningSet := make(map[string]bool)
        for _, model := range models.Reasoning {
            reasoningSet[model] = true
        }

        for model := range pricing {
            if !reasoningSet[model] {
                models.NonReasoning = append(models.NonReasoning, model)
            }
        }
    }

    return models, pricing
}

func loadPreset(presetName string) (*CustomModels, CustomPricing) {
    switch presetName {
    case "xai":
        return loadXAIGrokPreset()
    default:
        return nil, nil
    }
}

func loadXAIGrokPreset() (*CustomModels, CustomPricing) {
    models := &CustomModels{
        Reasoning: []string{
            "grok-4-0709",
            "grok-3-mini",
            "grok-3-mini-fast",
        },
        NonReasoning: []string{
            "grok-3",
            "grok-3-fast",
            "grok-2-vision-1212",
        },
    }

    pricing := CustomPricing{
        "grok-4-0709": ModelPricing{
            Input:         0.000003,  // $3 per million tokens
            Output:        0.000015,  // $15 per million tokens
            ContextWindow: 256000,    // 256k tokens
        },
        "grok-3": ModelPricing{
            Input:         0.000003,  // $3 per million tokens
            Output:        0.000015,  // $15 per million tokens
            ContextWindow: 131072,    // 131k tokens
        },
        "grok-3-mini": ModelPricing{
            Input:         0.0000003, // $0.30 per million tokens
            Output:        0.0000009, // $0.90 per million tokens
            ContextWindow: 131072,    // 131k tokens
        },
        "grok-3-fast": ModelPricing{
            Input:         0.000005,  // $5 per million tokens
            Output:        0.000025,  // $25 per million tokens
            ContextWindow: 131072,    // 131k tokens
        },
        "grok-3-mini-fast": ModelPricing{
            Input:         0.0000006, // $0.60 per million tokens
            Output:        0.000004,  // $4 per million tokens
            ContextWindow: 131072,    // 131k tokens
        },
        "grok-2-vision-1212": ModelPricing{
            Input:         0.000002,  // $2 per million tokens
            Output:        0.00001,   // $10 per million tokens
            ContextWindow: 32768,     // 32k tokens (vision model)
        },
    }

    return models, pricing
}

func getPresetBaseURL(presetName string) string {
    switch presetName {
    case "xai":
        return "https://api.x.ai/v1"
    default:
        return ""
    }
}
```

#### 3. Model Resolution Logic
```go
// pkg/llm/openai/openai.go

func (o *OpenAIThread) getAvailableModels(reasoning bool) []string {
    // Use custom models if configured
    if o.customModels != nil {
        if reasoning {
            return o.customModels.Reasoning
        }
        return o.customModels.NonReasoning
    }

    // Fall back to hardcoded defaults
    if reasoning {
        return ReasoningModels
    }
    return NonReasoningModels
}

func (o *OpenAIThread) getPricing(model string) (ModelPricing, bool) {
    // Check custom pricing first
    if o.customPricing != nil {
        if pricing, ok := o.customPricing[model]; ok {
            return pricing, true
        }
    }

    // Fall back to hardcoded pricing
    pricing, ok := ModelPricingMap[model]
    return pricing, ok
}
```

#### 4. Configuration Type Updates
```go
// pkg/llm/types/config.go

type Config struct {
    Provider        string
    Model           string
    MaxTokens       int
    ReasoningEffort string
    WeakModel       string
    // ... existing fields

    // Provider-specific configurations
    OpenAI *OpenAIConfig `mapstructure:"openai"`
}

type OpenAIConfig struct {
    Preset  string                    `mapstructure:"preset"`
    BaseURL string                    `mapstructure:"base_url"`
    Models  *OpenAIModelsConfig       `mapstructure:"models"`
    Pricing map[string]PricingConfig  `mapstructure:"pricing"`
}

type OpenAIModelsConfig struct {
    Reasoning    []string `mapstructure:"reasoning"`
    NonReasoning []string `mapstructure:"non_reasoning"`
}

type PricingConfig struct {
    Input         float64 `mapstructure:"input"`
    CachedInput   float64 `mapstructure:"cached_input"`
    Output        float64 `mapstructure:"output"`
    ContextWindow int     `mapstructure:"context_window"`
}
```

### Provider Presets

To simplify configuration for popular providers, Kodelet ships with built-in presets that include pre-configured models, pricing, and base URLs. This eliminates the need to manually configure every model and price.

#### Available Presets

1. **xai**: Complete configuration for xAI's Grok models
   - Base URL: `https://api.x.ai/v1`
   - All current Grok models with up-to-date pricing
   - Correct reasoning/non-reasoning categorization per xAI docs

#### Preset Behavior

1. **Preset Loading**: When a preset is specified, it loads the complete configuration for that provider
2. **Override Support**: Custom configuration fields override preset values when both are specified
3. **Base URL Handling**: Presets include base URLs, but `OPENAI_API_BASE` environment variable still takes precedence
4. **Pricing Updates**: Presets can be updated in new Kodelet releases to reflect current pricing

#### Usage Examples

**Simple preset usage:**
```yaml
provider: openai
model: grok-3
openai:
  preset: "xai"
```

**Preset with custom overrides:**
```yaml
provider: openai
model: grok-3
openai:
  preset: "xai"
  # Override specific pricing for local testing
  pricing:
    grok-3-mini:
      input: 0.0
      output: 0.0
```

### Model Auto-Population

To reduce configuration duplication and prevent inconsistencies, the system supports auto-populating the `non_reasoning` model list:

1. **Explicit Configuration**: If both `reasoning` and `non_reasoning` are explicitly configured, use them as-is
2. **Auto-Population**: If `reasoning` is configured but `non_reasoning` is empty/omitted, automatically populate `non_reasoning` with all models from the pricing section that are not in the reasoning list
3. **Validation**: This ensures all models with pricing information are categorized and available for use

This approach provides:
- **Reduced duplication**: Only need to maintain the reasoning model list
- **Consistency**: Impossible to have a model in pricing but missing from both lists
- **Flexibility**: Can still explicitly override non_reasoning if needed

### Migration Strategy

1. **Backward Compatibility**: The implementation will maintain full backward compatibility. Users who don't configure custom providers will continue to use the hardcoded OpenAI defaults.

2. **Configuration Precedence**:
   - Environment variables (highest priority)
   - Repository-specific config (`kodelet-config.yaml`)
   - Global config (`~/.kodelet/config.yaml`)
   - Hardcoded defaults (lowest priority)

3. **Validation**: Add validation to ensure required fields are present when custom configuration is provided.

## Example Configurations

### xAI (Grok) - Using Preset (Recommended)
```yaml
provider: openai
model: grok-3

openai:
  preset: "xai"  # Automatically configures all Grok models and pricing
```

### xAI (Grok) - Custom Configuration
```yaml
provider: openai
model: grok-3

openai:
  base_url: https://api.x.ai/v1
  models:
    reasoning:
      - grok-4-0709
      - grok-3-mini
      - grok-3-mini-fast
    # non_reasoning will be auto-populated with: grok-3, grok-3-fast, grok-2-vision-1212
  pricing:
    grok-4-0709:
      input: 0.000003         # $3 per million tokens
      output: 0.000015        # $15 per million tokens
      context_window: 256000  # 256k context window
    grok-3:
      input: 0.000003         # $3 per million tokens
      output: 0.000015        # $15 per million tokens
      context_window: 131072
    grok-3-mini:
      input: 0.0000003        # $0.30 per million tokens
      output: 0.0000009       # $0.90 per million tokens
      context_window: 131072
    grok-3-mini-fast:
      input: 0.0000006        # $0.60 per million tokens
      output: 0.000004        # $4 per million tokens
      context_window: 131072
    grok-3-fast:
      input: 0.000005         # $5 per million tokens
      output: 0.000025        # $25 per million tokens
      context_window: 131072
    grok-2-vision-1212:
      input: 0.000002         # $2 per million tokens
      output: 0.00001         # $10 per million tokens
      context_window: 32768   # 32k tokens (vision model)
```

### Local LLM (e.g., Ollama with OpenAI-compatible endpoint)
```yaml
provider: openai
model: llama3.3:70b

openai:
  base_url: http://localhost:11434/v1
  models:
    non_reasoning:
      - llama3.3:70b
      - qwen2.5-coder:32b
  pricing:
    llama3.3:70b:
      input: 0.0  # Free for local models
      output: 0.0
      context_window: 131072
```

## Alternatives Considered

1. **Create separate packages for each provider**:
   - Rejected due to code duplication and maintenance overhead
   - Most OpenAI-compatible APIs are truly compatible and don't need custom logic

2. **Use a generic "compatible" provider**:
   - Rejected because it would require users to change their provider setting
   - Better to extend the existing OpenAI provider for seamless migration

3. **Auto-detect provider from base URL**:
   - Rejected as it could be fragile and surprising to users
   - Explicit configuration is clearer

## Consequences

### Positive
- Users can leverage any OpenAI-compatible API without code changes
- Cost optimization by choosing alternative providers
- Support for local/self-hosted models
- Flexibility to add new providers without modifying code
- Maintains backward compatibility

### Negative
- Configuration becomes more complex for advanced users
- Need to document provider-specific quirks and limitations
- No validation of model availability until runtime
- Users responsible for ensuring pricing accuracy

## Implementation Plan

1. Update configuration types in `pkg/llm/types/config.go` to include preset field
2. Modify `pkg/llm/config.go` to handle the new OpenAI configuration section
3. Update `pkg/llm/openai/openai.go` to:
   - Accept custom base URL during client initialization
   - Load and use custom models and pricing
   - Implement preset loading with override support
   - Implement fallback logic
4. Add built-in presets starting with xAI Grok configuration
5. Add validation for custom configuration and preset names
6. Update documentation with examples for popular providers
7. Add tests for configuration loading, preset loading, and fallback behavior
8. Test with at least one alternative provider (e.g., xAI)

## Security Considerations

1. **API Key Management**: Continue using environment variables for API keys
2. **URL Validation**: Validate base URLs to prevent injection attacks
3. **HTTPS Enforcement**: Warn users when using non-HTTPS endpoints (except for localhost)

## Future Enhancements

1. **Additional Provider Presets**: Add presets for other popular providers (Groq, Together AI, Perplexity)
2. **Model Discovery**: Add optional endpoint to query available models
3. **Feature Flags**: Allow providers to specify supported features (e.g., tool calling, vision, image generation)
4. **Response Validation**: Add optional schema validation for non-standard responses
5. **Multi-modal Support**: Extend configuration to support image generation models (e.g., xAI's grok-2-image-1212)
6. **Vision Model Integration**: Better support for vision-enabled models with image input capabilities
7. **Preset Auto-Update**: Mechanism to update presets from remote sources while maintaining local overrides
