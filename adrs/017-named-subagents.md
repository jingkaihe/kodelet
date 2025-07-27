# ADR 017: Named Subagents

## Status
Proposed

## Context
Currently, kodelet supports a generic `subagent` tool that allows delegating tasks to a sub-agent with different configurations. However, users often need to create specialized agents for specific tasks (e.g., PR review, code refactoring, documentation writing) with predefined configurations and prompts. 

The fragment/recipe system already provides a pattern for loading markdown files with YAML frontmatter from specific directories. We can extend this pattern to support named subagents that are pre-configured and easily reusable.

## Decision
We will implement a named subagents feature that:

1. **Loads agent definitions from markdown files** in `./agents` (repository-specific) and `~/.kodelet/agents` (user-level)
2. **Registers each agent as a tool** in the tool registry at startup, similar to how MCP tools are loaded
3. **Supports configuration via YAML frontmatter** including provider, model, reasoning effort, max tokens, allowed tools, and allowed commands
4. **Uses the markdown body as the system prompt** for the agent

### Agent Definition Format
```markdown
---
name: pr_review_agent
description: This is a PR review agent
allowed_tools: [bash, file_edit, file_read, file_write, grep_tool, glob_tool]
provider: "openai"
model: "o3"
reasoning_effort: high
max_tokens: 16000
allowed_commands:
  - git *
  - npm test
  - make lint
---

You are a code review expert. Your task is to review pull requests thoroughly...
<the rest of the system prompt>
```

### Implementation Architecture

#### 1. Agent Package Structure (`pkg/agents`)

Create a new package `pkg/agents` with the following core components:

