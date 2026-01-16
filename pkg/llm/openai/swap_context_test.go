package openai

import (
	"context"
	"testing"
	"time"

	openaisdk "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// swapContextMockState implements tooltypes.State for testing SwapContext
type swapContextMockState struct {
	fileLastAccess map[string]time.Time
}

func newSwapContextMockState() *swapContextMockState {
	return &swapContextMockState{
		fileLastAccess: make(map[string]time.Time),
	}
}

func (m *swapContextMockState) SetFileLastAccessed(path string, t time.Time) error {
	m.fileLastAccess[path] = t
	return nil
}

func (m *swapContextMockState) GetFileLastAccessed(path string) (time.Time, error) {
	return m.fileLastAccess[path], nil
}
func (m *swapContextMockState) ClearFileLastAccessed(_ string) error       { return nil }
func (m *swapContextMockState) TodoFilePath() (string, error)              { return "", nil }
func (m *swapContextMockState) SetTodoFilePath(_ string)                   {}
func (m *swapContextMockState) SetFileLastAccess(fla map[string]time.Time) { m.fileLastAccess = fla }
func (m *swapContextMockState) FileLastAccess() map[string]time.Time       { return m.fileLastAccess }
func (m *swapContextMockState) BasicTools() []tooltypes.Tool               { return nil }
func (m *swapContextMockState) MCPTools() []tooltypes.Tool                 { return nil }
func (m *swapContextMockState) Tools() []tooltypes.Tool                    { return nil }
func (m *swapContextMockState) AddBackgroundProcess(_ tooltypes.BackgroundProcess) error {
	return nil
}
func (m *swapContextMockState) GetBackgroundProcesses() []tooltypes.BackgroundProcess { return nil }
func (m *swapContextMockState) RemoveBackgroundProcess(_ int) error                   { return nil }
func (m *swapContextMockState) DiscoverContexts() map[string]string                   { return nil }
func (m *swapContextMockState) GetLLMConfig() interface{}                             { return nil }
func (m *swapContextMockState) LockFile(_ string)                                     {}
func (m *swapContextMockState) UnlockFile(_ string)                                   {}

func createTestThread() *Thread {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
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
	thread.messages = []openaisdk.ChatCompletionMessage{
		{
			Role:    openaisdk.ChatMessageRoleUser,
			Content: "first message",
		},
		{
			Role:    openaisdk.ChatMessageRoleAssistant,
			Content: "assistant response",
		},
		{
			Role:    openaisdk.ChatMessageRoleUser,
			Content: "second message",
		},
	}

	summary := "This is a conversation summary"
	err := thread.SwapContext(context.Background(), summary)
	require.NoError(t, err)

	// Should have exactly one message - the summary
	assert.Len(t, thread.messages, 1)
	assert.Equal(t, openaisdk.ChatMessageRoleUser, thread.messages[0].Role)
	assert.Equal(t, summary, thread.messages[0].Content)
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

	state := newSwapContextMockState()
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
	assert.Equal(t, "", thread.messages[0].Content)
}

func TestSwapContext_FromEmptyMessages(t *testing.T) {
	thread := createTestThread()
	thread.messages = []openaisdk.ChatCompletionMessage{}

	err := thread.SwapContext(context.Background(), "summary from empty")
	require.NoError(t, err)

	assert.Len(t, thread.messages, 1)
}
