package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

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
func GetDefaultConversationService() (*ConversationService, error) {
	store, err := GetConversationStore()
	if err != nil {
		return nil, err
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
	Conversations []ConversationSummary `json:"conversations"`
	Total         int                   `json:"total"`
	Limit         int                   `json:"limit"`
	Offset        int                   `json:"offset"`
	HasMore       bool                  `json:"hasMore"`
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
	ToolCallID string                    `json:"toolCallId"`
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
	options := QueryOptions{
		StartDate:  req.StartDate,
		EndDate:    req.EndDate,
		SearchTerm: req.SearchTerm,
		Limit:      req.Limit,
		Offset:     req.Offset,
		SortBy:     req.SortBy,
		SortOrder:  req.SortOrder,
	}

	// Query conversations
	summaries, err := s.store.Query(options)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversations: %w", err)
	}

	// Calculate pagination info
	total := len(summaries)
	hasMore := req.Limit > 0 && total == req.Limit

	response := &ListConversationsResponse{
		Conversations: summaries,
		Total:         total,
		Limit:         req.Limit,
		Offset:        req.Offset,
		HasMore:       hasMore,
	}

	logger.G(ctx).WithField("count", len(summaries)).Debug("Listed conversations")
	return response, nil
}

// GetConversation retrieves a specific conversation with all its data
func (s *ConversationService) GetConversation(ctx context.Context, id string) (*GetConversationResponse, error) {
	logger.G(ctx).WithField("id", id).Debug("Getting conversation")

	// Load the conversation record
	record, err := s.store.Load(id)
	if err != nil {
		return nil, fmt.Errorf("failed to load conversation: %w", err)
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
	record, err := s.store.Load(conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to load conversation: %w", err)
	}

	// Find the tool result
	result, exists := record.ToolResults[toolCallID]
	if !exists {
		return nil, fmt.Errorf("tool result not found: %s", toolCallID)
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

	err := s.store.Delete(id)
	if err != nil {
		return fmt.Errorf("failed to delete conversation: %w", err)
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
	summaries, err := s.store.List()
	if err != nil {
		return "", fmt.Errorf("failed to list conversations: %w", err)
	}

	// Find conversations that start with the short ID
	var matches []string
	for _, summary := range summaries {
		if strings.HasPrefix(summary.ID, id) {
			matches = append(matches, summary.ID)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no conversation found with ID starting with '%s'", id)
	}

	if len(matches) > 1 {
		return "", fmt.Errorf("multiple conversations found with ID starting with '%s': %v", id, matches)
	}

	resolvedID := matches[0]
	logger.G(ctx).WithField("originalID", id).WithField("resolvedID", resolvedID).Debug("Resolved conversation ID")
	return resolvedID, nil
}

// SearchConversations performs full-text search across conversations
func (s *ConversationService) SearchConversations(ctx context.Context, query string, limit int) (*ListConversationsResponse, error) {
	logger.G(ctx).WithField("query", query).WithField("limit", limit).Debug("Searching conversations")

	req := &ListConversationsRequest{
		SearchTerm: query,
		Limit:      limit,
		SortBy:     "updated",
		SortOrder:  "desc",
	}

	return s.ListConversations(ctx, req)
}

// GetConversationStatistics returns statistics about conversations
func (s *ConversationService) GetConversationStatistics(ctx context.Context) (*ConversationStatistics, error) {
	logger.G(ctx).Debug("Getting conversation statistics")

	summaries, err := s.store.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}

	stats := &ConversationStatistics{
		TotalConversations: len(summaries),
		TotalMessages:      0,
		TotalUsage:         llmtypes.Usage{},
	}

	// Calculate statistics
	for _, summary := range summaries {
		stats.TotalMessages += summary.MessageCount
		
		// For detailed usage stats, we'd need to load each conversation
		// For now, we'll just count conversations
	}

	// Find oldest and newest conversations
	if len(summaries) > 0 {
		oldest := summaries[0].CreatedAt
		newest := summaries[0].UpdatedAt
		
		for _, summary := range summaries {
			if summary.CreatedAt.Before(oldest) {
				oldest = summary.CreatedAt
			}
			if summary.UpdatedAt.After(newest) {
				newest = summary.UpdatedAt
			}
		}
		
		stats.OldestConversation = &oldest
		stats.NewestConversation = &newest
	}

	logger.G(ctx).WithField("stats", stats).Debug("Retrieved conversation statistics")
	return stats, nil
}

// ConversationStatistics represents conversation statistics
type ConversationStatistics struct {
	TotalConversations  int                `json:"totalConversations"`
	TotalMessages       int                `json:"totalMessages"`
	TotalUsage          llmtypes.Usage     `json:"totalUsage"`
	OldestConversation  *time.Time         `json:"oldestConversation,omitempty"`
	NewestConversation  *time.Time         `json:"newestConversation,omitempty"`
}

// Close closes the underlying store
func (s *ConversationService) Close() error {
	return s.store.Close()
}