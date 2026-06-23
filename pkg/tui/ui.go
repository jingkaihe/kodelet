package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
)

const uiNotificationTTL = 5 * time.Second

type uiPromptMode int

const (
	uiPromptInput uiPromptMode = iota
	uiPromptConfirm
	uiPromptSelect
)

type uiPromptState struct {
	mode     uiPromptMode
	id       string
	title    string
	message  string
	helpText string

	placeholder      string
	defaultValue     string
	submitButtonText string
	cancelButtonText string
	required         bool
	secret           bool

	options     []string
	selectIndex int

	input    textinput.Model
	response chan extensions.UIInputResponse
}

type uiNotification struct {
	id      int
	level   uiNotificationLevel
	title   string
	message string
}

type uiNotificationLevel int

const (
	uiNotificationInfo uiNotificationLevel = iota
	uiNotificationWarning
	uiNotificationError
)

type uiPromptRequestMsg struct {
	runID  int
	prompt uiPromptState
}

type uiNotificationMsg struct {
	runID        int
	notification uiNotification
}

type uiNotificationExpiredMsg struct {
	id int
}

type tuiUIBroker struct {
	ch    chan<- tea.Msg
	runID int

	mu     sync.Mutex
	closed bool
}

func newTUIUIBroker(ch chan<- tea.Msg, runID int) *tuiUIBroker {
	return &tuiUIBroker{ch: ch, runID: runID}
}

func (b *tuiUIBroker) Input(ctx context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.ch == nil || b.isClosed() {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "tui input is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}
	prompt := uiPromptState{
		mode:             uiPromptInput,
		id:               request.ID,
		title:            request.Title,
		message:          request.Message,
		helpText:         request.HelpText,
		placeholder:      request.Placeholder,
		defaultValue:     request.DefaultValue,
		submitButtonText: request.SubmitButtonText,
		cancelButtonText: request.CancelButtonText,
		required:         request.Required,
		secret:           request.Secret,
		response:         make(chan extensions.UIInputResponse, 1),
	}
	return b.prompt(ctx, prompt)
}

func (b *tuiUIBroker) Confirm(ctx context.Context, request extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.ch == nil || b.isClosed() {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "tui confirm is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}
	prompt := uiPromptState{
		mode:             uiPromptConfirm,
		id:               request.ID,
		title:            request.Title,
		message:          request.Message,
		submitButtonText: request.ConfirmButtonText,
		cancelButtonText: request.CancelButtonText,
		response:         make(chan extensions.UIInputResponse, 1),
	}
	return b.prompt(ctx, prompt)
}

func (b *tuiUIBroker) Select(ctx context.Context, request extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.ch == nil || b.isClosed() {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "tui select is not available"}, nil
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = extensions.NewUIInputRequestID()
	}
	prompt := uiPromptState{
		mode:             uiPromptSelect,
		id:               request.ID,
		title:            request.Title,
		message:          request.Message,
		options:          append([]string{}, request.Options...),
		submitButtonText: request.SubmitButtonText,
		cancelButtonText: request.CancelButtonText,
		response:         make(chan extensions.UIInputResponse, 1),
	}
	return b.prompt(ctx, prompt)
}

func (b *tuiUIBroker) Notify(ctx context.Context, request extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	if b == nil || b.ch == nil || b.isClosed() {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "tui notify is not available"}, nil
	}
	if err := ctx.Err(); err != nil {
		return extensions.UIInputResponse{}, err
	}
	select {
	case <-ctx.Done():
		return extensions.UIInputResponse{}, ctx.Err()
	case b.ch <- uiNotificationMsg{runID: b.runID, notification: uiNotification{title: request.Title, message: request.Message}}:
		return extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted}, nil
	}
}

func (b *tuiUIBroker) prompt(ctx context.Context, prompt uiPromptState) (extensions.UIInputResponse, error) {
	select {
	case <-ctx.Done():
		return extensions.UIInputResponse{}, ctx.Err()
	case b.ch <- uiPromptRequestMsg{runID: b.runID, prompt: prompt}:
	}

	select {
	case <-ctx.Done():
		b.respond(prompt, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
		return extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed}, ctx.Err()
	case response := <-prompt.response:
		if response.Status == "" {
			response.Status = extensions.UIInputStatusDismissed
		}
		return response, nil
	}
}

func (b *tuiUIBroker) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

func (b *tuiUIBroker) isClosed() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func (b *tuiUIBroker) respond(prompt uiPromptState, response extensions.UIInputResponse) bool {
	if prompt.response == nil {
		return false
	}
	select {
	case prompt.response <- response:
		return true
	default:
		return false
	}
}

func newInputPromptModel(prompt uiPromptState, width int) uiPromptState {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = prompt.placeholder
	input.PlaceholderStyle = inputPlaceholderStyle
	input.TextStyle = composerTextStyle
	input.Cursor.Style = composerCursorStyle
	input.Cursor.TextStyle = composerTextStyle
	input.Width = max(1, width)
	if prompt.secret {
		input.EchoMode = textinput.EchoPassword
	}
	input.SetValue(prompt.defaultValue)
	input.Focus()
	prompt.input = input
	return prompt
}

func (m *model) openUIPrompt(prompt uiPromptState) tea.Cmd {
	if m.activeUIPrompt != nil {
		m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
	}
	m.profilePickerOpen = false
	m.dismissSlashCommandSuggestions()
	if prompt.mode == uiPromptInput {
		prompt = newInputPromptModel(prompt, m.uiDialogInputWidth())
	}
	m.activeUIPrompt = &prompt
	m.status = "waiting for input"
	m.resize()
	m.refreshViewport(false)
	if prompt.mode == uiPromptInput {
		return textinput.Blink
	}
	return nil
}

