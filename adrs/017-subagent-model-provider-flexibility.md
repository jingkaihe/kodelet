# ADR 017: Subagent Model and Provider Flexibility

## Status
Proposed

## Context
Currently, subagents in Kodelet are constrained to use the same model and provider as their parent (main) agent. This limitation prevents optimal model selection for different tasks. For example:
- A main agent using Claude Sonnet 4 cannot spawn a subagent using OpenAI's o3 reasoning model
- A main agent cannot delegate lightweight tasks to a faster model from the same provider
- Cost optimization through model mix-and-match is not possible

The current implementation directly copies the parent's configuration and reuses the parent's client instance, making it impossible to use different models or providers.

## Decision
We will extend the subagent architecture to support:
1. Different models from the same provider (e.g., main: claude-sonnet-4, subagent: claude-haiku)
2. Different providers entirely (e.g., main: anthropic/claude-sonnet-4, subagent: openai/o3)

This will be achieved through:
- Adding a single hardcoded subagent configuration in the configuration system
- No profile selection needed - subagents will always use the optimal model for complex analysis
- Modifying the `NewSubAgent` methods to use the configured subagent settings
- Creating separate client instances when using different providers
- Maintaining backward compatibility when no subagent configuration is specified

## Architecture Details

### Single Subagent Configuration

Instead of multiple profiles, users define one optimized subagent configuration for complex analysis tasks:

```yaml
# config.yaml
subagent:
  provider: "openai"
  model: "o3"
  reasoning_effort: "high"
  max_tokens: 32000
  # Optional: If not specified, uses same provider/model as main agent
```

### SubAgentInput Structure (No Changes)
```go
// pkg/tools/subagent.go
type SubAgentInput struct {
    Question string `json:"question"`
    // No profile field needed - configuration is automatic
}
```

### Configuration Types
```go
// pkg/types/llm/config.go
type SubAgentConfig struct {
    Provider         string `yaml:"provider"`
    Model            string `yaml:"model"`
    MaxTokens        int    `yaml:"max_tokens"`
    ReasoningEffort  string `yaml:"reasoning_effort"`  // OpenAI specific
    ThinkingBudget   int    `yaml:"thinking_budget"`   // Anthropic specific
    
    // OpenAI-compatible provider configuration
    OpenAI *OpenAIConfig `yaml:"openai,omitempty"`
}

type Config struct {
    // ... existing fields ...
    SubAgent *SubAgentConfig `yaml:"subagent,omitempty"`
}
```

### Modified Thread Interface
```go
// pkg/types/llm/thread.go
type Thread interface {
    // ... existing methods ...
    
    // Enhanced NewSubAgent - uses configured subagent settings automatically
    NewSubAgent(ctx context.Context) Thread
}
```

### Implementation in Providers

#### Anthropic Implementation
```go
// pkg/llm/anthropic/anthropic.go
func (t *AnthropicThread) NewSubAgent(ctx context.Context) llmtypes.Thread {
    config := t.config  // Start with parent's config
    config.IsSubAgent = true
    
    // Apply subagent configuration if specified
    if t.config.SubAgent != nil {
        subConfig := t.config.SubAgent
        
        // Check if we need a different provider
        if subConfig.Provider != "" && subConfig.Provider != "anthropic" {
            // Create a new thread with different provider
            newConfig := config
            newConfig.Provider = subConfig.Provider
            newConfig.Model = subConfig.Model
            newConfig.MaxTokens = subConfig.MaxTokens
            newConfig.ReasoningEffort = subConfig.ReasoningEffort
            newConfig.ThinkingBudgetTokens = subConfig.ThinkingBudget
            
            newThread, err := llm.NewThread(newConfig)
            if err != nil {
                logger.G(ctx).WithError(err).Error("Failed to create subagent with different provider, falling back to parent config")
                // Fall back to same provider
            } else {
                return newThread
            }
        } else {
            // Same provider - apply subagent settings
            if subConfig.Model != "" {
                config.Model = subConfig.Model
            }
            if subConfig.MaxTokens > 0 {
                config.MaxTokens = subConfig.MaxTokens
            }
            if subConfig.ThinkingBudget > 0 {
                config.ThinkingBudgetTokens = subConfig.ThinkingBudget
            }
        }
    }
    
    // Same provider path - can reuse client
    thread := &AnthropicThread{
        client:          t.client,  // Reuse parent's client for same provider
        config:          config,
        useSubscription: t.useSubscription,
        conversationID:  convtypes.GenerateID(),
        isPersisted:     false,
        usage:           t.usage,  // Share usage tracking
    }
    
    // ... rest of initialization ...
    
    return thread
}
```

