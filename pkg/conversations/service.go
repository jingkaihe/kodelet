package conversations

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

// toUsageSummaries converts conversations.ConversationSummary slice to usage.ConversationSummary interface slice
func toUsageSummaries(summaries []conversations.ConversationSummary) []usage.ConversationSummary {
	result := make([]usage.ConversationSummary, len(summaries))
	for i, s := range summaries {
		result[i] = s
	}
	return result
}

// ConversationServiceInterface defines the interface for conversation operations
type ConversationServiceInterface interface {
	ListConversations(ctx context.Context, req *ListConversationsRequest) (*ListConversationsResponse, error)
	GetConversation(ctx context.Context, id string) (*GetConversationResponse, error)
	GetToolResult(ctx context.Context, conversationID, toolCallID string) (*GetToolResultResponse, error)
	DeleteConversation(ctx context.Context, id string) error
	ResolveConversationID(ctx context.Context, id string) (string, error)
	GetConversationStatistics(ctx context.Context) (*ConversationStatistics, error)
	Close() error
}

// ConversationService provides high-level conversation operations
type ConversationService struct {
	store ConversationStore
}

// NewConversationService creates a new conversation service
func NewConversationService(store ConversationStore) *ConversationService {
	return &ConversationService{
		store: store,
	}
}

// GetDefaultConversationService returns a service with the default store
func GetDefaultConversationService(ctx context.Context) (*ConversationService, error) {
	store, err := GetConversationStore(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get conversation store")
	}
	return NewConversationService(store), nil
}

// ListConversationsRequest represents a request to list conversations
type ListConversationsRequest struct {
	StartDate  *time.Time `json:"startDate,omitempty"`
	EndDate    *time.Time `json:"endDate,omitempty"`
	SearchTerm string     `json:"searchTerm,omitempty"`
	Limit      int        `json:"limit,omitempty"`
	Offset     int        `json:"offset,omitempty"`
	SortBy     string     `json:"sortBy,omitempty"`
	SortOrder  string     `json:"sortOrder,omitempty"`
}

// ListConversationsResponse represents the response from listing conversations
type ListConversationsResponse struct {
	Conversations []conversations.ConversationSummary   `json:"conversations"`
	Total         int                     `json:"total"`
	Limit         int                     `json:"limit"`
	Offset        int                     `json:"offset"`
	HasMore       bool                    `json:"hasMore"`
	Stats         *ConversationStatistics `json:"stats,omitempty"`
}

// GetConversationResponse represents the response from getting a conversation
type GetConversationResponse struct {
	ID           string                                `json:"id"`
	CreatedAt    time.Time                             `json:"createdAt"`
	UpdatedAt    time.Time                             `json:"updatedAt"`
	ModelType    string                                `json:"modelType"`
	Summary      string                                `json:"summary,omitempty"`
	Usage        llmtypes.Usage                        `json:"usage"`
	RawMessages  json.RawMessage                       `json:"rawMessages"`
	ToolResults  map[string]tools.StructuredToolResult `json:"toolResults,omitempty"`
	MessageCount int                                   `json:"messageCount"`
}

// GetToolResultResponse represents the response from getting a tool result
type GetToolResultResponse struct {
	ToolCallID string                     `json:"toolCallId"`
	Result     tools.StructuredToolResult `json:"result"`
}

// ListConversations retrieves conversations with filtering and pagination
func (s *ConversationService) ListConversations(ctx context.Context, req *ListConversationsRequest) (*ListConversationsResponse, error) {
	logger.G(ctx).WithField("request", req).Debug("Listing conversations")

	// Set defaults
	if req.SortBy == "" {
		req.SortBy = "updated"
	}
	if req.SortOrder == "" {
		req.SortOrder = "desc"
	}

	// Convert request to query options
	options := conversations.QueryOptions{
		StartDate:  req.StartDate,
		EndDate:    req.EndDate,
		SearchTerm: req.SearchTerm,
		Limit:      req.Limit,
		Offset:     req.Offset,
		SortBy:     req.SortBy,
		SortOrder:  req.SortOrder,
	}

	// Query conversations with pagination
	result, err := s.store.Query(ctx, options)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query conversations")
	}

	summaries := result.ConversationSummaries
	total := result.Total

	// Calculate pagination info
	hasMore := req.Limit > 0 && len(summaries) == req.Limit

	// Calculate statistics for the returned conversations
	var stats *ConversationStatistics
	if len(summaries) > 0 {
		// Calculate usage statistics directly from summaries
		usageStats := usage.CalculateConversationUsageStats(toUsageSummaries(summaries))

		// Convert to ConversationStatistics
		stats = &ConversationStatistics{
			TotalConversations: usageStats.TotalConversations,
			TotalMessages:      usageStats.TotalMessages,
			TotalTokens:        usageStats.TotalTokens,
			TotalCost:          usageStats.TotalCost,
			InputTokens:        usageStats.InputTokens,
			OutputTokens:       usageStats.OutputTokens,
			CacheReadTokens:    usageStats.CacheReadTokens,
			CacheWriteTokens:   usageStats.CacheWriteTokens,
			InputCost:          usageStats.InputCost,
			OutputCost:         usageStats.OutputCost,
			CacheReadCost:      usageStats.CacheReadCost,
			CacheWriteCost:     usageStats.CacheWriteCost,
		}
	} else {
		summaries = []conversations.ConversationSummary{}
	}

	response := &ListConversationsResponse{
		Conversations: summaries,
		Total:         total,
		Limit:         req.Limit,
		Offset:        req.Offset,
		HasMore:       hasMore,
		Stats:         stats,
	}

	logger.G(ctx).WithField("count", len(summaries)).Debug("Listed conversations")
	return response, nil
}