```go
// pkg/agents/agent.go
package agents

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/invopop/jsonschema"
    "github.com/pkg/errors"
    "github.com/yuin/goldmark"
    "github.com/yuin/goldmark-meta"
    "github.com/yuin/goldmark/parser"
    "go.opentelemetry.io/otel/attribute"

    "github.com/jingkaihe/kodelet/pkg/logger"
    llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
    tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// AgentMetadata represents the YAML frontmatter configuration for an agent
type AgentMetadata struct {
    Name             string                `yaml:"name"`
    Description      string                `yaml:"description"`
    Provider         string                `yaml:"provider"`                  // required: anthropic, openai
    Model            string                `yaml:"model"`                     // required: model name
    ReasoningEffort  string                `yaml:"reasoning_effort,omitempty"` // OpenAI specific: low, medium, high
    MaxTokens        int                   `yaml:"max_tokens"`                // required: maximum tokens
    AllowedTools     []string              `yaml:"allowed_tools,omitempty"`    // tools this agent can use
    AllowedCommands  []string              `yaml:"allowed_commands,omitempty"` // bash commands this agent can execute
    ThinkingBudget   int                   `yaml:"thinking_budget,omitempty"`  // Anthropic specific thinking budget
    OpenAIConfig     *OpenAIAgentConfig    `yaml:"openai,omitempty"`          // OpenAI-compatible provider config
}

// OpenAIAgentConfig holds OpenAI-compatible provider configuration for agents
type OpenAIAgentConfig struct {
    BaseURL      string `yaml:"base_url,omitempty"`      // Custom API base URL
    APIKeyEnvVar string `yaml:"api_key_env_var,omitempty"` // Environment variable name for API key
}

// Agent represents a loaded agent with its metadata, system prompt, and file path
type Agent struct {
    Metadata     AgentMetadata
    SystemPrompt string
    Path         string
}

// AgentProcessor handles loading and processing of agent definitions from disk
type AgentProcessor struct {
    agentDirs []string
}

// AgentProcessorOption configures an AgentProcessor
type AgentProcessorOption func(*AgentProcessor) error

// WithAgentDirs sets custom agent directories
func WithAgentDirs(dirs ...string) AgentProcessorOption {
    return func(ap *AgentProcessor) error {
        if len(dirs) == 0 {
            return errors.New("at least one agent directory must be specified")
        }
        ap.agentDirs = dirs
        return nil
    }
}

// WithDefaultDirs sets the default agent directories (./agents, ~/.kodelet/agents)
func WithDefaultDirs() AgentProcessorOption {
    return func(ap *AgentProcessor) error {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return errors.Wrap(err, "failed to get user home directory")
        }
        ap.agentDirs = []string{
            "./agents",                                    // Repository-specific (higher precedence)
            filepath.Join(homeDir, ".kodelet", "agents"), // User home directory
        }
        return nil
    }
}

// NewAgentProcessor creates a new agent processor with optional configuration
func NewAgentProcessor(opts ...AgentProcessorOption) (*AgentProcessor, error) {
    ap := &AgentProcessor{}

    // If no options provided, use defaults
    if len(opts) == 0 {
        if err := WithDefaultDirs()(ap); err != nil {
            return nil, errors.Wrap(err, "failed to apply default agent directories")
        }
        return ap, nil
    }

    // Apply provided options
    for _, opt := range opts {
        if err := opt(ap); err != nil {
            return nil, errors.Wrap(err, "failed to apply agent processor option")
        }
    }

    // If no directories were set after applying options, apply defaults
    if len(ap.agentDirs) == 0 {
        if err := WithDefaultDirs()(ap); err != nil {
            return nil, errors.Wrap(err, "failed to apply default agent directories")
        }
    }

    return ap, nil
}

// findAgentFile searches for an agent file in the configured directories
func (ap *AgentProcessor) findAgentFile(agentName string) (string, error) {
    // Try both .md and no extension
    possibleNames := []string{
        agentName + ".md",
        agentName,
    }

    for _, dir := range ap.agentDirs {
        for _, name := range possibleNames {
            fullPath := filepath.Join(dir, name)
            if _, err := os.Stat(fullPath); err == nil {
                return fullPath, nil
            }
        }
    }

    return "", errors.Errorf("agent '%s' not found in directories: %v", agentName, ap.agentDirs)
}

// parseFrontmatter extracts YAML frontmatter and body content from agent markdown file
func (ap *AgentProcessor) parseFrontmatter(content string) (AgentMetadata, string, error) {
    var metadata AgentMetadata

    md := goldmark.New(
        goldmark.WithExtensions(
            meta.Meta,
        ),
    )

    source := []byte(content)
    var buf bytes.Buffer
    pctx := parser.NewContext()

    if err := md.Convert(source, &buf, parser.WithContext(pctx)); err != nil {
        return metadata, content, errors.Wrap(err, "failed to convert markdown")
    }

    metaData := meta.Get(pctx)
    if metaData != nil {
        // Parse metadata fields
        if name, ok := metaData["name"].(string); ok {
            metadata.Name = name
        }
        if description, ok := metaData["description"].(string); ok {
            metadata.Description = description
        }
        if provider, ok := metaData["provider"].(string); ok {
            metadata.Provider = provider
        }
        if model, ok := metaData["model"].(string); ok {
            metadata.Model = model
        }
        if reasoningEffort, ok := metaData["reasoning_effort"].(string); ok {
            metadata.ReasoningEffort = reasoningEffort
        }
        if maxTokens, ok := metaData["max_tokens"].(int); ok {
            metadata.MaxTokens = maxTokens
        }
        if thinkingBudget, ok := metaData["thinking_budget"].(int); ok {
            metadata.ThinkingBudget = thinkingBudget
        }

        // Parse allowed_tools and allowed_commands
        if allowedTools := metaData["allowed_tools"]; allowedTools != nil {
            metadata.AllowedTools = ap.parseStringArrayField(allowedTools)
        }
        if allowedCommands := metaData["allowed_commands"]; allowedCommands != nil {
            metadata.AllowedCommands = ap.parseStringArrayField(allowedCommands)
        }

        // Parse OpenAI config
        if openaiConfig := metaData["openai"]; openaiConfig != nil {
            if configMap, ok := openaiConfig.(map[interface{}]interface{}); ok {
                oaiConfig := &OpenAIAgentConfig{}
                if baseURL, ok := configMap["base_url"].(string); ok {
                    oaiConfig.BaseURL = baseURL
                }
                if apiKeyEnvVar, ok := configMap["api_key_env_var"].(string); ok {
                    oaiConfig.APIKeyEnvVar = apiKeyEnvVar
                }
                metadata.OpenAIConfig = oaiConfig
            }
        }
    }

    bodyContent := ap.extractBodyContent(content)
    return metadata, bodyContent, nil
}

// parseStringArrayField handles both []interface{} (YAML array) and string (comma-separated) formats
func (ap *AgentProcessor) parseStringArrayField(field interface{}) []string {
    switch v := field.(type) {
    case []interface{}:
        var result []string
        for _, item := range v {
            if str, ok := item.(string); ok {
                result = append(result, strings.TrimSpace(str))
            }
        }
        return result
    case string:
        if v == "" {
            return []string{}
        }
        var result []string
        for _, item := range strings.Split(v, ",") {
            if trimmed := strings.TrimSpace(item); trimmed != "" {
                result = append(result, trimmed)
            }
        }
        return result
    default:
        return []string{}
    }
}

// extractBodyContent extracts the markdown body content after YAML frontmatter
func (ap *AgentProcessor) extractBodyContent(content string) string {
    if !strings.HasPrefix(content, "---") {
        return content
    }

    lines := strings.Split(content, "\n")
    var frontmatterEnd = -1

    for i := 1; i < len(lines); i++ {
        if strings.TrimSpace(lines[i]) == "---" {
            frontmatterEnd = i
            break
        }
    }

    if frontmatterEnd == -1 {
        return content
    }

    contentLines := lines[frontmatterEnd+1:]
    return strings.Join(contentLines, "\n")
}

// LoadAgent loads a single agent by name
func (ap *AgentProcessor) LoadAgent(ctx context.Context, agentName string) (*Agent, error) {
    logger.G(ctx).WithField("agent", agentName).Debug("Loading agent")

    agentPath, err := ap.findAgentFile(agentName)
    if err != nil {
        return nil, err
    }

    logger.G(ctx).WithField("path", agentPath).Debug("Found agent file")

    content, err := os.ReadFile(agentPath)
    if err != nil {
        return nil, errors.Wrapf(err, "failed to read agent file '%s'", agentPath)
    }

    metadata, systemPrompt, err := ap.parseFrontmatter(string(content))
    if err != nil {
        return nil, errors.Wrapf(err, "failed to parse frontmatter in agent '%s'", agentPath)
    }

    // Validate required fields
    if metadata.Name == "" {
        metadata.Name = agentName
    }
    if metadata.Provider == "" {
        return nil, errors.Errorf("agent '%s' missing required field: provider", agentName)
    }
    if metadata.Model == "" {
        return nil, errors.Errorf("agent '%s' missing required field: model", agentName)
    }
    if metadata.MaxTokens == 0 {
        return nil, errors.Errorf("agent '%s' missing required field: max_tokens", agentName)
    }

    return &Agent{
        Metadata:     metadata,
        SystemPrompt: systemPrompt,
        Path:         agentPath,
    }, nil
}

// ListAgents returns all available agents from the configured directories
func (ap *AgentProcessor) ListAgents(ctx context.Context) ([]*Agent, error) {
    var agents []*Agent
    seen := make(map[string]bool)

    for _, dir := range ap.agentDirs {
        entries, err := os.ReadDir(dir)
        if err != nil {
            // Directory might not exist, continue
            logger.G(ctx).WithField("dir", dir).Debug("Agent directory not found, skipping")
            continue
        }

        for _, entry := range entries {
            if entry.IsDir() {
                continue
            }

            name := entry.Name()
            agentName := strings.TrimSuffix(name, ".md")

            // Only process if not already seen (precedence: repo > home)
            if !seen[agentName] {
                agent, err := ap.LoadAgent(ctx, agentName)
                if err != nil {
                    logger.G(ctx).WithField("agent", agentName).WithError(err).Warn("Failed to load agent, skipping")
                    continue
                }

                agents = append(agents, agent)
                seen[agentName] = true
            }
        }
    }

    logger.G(ctx).WithField("count", len(agents)).Info("Loaded agents")
    return agents, nil
}

// ValidateAgent validates that an agent has all required fields and configurations
func (ap *AgentProcessor) ValidateAgent(agent *Agent) error {
    if agent.Metadata.Name == "" {
        return errors.New("agent name is required")
    }
    if agent.Metadata.Provider == "" {
        return errors.New("agent provider is required")
    }
    if agent.Metadata.Model == "" {
        return errors.New("agent model is required")
    }
    if agent.Metadata.MaxTokens <= 0 {
        return errors.New("agent max_tokens must be greater than 0")
    }
    if strings.TrimSpace(agent.SystemPrompt) == "" {
        return errors.New("agent system prompt cannot be empty")
    }

    // Validate provider-specific fields
    switch agent.Metadata.Provider {
    case "openai":
        if agent.Metadata.ReasoningEffort != "" {
            validEfforts := []string{"low", "medium", "high"}
            found := false
            for _, effort := range validEfforts {
                if agent.Metadata.ReasoningEffort == effort {
                    found = true
                    break
                }
            }
            if !found {
                return errors.Errorf("invalid reasoning_effort '%s', must be one of: %v", 
                    agent.Metadata.ReasoningEffort, validEfforts)
            }
        }
    case "anthropic":
        // Anthropic-specific validations can be added here
    default:
        return errors.Errorf("unsupported provider '%s'", agent.Metadata.Provider)
    }

    return nil
}
```