#### OpenAI Implementation
```go
// pkg/llm/openai/openai.go
func (t *OpenAIThread) NewSubAgent(ctx context.Context) llmtypes.Thread {
    config := t.config  // Start with parent's config
    config.IsSubAgent = true
    
    // Apply subagent configuration if specified
    if t.config.SubAgent != nil {
        subConfig := t.config.SubAgent
        
        // Check if we need a different provider
        if subConfig.Provider != "" && subConfig.Provider != "anthropic" {
            // For OpenAI-compatible providers, check if we need different configuration
            needNewThread := false
            
            // Check if subagent has different OpenAI configuration (different base URL, preset, etc.)
            if subConfig.OpenAI != nil {
                // Compare with parent's OpenAI config
                parentOpenAI := t.config.OpenAI
                subOpenAI := subConfig.OpenAI
                
                if (parentOpenAI == nil && subOpenAI != nil) ||
                   (parentOpenAI != nil && subOpenAI != nil && 
                    (parentOpenAI.BaseURL != subOpenAI.BaseURL || 
                     parentOpenAI.Preset != subOpenAI.Preset)) {
                    needNewThread = true
                }
            }
            
            // Check if it's a different provider entirely
            if subConfig.Provider != "openai" {
                needNewThread = true
            }
            
            if needNewThread {
                // Create a new thread with different provider/configuration
                newConfig := config
                newConfig.Provider = subConfig.Provider
                newConfig.Model = subConfig.Model
                newConfig.MaxTokens = subConfig.MaxTokens
                newConfig.ReasoningEffort = subConfig.ReasoningEffort
                newConfig.ThinkingBudgetTokens = subConfig.ThinkingBudget
                newConfig.OpenAI = subConfig.OpenAI  // Use subagent's OpenAI config
                
                newThread, err := llm.NewThread(newConfig)
                if err != nil {
                    logger.G(ctx).WithError(err).Error("Failed to create subagent with different provider/config, falling back to parent config")
                    // Fall back to same provider
                } else {
                    return newThread
                }
            }
        }
        
        // Same provider/configuration - apply subagent settings to current config
        if subConfig.Model != "" {
            config.Model = subConfig.Model
        }
        if subConfig.MaxTokens > 0 {
            config.MaxTokens = subConfig.MaxTokens
        }
        if subConfig.ReasoningEffort != "" {
            config.ReasoningEffort = subConfig.ReasoningEffort
        }
        // Note: We don't change OpenAI config here since we're reusing the same client
    }
    
    // Same provider/configuration path - can reuse client
    thread := &OpenAIThread{
        client:          t.client,  // Reuse parent's client for same configuration
        config:          config,
        reasoningEffort: config.ReasoningEffort,
        conversationID:  convtypes.GenerateID(),
        isPersisted:     false,
        usage:           t.usage,  // Share usage tracking
        customModels:    t.customModels,
        customPricing:   t.customPricing,
        useCopilot:      t.useCopilot,
    }
    
    // ... rest of initialization ...
    
    return thread
}
```

