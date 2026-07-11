package tui

import (
	"context"
	"strings"
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

func TestExtensionDiagnosticsBecomeTUIWarningsAndErrors(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	sink, ok := extensions.DiagnosticSinkFromContext(m.ctx)
	require.True(t, ok)
	warning := extensions.Diagnostic{
		Level:     extensions.DiagnosticLevelWarning,
		Extension: "mcp",
		Message:   "failed to initialize MCP server",
		Fields: map[string]any{
			"server": "playwright",
			"error":  "spawn npxx ENOENT",
		},
	}
	sink.ReportDiagnostic(context.Background(), warning)

	msg, ok := receiveRunMsg(t, m.runCh).(uiDiagnosticMsg)
	require.True(t, ok)
	assert.Equal(t, uiNotificationWarning, msg.notification.level)
	assert.Equal(t, "MCP warning", msg.notification.title)
	assert.Equal(t, `failed to initialize MCP server "playwright": spawn npxx ENOENT`, msg.notification.message)
	updated, _ := m.Update(msg)
	m = updated.(model)
	require.Len(t, m.uiNotifications, 1)
	view := xansi.Strip(m.View())
	assert.Contains(t, view, "MCP warning")
	assert.Contains(t, view, "spawn npxx ENOENT")

	// Identical diagnostics are suppressed briefly because extension discovery
	// can initialize the same MCP configuration more than once.
	sink.ReportDiagnostic(context.Background(), warning)
	select {
	case duplicate := <-m.runCh:
		t.Fatalf("unexpected duplicate diagnostic: %#v", duplicate)
	default:
	}

	sink.ReportDiagnostic(context.Background(), extensions.Diagnostic{
		Level:     extensions.DiagnosticLevelError,
		Extension: "weather",
		Message:   "extension stopped",
	})
	errorMsg, ok := receiveRunMsg(t, m.runCh).(uiDiagnosticMsg)
	require.True(t, ok)
	assert.Equal(t, uiNotificationError, errorMsg.notification.level)
	assert.Equal(t, "weather error", errorMsg.notification.title)
}

func TestDiagnosticNotificationMessageIsBounded(t *testing.T) {
	notification, ok := notificationFromDiagnostic(extensions.Diagnostic{
		Level:     extensions.DiagnosticLevelError,
		Extension: "weather",
		Message:   strings.Repeat("x", diagnosticNotificationMaxRunes+20),
	})

	require.True(t, ok)
	assert.Len(t, []rune(notification.message), diagnosticNotificationMaxRunes)
	assert.True(t, strings.HasSuffix(notification.message, "…"))
}

func TestTUIUIBrokerUnavailableClosedAndContextCancellation(t *testing.T) {
	ctx := context.Background()
	var nilBroker *tuiUIBroker

	input, err := nilBroker.Input(ctx, extensions.UIInputRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, input.Status)
	confirm, err := nilBroker.Confirm(ctx, extensions.UIConfirmRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, confirm.Status)
	selection, err := nilBroker.Select(ctx, extensions.UISelectRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, selection.Status)
	notification, err := nilBroker.Notify(ctx, extensions.UINotifyRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, notification.Status)
	assert.True(t, nilBroker.isClosed())
	nilBroker.close()

	broker := newTUIUIBroker(nil, 1)
	input, err = broker.Input(ctx, extensions.UIInputRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, input.Status)

	broker = newTUIUIBroker(make(chan tea.Msg, 1), 2)
	broker.close()
	assert.True(t, broker.isClosed())
	confirm, err = broker.Confirm(ctx, extensions.UIConfirmRequest{})
	require.NoError(t, err)
	assert.Equal(t, extensions.UIInputStatusUnavailable, confirm.Status)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	promptCh := make(chan tea.Msg, 1)
	broker = newTUIUIBroker(promptCh, 3)
	_, err = broker.Notify(canceledCtx, extensions.UINotifyRequest{Title: "ignored"})
	assert.ErrorIs(t, err, context.Canceled)

	resultCh := make(chan uiBrokerResult, 1)
	promptCtx, cancelPrompt := context.WithCancel(ctx)
	go func() {
		response, err := broker.Input(promptCtx, extensions.UIInputRequest{Title: "Question"})
		resultCh <- uiBrokerResult{response: response, err: err}
	}()
	msg, ok := receiveRunMsg(t, promptCh).(uiPromptRequestMsg)
	require.True(t, ok)
	assert.NotEmpty(t, msg.prompt.id)
	cancelPrompt()
	result := receiveUIBrokerResult(t, resultCh)
	assert.ErrorIs(t, result.err, context.Canceled)
	assert.Equal(t, extensions.UIInputStatusDismissed, result.response.Status)

	manualPrompt := uiPromptState{response: make(chan extensions.UIInputResponse, 1)}
	assert.False(t, broker.respond(uiPromptState{}, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}))
	assert.True(t, broker.respond(manualPrompt, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}))
	assert.False(t, broker.respond(manualPrompt, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}))
}

