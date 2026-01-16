package google

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// mockState implements tooltypes.State for testing
type mockState struct {
	fileLastAccess map[string]time.Time
}

func newMockState() *mockState {
	return &mockState{
		fileLastAccess: make(map[string]time.Time),
	}
}

func (m *mockState) SetFileLastAccessed(path string, t time.Time) error {
	m.fileLastAccess[path] = t
	return nil
}

func (m *mockState) GetFileLastAccessed(path string) (time.Time, error) {
	return m.fileLastAccess[path], nil
}
func (m *mockState) ClearFileLastAccessed(_ string) error       { return nil }
func (m *mockState) TodoFilePath() (string, error)              { return "", nil }
func (m *mockState) SetTodoFilePath(_ string)                   {}
func (m *mockState) SetFileLastAccess(fla map[string]time.Time) { m.fileLastAccess = fla }
func (m *mockState) FileLastAccess() map[string]time.Time       { return m.fileLastAccess }
func (m *mockState) BasicTools() []tooltypes.Tool               { return nil }
func (m *mockState) MCPTools() []tooltypes.Tool                 { return nil }
func (m *mockState) Tools() []tooltypes.Tool                    { return nil }
func (m *mockState) AddBackgroundProcess(_ tooltypes.BackgroundProcess) error {
	return nil
}
func (m *mockState) GetBackgroundProcesses() []tooltypes.BackgroundProcess { return nil }
func (m *mockState) RemoveBackgroundProcess(_ int) error                   { return nil }
func (m *mockState) DiscoverContexts() map[string]string                   { return nil }
func (m *mockState) GetLLMConfig() interface{}                             { return nil }
func (m *mockState) LockFile(_ string)                                     {}
func (m *mockState) UnlockFile(_ string)                                   {}

func createTestThread() *Thread {
	config := llmtypes.Config{
		Model:     "gemini-2.5-pro",
		MaxTokens: 8192,
	}

	baseThread := base.NewThread(config, "test-conv-id", nil, hooks.Trigger{})

	thread := &Thread{
		Thread: baseThread,
	}

	return thread
}

func TestSwapContext_ReplacesMessages(t *testing.T) {
	thread := createTestThread()

	// Add some initial messages
	thread.messages = []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText("first message"),
		}, genai.RoleUser),
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText("assistant response"),
		}, genai.RoleModel),
		genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromText("second message"),
		}, genai.RoleUser),
	}

	summary := "This is a conversation summary"
	err := thread.SwapContext(context.Background(), summary)
	require.NoError(t, err)

	// Should have exactly one message - the summary
	assert.Len(t, thread.messages, 1)
	assert.Equal(t, genai.RoleUser, thread.messages[0].Role)

	// Verify the message content contains the summary
	require.Len(t, thread.messages[0].Parts, 1)
	assert.Equal(t, summary, thread.messages[0].Parts[0].Text)
}

func TestSwapContext_ClearsToolResults(t *testing.T) {
	thread := createTestThread()

	// Add some tool results
	thread.ToolResults = map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "bash", Success: true},
		"tool-2": {ToolName: "file_read", Success: false},
	}

	err := thread.SwapContext(context.Background(), "summary")
	require.NoError(t, err)

	// Tool results should be cleared
	assert.Len(t, thread.ToolResults, 0)
}

func TestSwapContext_UpdatesContextWindowEstimate(t *testing.T) {
	thread := createTestThread()
	thread.Usage.CurrentContextWindow = 100000

	// A long summary should result in estimated tokens (must exceed 400 chars for >100 tokens)
	longSummary := "This is a much longer summary that should have a meaningful token count. " +
		"It contains multiple sentences and should give us an estimate well above the minimum. " +
		"We need at least 400 characters to exceed the 100 token minimum threshold. " +
		"Adding more content here to ensure we get a realistic token estimate for testing. " +
		"This should now be enough content to properly test the token estimation logic. " +
		"Here is some additional text to make sure we are well over the 400 character mark."

	err := thread.SwapContext(context.Background(), longSummary)
	require.NoError(t, err)

	// Context window should be estimated based on summary length
	expectedTokens := len(longSummary) / 4
	assert.Greater(t, expectedTokens, 100, "Summary should produce more than 100 tokens")
	assert.Equal(t, expectedTokens, thread.Usage.CurrentContextWindow)
}

func TestSwapContext_ClearsFileAccessTracking(t *testing.T) {
	thread := createTestThread()

	state := newMockState()
	state.fileLastAccess["file1.go"] = time.Now()
	state.fileLastAccess["file2.go"] = time.Now()
	thread.State = state

	err := thread.SwapContext(context.Background(), "summary")
	require.NoError(t, err)

	// File access tracking should be cleared
	assert.Len(t, state.fileLastAccess, 0)
}

func TestSwapContext_HandlesNilState(t *testing.T) {
	thread := createTestThread()
	thread.State = nil

	// Should not panic with nil state
	err := thread.SwapContext(context.Background(), "summary")
	require.NoError(t, err)
}

func TestSwapContext_EmptySummary(t *testing.T) {
	thread := createTestThread()

	err := thread.SwapContext(context.Background(), "")
	require.NoError(t, err)

	assert.Len(t, thread.messages, 1)
	assert.Equal(t, "", thread.messages[0].Parts[0].Text)
}

func TestSwapContext_FromEmptyMessages(t *testing.T) {
	thread := createTestThread()
	thread.messages = []*genai.Content{}

	err := thread.SwapContext(context.Background(), "summary from empty")
	require.NoError(t, err)

	assert.Len(t, thread.messages, 1)
}
