package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type uiBrokerResult struct {
	response extensions.UIInputResponse
	err      error
}

func receiveUIBrokerResult(t *testing.T, ch <-chan uiBrokerResult) uiBrokerResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for UI broker result")
		return uiBrokerResult{}
	}
}

func TestTUIUIBrokerInputDialogResolvesResponse(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 7

	broker := newTUIUIBroker(m.runCh, m.activeRunID)
	resultCh := make(chan uiBrokerResult, 1)
	go func() {
		response, err := broker.Input(context.Background(), extensions.UIInputRequest{
			ID:               "input-1",
			Title:            "Token?",
			Message:          "Paste a token",
			HelpText:         "It will not be shown in the transcript.",
			Placeholder:      "secret-token",
			SubmitButtonText: "Send",
			CancelButtonText: "Skip",
			Required:         true,
			Secret:           true,
		})
		resultCh <- uiBrokerResult{response: response, err: err}
	}()

	msg, ok := receiveRunMsg(t, m.runCh).(uiPromptRequestMsg)
	require.True(t, ok)
	updated, _ := m.Update(msg)
	m = updated.(model)

	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptInput, m.activeUIPrompt.mode)
	assert.True(t, m.activeUIPrompt.secret)
	assert.Equal(t, "waiting for input", m.status)
	view := xansi.Strip(m.View())
	assert.Contains(t, view, "Token?")
	assert.Contains(t, view, "Paste a token")
	assert.Contains(t, view, "It will not be shown")
	assert.Contains(t, view, "[Esc] Skip")
	assert.Contains(t, view, "[Enter] Send")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("answer")})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	result := receiveUIBrokerResult(t, resultCh)
	require.NoError(t, result.err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, result.response.Status)
	assert.Equal(t, "answer", result.response.Value)
	assert.Nil(t, m.activeUIPrompt)
	assert.Equal(t, "working", m.status)
}

func TestTUIUIBrokerConfirmSelectAndNotify(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.running = true
	m.activeRunID = 3
	broker := newTUIUIBroker(m.runCh, m.activeRunID)

	confirmCh := make(chan uiBrokerResult, 1)
	go func() {
		response, err := broker.Confirm(context.Background(), extensions.UIConfirmRequest{
			ID:                "confirm-1",
			Title:             "Allow bash?",
			Message:           "An extension wants to run a command.",
			ConfirmButtonText: "Allow",
			CancelButtonText:  "Deny",
		})
		confirmCh <- uiBrokerResult{response: response, err: err}
	}()

	msg, ok := receiveRunMsg(t, m.runCh).(uiPromptRequestMsg)
	require.True(t, ok)
	updated, _ := m.Update(msg)
	m = updated.(model)
	view := xansi.Strip(m.View())
	assert.Contains(t, view, "Allow bash?")
	assert.Contains(t, view, "[Y] Allow")
	assert.Contains(t, view, "[N] Deny")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(model)
	confirm := receiveUIBrokerResult(t, confirmCh)
	require.NoError(t, confirm.err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, confirm.response.Status)
	assert.True(t, confirm.response.Confirmed)
	assert.Equal(t, "true", confirm.response.Value)

	selectCh := make(chan uiBrokerResult, 1)
	go func() {
		response, err := broker.Select(context.Background(), extensions.UISelectRequest{
			ID:      "select-1",
			Title:   "Pick food",
			Message: "Choose one",
			Options: []string{"Pasta", "Pizza", "Focaccia"},
		})
		selectCh <- uiBrokerResult{response: response, err: err}
	}()

	msg, ok = receiveRunMsg(t, m.runCh).(uiPromptRequestMsg)
	require.True(t, ok)
	updated, _ = m.Update(msg)
	m = updated.(model)
	assert.Equal(t, uiPromptSelect, m.activeUIPrompt.mode)
	assert.Equal(t, 0, m.activeUIPrompt.selectIndex)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	assert.Equal(t, 1, m.activeUIPrompt.selectIndex)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	selection := receiveUIBrokerResult(t, selectCh)
	require.NoError(t, selection.err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, selection.response.Status)
	assert.Equal(t, "Pizza", selection.response.Value)

	notify, err := broker.Notify(context.Background(), extensions.UINotifyRequest{Title: "Ready", Message: "Done"})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusSubmitted, notify.Status)
	notifyMsg, ok := receiveRunMsg(t, m.runCh).(uiNotificationMsg)
	require.True(t, ok)
	updated, _ = m.Update(notifyMsg)
	m = updated.(model)
	require.Len(t, m.uiNotifications, 1)
	view = xansi.Strip(m.View())
	assert.Contains(t, view, "Ready")
	assert.Contains(t, view, "Done")

	updated, _ = m.Update(uiNotificationExpiredMsg{id: m.uiNotifications[0].id})
	m = updated.(model)
	assert.Empty(t, m.uiNotifications)
}