#### 2. Agent Tool Implementation

```go
// pkg/agents/tool.go
package agents

// NamedAgentTool implements the tooltypes.Tool interface for named agents
type NamedAgentTool struct {
    agent *Agent
}

// NamedAgentInput represents the input parameters for a named agent tool
type NamedAgentInput struct {
    Query string `json:"query" jsonschema:"description=The query or task to send to the agent"`
}

// NamedAgentToolResult represents the result of a named agent execution
type NamedAgentToolResult struct {
    agentName string
    query     string
    result    string
    err       string
}

// NewNamedAgentTool creates a new tool from an agent definition
func NewNamedAgentTool(agent *Agent) *NamedAgentTool {
    return &NamedAgentTool{
        agent: agent,
    }
}

// Tool interface implementations
func (t *NamedAgentTool) Name() string {
    return t.agent.Metadata.Name
}

func (t *NamedAgentTool) Description() string {
    desc := t.agent.Metadata.Description
    if desc == "" {
        desc = fmt.Sprintf("Named agent: %s (Provider: %s, Model: %s)", 
            t.agent.Metadata.Name, t.agent.Metadata.Provider, t.agent.Metadata.Model)
    }
    return desc
}

func (t *NamedAgentTool) GenerateSchema() *jsonschema.Schema {
    return GenerateSchema[NamedAgentInput]()
}

func (t *NamedAgentTool) ValidateInput(state tooltypes.State, parameters string) error {
    input := &NamedAgentInput{}
    err := json.Unmarshal([]byte(parameters), input)
    if err != nil {
        return err
    }

    if strings.TrimSpace(input.Query) == "" {
        return errors.New("query is required")
    }

    return nil
}

func (t *NamedAgentTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
    input := &NamedAgentInput{}
    err := json.Unmarshal([]byte(parameters), input)
    if err != nil {
        return nil, err
    }

    return []attribute.KeyValue{
        attribute.String("agent_name", t.agent.Metadata.Name),
        attribute.String("agent_provider", t.agent.Metadata.Provider),
        attribute.String("agent_model", t.agent.Metadata.Model),
        attribute.String("query", input.Query),
    }, nil
}

func (t *NamedAgentTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
    input := &NamedAgentInput{}
    err := json.Unmarshal([]byte(parameters), input)
    if err != nil {
        return &NamedAgentToolResult{
            agentName: t.agent.Metadata.Name,
            err:       err.Error(),
        }
    }

    // Get subagent config from context
    subAgentConfig, ok := ctx.Value(llmtypes.SubAgentConfigKey).(llmtypes.SubAgentConfig)
    if !ok {
        return &NamedAgentToolResult{
            agentName: t.agent.Metadata.Name,
            query:     input.Query,
            err:       "sub-agent config not found in context",
        }
    }

    // Create agent-specific LLM config
    agentConfig := t.createAgentLLMConfig(subAgentConfig.Thread.GetConfig())
    
    // Create new subagent thread with agent config
    agentThread := subAgentConfig.Thread.NewSubAgent(ctx, agentConfig)
    
    // Create agent-specific state with restricted tools/commands
    agentState := t.createAgentState(ctx, state, agentConfig)
    agentThread.SetState(agentState)

    // Prepare the full prompt: system prompt + user query
    fullPrompt := t.constructFullPrompt(input.Query)

    handler := subAgentConfig.MessageHandler
    if handler == nil {
        logger.G(ctx).Warn("no message handler found in context, using console handler")
        handler = &llmtypes.ConsoleMessageHandler{}
    }

    // Execute the agent
    text, err := agentThread.SendMessage(ctx, fullPrompt, handler, llmtypes.MessageOpt{
        PromptCache:        true,
        UseWeakModel:       false,
        NoSaveConversation: true,
        CompactRatio:       subAgentConfig.CompactRatio,
        DisableAutoCompact: subAgentConfig.DisableAutoCompact,
    })
    
    if err != nil {
        return &NamedAgentToolResult{
            agentName: t.agent.Metadata.Name,
            query:     input.Query,
            err:       err.Error(),
        }
    }

    return &NamedAgentToolResult{
        agentName: t.agent.Metadata.Name,
        query:     input.Query,
        result:    text,
    }
}

// createAgentLLMConfig creates an LLM config from the agent metadata
func (t *NamedAgentTool) createAgentLLMConfig(baseConfig llmtypes.Config) llmtypes.Config {
    config := baseConfig
    config.IsSubAgent = true
    config.Provider = t.agent.Metadata.Provider
    config.Model = t.agent.Metadata.Model
    config.MaxTokens = t.agent.Metadata.MaxTokens
    config.AllowedTools = t.agent.Metadata.AllowedTools
    config.AllowedCommands = t.agent.Metadata.AllowedCommands

    // Provider-specific configurations
    switch t.agent.Metadata.Provider {
    case "openai":
        config.ReasoningEffort = t.agent.Metadata.ReasoningEffort
        if t.agent.Metadata.OpenAIConfig != nil {
            if config.OpenAI == nil {
                config.OpenAI = &llmtypes.OpenAIConfig{}
            }
            if t.agent.Metadata.OpenAIConfig.BaseURL != "" {
                config.OpenAI.BaseURL = t.agent.Metadata.OpenAIConfig.BaseURL
            }
            if t.agent.Metadata.OpenAIConfig.APIKeyEnvVar != "" {
                config.OpenAI.APIKeyEnvVar = t.agent.Metadata.OpenAIConfig.APIKeyEnvVar
            }
        }
    case "anthropic":
        config.ThinkingBudgetTokens = t.agent.Metadata.ThinkingBudget
    }

    return config
}

// createAgentState creates a state with agent-specific tool restrictions
func (t *NamedAgentTool) createAgentState(ctx context.Context, parentState tooltypes.State, agentConfig llmtypes.Config) tooltypes.State {
    // Create a new state with agent-specific configuration
    stateOpts := []tools.BasicStateOption{
        tools.WithLLMConfig(agentConfig),
        tools.WithSubAgentTools(agentConfig),
    }
    
    return tools.NewBasicState(ctx, stateOpts...)
}

// constructFullPrompt combines the system prompt with the user query
func (t *NamedAgentTool) constructFullPrompt(userQuery string) string {
    systemPrompt := strings.TrimSpace(t.agent.SystemPrompt)
    if systemPrompt == "" {
        return userQuery
    }
    
    return fmt.Sprintf("%s\n\n---\n\nUser Query: %s", systemPrompt, userQuery)
}

// NamedAgentToolResult interface implementations
func (r *NamedAgentToolResult) GetResult() string {
    return r.result
}

func (r *NamedAgentToolResult) GetError() string {
    return r.err
}

func (r *NamedAgentToolResult) IsError() bool {
    return r.err != ""
}

func (r *NamedAgentToolResult) AssistantFacing() string {
    return tooltypes.StringifyToolResult(r.result, r.GetError())
}

func (r *NamedAgentToolResult) StructuredData() tooltypes.StructuredToolResult {
    return tooltypes.StructuredToolResult{
        ToolName:  r.agentName,
        Query:     r.query,
        Result:    r.result,
        Error:     r.err,
        Timestamp: time.Now(),
    }
}
```

