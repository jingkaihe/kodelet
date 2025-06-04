# ADR 014: OpenAI Compatible API Support

## Status
Proposed

## Context
Kodelet currently supports two LLM providers: Anthropic's Claude models and OpenAI's GPT models. The OpenAI provider is implemented using OpenAI's official API endpoints and pricing structure. However, there's growing demand to support other LLM providers that offer OpenAI-compatible APIs, such as:

- XAI's Grok models (https://docs.x.ai/docs/overview)
- Local LLM deployments using OpenAI-compatible servers (ollama, vLLM, etc.)
- Other cloud providers offering OpenAI-compatible endpoints (Azure OpenAI, AWS Bedrock, etc.)

Currently, the OpenAI provider has hardcoded:
1. API endpoint (`https://api.openai.com/v1`)
2. Model list and pricing information in `ModelPricingMap`
3. Model-specific logic for reasoning models vs. standard models

This limits the flexibility to use other OpenAI-compatible providers that may have different:
- API endpoints
- Model names and capabilities
- Pricing structures
- Feature support

## Decision
We will enhance the existing `pkg/llm/openai` package to support configurable API endpoints and model configurations without creating separate provider packages. This approach maintains architectural simplicity while providing the flexibility needed for OpenAI-compatible APIs.

### Key Changes

#### 1. Configurable API Endpoint
Introduce support for custom API base URLs through environment variables and configuration:

```bash
# Environment variable (takes precedence)
export OPENAI_API_BASE="https://api.x.ai/v1"

# Configuration file
api_base: "https://api.x.ai/v1"
```

#### 2. Configurable Model Definitions
Allow models and their properties to be defined in configuration rather than hardcoded:

```yaml
# kodelet-config.yaml
provider: openai
api_base: "https://api.x.ai/v1"
models:
  grok-2-latest:
    pricing:
      input: 0.000002
      cached_input: 0.000001
      output: 0.000008
    context_window: 131072
    type: "standard"  # or "reasoning"
  grok-2-mini:
    pricing:
      input: 0.0000002
      cached_input: 0.0000001
      output: 0.0000008  
    context_window: 131072
    type: "standard"
model: "grok-2-latest"
weak_model: "grok-2-mini"
```

#### 3. Backward Compatibility
All existing OpenAI configurations will continue to work unchanged. The hardcoded `ModelPricingMap` will serve as fallback defaults when no custom model configuration is provided.

## Architecture Details

### Modified Components

#### Config Type Extensions
Extend the existing `llmtypes.Config` struct:

```go
type Config struct {
    // ... existing fields ...
    APIBase    string               // Custom API base URL
    Models     map[string]ModelDef  // Custom model definitions
}

type ModelDef struct {
    Pricing       ModelPricing
    ContextWindow int
    Type          string // "standard" or "reasoning"
}

type ModelPricing struct {
    Input         float64
    CachedInput   float64
    Output        float64
}
```

#### OpenAI Client Configuration
Modify `NewOpenAIThread` to support custom API base:

```go
func NewOpenAIThread(config llmtypes.Config) *OpenAIThread {
    clientConfig := openai.DefaultConfig(os.Getenv("OPENAI_API_KEY"))
    
    // Configure custom API base if provided
    if config.APIBase != "" {
        clientConfig.BaseURL = config.APIBase
    } else if apiBase := os.Getenv("OPENAI_API_BASE"); apiBase != "" {
        clientConfig.BaseURL = apiBase
    }
    
    client := openai.NewClientWithConfig(clientConfig)
    
    // ... rest of implementation
}
```

#### Dynamic Model Resolution
Enhance `getModelPricing` to check custom models first:

```go
func (t *OpenAIThread) getModelPricing(model string) ModelPricing {
    // Check custom models first
    if t.config.Models != nil {
        if modelDef, ok := t.config.Models[model]; ok {
            return modelDef.Pricing
        }
    }
    
    // Fall back to hardcoded pricing
    return getHardcodedModelPricing(model)
}

func (t *OpenAIThread) isReasoningModel(model string) bool {
    // Check custom models first
    if t.config.Models != nil {
        if modelDef, ok := t.config.Models[model]; ok {
            return modelDef.Type == "reasoning"
        }
    }
    
    // Fall back to hardcoded list
    return IsReasoningModel(model)
}
```

### Configuration Loading

#### Environment Variables
- `OPENAI_API_BASE`: Override the default OpenAI API endpoint
- `OPENAI_API_KEY`: API key (unchanged)

