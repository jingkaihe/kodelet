package tools

import (
	"context"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

type toolContextKey struct{}

// MetadataStore exposes provider-neutral conversation metadata to tools that
// need to inspect or mutate thread-level runtime state.
type MetadataStore interface {
	GetMetadata() map[string]any
	SetMetadataValue(key string, value any)
}

type ToolContext struct {
	ConversationID string
	WorkingDir     string
	Provider       string
	Model          string
	Profile        string
	MetadataStore  MetadataStore
}

func ContextWithToolContext(ctx context.Context, toolContext ToolContext) context.Context {
	toolContext = normalizeToolContext(toolContext)
	if toolContextIsEmpty(toolContext) {
		return ctx
	}
	return context.WithValue(ctx, toolContextKey{}, toolContext)
}

func ContextWithConversationID(ctx context.Context, conversationID string) context.Context {
	return ContextWithToolContext(ctx, ToolContext{ConversationID: conversationID})
}

func ToolContextFromThreadState(threadConfig llmtypes.Config, conversationID string, stateWorkingDir string, metadataStore MetadataStore) ToolContext {
	return normalizeToolContext(ToolContext{
		ConversationID: conversationID,
		WorkingDir:     firstNonEmpty(stateWorkingDir, threadConfig.WorkingDirectory),
		Provider:       threadConfig.Provider,
		Model:          threadConfig.Model,
		Profile:        threadConfig.Profile,
		MetadataStore:  metadataStore,
	})
}

func toolContextFromContext(ctx context.Context) ToolContext {
	toolContext, _ := ctx.Value(toolContextKey{}).(ToolContext)
	return normalizeToolContext(toolContext)
}

func normalizeToolContext(toolContext ToolContext) ToolContext {
	toolContext.ConversationID = strings.TrimSpace(toolContext.ConversationID)
	toolContext.WorkingDir = strings.TrimSpace(toolContext.WorkingDir)
	toolContext.Provider = strings.TrimSpace(toolContext.Provider)
	toolContext.Model = strings.TrimSpace(toolContext.Model)
	toolContext.Profile = strings.TrimSpace(toolContext.Profile)
	return toolContext
}

func toolContextIsEmpty(toolContext ToolContext) bool {
	return toolContext.ConversationID == "" &&
		toolContext.WorkingDir == "" &&
		toolContext.Provider == "" &&
		toolContext.Model == "" &&
		toolContext.Profile == "" &&
		toolContext.MetadataStore == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