#### 3. Package-Level Agent Loading and Registration

```go
// pkg/agents/manager.go
package agents

// AgentManager handles the loading and management of named agents
type AgentManager struct {
    processor *AgentProcessor
    agents    []*Agent
    tools     []tooltypes.Tool
}

// NewAgentManager creates a new agent manager with default configuration
func NewAgentManager(ctx context.Context) (*AgentManager, error) {
    processor, err := NewAgentProcessor()
    if err != nil {
        return nil, errors.Wrap(err, "failed to create agent processor")
    }

    return &AgentManager{
        processor: processor,
    }, nil
}

// LoadAllAgents loads all available agents and converts them to tools
func (am *AgentManager) LoadAllAgents(ctx context.Context) error {
    agents, err := am.processor.ListAgents(ctx)
    if err != nil {
        return errors.Wrap(err, "failed to list agents")
    }

    var tools []tooltypes.Tool
    for _, agent := range agents {
        // Validate each agent before creating a tool
        if err := am.processor.ValidateAgent(agent); err != nil {
            logger.G(ctx).WithField("agent", agent.Metadata.Name).WithError(err).Warn("Invalid agent configuration, skipping")
            continue
        }

        tool := NewNamedAgentTool(agent)
        tools = append(tools, tool)
        
        logger.G(ctx).WithFields(map[string]interface{}{
            "agent": agent.Metadata.Name,
            "provider": agent.Metadata.Provider,
            "model": agent.Metadata.Model,
        }).Debug("Registered named agent as tool")
    }

    am.agents = agents
    am.tools = tools
    
    logger.G(ctx).WithField("count", len(tools)).Info("Loaded named agents as tools")
    return nil
}

// GetAgentTools returns all loaded agent tools
func (am *AgentManager) GetAgentTools() []tooltypes.Tool {
    return am.tools
}

// GetAgent returns a specific agent by name
func (am *AgentManager) GetAgent(name string) (*Agent, error) {
    for _, agent := range am.agents {
        if agent.Metadata.Name == name {
            return agent, nil
        }
    }
    return nil, errors.Errorf("agent '%s' not found", name)
}

// ListAgentNames returns the names of all loaded agents
func (am *AgentManager) ListAgentNames() []string {
    var names []string
    for _, agent := range am.agents {
        names = append(names, agent.Metadata.Name)
    }
    return names
}

// CreateAgentManagerFromContext creates an agent manager and loads all agents
// This is the main entry point for integrating with the rest of kodelet
func CreateAgentManagerFromContext(ctx context.Context) (*AgentManager, error) {
    manager, err := NewAgentManager(ctx)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create agent manager")
    }

    if err := manager.LoadAllAgents(ctx); err != nil {
        return nil, errors.Wrap(err, "failed to load agents")
    }

    return manager, nil
}
```

