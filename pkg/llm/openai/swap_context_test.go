package openai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	openaisdk "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestThread() *Thread {
	config := llmtypes.Config{
		Model:     "gpt-4.1",
		MaxTokens: 8192,
	}

	baseThread := base.NewThread(config, "test-conv-id")

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

	messages, history, usage := thread.messages, thread.GetCompactionHistory(), thread.GetUsage()
	for _, empty := range []string{"", " \n\t "} {
		require.ErrorContains(t, thread.SwapContext(t.Context(), empty), "compact summary is empty")
		assert.Equal(t, messages, thread.messages)
		assert.Equal(t, history, thread.GetCompactionHistory())
		assert.Equal(t, usage, thread.GetUsage())
	}
}

func TestSwapContext_PreservesToolResults(t *testing.T) {
	thread := createTestThread()

	// Add some tool results
	thread.ToolResults = map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "bash", Success: true},
		"tool-2": {ToolName: "file_read", Success: false},
	}

	err := thread.SwapContext(context.Background(), "summary")
	require.NoError(t, err)

	// Historical results remain available to the archived transcript.
	assert.Len(t, thread.ToolResults, 2)
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

func TestSwapContext_HandlesNilState(t *testing.T) {
	thread := createTestThread()
	thread.State = nil

	// Should not panic with nil state
	err := thread.SwapContext(context.Background(), "summary")
	require.NoError(t, err)
}

func TestRepeatedCompactionArchivesOnlyVisibleSuffix(t *testing.T) {
	thread := createTestThread()
	thread.messages = []openaisdk.ChatCompletionMessage{
		{Role: openaisdk.ChatMessageRoleUser, Content: "first question"},
		{Role: openaisdk.ChatMessageRoleAssistant, Content: "first answer"},
	}
	original, err := json.Marshal(thread.messages)
	require.NoError(t, err)
	require.NoError(t, thread.SwapContext(t.Context(), "first summary"))
	firstHistory := thread.GetCompactionHistory()
	require.Len(t, firstHistory.Segments, 1)
	assert.JSONEq(t, string(original), string(firstHistory.Segments[0].RawMessages))
	assert.Equal(t, 1, firstHistory.ActiveDisplayStart)
	assert.Equal(t, "summary", firstHistory.Segments[0].Marker.Method)
	assert.Equal(t, "first summary", firstHistory.Segments[0].Marker.Summary)
	assert.NotEmpty(t, firstHistory.Segments[0].Marker.ID)
	assert.False(t, firstHistory.Segments[0].Marker.CreatedAt.IsZero())

	thread.messages = append(thread.messages,
		openaisdk.ChatCompletionMessage{Role: openaisdk.ChatMessageRoleUser, Content: "second question"},
		openaisdk.ChatCompletionMessage{Role: openaisdk.ChatMessageRoleAssistant, Content: "second answer"},
	)
	visible, err := json.Marshal(thread.messages[1:])
	require.NoError(t, err)
	require.NoError(t, thread.SwapContext(t.Context(), "second summary"))
	history := thread.GetCompactionHistory()
	require.Len(t, history.Segments, 2)
	assert.Equal(t, firstHistory.Segments[0], history.Segments[0])
	assert.JSONEq(t, string(visible), string(history.Segments[1].RawMessages))
	assert.NotContains(t, string(history.Segments[1].RawMessages), "first summary")
	assert.NotEqual(t, history.Segments[0].Marker.ID, history.Segments[1].Marker.ID)
	assert.Equal(t, "second summary", history.Segments[1].Marker.Summary)
	assert.Equal(t, 1, history.ActiveDisplayStart)
	assert.Equal(t, []openaisdk.ChatCompletionMessage{{Role: openaisdk.ChatMessageRoleUser, Content: "second summary"}}, thread.messages,
		"archiving must not reintroduce old messages into inference")

	thread.AddUserMessage(t.Context(), "third question")
	raw, err := json.Marshal(thread.messages)
	require.NoError(t, err)
	tail, err := history.VisibleMessages(raw)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"role":"user","content":[{"type":"text","text":"third question"}]}]`, string(tail))
}

type compactionTestEnvironment struct {
	agentenv.UtilityEnvironment
	onAgentStart func(context.Context) error
}

func (e *compactionTestEnvironment) DispatchAgentStart(ctx context.Context) error {
	return e.onAgentStart(ctx)
}

func TestCompactionBoundaryTracksSystemPrependAndSubsequentAppends(t *testing.T) {
	thread := createTestThread()
	thread.AddUserMessage(t.Context(), "original question")
	require.NoError(t, thread.SwapContext(t.Context(), "original summary"))
	firstHistory := thread.GetCompactionHistory()

	// Cancel only after input admission and system-message insertion. The model
	// client is deliberately unset: neither turn may make a network request.
	for _, question := range []string{"second question", "third question"} {
		ctx, cancel := context.WithCancel(t.Context())
		thread.SetEnvironment(&compactionTestEnvironment{onAgentStart: func(context.Context) error {
			cancel()
			return nil
		}})
		_, err := thread.SendMessage(ctx, question, &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{DisableUsageLog: true})
		cancel()
		require.NoError(t, err)
		assert.Equal(t, 2, thread.GetCompactionHistory().ActiveDisplayStart,
			"prepend moves the boundary once; later appends must not move it again")
	}
	raw, err := json.Marshal(thread.messages)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"role":"system"},
		{"role":"user","content":"original summary"},
		{"role":"user","content":[{"type":"text","text":"second question"}]},
		{"role":"user","content":[{"type":"text","text":"third question"}]}
	]`, string(raw))

	require.NoError(t, thread.SwapContext(t.Context(), "next summary"))
	history := thread.GetCompactionHistory()
	require.Len(t, history.Segments, 2)
	assert.Equal(t, firstHistory.Segments[0], history.Segments[0])
	assert.JSONEq(t, `[
		{"role":"user","content":[{"type":"text","text":"second question"}]},
		{"role":"user","content":[{"type":"text","text":"third question"}]}
	]`, string(history.Segments[1].RawMessages))
	assert.Equal(t, 1, history.ActiveDisplayStart)
}

