package conversations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationStore implements ConversationStore for testing
type mockConversationStore struct {
	conversations map[string]*ConversationRecord
	summaries     []ConversationSummary
	queryFunc     func(options QueryOptions) ([]ConversationSummary, error)
	loadFunc      func(id string) (*ConversationRecord, error)
	deleteFunc    func(id string) error
	listFunc      func() ([]ConversationSummary, error)
	closeFunc     func() error
}

func newMockConversationStore() *mockConversationStore {
	return &mockConversationStore{
		conversations: make(map[string]*ConversationRecord),
		summaries:     []ConversationSummary{},
	}
}

func (m *mockConversationStore) Save(record ConversationRecord) error {
	m.conversations[record.ID] = &record
	return nil
}

func (m *mockConversationStore) Load(id string) (ConversationRecord, error) {
	if m.loadFunc != nil {
		rec, err := m.loadFunc(id)
		if err != nil {
			return ConversationRecord{}, err
		}
		if rec == nil {
			return ConversationRecord{}, errors.New("conversation not found")
		}
		return *rec, nil
	}
	record, exists := m.conversations[id]
	if !exists {
		return ConversationRecord{}, errors.New("conversation not found")
	}
	return *record, nil
}

func (m *mockConversationStore) List() ([]ConversationSummary, error) {
	if m.listFunc != nil {
		return m.listFunc()
	}
	return m.summaries, nil
}

func (m *mockConversationStore) Query(options QueryOptions) ([]ConversationSummary, error) {
	if m.queryFunc != nil {
		return m.queryFunc(options)
	}
	return m.summaries, nil
}

func (m *mockConversationStore) Delete(id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(id)
	}
	delete(m.conversations, id)
	return nil
}

func (m *mockConversationStore) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestConversationService_ListConversations(t *testing.T) {
	tests := []struct {
		name           string
		request        *ListConversationsRequest
		storeSummaries []ConversationSummary
		storeError     error
		expectedError  bool
		expectedCount  int
	}{
		{
			name:    "successful list with defaults",
			request: &ListConversationsRequest{},
			storeSummaries: []ConversationSummary{
				{ID: "1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				{ID: "2", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			},
			expectedCount: 2,
		},
		{
			name: "list with search term",
			request: &ListConversationsRequest{
				SearchTerm: "test",
				Limit:      10,
			},
			storeSummaries: []ConversationSummary{
				{ID: "1", Summary: "test conversation"},
			},
			expectedCount: 1,
		},
		{
			name:           "empty list",
			request:        &ListConversationsRequest{},
			storeSummaries: []ConversationSummary{},
			expectedCount:  0,
		},
		{
			name:          "store error",
			request:       &ListConversationsRequest{},
			storeError:    assert.AnError,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := newMockConversationStore()
			mockStore.summaries = tt.storeSummaries
			if tt.storeError != nil {
				mockStore.queryFunc = func(options QueryOptions) ([]ConversationSummary, error) {
					return nil, tt.storeError
				}
			}

			service := NewConversationService(mockStore)
			ctx := context.Background()

			response, err := service.ListConversations(ctx, tt.request)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, response)
			assert.Equal(t, tt.expectedCount, len(response.Conversations))
			assert.Equal(t, tt.expectedCount, response.Total)
		})
	}
}

func TestConversationService_GetConversation(t *testing.T) {
	now := time.Now()
	testRecord := &ConversationRecord{
		ID:          "test-id",
		CreatedAt:   now,
		UpdatedAt:   now,
		ModelType:   "anthropic",
		Summary:     "Test conversation",
		Usage:       llm.Usage{InputTokens: 50, OutputTokens: 50},
		RawMessages: []byte(`[{"role":"user","content":"hello"}]`),
		ToolResults: map[string]tools.StructuredToolResult{
			"tool1": {ToolName: "test-tool"},
		},
	}

	tests := []struct {
		name          string
		id            string
		storeRecord   *ConversationRecord
		storeError    error
		expectedError bool
	}{
		{
			name:        "successful get",
			id:          "test-id",
			storeRecord: testRecord,
		},
		{
			name:          "conversation not found",
			id:            "missing-id",
			storeError:    errors.New("conversation not found"),
			expectedError: true,
		},
		{
			name:          "store error",
			id:            "test-id",
			storeError:    assert.AnError,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := newMockConversationStore()
			if tt.storeRecord != nil {
				mockStore.conversations[tt.storeRecord.ID] = tt.storeRecord
			}
			if tt.storeError != nil {
				mockStore.loadFunc = func(id string) (*ConversationRecord, error) {
					return nil, tt.storeError
				}
			}

			service := NewConversationService(mockStore)
			ctx := context.Background()

			response, err := service.GetConversation(ctx, tt.id)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, response)
			assert.Equal(t, testRecord.ID, response.ID)
			assert.Equal(t, testRecord.Summary, response.Summary)
			assert.Equal(t, 1, response.MessageCount)
		})
	}
}