func TestUIPromptDefaultsRequiredDismissAndEmptySelect(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	responseCh := make(chan extensions.UIInputResponse, 1)
	cmd := m.openUIPrompt(uiPromptState{mode: uiPromptInput, required: true, response: responseCh})
	assert.NotNil(t, cmd)
	assert.Equal(t, "Extension requested input", uiPromptTitle(uiPromptInput, ""))
	assert.Equal(t, "Submit", uiPromptSubmitLabel(*m.activeUIPrompt))
	m.submitUIPrompt()
	assert.NotNil(t, m.activeUIPrompt)
	assert.Empty(t, responseCh)
	m.dismissUIPrompt()
	assert.Nil(t, m.activeUIPrompt)
	assert.Equal(t, "ready", m.status)
	assert.Equal(t, extensions.UIInputStatusDismissed, (<-responseCh).Status)

	responseCh = make(chan extensions.UIInputResponse, 1)
	m.openUIPrompt(uiPromptState{mode: uiPromptConfirm, response: responseCh})
	assert.Equal(t, "Extension requested confirmation", uiPromptTitle(uiPromptConfirm, ""))
	assert.Equal(t, "Confirm", uiPromptSubmitLabel(*m.activeUIPrompt))
	m.dismissUIPrompt()
	confirm := <-responseCh
	assert.Equal(t, extensions.UIInputStatusDismissed, confirm.Status)
	assert.False(t, confirm.Confirmed)
	assert.Equal(t, "false", confirm.Value)

	responseCh = make(chan extensions.UIInputResponse, 1)
	m.openUIPrompt(uiPromptState{mode: uiPromptSelect, response: responseCh})
	assert.Equal(t, "Extension requested selection", uiPromptTitle(uiPromptSelect, ""))
	assert.Equal(t, "Select", uiPromptSubmitLabel(*m.activeUIPrompt))
	m.submitUIPrompt()
	assert.NotNil(t, m.activeUIPrompt)
	assert.Empty(t, responseCh)
	assert.False(t, m.moveUISelect(1))
	m.dismissUIPrompt()
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

func TestPromptFromChatEventVariantsAndMissingPayloads(t *testing.T) {
	for _, event := range []chat.ChatEvent{
		{Kind: "ui-input"},
		{Kind: "ui-confirm"},
		{Kind: "ui-select"},
		{Kind: "ui-notify"},
	} {
		prompt, ok := promptFromChatEvent(event)
		assert.False(t, ok)
		assert.Equal(t, uiPromptState{}, prompt)
	}

	input, ok := promptFromChatEvent(chat.ChatEvent{Kind: "ui-input", UIInput: &chat.UIInputEvent{ID: "input", DefaultValue: "value"}})
	require.True(t, ok)
	assert.Equal(t, uiPromptInput, input.mode)
	assert.Equal(t, "value", input.defaultValue)

	confirm, ok := promptFromChatEvent(chat.ChatEvent{Kind: "ui-confirm-request", UIConfirm: &chat.UIConfirmEvent{ID: "confirm", ConfirmButtonText: "Yes"}})
	require.True(t, ok)
	assert.Equal(t, uiPromptConfirm, confirm.mode)
	assert.Equal(t, "Yes", confirm.submitButtonText)

	selection, ok := promptFromChatEvent(chat.ChatEvent{Kind: "ui-select", UISelect: &chat.UISelectEvent{ID: "select", Options: []string{"one"}}})
	require.True(t, ok)
	assert.Equal(t, uiPromptSelect, selection.mode)
	assert.Equal(t, []string{"one"}, selection.options)

	_, ok = promptFromChatEvent(chat.ChatEvent{Kind: "unknown"})
	assert.False(t, ok)
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
			assert.NotEmpty(t, theme.UI.NotificationWarningBorder)
			assert.NotEmpty(t, theme.UI.NotificationWarningTitle)
			assert.NotEmpty(t, theme.UI.NotificationErrorBorder)
			assert.NotEmpty(t, theme.UI.NotificationErrorTitle)
		})
	}
}

func TestCatppuccinMochaDialogThemeAvoidsPinkPurpleAccents(t *testing.T) {
	theme := themes[DefaultThemeName]

	assert.Equal(t, theme.InputBorder, theme.UI.DialogBorder)
	assert.Equal(t, theme.ComposerText, theme.UI.DialogTitle)
	assert.Equal(t, "#fab387", theme.UI.DialogCancel)
}