func TestNoSaveRestoresCompactionHistoryAndResults(t *testing.T) {
	for _, exit := range []string{"cancelled", "hook error"} {
		t.Run(exit, func(t *testing.T) {
			thread := createTestThread()
			thread.AddUserMessage(t.Context(), "original question")
			require.NoError(t, thread.SwapContext(t.Context(), "original summary"))
			thread.AddUserMessage(t.Context(), "continued question")
			thread.ToolResults["historic"] = tooltypes.StructuredToolResult{ToolName: "bash", Success: true}
			originalMessages, err := json.Marshal(thread.messages)
			require.NoError(t, err)
			originalHistory := thread.GetCompactionHistory()
			originalResults := thread.GetStructuredToolResults()
			store := &MockConversationStore{}
			thread.Store, thread.Persisted = store, true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := false
			thread.SetEnvironment(&compactionTestEnvironment{onAgentStart: func(ctx context.Context) error {
				called = true
				assert.Equal(t, 2, thread.GetCompactionHistory().ActiveDisplayStart)
				require.NoError(t, thread.SwapContext(ctx, "temporary summary"))
				thread.ToolResults["temporary"] = tooltypes.StructuredToolResult{ToolName: "file_read", Success: true}
				require.Len(t, thread.GetCompactionHistory().Segments, 2)
				if exit == "hook error" {
					return assert.AnError
				}
				cancel()
				return nil
			}})

			_, err = thread.SendMessage(ctx, "temporary question", &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
				NoSaveConversation: true,
				DisableUsageLog:    true,
			})
			if exit == "hook error" {
				require.ErrorIs(t, err, assert.AnError)
			} else {
				require.NoError(t, err)
			}
			assert.True(t, called, "exercise replacement after no-save state is captured, without a model request")
			raw, err := json.Marshal(thread.messages)
			require.NoError(t, err)
			assert.JSONEq(t, string(originalMessages), string(raw))
			assert.Equal(t, originalHistory, thread.GetCompactionHistory())
			assert.Equal(t, originalResults, thread.GetStructuredToolResults())
			assert.Empty(t, store.SavedRecords)
			assert.False(t, thread.ConversationForkBlocked())
		})
	}
}