#### 4. Integration with Tool State System

Add a new option to the tool state system for named agents:

```go
// In pkg/tools/state.go

// WithNamedAgentTools loads named agents and adds them as tools
func WithNamedAgentTools() BasicStateOption {
    return func(ctx context.Context, s *BasicState) error {
        agentManager, err := agents.CreateAgentManagerFromContext(ctx)
        if err != nil {
            // Log warning but don't fail - agents are optional
            logger.G(ctx).WithError(err).Warn("Failed to load named agents, continuing without them")
            return nil
        }

        agentTools := agentManager.GetAgentTools()
        s.mcpTools = append(s.mcpTools, agentTools...)
        
        logger.G(ctx).WithField("count", len(agentTools)).Debug("Added named agent tools to state")
        return nil
    }
}
```
```

#### 5. Integration with Run Commands

The named agents will be integrated into the main kodelet commands following the same pattern as MCP tools:

```go
// In cmd/kodelet/run.go (and similar files: chat.go, watch.go, etc.)

func init() {
    // Add the named agent tools option to the state creation
}

// In the Run command execution:
var stateOpts []tools.BasicStateOption

stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))

// Add named agents to all commands that support tools
stateOpts = append(stateOpts, tools.WithNamedAgentTools())

if config.EnableBrowserTools {
    stateOpts = append(stateOpts, tools.WithMainToolsAndBrowser())
} else {
    stateOpts = append(stateOpts, tools.WithMainTools())
}