### Updated SubAgent Tool
```go
// pkg/tools/subagent.go
func (t *SubAgentTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
    input := &SubAgentInput{}
    err := json.Unmarshal([]byte(parameters), input)
    if err != nil {
        return &SubAgentToolResult{
            err: err.Error(),
        }
    }
    
    // Get subagent config from context
    subAgentConfig, ok := ctx.Value(llmtypes.SubAgentConfig{}).(llmtypes.SubAgentConfig)
    if !ok {
        return &SubAgentToolResult{
            err:      "sub-agent config not found in context",
            question: input.Question,
        }
    }
    
    // Create subagent thread - will automatically use configured subagent settings
    thread := subAgentConfig.Thread.NewSubAgent(ctx)
    
    handler := subAgentConfig.MessageHandler
    if handler == nil {
        logger.G(ctx).Warn("no message handler found in context, using console handler")
        handler = &llmtypes.ConsoleMessageHandler{}
    }
    
    text, err := thread.SendMessage(ctx, input.Question, handler, llmtypes.MessageOpt{
        PromptCache:        true,
        UseWeakModel:       false,
        NoSaveConversation: true,
        CompactRatio:       subAgentConfig.CompactRatio,
        DisableAutoCompact: subAgentConfig.DisableAutoCompact,
    })
    
    if err != nil {
        return &SubAgentToolResult{
            err:      err.Error(),
            question: input.Question,
        }
    }
    
    return &SubAgentToolResult{
        result:   text,
        question: input.Question,
    }
}
```

### Configuration Loading Enhancement
```go
// pkg/llm/config.go - Enhanced to load subagent configuration
func GetConfigFromViper() llmtypes.Config {
    // ... existing configuration loading ...
    
    // Load subagent configuration
    if viper.IsSet("subagent") {
        subagentMap := viper.GetStringMap("subagent")
        subConfig := &llmtypes.SubAgentConfig{}
        
        if provider, ok := subagentMap["provider"].(string); ok {
            subConfig.Provider = provider
        }
        if model, ok := subagentMap["model"].(string); ok {
            subConfig.Model = model
        }
        if maxTokens, ok := subagentMap["max_tokens"].(int); ok {
            subConfig.MaxTokens = maxTokens
        }
        if reasoningEffort, ok := subagentMap["reasoning_effort"].(string); ok {
            subConfig.ReasoningEffort = reasoningEffort
        }
        if thinkingBudget, ok := subagentMap["thinking_budget"].(int); ok {
            subConfig.ThinkingBudget = thinkingBudget
        }
        
        // Load OpenAI-specific configuration for subagent
        if viper.IsSet("subagent.openai") {
            openaiConfig := &llmtypes.OpenAIConfig{}
            
            // Load basic settings
            openaiConfig.Preset = viper.GetString("subagent.openai.preset")
            openaiConfig.BaseURL = viper.GetString("subagent.openai.base_url")
            
            // Load models configuration
            if viper.IsSet("subagent.openai.models") {
                openaiConfig.Models = &llmtypes.CustomModels{
                    Reasoning:    viper.GetStringSlice("subagent.openai.models.reasoning"),
                    NonReasoning: viper.GetStringSlice("subagent.openai.models.non_reasoning"),
                }
            }
            
            // Load pricing configuration
            if viper.IsSet("subagent.openai.pricing") {
                openaiConfig.Pricing = make(map[string]llmtypes.ModelPricing)
                pricingMap := viper.GetStringMap("subagent.openai.pricing")
                
                for model, pricingData := range pricingMap {
                    if pricingSubMap, ok := pricingData.(map[string]interface{}); ok {
                        pricing := llmtypes.ModelPricing{}
                        
                        if input, ok := pricingSubMap["input"].(float64); ok {
                            pricing.Input = input
                        }
                        if cachedInput, ok := pricingSubMap["cached_input"].(float64); ok {
                            pricing.CachedInput = cachedInput
                        }
                        if output, ok := pricingSubMap["output"].(float64); ok {
                            pricing.Output = output
                        }
                        if contextWindow, ok := pricingSubMap["context_window"].(int); ok {
                            pricing.ContextWindow = contextWindow
                        }
                        
                        openaiConfig.Pricing[model] = pricing
                    }
                }
            }
            
            subConfig.OpenAI = openaiConfig
        }
        
        config.SubAgent = subConfig
    }
    
    return config
}

// pkg/llm/thread.go - No changes needed, but enhanced validation
func NewThread(config llmtypes.Config) (llmtypes.Thread, error) {
    config.Model = resolveModelAlias(config.Model, config.Aliases)
    
    // Ensure API keys are available for the requested provider
    switch strings.ToLower(config.Provider) {
    case "openai":
        if os.Getenv("OPENAI_API_KEY") == "" && !config.UseCopilot {
            return nil, errors.New("OPENAI_API_KEY environment variable is required")
        }
        return openai.NewOpenAIThread(config), nil
    case "anthropic":
        if os.Getenv("ANTHROPIC_API_KEY") == "" {
            return nil, errors.New("ANTHROPIC_API_KEY environment variable is required")
        }
        return anthropic.NewAnthropicThread(config)
    default:
        return nil, errors.Errorf("unsupported provider: %s", config.Provider)
    }
}
```