type brokerCheckingRunner struct {
	hasInput   bool
	hasConfirm bool
	hasSelect  bool
	hasNotify  bool
}

func (r *brokerCheckingRunner) Run(ctx context.Context, req chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
	_, r.hasInput = extensions.UIInputBrokerFromContext(ctx)
	_, r.hasConfirm = extensions.UIConfirmBrokerFromContext(ctx)
	_, r.hasSelect = extensions.UISelectBrokerFromContext(ctx)
	_, r.hasNotify = extensions.UINotifyBrokerFromContext(ctx)
	return "conversation-done", nil
}

func TestSubmitAttachesTUIUIBrokerToRunContext(t *testing.T) {
	runner := &brokerCheckingRunner{}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("hello")

	cmd := m.submit()
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())

	done, ok := receiveRunMsg(t, m.runCh).(chatDoneMsg)
	require.True(t, ok)
	assert.Equal(t, "conversation-done", done.conversationID)
	assert.True(t, runner.hasInput)
	assert.True(t, runner.hasConfirm)
	assert.True(t, runner.hasSelect)
	assert.True(t, runner.hasNotify)
}

func TestUIChatEventsOpenDialogsAndNotifications(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 90
	m.height = 24
	m.resize()
	m.running = true
	m.activeRunID = 1

	updated, _ := m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{
		Kind: "ui-input-request",
		UIInput: &chat.UIInputEvent{
			ID:      "input-1",
			Title:   "Question",
			Message: "Answer needed",
		},
	}})
	m = updated.(model)
	require.NotNil(t, m.activeUIPrompt)
	assert.Equal(t, uiPromptInput, m.activeUIPrompt.mode)
	assert.Contains(t, xansi.Strip(m.View()), "Question")

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	assert.Nil(t, m.activeUIPrompt)

	updated, _ = m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{
		Kind:     "ui-notification",
		UINotify: &chat.UINotifyEvent{Title: "Heads up", Message: "Extension finished"},
	}})
	m = updated.(model)
	require.Len(t, m.uiNotifications, 1)
	assert.Contains(t, xansi.Strip(m.View()), "Heads up")
}

func TestUIThemeFieldsAreConfiguredForAllThemes(t *testing.T) {
	for name, theme := range themes {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, theme.UI.DialogBorder)
			assert.NotEmpty(t, theme.UI.DialogTitle)
			assert.NotEmpty(t, theme.UI.DialogBody)
			assert.NotEmpty(t, theme.UI.DialogSelected)
			assert.NotEmpty(t, theme.UI.NotificationBorder)
			assert.NotEmpty(t, theme.UI.NotificationTitle)
			assert.NotEmpty(t, theme.UI.NotificationBody)
		})
	}
}

func TestCatppuccinMochaDialogThemeAvoidsPinkPurpleAccents(t *testing.T) {
	theme := themes[DefaultThemeName]

	assert.Equal(t, theme.InputBorder, theme.UI.DialogBorder)
	assert.Equal(t, "#94e2d5", theme.UI.DialogTitle)
	assert.Equal(t, "#fab387", theme.UI.DialogCancel)
}
