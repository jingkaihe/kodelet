package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

// NamedAgentTool implements the tooltypes.Tool interface for named agents
type NamedAgentTool struct {
	agent *Agent
}

// NamedAgentInput represents the input parameters for a named agent tool
type NamedAgentInput struct {
	Query string `json:"query" jsonschema:"description=The query or task to send to the agent"`
}

// NamedAgentMetadata represents metadata for named agent execution
type NamedAgentMetadata struct {
	Query    string `json:"query"`
	Response string `json:"response"`
}

func (m NamedAgentMetadata) ToolType() string { return "named_agent" }

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
	return utils.GenerateSchema[NamedAgentInput]()
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
	result := tooltypes.StructuredToolResult{
		ToolName:  r.agentName,
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Always populate metadata, even for errors
	result.Metadata = &NamedAgentMetadata{
		Query:    r.query,
		Response: r.result,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}