package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/goals"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
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
	sourceRecord.CWD = "/tmp/test-project"
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
	sourceRecord.Metadata = map[string]any{
		"test_key":        "test_value",
		goals.MetadataKey: goals.New("finish the parent task", time.Now()),
	}

	// Save to store
	store := newMockStore()
	err := store.Save(ctx, sourceRecord)
	require.NoError(t, err)

	// Load the source conversation
	loadedSource, err := store.Load(ctx, "source-id")
	require.NoError(t, err)

	// Simulate fork operation
	forkedRecord := convtypes.ForkConversationRecord(loadedSource)

	// Save forked conversation
	err = store.Save(ctx, forkedRecord)
	require.NoError(t, err)

	// Verify forked conversation
	loadedForked, err := store.Load(ctx, forkedRecord.ID)
	require.NoError(t, err)

	// Assert that messages and context are copied
	assert.Equal(t, loadedSource.RawMessages, loadedForked.RawMessages)
	assert.Equal(t, loadedSource.CWD, loadedForked.CWD)
	assert.Equal(t, loadedSource.Provider, loadedForked.Provider)
	assert.Equal(t, loadedSource.Summary, loadedForked.Summary)
	assert.Equal(t, loadedSource.ToolResults, loadedForked.ToolResults)

	// Assert that ordinary metadata is copied while the parent goal is not inherited.
	assert.Equal(t, loadedSource.Metadata["test_key"], loadedForked.Metadata["test_key"])
	assert.NotContains(t, loadedForked.Metadata, goals.MetadataKey)

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
	assert.False(t, config.TruncateToolResults)
}

func TestConversationConfigDefaults(t *testing.T) {
	listConfig := NewConversationListConfig()
	assert.Equal(t, 10, listConfig.Limit)
	assert.Equal(t, "updated_at", listConfig.SortBy)
	assert.Equal(t, "desc", listConfig.SortOrder)
	assert.False(t, listConfig.JSONOutput)

	assert.False(t, NewConversationDeleteConfig().NoConfirm)
	assert.False(t, NewConversationImportConfig().Force)
	assert.False(t, NewConversationExportConfig().UseGist)
	assert.False(t, NewConversationExportConfig().UsePublicGist)
	assert.Equal(t, "", NewConversationEditConfig().Editor)
	assert.Equal(t, "", NewConversationEditConfig().EditArgs)
}

func TestConversationConfigFromFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("start", "", "")
	cmd.Flags().String("end", "", "")
	cmd.Flags().String("search", "", "")
	cmd.Flags().String("provider", "", "")
	cmd.Flags().Int("limit", 10, "")
	cmd.Flags().Int("offset", 0, "")
	cmd.Flags().String("sort-by", "updated_at", "")
	cmd.Flags().String("sort-order", "desc", "")
	cmd.Flags().Bool("json", false, "")
	require.NoError(t, cmd.Flags().Set("start", "2026-01-01"))
	require.NoError(t, cmd.Flags().Set("end", "2026-01-31"))
	require.NoError(t, cmd.Flags().Set("search", "golang"))
	require.NoError(t, cmd.Flags().Set("provider", "openai"))
	require.NoError(t, cmd.Flags().Set("limit", "20"))
	require.NoError(t, cmd.Flags().Set("offset", "5"))
	require.NoError(t, cmd.Flags().Set("sort-by", "created_at"))
	require.NoError(t, cmd.Flags().Set("sort-order", "asc"))
	require.NoError(t, cmd.Flags().Set("json", "true"))
	listConfig := getConversationListConfigFromFlags(cmd)
	assert.Equal(t, "2026-01-01", listConfig.StartDate)
	assert.Equal(t, "2026-01-31", listConfig.EndDate)
	assert.Equal(t, "golang", listConfig.Search)
	assert.Equal(t, "openai", listConfig.Provider)
	assert.Equal(t, 20, listConfig.Limit)
	assert.Equal(t, 5, listConfig.Offset)
	assert.Equal(t, "created_at", listConfig.SortBy)
	assert.Equal(t, "asc", listConfig.SortOrder)
	assert.True(t, listConfig.JSONOutput)

	deleteCmd := &cobra.Command{}
	deleteCmd.Flags().Bool("no-confirm", false, "")
	require.NoError(t, deleteCmd.Flags().Set("no-confirm", "true"))
	assert.True(t, getConversationDeleteConfigFromFlags(deleteCmd).NoConfirm)

	showCmd := &cobra.Command{}
	showCmd.Flags().String("format", "text", "")
	showCmd.Flags().Bool("no-header", false, "")
	showCmd.Flags().Bool("stats-only", false, "")
	showCmd.Flags().Bool("truncate-tool-results", false, "")
	require.NoError(t, showCmd.Flags().Set("format", "markdown"))
	require.NoError(t, showCmd.Flags().Set("no-header", "true"))
	require.NoError(t, showCmd.Flags().Set("stats-only", "true"))
	require.NoError(t, showCmd.Flags().Set("truncate-tool-results", "true"))
	showConfig := getConversationShowConfigFromFlags(showCmd)
	assert.Equal(t, "markdown", showConfig.Format)
	assert.True(t, showConfig.NoHeader)
	assert.True(t, showConfig.StatsOnly)
	assert.True(t, showConfig.TruncateToolResults)

	importCmd := &cobra.Command{}
	importCmd.Flags().Bool("force", false, "")
	require.NoError(t, importCmd.Flags().Set("force", "true"))
	assert.True(t, getConversationImportConfigFromFlags(importCmd).Force)

	exportCmd := &cobra.Command{}
	exportCmd.Flags().Bool("gist", false, "")
	exportCmd.Flags().Bool("public-gist", false, "")
	require.NoError(t, exportCmd.Flags().Set("gist", "true"))
	require.NoError(t, exportCmd.Flags().Set("public-gist", "true"))
	exportConfig := getConversationExportConfigFromFlags(exportCmd)
	assert.True(t, exportConfig.UseGist)
	assert.True(t, exportConfig.UsePublicGist)

	editCmd := &cobra.Command{}
	editCmd.Flags().String("editor", "", "")
	editCmd.Flags().String("edit-args", "", "")
	require.NoError(t, editCmd.Flags().Set("editor", "code"))
	require.NoError(t, editCmd.Flags().Set("edit-args", "--wait"))
	editConfig := getConversationEditConfigFromFlags(editCmd)
	assert.Equal(t, "code", editConfig.Editor)
	assert.Equal(t, "--wait", editConfig.EditArgs)
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
		displayConversationHeader(os.Stdout, record, record.Provider, "", "")
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
		displayConversationHeader(os.Stdout, record, record.Provider, "", "")
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
		Provider:  "openai",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	output := captureStdout(t, func() {
		displayConversationHeader(os.Stdout, record, record.Provider, "", "")
	})

	assert.Contains(t, output, "test-conv-789")
	assert.Contains(t, output, "openai")
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

func TestConversationListOutputRenderTableAndJSON(t *testing.T) {
	now := time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC)
	summaries := []convtypes.ConversationSummary{
		{
			ID:           "conv-1",
			CreatedAt:    now,
			UpdatedAt:    now.Add(time.Hour),
			MessageCount: 3,
			Provider:     "openai-responses",
			Summary:      "A summary\nwith newline and a long suffix that should be truncated in table output because it is too wide",
			Usage:        llmtypes.Usage{InputCost: 0.01, OutputCost: 0.02, CurrentContextWindow: 1200, MaxContextWindow: 4000},
		},
		{
			ID:           "conv-2",
			CreatedAt:    now,
			UpdatedAt:    now,
			MessageCount: 1,
			Provider:     "custom-provider",
			FirstMessage: "first message",
		},
	}
	metadata := map[string]map[string]any{
		"conv-1": {"platform": "Codex", "api_mode": "chat"},
	}

	output := NewConversationListOutput(summaries, metadata, TableFormat)
	require.Len(t, output.Conversations, 2)
	assert.Equal(t, "OpenAI", output.Conversations[0].Provider)
	assert.Equal(t, "codex", output.Conversations[0].Platform)
	assert.Equal(t, "chat_completions", output.Conversations[0].APIMode)
	assert.NotContains(t, output.Conversations[0].Preview, "\n")

	var table bytes.Buffer
	require.NoError(t, output.Render(&table))
	assert.Contains(t, table.String(), "conv-1")
	assert.Contains(t, table.String(), "$0.0300")
	assert.Contains(t, table.String(), "1200/4000")
	assert.Contains(t, table.String(), "...")

	output.Format = JSONFormat
	var jsonBuf bytes.Buffer
	require.NoError(t, output.Render(&jsonBuf))
	var parsed struct {
		Conversations []ConversationSummaryOutput `json:"conversations"`
	}
	require.NoError(t, json.Unmarshal(jsonBuf.Bytes(), &parsed))
	require.Len(t, parsed.Conversations, 2)
	assert.Equal(t, "conv-2", parsed.Conversations[1].ID)
}

func TestDisplayConversation(t *testing.T) {
	output := captureStdout(t, func() {
		displayConversation(os.Stdout, []llmtypes.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "tool", Content: "result"},
			{Role: "", Content: "blank role"},
		})
	})

	assert.Contains(t, output, "hello")
	assert.Contains(t, output, "hi")
	assert.Contains(t, output, "result")
	assert.Contains(t, output, "blank role")
}

