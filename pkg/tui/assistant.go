package tui

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
)

// AssistantClient handles the interaction with the LLM thread
type AssistantClient struct {
	thread             llmtypes.Thread
	mcpManager         *tools.MCPManager
	customManager      *tools.CustomToolManager
	maxTurns           int
	compactRatio       float64
	disableAutoCompact bool
}

// NewAssistantClient creates a new assistant client
func NewAssistantClient(ctx context.Context, conversationID string, enablePersistence bool, mcpManager *tools.MCPManager, customManager *tools.CustomToolManager, maxTurns int, compactRatio float64, disableAutoCompact bool, ideMode bool) *AssistantClient {
	config, err := llm.GetConfigFromViper()
	if err != nil {
		logger.G(ctx).WithError(err).Fatal("Failed to load configuration during assistant client initialization")
	}

	config.IDE = ideMode

	// Set MCP configuration for system prompt
	executionMode := viper.GetString("mcp.execution_mode")
	workspaceDir := viper.GetString("mcp.code_execution.workspace_dir")
	if workspaceDir == "" {
		workspaceDir = ".kodelet/mcp"
	}
	config.MCPExecutionMode = executionMode
	config.MCPWorkspaceDir = workspaceDir

	thread, err := llm.NewThread(config)
	if err != nil {
		logger.G(ctx).WithError(err).Fatal("Failed to create LLM thread during assistant client initialization")
	}

	// Create state with main tools
	var stateOpts []tools.BasicStateOption
	stateOpts = append(stateOpts, tools.WithLLMConfig(config))
	stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
	stateOpts = append(stateOpts, tools.WithCustomTools(customManager))
	stateOpts = append(stateOpts, tools.WithMainTools())
	state := tools.NewBasicState(ctx, stateOpts...)
	thread.SetState(state)

	// Configure conversation persistence
	if conversationID != "" {
		thread.SetConversationID(conversationID)
	}

	thread.EnablePersistence(ctx, enablePersistence)

	return &AssistantClient{
		thread:             thread,
		mcpManager:         mcpManager,
		customManager:      customManager,
		maxTurns:           maxTurns,
		compactRatio:       compactRatio,
		disableAutoCompact: disableAutoCompact,
	}
}

// GetThreadMessages returns the messages from the thread
func (a *AssistantClient) GetThreadMessages() ([]llmtypes.Message, error) {
	return a.thread.GetMessages()
}

// func (a *AssistantClient) AddUserMessage(message string, imagePaths ...string) {
// 	a.thread.AddUserMessage(message, imagePaths...)
// }

// func (a *AssistantClient) SaveConversation(ctx context.Context) error {
// 	return a.thread.SaveConversation(ctx, true)
// }

// SendMessage sends a message to the assistant and processes the response
func (a *AssistantClient) SendMessage(ctx context.Context, message string, messageCh chan llmtypes.MessageEvent, imagePaths ...string) error {
	// Create a handler for channel-based events
	handler := &llmtypes.ChannelMessageHandler{MessageCh: messageCh}

	// Send the message using the persistent thread
	_, err := a.thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache:        true,
		Images:             imagePaths,
		MaxTurns:           a.maxTurns,
		CompactRatio:       a.compactRatio,
		DisableAutoCompact: a.disableAutoCompact,
	})

	return err
}

// GetUsage returns the current token usage
func (a *AssistantClient) GetUsage() llmtypes.Usage {
	return a.thread.GetUsage()
}

// GetConversationID returns the current conversation ID
func (a *AssistantClient) GetConversationID() string {
	return a.thread.GetConversationID()
}

// IsPersisted returns whether this thread is being persisted
func (a *AssistantClient) IsPersisted() bool {
	return a.thread.IsPersisted()
}

// GetModelInfo returns the provider and model name being used
func (a *AssistantClient) GetModelInfo() (provider, model string) {
	config := a.thread.GetConfig()
	return config.Provider, config.Model
}

// Close performs cleanup operations for the assistant client
func (a *AssistantClient) Close(ctx context.Context) error {
	if a.mcpManager != nil {
		return a.mcpManager.Close(ctx)
	}
	return nil
}