appState := tools.NewBasicState(ctx, stateOpts...)
```

#### 6. Usage Examples

Once implemented, users can invoke named agents like any other tool:

```bash
# Example agent invocation through the LLM
kodelet run "Use the pr_review_agent to review the changes in this PR"

# The LLM will automatically use the pr_review_agent tool with the system prompt
# defined in the agent file
```

Agent files would be organized as:

```
./agents/pr_review_agent.md           # Repository-specific PR review agent
./agents/code_refactor_agent.md       # Repository-specific refactoring agent
~/.kodelet/agents/general_review.md   # User-level general review agent
~/.kodelet/agents/documentation.md    # User-level documentation agent
```

### Directory Structure and Precedence
- Repository-specific agents in `./agents` take precedence over user-level agents
- Agent names must be unique within the loaded set
- Files must have `.md` extension

### Error Handling
- Invalid YAML frontmatter will log a warning and skip the agent
- Missing required fields (name, provider, model) will skip the agent
- Duplicate agent names will use the first loaded (repository takes precedence)

## Consequences

### Positive
- **Reusability**: Users can define specialized agents once and reuse them across projects
- **Consistency**: Teams can share agent definitions for consistent behavior
- **Flexibility**: Support for different providers and models per agent
- **Discoverability**: Agents appear in the tool list with clear descriptions
- **Familiar Pattern**: Builds on the existing fragments/recipe pattern

### Negative
- **Complexity**: Adds another configuration layer to the system
- **Tool Proliferation**: Many agents could clutter the tool list
- **Naming Conflicts**: Potential for agent name collisions
- **Memory Usage**: Each agent maintains its own thread and state

### Neutral
- Agents are stateless between invocations (same as current subagent)
- System prompts are fixed at load time (no dynamic updates)
- Configuration validation happens at startup

## Alternatives Considered

1. **Extend Fragments System**: Could have extended fragments to support agent configuration, but this would conflate two different concepts (prompts vs. agents)

2. **Configuration File Approach**: Could use YAML/JSON config files instead of markdown, but markdown allows for better documentation and readability of system prompts

3. **Dynamic Agent Creation**: Could allow creating agents on-the-fly via commands, but static definitions are simpler and more predictable

## Implementation Plan

### Phase 1: Core Agent Infrastructure
1. **Create `pkg/agents` package** with the following files:
   - `agent.go` - Core types, AgentProcessor, and loading logic
   - `tool.go` - NamedAgentTool implementation and tool result handling
   - `manager.go` - AgentManager for coordinating agent loading and tool creation
   - `agent_test.go` - Unit tests for agent loading and validation

### Phase 2: Tool Integration
2. **Add agent support to tool state system**:
   - Implement `WithNamedAgentTools()` option in `pkg/tools/state.go`
   - Add agent manager import and initialization
   - Ensure proper error handling for missing agent directories

### Phase 3: Command Integration
3. **Integrate into main commands**:
   - Update `cmd/kodelet/run.go` to include `WithNamedAgentTools()`
   - Update `cmd/kodelet/chat.go` for interactive mode
   - Update `cmd/kodelet/watch.go` for file watching mode  
   - Update other relevant commands (pr.go, issue_resolve.go, etc.)

### Phase 4: Testing and Validation
4. **Comprehensive testing**:
   - Unit tests for agent loading, parsing, and validation
   - Integration tests for agent tool execution
   - End-to-end tests with sample agent definitions
   - Error handling tests for malformed agent files

### Phase 5: Documentation and Examples
5. **Documentation and examples**:
   - Create sample agent definitions in `recipes/agents/` directory
   - Update KODELET.md with named agents section
   - Add usage examples and best practices
   - Document agent configuration options and validation rules

### Implementation Notes
- **Graceful degradation**: Agent loading failures should not prevent kodelet from starting
- **Performance**: Agent loading happens once at startup, not on each invocation
- **Security**: Agent tool/command restrictions are enforced at the LLM config level
- **Extensibility**: The design allows for future enhancements like dynamic agent reloading

## References
- Fragment system implementation: `pkg/fragments/fragments.go`
- MCP tool loading: `pkg/tools/mcp.go`
- Subagent tool: `pkg/tools/subagent.go`
- LLM configuration: `pkg/types/llm/config.go`