package conversations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConversationStore implements ConversationStore for testing
type mockConversationStore struct {
	conversations map[string]*conversations.ConversationRecord
	summaries     []conversations.ConversationSummary
	queryFunc     func(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error)
	loadFunc      func(ctx context.Context, id string) (*conversations.ConversationRecord, error)
	deleteFunc    func(ctx context.Context, id string) error
	listFunc      func(ctx context.Context) ([]conversations.ConversationSummary, error)
	closeFunc     func() error
}

func newMockConversationStore() *mockConversationStore {
	return &mockConversationStore{
		conversations: make(map[string]*conversations.ConversationRecord),
		summaries:     []conversations.ConversationSummary{},
	}
}

func (m *mockConversationStore) Save(_ context.Context, record conversations.ConversationRecord) error {
	m.conversations[record.ID] = &record
	return nil
}

func (m *mockConversationStore) Load(ctx context.Context, id string) (conversations.ConversationRecord, error) {
	if m.loadFunc != nil {
		rec, err := m.loadFunc(ctx, id)
		if err != nil {
			return conversations.ConversationRecord{}, err
		}
		if rec == nil {
			return conversations.ConversationRecord{}, errors.New("conversation not found")
		}
		return *rec, nil
	}
	record, exists := m.conversations[id]
	if !exists {
		return conversations.ConversationRecord{}, errors.New("conversation not found")
	}
	return *record, nil
}

func (m *mockConversationStore) List(ctx context.Context) ([]conversations.ConversationSummary, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx)
	}
	return m.summaries, nil
}

func (m *mockConversationStore) Query(ctx context.Context, options conversations.QueryOptions) (conversations.QueryResult, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, options)
	}
	return conversations.QueryResult{
		ConversationSummaries: m.summaries,
		Total:                 len(m.summaries),
		QueryOptions:          options,
	}, nil
}

func (m *mockConversationStore) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
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
		storeSummaries []conversations.ConversationSummary
		storeError     error
		expectedError  bool
		expectedCount  int
	}{
		{
			name:    "successful list with defaults",
			request: &ListConversationsRequest{},
			storeSummaries: []conversations.ConversationSummary{
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
			storeSummaries: []conversations.ConversationSummary{
				{ID: "1", Summary: "test conversation"},
			},
			expectedCount: 1,
		},
		{
			name:           "empty list",
			request:        &ListConversationsRequest{},
			storeSummaries: []conversations.ConversationSummary{},
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
				mockStore.queryFunc = func(_ context.Context, _ conversations.QueryOptions) (conversations.QueryResult, error) {
					return conversations.QueryResult{}, tt.storeError
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
	testRecord := &conversations.ConversationRecord{
		ID:          "test-id",
		CreatedAt:   now,
		UpdatedAt:   now,
		Provider:    "anthropic",
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
		storeRecord   *conversations.ConversationRecord
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
				mockStore.loadFunc = func(_ context.Context, _ string) (*conversations.ConversationRecord, error) {
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
				mockStore.deleteFunc = func(_ context.Context, _ string) error {
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
