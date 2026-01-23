package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
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

// Save saves a conversation record to the mock store
//
//nolint:unparam // error is always nil in mock implementation
func (m *mockStore) Save(_ context.Context, record convtypes.ConversationRecord) error {
	m.conversations[record.ID] = record
	return nil
}

// Load retrieves a conversation record from the mock store by ID
func (m *mockStore) Load(_ context.Context, id string) (convtypes.ConversationRecord, error) {
	record, exists := m.conversations[id]
	if !exists {
		return convtypes.ConversationRecord{}, errors.New("conversation not found")
	}
	return record, nil
}

// Delete removes a conversation record from the mock store by ID
func (m *mockStore) Delete(_ context.Context, id string) error {
	delete(m.conversations, id)
	return nil
}

// Query returns all conversation summaries from the mock store
func (m *mockStore) Query(_ context.Context, options convtypes.QueryOptions) (convtypes.QueryResult, error) {
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

// Close closes the mock store (no-op for in-memory implementation)
func (m *mockStore) Close() error {
	return nil
}

// TestConversationFork tests the conversation fork functionality
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

func TestConversationShowConfigDefaults(t *testing.T) {
	config := NewConversationShowConfig()

	assert.Equal(t, "text", config.Format)
	assert.False(t, config.NoHeader)
	assert.False(t, config.StatsOnly)
}

func TestDisplayConversationHeader(t *testing.T) {
	record := convtypes.ConversationRecord{
		ID:        "test-conv-123",
		Provider:  "anthropic",
		Summary:   "Test conversation about Go testing",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:              1000,
			OutputTokens:             500,
			CacheReadInputTokens:     200,
			CacheCreationInputTokens: 100,
			InputCost:                0.01,
			OutputCost:               0.005,
			CacheReadCost:            0.001,
			CacheCreationCost:        0.002,
			CurrentContextWindow:     5000,
			MaxContextWindow:         200000,
		},
	}

	output := captureStdout(t, func() {
		displayConversationHeader(record)
	})

	assert.Contains(t, output, "test-conv-123")
	assert.Contains(t, output, "anthropic")
	assert.Contains(t, output, "Test conversation about Go testing")
	assert.Contains(t, output, "2026-01-23T10:00:00Z")
	assert.Contains(t, output, "2026-01-23T10:30:00Z")
	assert.Contains(t, output, "1000")
	assert.Contains(t, output, "500")
	assert.Contains(t, output, "200")
	assert.Contains(t, output, "100")
	assert.Contains(t, output, "$0.0180")
	assert.Contains(t, output, "5000 / 200000")
}

func TestDisplayConversationHeaderWithoutCache(t *testing.T) {
	record := convtypes.ConversationRecord{
		ID:        "test-conv-456",
		Provider:  "openai",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:  500,
			OutputTokens: 250,
			InputCost:    0.005,
			OutputCost:   0.0025,
		},
	}

	output := captureStdout(t, func() {
		displayConversationHeader(record)
	})

	assert.Contains(t, output, "test-conv-456")
	assert.Contains(t, output, "openai")
	assert.Contains(t, output, "500")
	assert.Contains(t, output, "250")
	assert.NotContains(t, output, "Cache Read")
	assert.NotContains(t, output, "Cache Creation")
	assert.NotContains(t, output, "Context Window")
}

func TestDisplayConversationHeaderWithoutSummary(t *testing.T) {
	record := convtypes.ConversationRecord{
		ID:        "test-conv-789",
		Provider:  "google",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	output := captureStdout(t, func() {
		displayConversationHeader(record)
	})

	assert.Contains(t, output, "test-conv-789")
	assert.Contains(t, output, "google")
	assert.NotContains(t, output, "Summary:")
}

func TestConversationShowOutputJSON(t *testing.T) {
	output := ConversationShowOutput{
		ID:        "test-conv-json",
		Provider:  "anthropic",
		Summary:   "Test summary",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:          1000,
			OutputTokens:         500,
			InputCost:            0.01,
			OutputCost:           0.005,
			CurrentContextWindow: 5000,
			MaxContextWindow:     200000,
		},
		Messages: []llmtypes.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	require.NoError(t, err)

	jsonStr := string(jsonBytes)
	assert.Contains(t, jsonStr, `"id": "test-conv-json"`)
	assert.Contains(t, jsonStr, `"provider": "anthropic"`)
	assert.Contains(t, jsonStr, `"summary": "Test summary"`)
	assert.Contains(t, jsonStr, `"inputTokens": 1000`)
	assert.Contains(t, jsonStr, `"outputTokens": 500`)
	assert.Contains(t, jsonStr, `"messages"`)
	assert.Contains(t, jsonStr, `"role": "user"`)
	assert.Contains(t, jsonStr, `"content": "Hello"`)
}

func TestConversationShowOutputJSONStatsOnly(t *testing.T) {
	output := ConversationShowOutput{
		ID:        "test-conv-stats",
		Provider:  "openai",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:  500,
			OutputTokens: 250,
		},
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	require.NoError(t, err)

	jsonStr := string(jsonBytes)
	assert.Contains(t, jsonStr, `"id": "test-conv-stats"`)
	assert.Contains(t, jsonStr, `"provider": "openai"`)
	assert.NotContains(t, jsonStr, `"messages"`)
	assert.NotContains(t, jsonStr, `"summary"`)
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	f()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)

	return buf.String()
}