func TestRenderConversationHeaderMarkdown(t *testing.T) {
	record := convtypes.ConversationRecord{
		ID:        "conv-md",
		Summary:   "Summary line",
		CreatedAt: time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 23, 10, 30, 0, 0, time.UTC),
		Usage: llmtypes.Usage{
			InputTokens:          123,
			OutputTokens:         45,
			InputCost:            0.001,
			OutputCost:           0.002,
			CurrentContextWindow: 1000,
			MaxContextWindow:     8000,
		},
	}

	output := renderConversationHeaderMarkdown(record, "OpenAI", "fireworks", "responses")
	assert.Contains(t, output, "# Conversation")
	assert.Contains(t, output, "- **ID:** `conv-md`")
	assert.Contains(t, output, "- **Provider:** OpenAI")
	assert.Contains(t, output, "- **Platform:** `fireworks`")
	assert.Contains(t, output, "- **API Mode:** `responses`")
	assert.Contains(t, output, "## Usage")
	assert.Contains(t, output, "- **Total Cost:** $0.0030")
	assert.Contains(t, output, "- **Context Window:** 1000 / 8000")
}

func TestDisplayProviderName(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		expected string
	}{
		{
			name:     "openai provider",
			provider: "openai",
			expected: "OpenAI",
		},
		{
			name:     "openai responses legacy provider",
			provider: "openai-responses",
			expected: "OpenAI",
		},
		{
			name:     "anthropic provider",
			provider: "anthropic",
			expected: "Anthropic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, displayProviderName(tt.provider))
		})
	}
}

func TestExtractProviderMetadata(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		metadata         map[string]any
		expectedPlatform string
		expectedAPIMode  string
	}{
		{
			name:     "openai metadata",
			provider: "openai",
			metadata: map[string]any{
				"platform": "Fireworks",
				"api_mode": "responses",
			},
			expectedPlatform: "fireworks",
			expectedAPIMode:  "responses",
		},
		{
			name:             "legacy openai responses provider without metadata",
			provider:         "openai-responses",
			metadata:         map[string]any{},
			expectedPlatform: "",
			expectedAPIMode:  "responses",
		},
		{
			name:             "non-openai provider",
			provider:         "anthropic",
			metadata:         map[string]any{"platform": "x", "api_mode": "chat"},
			expectedPlatform: "x",
			expectedAPIMode:  "chat_completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, apiMode := extractProviderMetadata(tt.provider, tt.metadata)
			assert.Equal(t, tt.expectedPlatform, platform)
			assert.Equal(t, tt.expectedAPIMode, apiMode)
		})
	}
}

func TestMarkdownHelpers(t *testing.T) {
	assert.Equal(t, "`plain`", inlineMarkdownCode("plain"))
	assert.Equal(t, "``has`tick``", inlineMarkdownCode("has`tick"))
	assert.Equal(t, "hello world", sanitizeMarkdownText("hello\nworld"))
}

func TestCreateGistInvokesGHWithExpectedVisibility(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh script")
	}

	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	argsPath := filepath.Join(tmpDir, "args.txt")
	ghPath := filepath.Join(binDir, "gh")
	script := "#!/bin/sh\n" +
		"printf '%s\n' \"$@\" > \"$GH_ARGS_FILE\"\n" +
		"echo https://gist.github.com/test/gist-id\n"
	require.NoError(t, os.WriteFile(ghPath, []byte(script), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_ARGS_FILE", argsPath)

	publicOutput := captureAllStdout(t, func() {
		require.NoError(t, createGist("conv-gist", []byte(`{"id":"conv-gist"}`), false))
	})
	publicArgs := readGistArgs(t, argsPath)
	assert.Contains(t, publicOutput, "https://gist.github.com/test/gist-id")
	assert.Contains(t, publicOutput, "public gist")
	assert.Contains(t, publicArgs, "--public")
	assert.Contains(t, publicArgs, "--filename")
	assert.Contains(t, publicArgs, "conversation_conv-gist.json")

	privateOutput := captureAllStdout(t, func() {
		require.NoError(t, createGist("conv-gist", []byte(`{"id":"conv-gist"}`), true))
	})
	privateArgs := readGistArgs(t, argsPath)
	assert.Contains(t, privateOutput, "private gist")
	assert.NotContains(t, privateArgs, "--public")
	assert.Contains(t, privateArgs, "conversation_conv-gist.json")
}

func readGistArgs(t *testing.T, argsPath string) string {
	t.Helper()

	data, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
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