func TestConversationService_DeleteConversation(t *testing.T) {
	tests := []struct {
		name          string
		id            string
		storeError    error
		expectedError bool
	}{
		{
			name: "successful delete",
			id:   "test-id",
		},
		{
			name:          "delete error",
			id:            "test-id",
			storeError:    assert.AnError,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := newMockConversationStore()
			if tt.storeError != nil {
				mockStore.deleteFunc = func(id string) error {
					return tt.storeError
				}
			}

			service := NewConversationService(mockStore)
			ctx := context.Background()

			err := service.DeleteConversation(ctx, tt.id)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
		})
	}
}

func TestConversationService_ResolveConversationID(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		storeSummaries []ConversationSummary
		expectedID     string
		expectedError  bool
		errorContains  string
	}{
		{
			name:       "full UUID already",
			id:         "12345678-1234-1234-1234-123456789012",
			expectedID: "12345678-1234-1234-1234-123456789012",
		},
		{
			name: "short ID with single match",
			id:   "abc",
			storeSummaries: []ConversationSummary{
				{ID: "abcdef12-1234-1234-1234-123456789012"},
				{ID: "defabc12-1234-1234-1234-123456789012"},
			},
			expectedID: "abcdef12-1234-1234-1234-123456789012",
		},
		{
			name: "short ID with no matches",
			id:   "xyz",
			storeSummaries: []ConversationSummary{
				{ID: "abcdef12-1234-1234-1234-123456789012"},
			},
			expectedError: true,
			errorContains: "no conversation found",
		},
		{
			name: "short ID with multiple matches",
			id:   "abc",
			storeSummaries: []ConversationSummary{
				{ID: "abcdef12-1234-1234-1234-123456789012"},
				{ID: "abc12345-1234-1234-1234-123456789012"},
			},
			expectedError: true,
			errorContains: "multiple conversations found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := newMockConversationStore()
			mockStore.summaries = tt.storeSummaries

			service := NewConversationService(mockStore)
			ctx := context.Background()

			resolvedID, err := service.ResolveConversationID(ctx, tt.id)

			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedID, resolvedID)
		})
	}
}

func TestConversationService_GetConversationStatistics(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-24 * time.Hour)
	later := now.Add(24 * time.Hour)

	tests := []struct {
		name           string
		storeSummaries []ConversationSummary
		storeError     error
		expectedStats  *ConversationStatistics
		expectedError  bool
	}{
		{
			name: "statistics with conversations",
			storeSummaries: []ConversationSummary{
				{ID: "1", CreatedAt: earlier, UpdatedAt: now, MessageCount: 5},
				{ID: "2", CreatedAt: now, UpdatedAt: later, MessageCount: 3},
			},
			expectedStats: &ConversationStatistics{
				TotalConversations: 2,
				TotalMessages:      4, // 2 messages per conversation
			},
		},
		{
			name:           "empty statistics",
			storeSummaries: []ConversationSummary{},
			expectedStats: &ConversationStatistics{
				TotalConversations: 0,
				TotalMessages:      0,
			},
		},
		{
			name:          "store error",
			storeError:    assert.AnError,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := newMockConversationStore()
			mockStore.summaries = tt.storeSummaries

			// Set up conversation records for the summaries
			for _, summary := range tt.storeSummaries {
				record := &ConversationRecord{
					ID:          summary.ID,
					CreatedAt:   summary.CreatedAt,
					UpdatedAt:   summary.UpdatedAt,
					ModelType:   "anthropic",
					Summary:     "Test conversation",
					Usage:       llm.Usage{InputTokens: 50, OutputTokens: 50},
					RawMessages: []byte(`[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]`),
					ToolResults: map[string]tools.StructuredToolResult{},
				}
				mockStore.conversations[summary.ID] = record
			}

			if tt.storeError != nil {
				mockStore.listFunc = func() ([]ConversationSummary, error) {
					return nil, tt.storeError
				}
			}

			service := NewConversationService(mockStore)
			ctx := context.Background()

			stats, err := service.GetConversationStatistics(ctx)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, stats)
			assert.Equal(t, tt.expectedStats.TotalConversations, stats.TotalConversations)
			assert.Equal(t, tt.expectedStats.TotalMessages, stats.TotalMessages)
		})
	}
}

func TestConversationService_Close(t *testing.T) {
	t.Run("successful close", func(t *testing.T) {
		mockStore := newMockConversationStore()
		closeCalled := false
		mockStore.closeFunc = func() error {
			closeCalled = true
			return nil
		}

		service := NewConversationService(mockStore)
		err := service.Close()

		assert.NoError(t, err)
		assert.True(t, closeCalled)
	})

	t.Run("close with error", func(t *testing.T) {
		mockStore := newMockConversationStore()
		mockStore.closeFunc = func() error {
			return assert.AnError
		}

		service := NewConversationService(mockStore)
		err := service.Close()

		assert.Error(t, err)
	})
}