func (m *model) resolveUIPrompt(response extensions.UIInputResponse) {
	if m.activeUIPrompt == nil {
		return
	}
	prompt := *m.activeUIPrompt
	m.activeUIPrompt = nil
	if m.running {
		m.status = "working"
	} else {
		m.status = "ready"
	}
	if response.Status == "" {
		response.Status = extensions.UIInputStatusDismissed
	}
	select {
	case prompt.response <- response:
	default:
	}
	m.resize()
	m.refreshViewport(false)
}

func (m *model) submitUIPrompt() {
	if m.activeUIPrompt == nil {
		return
	}
	prompt := m.activeUIPrompt
	switch prompt.mode {
	case uiPromptInput:
		value := prompt.input.Value()
		if prompt.required && strings.TrimSpace(value) == "" {
			return
		}
		m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: value})
	case uiPromptConfirm:
		m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Confirmed: true, Value: "true"})
	case uiPromptSelect:
		if len(prompt.options) == 0 {
			return
		}
		index := prompt.selectIndex
		if index < 0 || index >= len(prompt.options) {
			index = 0
		}
		m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted, Value: prompt.options[index]})
	}
}

func (m *model) dismissUIPrompt() {
	if m.activeUIPrompt == nil {
		return
	}
	response := extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed}
	if m.activeUIPrompt.mode == uiPromptConfirm {
		response.Confirmed = false
		response.Value = "false"
	}
	m.resolveUIPrompt(response)
}

func (m *model) moveUISelect(delta int) bool {
	if m.activeUIPrompt == nil || m.activeUIPrompt.mode != uiPromptSelect || len(m.activeUIPrompt.options) == 0 {
		return false
	}
	next := m.activeUIPrompt.selectIndex + delta
	if next < 0 {
		next = len(m.activeUIPrompt.options) - 1
	} else if next >= len(m.activeUIPrompt.options) {
		next = 0
	}
	m.activeUIPrompt.selectIndex = next
	return true
}

func (m *model) addUINotification(notification uiNotification) tea.Cmd {
	message := strings.TrimSpace(notification.message)
	title := strings.TrimSpace(notification.title)
	if title == "" && message == "" {
		return nil
	}
	m.nextUINotificationID++
	notification.id = m.nextUINotificationID
	notification.title = title
	notification.message = message
	m.uiNotifications = append(m.uiNotifications, notification)
	if len(m.uiNotifications) > 3 {
		m.uiNotifications = append([]uiNotification{}, m.uiNotifications[len(m.uiNotifications)-3:]...)
	}
	m.refreshViewport(false)
	return tea.Tick(uiNotificationTTL, func(time.Time) tea.Msg {
		return uiNotificationExpiredMsg{id: notification.id}
	})
}

func (m *model) removeUINotification(id int) bool {
	for i, notification := range m.uiNotifications {
		if notification.id != id {
			continue
		}
		m.uiNotifications = append(m.uiNotifications[:i], m.uiNotifications[i+1:]...)
		m.refreshViewport(false)
		return true
	}
	return false
}

func uiPromptTitle(mode uiPromptMode, title string) string {
	title = strings.TrimSpace(title)
	if title != "" {
		return title
	}
	switch mode {
	case uiPromptConfirm:
		return "Extension requested confirmation"
	case uiPromptSelect:
		return "Extension requested selection"
	default:
		return "Extension requested input"
	}
}

func uiPromptSubmitLabel(prompt uiPromptState) string {
	label := strings.TrimSpace(prompt.submitButtonText)
	if label != "" {
		return label
	}
	switch prompt.mode {
	case uiPromptConfirm:
		return "Confirm"
	case uiPromptSelect:
		return "Select"
	default:
		return "Submit"
	}
}

func uiPromptCancelLabel(prompt uiPromptState) string {
	label := strings.TrimSpace(prompt.cancelButtonText)
	if label != "" {
		return label
	}
	return "Cancel"
}

func promptFromChatEvent(event chat.ChatEvent) (uiPromptState, bool) {
	switch event.Kind {
	case "ui-input", "ui-input-request":
		if event.UIInput == nil {
			return uiPromptState{}, false
		}
		request := event.UIInput
		return uiPromptState{
			mode:             uiPromptInput,
			id:               request.ID,
			title:            request.Title,
			message:          request.Message,
			helpText:         request.HelpText,
			placeholder:      request.Placeholder,
			defaultValue:     request.DefaultValue,
			submitButtonText: request.SubmitButtonText,
			cancelButtonText: request.CancelButtonText,
			required:         request.Required,
			secret:           request.Secret,
			response:         make(chan extensions.UIInputResponse, 1),
		}, true
	case "ui-confirm", "ui-confirm-request":
		if event.UIConfirm == nil {
			return uiPromptState{}, false
		}
		request := event.UIConfirm
		return uiPromptState{
			mode:             uiPromptConfirm,
			id:               request.ID,
			title:            request.Title,
			message:          request.Message,
			submitButtonText: request.ConfirmButtonText,
			cancelButtonText: request.CancelButtonText,
			response:         make(chan extensions.UIInputResponse, 1),
		}, true
	case "ui-select", "ui-select-request":
		if event.UISelect == nil {
			return uiPromptState{}, false
		}
		request := event.UISelect
		return uiPromptState{
			mode:             uiPromptSelect,
			id:               request.ID,
			title:            request.Title,
			message:          request.Message,
			options:          append([]string{}, request.Options...),
			submitButtonText: request.SubmitButtonText,
			cancelButtonText: request.CancelButtonText,
			response:         make(chan extensions.UIInputResponse, 1),
		}, true
	default:
		return uiPromptState{}, false
	}
}