## Usage Examples

### Example 1: Simple Subagent Call (Automatic Configuration)
```json
{
  "tool_name": "subagent",
  "parameters": {
    "question": "Search for authentication code in the codebase and analyze the security patterns"
  }
}
```
*Uses the configured subagent model automatically - could be OpenAI o3 for complex analysis*

### Example 2: Complex Analysis Task
```json
{
  "tool_name": "subagent", 
  "parameters": {
    "question": "Review this entire microservices architecture and provide a detailed report on potential scalability issues, security vulnerabilities, and recommendations for improvement"
  }
}
```
*Automatically delegates to the high-quality model configured for subagents*

### Example 3: Backward Compatible (No Configuration)
```json
{
  "tool_name": "subagent",
  "parameters": {
    "question": "Analyze error handling patterns"
  }
}
```
*Uses same model/provider as parent when no subagent configuration is specified*

## Implementation Considerations

### 1. OpenAI-Compatible Provider Support

The design handles the complexity of OpenAI-compatible providers (like xAI, together.ai, etc.) that use the same client but different endpoints:

**Key Challenge**: How to enable scenarios like:
- Main agent: OpenAI GPT-4o 
- Subagent: xAI Grok-3 (different provider, same client type)

**Solution**: 
- Both providers use `provider: openai` (same client)
- Differentiation happens through the `openai` configuration block
- When base URLs or presets differ, a new thread is created
- When they're the same, the existing client is reused

**Example Scenarios**:
```yaml
# Scenario 1: OpenAI -> xAI
provider: openai
model: gpt-4o

subagent:
  provider: openai
  model: grok-3
  openai:
    preset: grok
    base_url: "https://api.x.ai/v1"
```

```yaml
# Scenario 2: xAI -> OpenAI  
provider: openai
model: grok-2
openai:
  preset: grok
  base_url: "https://api.x.ai/v1"

subagent:
  provider: openai
  model: o3
  openai:
    preset: openai  # Uses default OpenAI endpoint
```

### 2. API Key Management
- Both providers' API keys must be available in environment variables
- The system will validate API key availability when creating cross-provider subagents
- Clear error messages when API keys are missing
- For OpenAI-compatible providers, the same OPENAI_API_KEY variable is used with different base URLs

### 3. Usage Tracking
- Usage from subagents with different providers should be tracked separately
- Consider implementing provider-specific usage aggregation
- Token costs should reflect the actual model used
- OpenAI-compatible providers may have different pricing structures

### 4. Client Instance Management
- Same-provider subagents can safely reuse parent's client instance **only if base URL and configuration match**
- Cross-provider subagents require new client instances
- OpenAI-compatible providers with different base URLs require new client instances even though they use the same OpenAI client type
- Client lifecycle management is handled within the thread implementation

### 5. System Prompt Compatibility
- System prompts may need adjustment based on the target model
- Consider provider-specific prompt templates for subagents
- Ensure tool descriptions work across different providers
- OpenAI-compatible providers may have model-specific prompt requirements

### 6. Error Handling
- Graceful fallback to parent's provider if override fails
- Clear error messages for configuration issues
- Validation of model names against known models
- Special handling for OpenAI-compatible provider configuration validation

## Alternatives Considered

