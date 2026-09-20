package anthropic

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestThread() *Thread {
	config := llmtypes.Config{
		Model:     "claude-sonnet-4-6",
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
	thread.messages = []anthropic.MessageParam{
		{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("first message"),
			},
		},
		{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("assistant response"),
			},
		},
		{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("second message"),
			},
		},
	}

	summary := "This is a conversation summary"
	err := thread.SwapContext(context.Background(), summary)
	require.NoError(t, err)

	// Should have exactly one message - the summary
	assert.Len(t, thread.messages, 1)
	assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[0].Role)

	// Verify the message content contains the summary
	require.Len(t, thread.messages[0].Content, 1)
	textBlock := thread.messages[0].Content[0]
	require.NotNil(t, textBlock.OfText)
	assert.Equal(t, summary, textBlock.OfText.Text)
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

func TestSwapContext_RejectsEmptySummaryWithoutChangingHistory(t *testing.T) {
	for _, summary := range []string{"", " \n\t "} {
		t.Run(summary, func(t *testing.T) {
			thread := createTestThread()
			thread.AddUserMessage(t.Context(), "original request")
			require.NoError(t, thread.SwapContext(t.Context(), "valid summary"))
			thread.AddUserMessage(t.Context(), "next request")
			messages := append([]anthropic.MessageParam(nil), thread.messages...)
			history := thread.GetCompactionHistory()
			usage := thread.GetUsage()

			require.ErrorContains(t, thread.SwapContext(t.Context(), summary), "compact summary is empty")
			assert.Equal(t, messages, thread.messages)
			assert.Equal(t, history, thread.GetCompactionHistory())
			assert.Equal(t, usage, thread.GetUsage())
			thread.Store = &MockConversationStore{}
			thread.Persisted = true
			require.NoError(t, thread.SaveConversation(t.Context()))
		})
	}
}

func TestRepeatedCompactionArchivesOnlyVisibleSuffix(t *testing.T) {
	thread := createTestThread()
	thread.AddUserMessage(t.Context(), "first question")
	thread.messages = append(thread.messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock("first answer")))
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

	thread.AddUserMessage(t.Context(), "second question")
	thread.messages = append(thread.messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock("second answer")))
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
	assert.Equal(t, []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("second summary"))}, thread.messages,
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