#### Configuration File Structure
```yaml
provider: openai
api_base: "https://api.x.ai/v1"  # Optional custom endpoint
model: "grok-2-latest"
weak_model: "grok-2-mini"
models:  # Optional custom model definitions
  grok-2-latest:
    pricing:
      input: 0.000002
      cached_input: 0.000001
      output: 0.000008
    context_window: 131072
    type: "standard"
  grok-2-mini:
    pricing:
      input: 0.0000002
      cached_input: 0.0000001
      output: 0.0000008
    context_window: 131072
    type: "standard"
```

### Example Use Cases

#### XAI Integration
```bash
export OPENAI_API_KEY="xai-api-key"
export OPENAI_API_BASE="https://api.x.ai/v1"
export KODELET_MODEL="grok-2-latest"
```

#### Local LLM with Ollama
```bash
export OPENAI_API_KEY="dummy-key"
export OPENAI_API_BASE="http://localhost:11434/v1"
export KODELET_MODEL="llama3.1:8b"
```

## Implementation Constraints

### Security Considerations
1. **HTTPS Enforcement**: For remote endpoints, enforce HTTPS in production
2. **API Key Validation**: Validate that API keys are provided for remote endpoints
3. **Local Development**: Allow HTTP for localhost/127.0.0.1 endpoints

### Compatibility Requirements
1. **Zero Breaking Changes**: All existing configurations must continue working
2. **Provider Interface**: Maintain the existing `Thread` interface contract
3. **Feature Parity**: Support the same capabilities regardless of endpoint

### Error Handling
1. **Graceful Degradation**: If custom models fail, fall back to defaults when possible
2. **Clear Error Messages**: Provide actionable feedback for configuration issues
3. **Validation**: Validate configuration at startup and provide clear error messages

## Benefits

### Positive Consequences
- **Provider Flexibility**: Users can easily switch between different OpenAI-compatible providers
- **Cost Optimization**: Access to providers with different pricing structures
- **Local Development**: Support for local LLM deployments for development/testing
- **Future-Proof**: Easy to add new OpenAI-compatible providers without code changes
- **Architectural Simplicity**: No need for separate provider packages

### Negative Consequences
- **Configuration Complexity**: More configuration options to understand and maintain
- **Testing Burden**: Need to test against multiple provider endpoints
- **Documentation**: More documentation needed for different provider setups
- **Support Complexity**: Users may encounter issues with third-party provider compatibility

## Migration Strategy

### Phase 1: Core Infrastructure
1. Extend configuration types to support custom API base and models
2. Modify OpenAI client initialization to use configurable endpoints
3. Update model resolution logic to check custom definitions first

### Phase 2: Configuration Integration  
1. Update configuration loading to parse new fields
2. Implement environment variable support for `OPENAI_API_BASE`
3. Add validation for configuration combinations

### Phase 3: Documentation and Examples
1. Create documentation for popular OpenAI-compatible providers
2. Add configuration examples for XAI, local deployments, etc.
3. Update CLI help and error messages

### Phase 4: Testing and Validation
1. Add tests for different endpoint configurations
2. Validate against real third-party endpoints
3. Performance testing with different providers

## Alternatives Considered

### 1. Separate Provider Packages
Create individual packages like `pkg/llm/xai`, `pkg/llm/local`, etc.

**Rejected because:**
- Violates the requirement to keep compatibility logic in `pkg/llm/openai`
- Would lead to code duplication
- Creates maintenance burden for similar implementations

### 2. Provider Plugin System
Create a plugin architecture for different providers.

**Rejected because:**
- Over-engineered for the current need
- Adds unnecessary complexity
- Most providers are OpenAI-compatible, so simple configuration is sufficient

### 3. Runtime Provider Discovery
Automatically detect provider capabilities at runtime.

**Rejected because:**
- Adds complexity and potential failure points
- Makes pricing and model information unpredictable
- Explicit configuration is more reliable

## Implementation Notes

### Model Type Detection
For providers that don't clearly separate reasoning vs. standard models, default to "standard" type and let users override in configuration.

### Pricing Fallbacks
When no pricing information is available for custom models:
1. Log a warning about unknown pricing
2. Use a configurable default pricing or GPT-4.1 equivalent
3. Continue operation with usage tracking as "unknown cost"

### API Compatibility Validation
Add optional validation checks to verify that the custom endpoint supports expected OpenAI features:
- Tool calling
- Multi-turn conversations  
- Image input (if configured)

This can be implemented as a "dry run" configuration check command.

## Success Metrics

- Successful integration with XAI API using configuration only
- No regression in existing OpenAI functionality
- Clear documentation and examples for common providers
- Community adoption of alternative providers

## Future Considerations

This design enables future enhancements like:
- Provider-specific feature flags
- Automatic model discovery from provider endpoints
- Provider-specific optimization strategies
- Enhanced monitoring and analytics per provider