1. **Direct Model/Provider Parameters in Tool Input**
   - Rejected: LLMs lack reliable knowledge of current models, pricing, and capabilities
   - Would make the system unpredictable and error-prone
   - Complex technical parameters are better handled by configuration

2. **Separate SubAgent Configuration in Context**
   - Rejected: Would require significant changes to the context passing mechanism
   - Current approach is more flexible and backward compatible

3. **Global Subagent Configuration**
   - Rejected: Less flexible, doesn't allow per-task model selection
   - Would require restart to change subagent models

4. **New Tool for Cross-Provider Subagents**
   - Rejected: Would fragment the interface and confuse users
   - Better to enhance existing tool with semantic profiles

## Example Configuration

Users can add a subagent configuration to their `config.yaml` or `kodelet-config.yaml`:

### Basic Cross-Provider Configuration
```yaml
# Example configuration showing subagent setup
provider: anthropic
model: claude-sonnet-4

# Single subagent configuration - optimized for complex analysis tasks
subagent:
  provider: openai
  model: o3
  reasoning_effort: high
  max_tokens: 32000
```

### OpenAI-Compatible Provider Mix-and-Match

The design supports mixing different OpenAI-compatible providers:

```yaml
# Example: Main agent using OpenAI, subagent using xAI (Grok)
provider: openai
model: gpt-4o

subagent:
  provider: openai  # Still uses OpenAI client
  model: grok-3
  reasoning_effort: high
  max_tokens: 25000
  openai:
    preset: grok      # Uses xAI preset
    base_url: "https://api.x.ai/v1"
```

```yaml
# Example: Both using OpenAI-compatible providers but different ones
provider: openai
model: gpt-4o
openai:
  preset: openai

subagent:
  provider: openai
  model: grok-3
  openai:
    preset: grok
    base_url: "https://api.x.ai/v1"
```

```yaml
# Example: Custom OpenAI-compatible provider for subagent
provider: anthropic
model: claude-sonnet-4

subagent:
  provider: openai
  model: custom-reasoning-model
  max_tokens: 32000
  openai:
    base_url: "https://api.custom-ai.com/v1"
    # Custom pricing can also be specified
    pricing:
      custom-reasoning-model:
        input: 0.00005
        output: 0.00015
        context_window: 64000
```

### Alternative Examples

```yaml
# Example 1: Use same provider, different model
provider: anthropic
model: claude-sonnet-4

subagent:
  provider: anthropic
  model: claude-3-5-sonnet  # Slightly different model for subagents
  thinking_budget: 15000
  max_tokens: 20000
```

```yaml
# Example 2: No subagent config - uses parent's configuration
provider: openai
model: gpt-4o
# No subagent section - subagents will use gpt-4o like parent
```

## Consequences

### Positive
- Enables optimal model selection for complex subagent tasks
- Cost optimization through appropriate model choice
- Leverages strengths of different providers (e.g., OpenAI for reasoning, Anthropic for general tasks)
- Maintains backward compatibility
- Minimal API changes
- Deterministic and reliable model selection
- User-controlled configuration without LLM guesswork
- Simple configuration - just one subagent setting, no complex profiles
- Perfect for the high-quality analysis tasks subagents typically handle

### Negative  
- Increased complexity in thread creation logic
- Requires multiple API keys for cross-provider functionality
- Potential for increased costs if not managed carefully (but subagents are used for complex tasks where quality matters)
- More complex usage tracking across providers
- Requires users to understand and configure subagent settings upfront

## Migration Path

1. No migration required for existing usage - fully backward compatible
2. Documentation updates to explain new capabilities
3. Examples showing model mix-and-match patterns
4. Best practices guide for model selection

## Future Enhancements

1. **Model Capability Metadata**
   - Add metadata about model strengths (reasoning, speed, cost)
   - Automatic model selection based on task type

2. **Subagent Pools**
   - Pre-warmed subagents for common model combinations
   - Reduced latency for subagent creation

3. **Cost Optimization**
   - Automatic fallback to cheaper models when appropriate
   - Cost budgets for subagent operations

4. **Provider-Specific Features**
   - Leverage unique features of each provider in subagents
   - Provider-specific parameter validation