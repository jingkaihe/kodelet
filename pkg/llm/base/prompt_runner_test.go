package base

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type promptRunnerThread struct {
	config         llmtypes.Config
	conversationID string
	state          tooltypes.State
	metadata       map[string]any
	sentPrompt     string
	sentOpt        llmtypes.MessageOpt
	sendErr        error
	prepareCalled  bool
	seedCalled     bool
	persisted      bool
	usage          llmtypes.Usage
}

func newPromptRunnerThread() *promptRunnerThread {
	return &promptRunnerThread{metadata: make(map[string]any), conversationID: "conv-test"}
}

func (t *promptRunnerThread) SetState(state tooltypes.State)                    { t.state = state }
func (t *promptRunnerThread) GetState() tooltypes.State                         { return t.state }
func (t *promptRunnerThread) AddUserMessage(context.Context, string, ...string) {}
func (t *promptRunnerThread) SendMessage(_ context.Context, prompt string, handler llmtypes.MessageHandler, opt llmtypes.MessageOpt) (string, error) {
	t.sentPrompt = prompt
	t.sentOpt = opt
	if t.sendErr != nil {
		return "", t.sendErr
	}
	handler.HandleText("collected output")
	return "ignored final output", nil
}
func (t *promptRunnerThread) GetUsage() llmtypes.Usage                 { return t.usage }
func (t *promptRunnerThread) GetConversationID() string                { return t.conversationID }
func (t *promptRunnerThread) SetConversationID(id string)              { t.conversationID = id }
func (t *promptRunnerThread) SaveConversation(context.Context) error   { return nil }
func (t *promptRunnerThread) IsPersisted() bool                        { return t.persisted }
func (t *promptRunnerThread) EnablePersistence(context.Context, bool)  {}
func (t *promptRunnerThread) Provider() string                         { return "test" }
func (t *promptRunnerThread) GetMessages() ([]llmtypes.Message, error) { return nil, nil }
func (t *promptRunnerThread) GetConfig() llmtypes.Config               { return t.config }
func (t *promptRunnerThread) AggregateSubagentUsage(llmtypes.Usage)    {}
func (t *promptRunnerThread) SetMetadataValue(key string, value any)   { t.metadata[key] = value }
func (t *promptRunnerThread) GetMetadata() map[string]any              { return t.metadata }
func (t *promptRunnerThread) PrepareUtilityMode(context.Context)       { t.prepareCalled = true }

var (
	_ llmtypes.Thread = (*promptRunnerThread)(nil)
	_ UtilityThread   = (*promptRunnerThread)(nil)
)

func TestRunPreparedPrompt(t *testing.T) {
	thread := newPromptRunnerThread()
	output, err := RunPreparedPrompt(
		context.Background(),
		func() (llmtypes.Thread, error) { return thread, nil },
		func(prepared llmtypes.Thread) error {
			assert.Same(t, thread, prepared)
			thread.prepareCalled = true
			return nil
		},
		"utility prompt",
		llmtypes.MessageOpt{UseWeakModel: true},
	)

	require.NoError(t, err)
	assert.Equal(t, "collected output\n", output)
	assert.True(t, thread.prepareCalled)
	assert.Equal(t, "utility prompt", thread.sentPrompt)
	assert.True(t, thread.sentOpt.UseWeakModel)
}

func TestRunPreparedPromptTypedErrors(t *testing.T) {
	wantErr := errors.New("create failed")
	_, err := RunPreparedPromptTyped[*promptRunnerThread](
		context.Background(),
		func() (*promptRunnerThread, error) { return nil, wantErr },
		nil,
		"prompt",
		llmtypes.MessageOpt{},
	)
	assert.ErrorIs(t, err, wantErr)

	thread := newPromptRunnerThread()
	wantErr = errors.New("prepare failed")
	_, err = RunPreparedPromptTyped(
		context.Background(),
		func() (*promptRunnerThread, error) { return thread, nil },
		func(*promptRunnerThread) error { return wantErr },
		"prompt",
		llmtypes.MessageOpt{},
	)
	assert.ErrorIs(t, err, wantErr)

	thread = newPromptRunnerThread()
	thread.sendErr = errors.New("send failed")
	_, err = RunPreparedPromptTyped(
		context.Background(),
		func() (*promptRunnerThread, error) { return thread, nil },
		nil,
		"prompt",
		llmtypes.MessageOpt{},
	)
	assert.ErrorIs(t, err, thread.sendErr)
}

