package main

import (
	"context"
	"encoding/json"
	"maps"
	"testing"
	"time"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore is a simple in-memory store for testing
type mockStore struct {
	conversations map[string]convtypes.ConversationRecord
}

func newMockStore() *mockStore {
	return &mockStore{
		conversations: make(map[string]convtypes.ConversationRecord),
	}
}

//nolint:unparam // error is always nil in mock implementation
func (m *mockStore) Save(_ context.Context, record convtypes.ConversationRecord) error {
	m.conversations[record.ID] = record
	return nil
}

func (m *mockStore) Load(_ context.Context, id string) (convtypes.ConversationRecord, error) {
	record, exists := m.conversations[id]
	if !exists {
		return convtypes.ConversationRecord{}, errors.New("conversation not found")
	}
	return record, nil
}

func (m *mockStore) Delete(ctx context.Context, id string) error {
	delete(m.conversations, id)
	return nil
}

func (m *mockStore) Query(ctx context.Context, options convtypes.QueryOptions) (convtypes.QueryResult, error) {
	summaries := []convtypes.ConversationSummary{}
	for _, record := range m.conversations {
		summaries = append(summaries, record.ToSummary())
	}
	return convtypes.QueryResult{
		ConversationSummaries: summaries,
		Total:                 len(summaries),
		QueryOptions:          options,
	}, nil
}

func (m *mockStore) Close() error {
	return nil
}

func TestConversationFork(t *testing.T) {
	ctx := context.Background()

	// Create a source conversation with non-zero usage
	sourceRecord := convtypes.NewConversationRecord("source-id")
	sourceRecord.Provider = "anthropic"
	sourceRecord.Summary = "Test conversation"
	sourceRecord.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"Hello"}]}]`)
	sourceRecord.Usage = llmtypes.Usage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheCreationInputTokens: 20,
		CacheReadInputTokens:     10,
		InputCost:                0.001,
		OutputCost:               0.002,
		CacheCreationCost:        0.0005,
		CacheReadCost:            0.0001,
		CurrentContextWindow:     50000,
		MaxContextWindow:         200000,
	}
	sourceRecord.ToolResults = map[string]tools.StructuredToolResult{
		"tool-1": {
			ToolName:  "bash",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: tools.BashMetadata{
				Command:       "echo test",
				ExitCode:      0,
				Output:        "test output",
				ExecutionTime: 100 * time.Millisecond,
			},
		},
	}
	sourceRecord.FileLastAccess = map[string]time.Time{
		"test.go": time.Now(),
	}
	sourceRecord.Metadata = map[string]any{
		"test_key": "test_value",
	}

	// Save to store
	store := newMockStore()
	err := store.Save(ctx, sourceRecord)
	require.NoError(t, err)

	// Load the source conversation
	loadedSource, err := store.Load(ctx, "source-id")
	require.NoError(t, err)

	// Simulate fork operation
	forkedRecord := convtypes.NewConversationRecord("")
	forkedRecord.RawMessages = loadedSource.RawMessages
	forkedRecord.Provider = loadedSource.Provider
	forkedRecord.Summary = loadedSource.Summary
	forkedRecord.ToolResults = loadedSource.ToolResults
	forkedRecord.BackgroundProcesses = loadedSource.BackgroundProcesses

	if loadedSource.FileLastAccess != nil {
		forkedRecord.FileLastAccess = make(map[string]time.Time)
		maps.Copy(forkedRecord.FileLastAccess, loadedSource.FileLastAccess)
	}

	if loadedSource.Metadata != nil {
		forkedRecord.Metadata = make(map[string]any)
		maps.Copy(forkedRecord.Metadata, loadedSource.Metadata)
	}

	// Preserve context window information from source
	forkedRecord.Usage.CurrentContextWindow = loadedSource.Usage.CurrentContextWindow
	forkedRecord.Usage.MaxContextWindow = loadedSource.Usage.MaxContextWindow

	// Save forked conversation
	err = store.Save(ctx, forkedRecord)
	require.NoError(t, err)

	// Verify forked conversation
	loadedForked, err := store.Load(ctx, forkedRecord.ID)
	require.NoError(t, err)

	// Assert that messages and context are copied
	assert.Equal(t, loadedSource.RawMessages, loadedForked.RawMessages)
	assert.Equal(t, loadedSource.Provider, loadedForked.Provider)
	assert.Equal(t, loadedSource.Summary, loadedForked.Summary)
	assert.Equal(t, loadedSource.ToolResults, loadedForked.ToolResults)

	// Assert that FileLastAccess is copied (deep copy)
	assert.Equal(t, len(loadedSource.FileLastAccess), len(loadedForked.FileLastAccess))
	for k, v := range loadedSource.FileLastAccess {
		forkedV, exists := loadedForked.FileLastAccess[k]
		assert.True(t, exists)
		assert.Equal(t, v.Unix(), forkedV.Unix()) // Compare Unix timestamps
	}

	// Assert that Metadata is copied (deep copy)
	assert.Equal(t, loadedSource.Metadata, loadedForked.Metadata)

	// Assert that IDs are different
	assert.NotEqual(t, loadedSource.ID, loadedForked.ID)

	// Assert that usage statistics are reset to zero
	assert.Equal(t, 0, loadedForked.Usage.InputTokens)
	assert.Equal(t, 0, loadedForked.Usage.OutputTokens)
	assert.Equal(t, 0, loadedForked.Usage.CacheCreationInputTokens)
	assert.Equal(t, 0, loadedForked.Usage.CacheReadInputTokens)
	assert.Equal(t, 0.0, loadedForked.Usage.InputCost)
	assert.Equal(t, 0.0, loadedForked.Usage.OutputCost)
	assert.Equal(t, 0.0, loadedForked.Usage.CacheCreationCost)
	assert.Equal(t, 0.0, loadedForked.Usage.CacheReadCost)

	// Assert that context window information is preserved
	assert.Equal(t, loadedSource.Usage.CurrentContextWindow, loadedForked.Usage.CurrentContextWindow)
	assert.Equal(t, loadedSource.Usage.MaxContextWindow, loadedForked.Usage.MaxContextWindow)

	// Assert that timestamps are different (forked should be newer)
	assert.True(t, loadedForked.CreatedAt.After(loadedSource.CreatedAt) || loadedForked.CreatedAt.Equal(loadedSource.CreatedAt))
	assert.True(t, loadedForked.UpdatedAt.After(loadedSource.UpdatedAt) || loadedForked.UpdatedAt.Equal(loadedSource.UpdatedAt))
}