// GetConversation retrieves a specific conversation with all its data
func (s *ConversationService) GetConversation(ctx context.Context, id string) (*GetConversationResponse, error) {
	logger.G(ctx).WithField("id", id).Debug("Getting conversation")

	// Load the conversation record
	record, err := s.store.Load(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load conversation")
	}

	// Calculate message count by parsing the raw messages
	messageCount := 0
	if len(record.RawMessages) > 0 {
		var messages []interface{}
		if err := json.Unmarshal(record.RawMessages, &messages); err == nil {
			messageCount = len(messages)
		}
	}

	response := &GetConversationResponse{
		ID:           record.ID,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
		ModelType:    record.ModelType,
		Summary:      record.Summary,
		Usage:        record.Usage,
		RawMessages:  record.RawMessages,
		ToolResults:  record.ToolResults,
		MessageCount: messageCount,
	}

	logger.G(ctx).WithField("id", id).WithField("messageCount", messageCount).Debug("Retrieved conversation")
	return response, nil
}

// GetToolResult retrieves a specific tool result from a conversation
func (s *ConversationService) GetToolResult(ctx context.Context, conversationID, toolCallID string) (*GetToolResultResponse, error) {
	logger.G(ctx).WithField("conversationID", conversationID).WithField("toolCallID", toolCallID).Debug("Getting tool result")

	// Load the conversation record
	record, err := s.store.Load(ctx, conversationID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load conversation")
	}

	// Find the tool result
	result, exists := record.ToolResults[toolCallID]
	if !exists {
		return nil, errors.Errorf("tool result not found: %s", toolCallID)
	}

	response := &GetToolResultResponse{
		ToolCallID: toolCallID,
		Result:     result,
	}

	logger.G(ctx).WithField("toolName", result.ToolName).Debug("Retrieved tool result")
	return response, nil
}

// DeleteConversation deletes a conversation
func (s *ConversationService) DeleteConversation(ctx context.Context, id string) error {
	logger.G(ctx).WithField("id", id).Debug("Deleting conversation")

	err := s.store.Delete(ctx, id)
	if err != nil {
		return errors.Wrap(err, "failed to delete conversation")
	}

	logger.G(ctx).WithField("id", id).Info("Deleted conversation")
	return nil
}

// ResolveConversationID resolves a conversation ID, supporting both full and short IDs
func (s *ConversationService) ResolveConversationID(ctx context.Context, id string) (string, error) {
	logger.G(ctx).WithField("id", id).Debug("Resolving conversation ID")

	// If it's already a full ID (UUID format), return as-is
	if len(id) == 36 && strings.Count(id, "-") == 4 {
		return id, nil
	}

	// For short IDs, we need to search through conversations
	summaries, err := s.store.List(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list conversations")
	}

	// Find conversations that start with the short ID
	var matches []string
	for _, summary := range summaries {
		if strings.HasPrefix(summary.ID, id) {
			matches = append(matches, summary.ID)
		}
	}

	if len(matches) == 0 {
		return "", errors.Errorf("no conversation found with ID starting with '%s'", id)
	}

	if len(matches) > 1 {
		return "", errors.Errorf("multiple conversations found with ID starting with '%s': %v", id, matches)
	}

	resolvedID := matches[0]
	logger.G(ctx).WithField("originalID", id).WithField("resolvedID", resolvedID).Debug("Resolved conversation ID")
	return resolvedID, nil
}

// GetConversationStatistics returns statistics about conversations
func (s *ConversationService) GetConversationStatistics(ctx context.Context) (*ConversationStatistics, error) {
	logger.G(ctx).Debug("Getting conversation statistics")

	summaries, err := s.store.List(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list conversations")
	}

	if len(summaries) == 0 {
		stats := &ConversationStatistics{
			TotalConversations: 0,
			TotalMessages:      0,
		}
		return stats, nil
	}

	// Calculate usage statistics directly from summaries
	usageStats := usage.CalculateConversationUsageStats(toUsageSummaries(summaries))

	// Convert to ConversationStatistics
	stats := &ConversationStatistics{
		TotalConversations: usageStats.TotalConversations,
		TotalMessages:      usageStats.TotalMessages,
		TotalTokens:        usageStats.TotalTokens,
		TotalCost:          usageStats.TotalCost,
		InputTokens:        usageStats.InputTokens,
		OutputTokens:       usageStats.OutputTokens,
		CacheReadTokens:    usageStats.CacheReadTokens,
		CacheWriteTokens:   usageStats.CacheWriteTokens,
		InputCost:          usageStats.InputCost,
		OutputCost:         usageStats.OutputCost,
		CacheReadCost:      usageStats.CacheReadCost,
		CacheWriteCost:     usageStats.CacheWriteCost,
	}

	logger.G(ctx).WithField("stats", stats).Debug("Retrieved conversation statistics")
	return stats, nil
}

// ConversationStatistics represents conversation statistics
type ConversationStatistics struct {
	TotalConversations int     `json:"totalConversations"`
	TotalMessages      int     `json:"totalMessages"`
	TotalTokens        int     `json:"totalTokens"`
	TotalCost          float64 `json:"totalCost"`
	InputTokens        int     `json:"inputTokens"`
	OutputTokens       int     `json:"outputTokens"`
	CacheReadTokens    int     `json:"cacheReadTokens"`
	CacheWriteTokens   int     `json:"cacheWriteTokens"`
	InputCost          float64 `json:"inputCost"`
	OutputCost         float64 `json:"outputCost"`
	CacheReadCost      float64 `json:"cacheReadCost"`
	CacheWriteCost     float64 `json:"cacheWriteCost"`
}

// Close closes the underlying store
func (s *ConversationService) Close() error {
	return s.store.Close()
}