func TestUtilityPromptOptions(t *testing.T) {
	opt := UtilityPromptOptions(true)
	assert.Equal(t, llmtypes.InitiatorAgent, opt.Initiator)
	assert.True(t, opt.UseWeakModel)
	assert.False(t, opt.PromptCache)
	assert.True(t, opt.NoToolUse)
	assert.True(t, opt.DisableUsageLog)
	assert.True(t, opt.NoSaveConversation)
}

func TestRunUtilityPromptSeedsAndPreparesThread(t *testing.T) {
	thread := newPromptRunnerThread()
	output, err := RunUtilityPrompt(
		context.Background(),
		func() (*promptRunnerThread, error) { return thread, nil },
		func(seedThread *promptRunnerThread) {
			assert.Same(t, thread, seedThread)
			seedThread.seedCalled = true
		},
		"summarize this",
		true,
	)

	require.NoError(t, err)
	assert.Equal(t, "collected output\n", output)
	assert.True(t, thread.seedCalled)
	assert.True(t, thread.prepareCalled)
	assert.Equal(t, "summarize this", thread.sentPrompt)
	assert.True(t, thread.sentOpt.UseWeakModel)
	assert.True(t, thread.sentOpt.NoToolUse)
	assert.Equal(t, llmtypes.InitiatorAgent, thread.sentOpt.Initiator)

	thread = newPromptRunnerThread()
	output, err = RunUtilityPrompt(
		context.Background(),
		func() (*promptRunnerThread, error) { return thread, nil },
		nil,
		"summarize this",
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, "collected output\n", output)
	assert.False(t, thread.seedCalled)
	assert.True(t, thread.prepareCalled)
	assert.False(t, thread.sentOpt.UseWeakModel)
}

func TestFirstUserMessageName(t *testing.T) {
	t.Run("prefers first user text message", func(t *testing.T) {
		messages := []conversations.StreamableMessage{
			{Kind: "text", Role: "assistant", Content: "Ignore"},
			{Kind: "text", Role: "user", Content: "  First user message  "},
			{Kind: "text", Role: "user", Content: "Second user message"},
		}

		assert.Equal(t, "First user message", FirstUserMessageName(messages))
	})

	t.Run("uses raw item text when content is empty", func(t *testing.T) {
		messages := []conversations.StreamableMessage{
			{
				Kind:    "text",
				Role:    "user",
				RawItem: []byte(`{"content":[{"type":"input_text","text":"Message from raw item"}]}`),
			},
		}

		assert.Equal(t, "Message from raw item", FirstUserMessageName(messages))
	})

	t.Run("truncates long fallback to 100 chars", func(t *testing.T) {
		long := "This is a very long user message that should be truncated when used as the fallback conversation summary text."
		messages := []conversations.StreamableMessage{{Kind: "text", Role: "user", Content: long}}

		fallback := FirstUserMessageName(messages)
		assert.Len(t, fallback, 100)
		assert.True(t, strings.HasSuffix(fallback, "..."))
	})

	t.Run("truncates unicode names without splitting runes", func(t *testing.T) {
		name := FirstUserMessageName([]conversations.StreamableMessage{{Kind: "text", Role: "user", Content: strings.Repeat("界", 101)}})

		assert.Equal(t, strings.Repeat("界", 97)+"...", name)
	})
}
