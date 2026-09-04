package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/google/shlex"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/steer"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

func waitForMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func waitForTranscriptRefresh() tea.Cmd {
	return tea.Tick(transcriptRefreshDelay, func(time.Time) tea.Msg {
		return transcriptRefreshMsg{}
	})
}

func isTextareaNewlineKey(key string) bool {
	switch key {
	case "shift+enter", "alt+enter", "ctrl+j":
		return true
	}
	return false
}

func normalizeSingleLinePaste(text string) string {
	text = xansi.Strip(text)
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, extensionSurfaceFocused := m.focusedExtensionSurfaceKey()
	if key, ok := msg.(tea.KeyPressMsg); ok && m.activeUIPrompt == nil && m.conversationPicker == nil && !m.shortcutsOpen && m.historySearch == nil && !extensionSurfaceFocused && isTextareaNewlineKey(key.String()) {
		return m, m.insertTextareaNewline()
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		cmds = append(cmds, m.updateExtensionSurfaceLayouts()...)
		m.refreshViewport(true)

	case tea.BackgroundColorMsg:
		if m.themeSelection != AutoThemeName {
			break
		}
		theme, err := resolveTheme(automaticThemeName(msg.IsDark()))
		if err != nil {
			break
		}
		return m, m.applyResolvedTheme(theme)

	case extensionUIFlushMsg:
		if m.extensionUI == nil {
			break
		}
		cmd := m.applyExtensionUIBatch(m.extensionUI.drain())
		return m, tea.Batch(waitForMsg(m.runCh), cmd)

	case extensionUITransportErrorMsg:
		if m.extensionUI != nil {
			m.extensionUI.CleanupExtensionUI(msg.owner)
		}
		return m, nil

	case extensionUITranscriptMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil {
			return m, waitForMsg(m.runCh)
		}
		active := state == m.conversationState
		state.entries = append(state.entries, chatEntry{kind: entryInfo, title: msg.title, content: msg.message})
		if active {
			m.refreshViewport(m.autoFollow)
		} else {
			state.unread = true
		}
		return m, waitForMsg(m.runCh)

	case uiPromptRequestMsg:
		state := m.uiBrokerState(msg.runID, msg.conversationKey)
		if state == nil {
			respondUIPrompt(msg.prompt, extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "tui input request is no longer active"})
			return m, waitForMsg(m.runCh)
		}
		if m.uiBrokerRunCancelling(msg.runID, state) {
			respondUIPrompt(msg.prompt, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
			return m, waitForMsg(m.runCh)
		}
		msg.prompt.runID = msg.runID
		cmd := m.openUIPromptForState(state, msg.prompt)
		return m, tea.Batch(waitForMsg(m.runCh), cmd)

	case uiNotificationMsg:
		state := m.uiBrokerState(msg.runID, msg.conversationKey)
		if state == nil {
			respondUINotification(msg, extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "tui notification is no longer active"})
			return m, waitForMsg(m.runCh)
		}
		if m.uiBrokerRunCancelling(msg.runID, state) {
			respondUINotification(msg, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
			return m, waitForMsg(m.runCh)
		}
		if state != m.conversationState {
			state.unread = true
		}
		notification := msg.notification
		notification.conversationKey = state.key
		cmd := m.addUINotification(notification)
		respondUINotification(msg, extensions.UIInputResponse{Status: extensions.UIInputStatusSubmitted})
		return m, tea.Batch(waitForMsg(m.runCh), cmd)

	case uiDiagnosticMsg:
		cmd := m.addUINotification(msg.notification)
		return m, tea.Batch(waitForMsg(m.runCh), cmd)

	case uiNotificationExpiredMsg:
		m.removeUINotification(msg.id)
		return m, nil

	case editorFinishedMsg:
		cmd := m.applyEditorResult(msg)
		return m, cmd

	case remoteSteerMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil {
			return m, nil
		}
		if msg.err != nil {
			state.err = errors.Wrap(msg.err, "failed to queue steering message")
			state.steerError = fmt.Sprintf("Failed to queue steering: %v", msg.err)
			state.status = "steering failed"
		} else if msg.queued {
			state.queuedSteering = append(state.queuedSteering, msg.message)
			state.steerError = ""
			state.status = "steering queued"
		} else {
			state.steerError = "Steering message was not queued."
			state.status = "steering ignored"
		}
		if state == m.conversationState {
			m.refreshViewport(true)
		} else {
			state.unread = true
		}
		return m, nil

	case remoteStopMsg:
		state := m.stateForKey(msg.conversationKey)
		if state != nil && msg.err != nil {
			state.err = errors.Wrap(msg.err, "failed to stop remote conversation")
			state.status = "cancellation failed"
			if run := m.runs[state.activeRunID]; run != nil && run.observed {
				state.runCancelling = false
			}
			if state == m.conversationState {
				m.refreshViewport(true)
			} else {
				state.unread = true
			}
		}
		return m, nil

	case initialHistoryMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil {
			break
		}
		active := state == m.conversationState
		currentState := m.conversationState
		m.conversationState = state
		wasInitialHistoryPending := m.initialHistoryPending
		m.initialHistoryPending = false
		m.deferSubmitUntilHistory = false
		queuedHistoryMessage := m.submitAfterHistoryLoad
		queuedHistoryPreserveComposer := m.submitAfterHistoryLoadPreserveComposer
		m.submitAfterHistoryLoad = ""
		m.submitAfterHistoryLoadPreserveComposer = false
		reloadSlashCommands := wasInitialHistoryPending && !m.remote
		m.extensionDiscoveryBlocked = m.remote || (wasInitialHistoryPending && msg.err != nil)
		var reloadMessageHistory tea.Cmd
		var remoteQueuedSubmit string
		var remoteQueuedSubmitPreserveComposer bool
		if msg.err != nil {
			m.err = msg.err
			m.status = "history load failed"
			m.profilePickerOpen = false
			m.reasoningPickerOpen = false
			m.entries = append(m.entries, chatEntry{
				kind: entryAssistant,
				blocks: []assistantBlock{{
					kind: blockText,
					text: fmt.Sprintf("Failed to resume conversation: %v", msg.err),
				}},
			})
		} else if msg.loaded {
			m.loaded = true
			if strings.TrimSpace(msg.title) != "" {
				m.title = strings.TrimSpace(msg.title)
			}
			if !msg.updatedAt.IsZero() {
				m.updatedAt = msg.updatedAt
			}
			if strings.TrimSpace(m.conversationID) != "" {
				m.setProfile(msg.profile)
				m.profilePickerOpen = false
				if strings.TrimSpace(msg.reasoningEffort) != "" {
					m.reasoningEffortOptions = []string{msg.reasoningEffort}
					m.setReasoningEffort(msg.reasoningEffort, false)
				}
				m.reasoningPickerOpen = false
			}
			if strings.TrimSpace(msg.cwd) != "" {
				reloadSlashCommands = reloadSlashCommands || (!m.remote && strings.TrimSpace(m.requestedCWD) == "" && strings.TrimSpace(m.cwd) != strings.TrimSpace(msg.cwd))
				m.cwd = strings.TrimSpace(msg.cwd)
				m.refreshNewConversationPromptCWD(m.cwd)
				if !m.remote {
					reloadMessageHistory = m.updateMessageHistoryScope(m.cwd)
				}
			}
			if len(m.entries) == 0 && len(msg.entries) > 0 {
				m.clearActiveAssistantEntry()
				m.entries = msg.entries
				m.usage = msg.usage
				m.status = fmt.Sprintf("resumed %s", shortID(m.conversationID))
				m.prependMessageHistoryTexts(userMessagesFromEntries(msg.entries))
			}
		}
		if reloadSlashCommands {
			cmds = append(cmds, loadSlashCommandsForConversation(m.ctx, state.key, m.slashCommandCWD()))
		}
		if reloadMessageHistory != nil {
			cmds = append(cmds, reloadMessageHistory)
		}
		if msg.loaded && wasInitialHistoryPending && !m.running && m.remote {
			remoteQueuedSubmit = strings.TrimSpace(queuedHistoryMessage)
			remoteQueuedSubmitPreserveComposer = queuedHistoryPreserveComposer
		} else if msg.loaded && wasInitialHistoryPending && !m.running {
			m.extensionLifecyclePending = true
			if strings.TrimSpace(queuedHistoryMessage) != "" {
				m.submitAfterExtensionLifecycle = queuedHistoryMessage
				m.submitAfterExtensionLifecyclePreserveComposer = queuedHistoryPreserveComposer
			}
			lifecycleBroker := newTUIUIBrokerForConversation(m.runCh, 0, state.key)
			lifecycleCtx := contextWithTUIConversation(m.ctx, state.key)
			lifecycleCtx = extensions.ContextWithUIInputBroker(lifecycleCtx, lifecycleBroker)
			cmds = append(cmds, closeTUIBrokerAfter(
				startExtensionLifecycleForConversation(lifecycleCtx, state.key, m.cwd, m.conversationID, msg.provider, msg.model, msg.profile, m.extensionRuntimes),
				lifecycleBroker,
			))
		}
		m.conversationState = currentState
		if remoteQueuedSubmit != "" {
			if remoteQueuedSubmitPreserveComposer {
				cmds = append(cmds, m.startConversationRunPreservingComposer(state, remoteQueuedSubmit))
			} else {
				cmds = append(cmds, m.startConversationRun(state, remoteQueuedSubmit))
			}
		} else if msg.loaded && m.remote {
			cmds = append(cmds, m.startConversationStream(state))
		}
		if active {
			m.refreshViewport(true)
		}

	case conversationHistoryRefreshMsg:
		history := msg.history
		state := m.stateForKey(history.conversationKey)
		if state == nil || state.streamRunID != msg.runID || state.streamTurn != msg.turn || state.running || history.err != nil || !history.loaded {
			break
		}
		if strings.TrimSpace(history.conversationID) != "" && strings.TrimSpace(history.conversationID) != strings.TrimSpace(state.conversationID) {
			break
		}
		active := state == m.conversationState
		currentState := m.conversationState
		m.conversationState = state
		m.clearActiveAssistantEntry()
		m.entries = history.entries
		m.usage = history.usage
		if strings.TrimSpace(history.title) != "" {
			m.title = strings.TrimSpace(history.title)
		}
		if !history.updatedAt.IsZero() {
			m.updatedAt = history.updatedAt
		}
		if strings.TrimSpace(history.cwd) != "" {
			m.cwd = strings.TrimSpace(history.cwd)
		}
		if strings.TrimSpace(history.profile) != "" {
			m.setProfile(history.profile)
		}
		if strings.TrimSpace(history.reasoningEffort) != "" {
			m.reasoningEffortOptions = []string{history.reasoningEffort}
			m.setReasoningEffort(history.reasoningEffort, false)
		}
		m.conversationState = currentState
		if active {
			m.refreshViewport(true)
		}

	case extensionLifecycleMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil || strings.TrimSpace(msg.conversationID) != strings.TrimSpace(state.conversationID) {
			break
		}
		active := state == m.conversationState
		currentState := m.conversationState
		m.conversationState = state
		m.extensionLifecyclePending = false
		if msg.err != nil {
			m.slashCommandErr = msg.err
		}
		queuedMessage := m.submitAfterExtensionLifecycle
		queuedPreserveComposer := m.submitAfterExtensionLifecyclePreserveComposer
		m.submitAfterExtensionLifecycle = ""
		m.submitAfterExtensionLifecyclePreserveComposer = false
		if strings.TrimSpace(queuedMessage) != "" {
			if queuedPreserveComposer {
				m.conversationState = currentState
				cmds = append(cmds, m.startConversationRunPreservingComposer(state, queuedMessage))
				m.conversationState = state
			} else if active {
				currentDraft := m.textarea.Value()
				m.textarea.SetValue(queuedMessage)
				cmds = append(cmds, m.submit())
				if strings.TrimSpace(currentDraft) != "" && currentDraft != queuedMessage {
					m.textarea.SetValue(currentDraft)
				}
			} else {
				m.conversationState = currentState
				cmds = append(cmds, m.startConversationRun(state, queuedMessage))
				m.conversationState = state
			}
		} else if m.status == "restoring extensions" {
			m.status = "ready"
		}
		m.conversationState = currentState

	case slashCommandsMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil {
			break
		}
		active := state == m.conversationState
		currentState := m.conversationState
		m.conversationState = state
		if strings.TrimSpace(msg.cwd) != strings.TrimSpace(m.slashCommandCWD()) {
			m.conversationState = currentState
			break
		}
		if msg.extensionsOnly {
			m.slashCommands = mergeSlashCommands(m.slashCommands, msg.commands)
			m.extensionShortcuts = effectiveExtensionShortcuts(m.ctx, msg.shortcuts)
		} else {
			m.slashCommands = msg.commands
			m.extensionShortcuts = nil
			if !m.extensionDiscoveryBlocked {
				cmds = append(cmds, loadExtensionSlashCommandsForConversation(m.ctx, state.key, m.slashCommandCWD(), m.extensionRuntimes))
			}
		}
		m.slashCommandErr = msg.err
		m.resetSlashCommandIndex()
		m.conversationState = currentState
		if active {
			m.resize()
			m.refreshViewport(false)
		}

	case extensionShortcutDoneMsg:
		call := m.shortcutCalls[msg.callID]
		delete(m.shortcutCalls, msg.callID)
		if call == nil {
			break
		}
		state := m.stateForKey(call.conversationKey)
		if state == nil {
			break
		}
		var promptFocusCmd tea.Cmd
		if state.activeUIPrompt != nil && state.activeUIPrompt.runID == msg.callID {
			promptFocusCmd = m.resolveUIPromptForState(state, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
		}
		if msg.err == nil && msg.result != nil {
			switch msg.result.Action {
			case extensions.ShortcutActionSubmit:
				if strings.TrimSpace(msg.result.Message) == "" {
					msg.err = errors.New("extension shortcut returned an empty submit message")
				}
			default:
				msg.err = errors.Errorf("extension shortcut returned unknown action %q", msg.result.Action)
			}
		}
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			if state != m.conversationState {
				state.unread = true
			}
			message := fmt.Sprintf("%s: %v", formatShortcutKey(msg.key), msg.err)
			if extensionID := strings.TrimSpace(msg.extensionID); extensionID != "" {
				message = fmt.Sprintf("%s (%s): %v", formatShortcutKey(msg.key), extensionID, msg.err)
			}
			notificationCmd := m.addUINotification(uiNotification{
				conversationKey: state.key,
				level:           uiNotificationError,
				title:           "Extension shortcut failed",
				message:         message,
			})
			return m, tea.Batch(promptFocusCmd, notificationCmd)
		}
		if !msg.matched && !m.remote {
			return m, tea.Batch(promptFocusCmd, loadExtensionSlashCommandsForConversation(m.ctx, state.key, slashCommandCWDForState(state), m.extensionRuntimes))
		}
		if msg.err == nil && msg.result != nil && msg.result.Action == extensions.ShortcutActionSubmit {
			return m, tea.Batch(promptFocusCmd, m.submitShortcutMessage(state, msg.result.Message))
		}
		return m, promptFocusCmd

	case messageHistoryMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil {
			break
		}
		currentState := m.conversationState
		m.conversationState = state
		if strings.TrimSpace(msg.scopeCWD) != strings.TrimSpace(m.messageHistoryScopeCWD) {
			m.conversationState = currentState
			break
		}
		if msg.err != nil {
			m.err = msg.err
			m.status = "history unavailable"
			m.conversationState = currentState
			break
		}
		m.appendMessageHistoryTexts(msg.messages)
		m.conversationState = currentState

	case conversationListMsg:
		m.applyConversationList(msg)
		return m, nil

	case tea.PasteMsg:
		if m.shortcutsOpen {
			return m, nil
		}
		if m.activeUIPrompt != nil {
			break
		}
		if m.conversationPicker != nil {
			m.appendConversationPickerQuery(normalizeSingleLinePaste(msg.Content))
			return m, nil
		}
		if key, ok := m.focusedExtensionSurfaceKey(); ok {
			surface := m.extensionSurfaces[key]
			// Bubble Tea v1 wrapped pasted key strings in brackets. Preserve that
			// public surface-input representation while carrying raw text separately.
			return m, m.nextExtensionSurfaceInputCmd(key, surface, extensions.UISurfaceInputKey, "["+msg.Content+"]", msg.Content, false, false, false, nil)
		}
		if m.historySearch != nil {
			m.appendHistorySearchQuery(normalizeSingleLinePaste(msg.Content))
			m.resize()
			return m, nil
		}

	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" && m.remote {
			m.cancel()
			return m, tea.Quit
		}
		if m.shortcutsOpen {
			switch key {
			case "ctrl+l":
				return m, m.openConversationPicker("")
			case "esc", "enter", "?", "q", "Q", "ctrl+c", "ctrl+d":
				oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
				var oldFocus tuiExtensionSurface
				if oldFocused {
					oldFocus = m.extensionSurfaces[oldFocusKey]
				}
				m.shortcutsOpen = false
				m.refreshViewport(false)
				return m, tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
			default:
				return m, nil
			}
		}
		if m.activeUIPrompt != nil {
			cmd := m.updateUIPromptKey(msg)
			return m, cmd
		}
		if m.conversationPicker != nil {
			cmd := m.updateConversationPickerKey(msg)
			return m, cmd
		}
		if key == "ctrl+l" {
			return m, m.openConversationPicker("")
		}
		if key != "ctrl+c" && key != "ctrl+d" {
			if cmd, handled := m.routeExtensionSurfaceKey(msg); handled {
				return m, cmd
			}
		}
		if m.historySearch != nil {
			m.updateHistorySearchKey(msg)
			m.resize()
			return m, nil
		}
		if shortcut, ok := m.extensionShortcutForKey(msg.Keystroke()); ok {
			return m, m.startExtensionShortcut(shortcut)
		}
		switch key {
		case "ctrl+c", "ctrl+d":
			if m.running {
				if key == "ctrl+d" {
					return m, nil
				}
				if m.runCancelling {
					m.cancelAllRuns()
					m.quitAfterRun = true
					m.status = "exiting"
					m.refreshViewport(true)
					return m, nil
				}
				return m, m.cancelActiveRun()
			}
			m.cancel()
			return m, tea.Quit
		case "esc":
			if m.historySearch != nil {
				m.cancelHistorySearch()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.slashCommandSuggestionsOpen() {
				m.dismissSlashCommandSuggestions()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.closeProfilePicker()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.reasoningPickerOpen {
				m.closeReasoningPicker()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.running && !m.runCancelling && !m.remote {
				return m, m.cancelActiveRun()
			}
			return m, nil
		case "ctrl+t":
			if m.canChangeProfile() {
				m.toggleProfilePickerFromKeyboard()
				m.resize()
				m.refreshViewport(false)
			}
			return m, nil
		case "ctrl+y":
			if m.canChangeReasoningEffort() {
				m.toggleReasoningPickerFromKeyboard()
				m.resize()
				m.refreshViewport(false)
			}
			return m, nil
		case "ctrl+o":
			m.toggleAllDetails()
			m.resize()
			m.refreshViewport(false)
			return m, nil
		case "ctrl+g":
			if cmd := m.openComposerInEditor(); cmd != nil {
				return m, cmd
			}
			return m, nil
		case "ctrl+r":
			m.openHistorySearch()
			m.resize()
			m.refreshViewport(false)
			return m, nil
		case "?":
			if strings.TrimSpace(m.textarea.Value()) == "" {
				return m, m.openShortcutsDialog()
			}
		case "up", "shift+tab":
			if m.slashCommandSuggestionsOpen() {
				m.moveSlashCommandSelection(-1)
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.moveProfilePicker(-1)
				m.refreshViewport(false)
				return m, nil
			}
			if m.reasoningPickerOpen {
				m.moveReasoningPicker(-1)
				m.refreshViewport(false)
				return m, nil
			}
		case "down", "tab":
			if m.slashCommandSuggestionsOpen() {
				if key == "tab" {
					m.selectSlashCommand()
					m.resize()
					m.refreshViewport(false)
				} else {
					m.moveSlashCommandSelection(1)
					m.refreshViewport(false)
				}
				return m, nil
			}
			if m.profilePickerOpen {
				m.moveProfilePicker(1)
				m.refreshViewport(false)
				return m, nil
			}
			if m.reasoningPickerOpen {
				m.moveReasoningPicker(1)
				m.refreshViewport(false)
				return m, nil
			}
		case "enter":
			if cmd, handled := m.handleLocalSlashCommand(strings.TrimSpace(m.textarea.Value())); handled {
				return m, cmd
			}
			if m.slashCommandSuggestionsOpen() {
				m.selectSlashCommand()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.selectProfilePickerOption(m.profilePickerIndex)
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.reasoningPickerOpen {
				m.selectReasoningPickerOption(m.reasoningPickerIndex)
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.running {
				if m.runCancelling {
					return m, nil
				}
				return m, m.submitSteering()
			}
			if cmd := m.submit(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}

	case tea.MouseMsg:
		mouse := msg.Mouse()
		action := mouseActionFor(msg)
		if m.shortcutsOpen {
			if action == tuiMouseActionPress && mouse.Button == tea.MouseLeft {
				oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
				var oldFocus tuiExtensionSurface
				if oldFocused {
					oldFocus = m.extensionSurfaces[oldFocusKey]
				}
				m.shortcutsOpen = false
				m.refreshViewport(false)
				return m, tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
			}
			return m, nil
		}
		if m.activeUIPrompt != nil {
			return m, nil
		}
		if m.conversationPicker != nil {
			return m, nil
		}
		if cmd, handled := m.routeExtensionSurfaceMouse(msg); handled {
			return m, cmd
		}
		if m.routeExtensionWidgetMouse(msg) {
			return m, nil
		}
		if action == tuiMouseActionPress && mouse.Button == tea.MouseLeft {
			if optionIndex, ok := m.profilePickerOptionAt(mouse.X, mouse.Y); ok {
				m.selectProfilePickerOption(optionIndex)
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profileComposerRegionContains(mouse.X, mouse.Y) {
				m.toggleProfilePickerFromClick()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if optionIndex, ok := m.reasoningPickerOptionAt(mouse.X, mouse.Y); ok {
				m.selectReasoningPickerOption(optionIndex)
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.reasoningComposerRegionContains(mouse.X, mouse.Y) {
				m.toggleReasoningPickerFromClick()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.profilePickerOpen {
				m.closeProfilePicker()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.reasoningPickerOpen {
				m.closeReasoningPicker()
				m.resize()
				m.refreshViewport(false)
				return m, nil
			}
			if m.toggleDetailAt(mouse.Y) {
				m.refreshViewport(false)
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.updateViewport(msg, &cmd)
		return m, cmd

	case chatEventMsg:
		run := m.runs[msg.runID]
		state := m.stateForRun(msg.runID)
		if state == nil {
			return m, waitForMsg(m.runCh)
		}
		observed := run != nil && run.observed
		if run != nil && msg.event.Cancelled {
			run.cancelled = true
		}
		if observed && state.streamRunID != msg.runID {
			return m, waitForMsg(m.runCh)
		}
		terminal := msg.event.Kind == "done" || msg.event.Kind == "error"
		if observed && observedChatEventStartsRun(msg.event) {
			m.beginObservedConversationRun(state, msg.runID)
		}
		if state.runCancelling && (!observed || !terminal) {
			return m, waitForMsg(m.runCh)
		}
		active := state == m.conversationState
		if prompt, ok := promptFromChatEvent(msg.event); ok {
			prompt.runID = msg.runID
			cmd := m.openUIPromptForState(state, prompt)
			return m, tea.Batch(waitForMsg(m.runCh), cmd)
		}
		if msg.event.Kind == "ui-notify" || msg.event.Kind == "ui-notification" {
			if msg.event.UINotify == nil {
				return m, waitForMsg(m.runCh)
			}
			if !active {
				state.unread = true
			}
			cmd := m.addUINotification(uiNotification{conversationKey: state.key, title: msg.event.UINotify.Title, message: msg.event.UINotify.Message})
			return m, tea.Batch(waitForMsg(m.runCh), cmd)
		}
		if strings.TrimSpace(msg.event.ConversationID) != "" {
			m.setConversationID(state, msg.event.ConversationID)
		}
		currentState := m.conversationState
		m.conversationState = state
		m.applyChatEvent(msg.event)
		m.updatedAt = time.Now()
		if !active && chatEventMarksConversationUnread(msg.event) {
			m.unread = true
		}
		m.conversationState = currentState
		if observed && terminal {
			nextCmd, quit := m.finishObservedConversationRun(state, msg.runID, msg.event)
			if quit {
				return m, tea.Quit
			}
			return m, tea.Batch(waitForMsg(m.runCh), nextCmd)
		}
		if !active {
			return m, waitForMsg(m.runCh)
		}
		if shouldDebounceChatEvent(msg.event) {
			return m, tea.Batch(waitForMsg(m.runCh), m.queueTranscriptRefresh(m.autoFollow))
		}
		m.refreshViewport(m.autoFollow)
		return m, waitForMsg(m.runCh)

	case conversationStreamDoneMsg:
		state := m.stateForKey(msg.conversationKey)
		if state == nil || state.streamRunID != msg.runID {
			return m, waitForMsg(m.runCh)
		}
		run := m.runs[msg.runID]
		if run == nil || !run.observed {
			return m, waitForMsg(m.runCh)
		}
		active := state == m.conversationState
		wasRunning := state.running && state.activeRunID == msg.runID
		wasCancelling := state.runCancelling
		var promptFocusCmd tea.Cmd
		if state.activeUIPrompt != nil && state.activeUIPrompt.origin == uiPromptExtension && state.activeUIPrompt.runID == msg.runID {
			promptFocusCmd = m.resolveUIPromptForState(state, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
		}
		state.streamRunID = 0
		state.cancelStream = nil
		delete(m.runs, msg.runID)
		if m.runByState[state.key] == msg.runID {
			delete(m.runByState, state.key)
		}
		if wasRunning {
			currentState := m.conversationState
			m.conversationState = state
			m.finishActiveBlocks()
			m.running = false
			m.runCancelling = false
			m.cancelRun = nil
			m.activeRunID = 0
			m.clearActiveAssistantEntry()
			m.updatedAt = time.Now()
			if wasCancelling || errors.Is(msg.err, context.Canceled) {
				m.status = "cancelled"
			} else {
				streamErr := msg.err
				if streamErr == nil {
					streamErr = errors.New("control-plane conversation stream ended")
				}
				m.err = streamErr
				m.status = "stream disconnected"
			}
			if !active {
				m.unread = true
			}
			m.conversationState = currentState
		} else if !errors.Is(msg.err, context.Canceled) {
			streamErr := msg.err
			if streamErr == nil {
				streamErr = errors.New("control-plane conversation stream ended")
			}
			if state.status != "error" {
				state.err = streamErr
				state.status = "stream disconnected"
			}
			if !active {
				state.unread = true
			}
		}
		if active {
			m.refreshViewport(state.autoFollow)
		}
		if m.quitAfterRun && wasRunning {
			m.quitAfterRun = false
			m.cancel()
			return m, tea.Quit
		}
		return m, tea.Batch(waitForMsg(m.runCh), promptFocusCmd)

	case chatDoneMsg:
		state := m.stateForRun(msg.runID)
		if state == nil {
			return m, waitForMsg(m.runCh)
		}
		active := state == m.conversationState
		run := m.runs[msg.runID]
		wasCancelled := state.runCancelling || errors.Is(msg.err, context.Canceled) || (run != nil && run.cancelled)
		if msg.conversationID != "" {
			m.setConversationID(state, msg.conversationID)
		}
		var promptFocusCmd tea.Cmd
		if state.activeUIPrompt != nil && state.activeUIPrompt.origin == uiPromptExtension && (state.activeUIPrompt.runID == 0 || state.activeUIPrompt.runID == msg.runID) {
			promptFocusCmd = m.resolveUIPromptForState(state, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
		}
		currentState := m.conversationState
		m.conversationState = state
		m.finishActiveBlocks()
		m.running = false
		m.runCancelling = false
		m.cancelRun = nil
		m.activeRunID = 0
		if msg.err != nil && !wasCancelled {
			m.err = msg.err
			m.status = "error"
			idx := m.ensureAssistantEntry()
			appendTextBlock(&m.entries[idx], fmt.Sprintf("Error: %v", msg.err))
		} else if wasCancelled {
			m.status = "cancelled"
		} else {
			m.status = "ready"
		}
		m.clearActiveAssistantEntry()
		m.updatedAt = time.Now()
		if !active {
			m.unread = true
		}
		runSucceeded := msg.err == nil && !wasCancelled
		queuedFollowUp := ""
		if runSucceeded && !m.quitAfterRun && len(m.queuedFollowUps) > 0 {
			queuedFollowUp = m.queuedFollowUps[0]
			m.queuedFollowUps = m.queuedFollowUps[1:]
		} else if !runSucceeded {
			m.queuedFollowUps = nil
		}
		m.conversationState = currentState
		if run != nil {
			delete(m.runByState, run.conversationKey)
		}
		delete(m.runs, msg.runID)
		delete(m.runByState, state.key)
		if active && queuedFollowUp == "" {
			m.refreshViewport(state.autoFollow)
		}
		if m.quitAfterRun {
			m.quitAfterRun = false
			m.cancel()
			return m, tea.Quit
		}
		if queuedFollowUp != "" {
			followUpCmd := m.startConversationRunPreservingComposer(state, queuedFollowUp)
			return m, tea.Batch(waitForMsg(m.runCh), promptFocusCmd, followUpCmd)
		}
		return m, tea.Batch(waitForMsg(m.runCh), promptFocusCmd, m.startConversationStream(state))

	case transcriptRefreshMsg:
		if m.pendingRefresh {
			m.refreshViewport(m.pendingRefreshBottom)
		}
		m.pendingRefresh = false
		m.pendingRefreshBottom = false
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		for _, state := range m.conversations {
			if state != nil && state.running {
				state.workingFrame++
			}
		}
		now := time.Now()
		// Live elapsed values are replaced in View without rebuilding the transcript.
		// Refresh only when a value outgrows the width reserved by its placeholder.
		if m.running && m.transcriptElapsedPlaceholderOverflow(now) {
			m.refreshViewport(m.autoFollow)
		}
		cmds = append(cmds, cmd)
	}

	if m.activeUIPrompt != nil {
		if m.activeUIPrompt.mode == uiPromptInput {
			var cmd tea.Cmd
			m.activeUIPrompt.input, cmd = m.activeUIPrompt.input.Update(msg)
			m.refreshViewport(false)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	previousTextareaValue := m.textarea.Value()
	previousSlashCommandHeight := m.slashCommandSuggestionsHeight()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	if m.textarea.Value() != previousTextareaValue {
		if m.slashDismissedDraft != "" && m.slashDismissedDraft != m.textarea.Value() {
			m.slashDismissedDraft = ""
		}
		m.slashCommandIndex = -1
	}
	if m.slashCommandSuggestionsHeight() != previousSlashCommandHeight {
		m.resize()
		m.refreshViewport(false)
	}

	if shouldUpdateViewport(msg) {
		var vpCmd tea.Cmd
		m.updateViewport(msg, &vpCmd)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) updateUIPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+d":
		return m.dismissUIPrompt()
	case "enter":
		return m.submitUIPrompt()
	case "up", "shift+tab", "left":
		if m.moveUISelect(-1) {
			m.refreshViewport(false)
			return nil
		}
	case "down", "tab", "right":
		if m.moveUISelect(1) {
			m.refreshViewport(false)
			return nil
		}
	case "y", "Y":
		if m.activeUIPrompt.mode == uiPromptConfirm {
			return m.submitUIPrompt()
		}
	case "n", "N":
		if m.activeUIPrompt.mode == uiPromptConfirm {
			return m.dismissUIPrompt()
		}
	}

	if m.activeUIPrompt.mode != uiPromptInput {
		return nil
	}
	var cmd tea.Cmd
	m.activeUIPrompt.input, cmd = m.activeUIPrompt.input.Update(msg)
	m.refreshViewport(false)
	return cmd
}

func shouldUpdateViewport(msg tea.Msg) bool {
	if isVerticalViewportNavigation(msg) {
		return true
	}

	if msg, ok := msg.(tea.MouseMsg); ok {
		return isHorizontalViewportMouseNavigation(msg)
	}

	return false
}

func (m *model) updateViewport(msg tea.Msg, cmd *tea.Cmd) {
	before := m.viewport.YOffset()
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	if cmd != nil {
		*cmd = vpCmd
	}
	if before != m.viewport.YOffset() && isVerticalViewportNavigation(msg) {
		m.autoFollow = m.viewport.AtBottom()
	}
}

func isVerticalViewportNavigation(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "pgup", "pgdown":
			return true
		}
	case tea.MouseMsg:
		mouse := msg.Mouse()
		if mouseActionFor(msg) != tuiMouseActionPress || mouse.Mod.Contains(tea.ModShift) {
			return false
		}
		return mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown
	}
	return false
}

func isHorizontalViewportMouseNavigation(msg tea.MouseMsg) bool {
	if mouseActionFor(msg) != tuiMouseActionPress {
		return false
	}
	mouse := msg.Mouse()
	switch mouse.Button {
	case tea.MouseWheelLeft, tea.MouseWheelRight:
		return true
	case tea.MouseWheelUp, tea.MouseWheelDown:
		return mouse.Mod.Contains(tea.ModShift)
	default:
		return false
	}
}

type tuiMouseAction uint8

const (
	tuiMouseActionUnknown tuiMouseAction = iota
	tuiMouseActionPress
	tuiMouseActionRelease
	tuiMouseActionMotion
)

func mouseActionFor(msg tea.MouseMsg) tuiMouseAction {
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseWheelMsg:
		return tuiMouseActionPress
	case tea.MouseReleaseMsg:
		return tuiMouseActionRelease
	case tea.MouseMotionMsg:
		return tuiMouseActionMotion
	default:
		return tuiMouseActionUnknown
	}
}

func (m *model) queueTranscriptRefresh(scrollBottom bool) tea.Cmd {
	m.pendingRefreshBottom = m.pendingRefreshBottom || scrollBottom
	if m.pendingRefresh {
		return nil
	}
	m.pendingRefresh = true
	return waitForTranscriptRefresh()
}

func (m *model) openShortcutsDialog() tea.Cmd {
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}
	m.profilePickerOpen = false
	m.reasoningPickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.shortcutsOpen = true
	m.resize()
	m.refreshViewport(false)
	return tea.Sequence(m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
}

func (m *model) openComposerInEditor() tea.Cmd {
	if m.running {
		return m.notifyEditorWarning("Cannot edit in $EDITOR while Kodelet is running.")
	}

	editorCommand := strings.TrimSpace(os.Getenv("EDITOR"))
	if editorCommand == "" {
		editorCommand = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if editorCommand == "" {
		return m.notifyEditorWarning("Set $EDITOR or $VISUAL to use Ctrl+G.")
	}

	path, err := writeComposerEditorFile(m.textarea.Value())
	if err != nil {
		return m.notifyEditorError("Failed to prepare $EDITOR: " + err.Error())
	}

	cmd, err := editorExecCommand(editorCommand, path)
	if err != nil {
		_ = os.Remove(path)
		return m.notifyEditorError("Failed to launch $EDITOR: " + err.Error())
	}
	m.profilePickerOpen = false
	m.reasoningPickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.shortcutsOpen = false
	m.steerError = ""
	m.status = "editing"
	m.refreshViewport(false)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorFinishedMsg{path: path, err: err}
	})
}

func (m *model) notifyEditorError(message string) tea.Cmd {
	m.steerError = ""
	return m.addUINotification(uiNotification{level: uiNotificationError, title: "Editor failed", message: message})
}

func (m *model) notifyEditorWarning(message string) tea.Cmd {
	m.steerError = ""
	return m.addUINotification(uiNotification{level: uiNotificationWarning, title: "Editor unavailable", message: message})
}

func writeComposerEditorFile(value string) (string, error) {
	file, err := os.CreateTemp("", "kodelet-composer-*.md")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(value); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func editorExecCommand(editorCommand, path string) (*exec.Cmd, error) {
	parts, err := shlex.Split(editorCommand)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return nil, errors.New("empty editor command")
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, path)
	cmd := exec.Command(parts[0], args...) //nolint:gosec // user-provided editor command is intentional.
	return cmd, nil
}

func (m *model) applyEditorResult(msg editorFinishedMsg) tea.Cmd {
	defer func() { _ = os.Remove(msg.path) }()

	if msg.err != nil {
		m.status = "ready"
		if errors.Is(msg.err, exec.ErrNotFound) {
			return m.notifyEditorWarning("Editor command not found: " + msg.err.Error())
		}
		return m.notifyEditorError(msg.err.Error())
	}

	content, err := os.ReadFile(filepath.Clean(msg.path))
	if err != nil {
		m.status = "ready"
		return m.notifyEditorError("Failed to read edited draft: " + err.Error())
	}

	m.status = "ready"
	m.steerError = ""
	m.textarea.SetValue(strings.TrimRight(string(content), "\n"))
	m.resize()
	m.refreshViewport(false)
	return textarea.Blink
}

func shouldDebounceChatEvent(event chat.ChatEvent) bool {
	switch event.Kind {
	case "text-delta", "thinking-delta", "tool-update":
		return true
	default:
		return false
	}
}

func chatEventMarksConversationUnread(event chat.ChatEvent) bool {
	switch event.Kind {
	case "conversation", "usage":
		return false
	default:
		return true
	}
}

func observedChatEventStartsRun(event chat.ChatEvent) bool {
	switch event.Kind {
	case "user-message":
		return true
	case "conversation":
		return strings.TrimSpace(event.ConversationName) == ""
	default:
		return false
	}
}

func (m *model) beginObservedConversationRun(state *conversationState, runID int) {
	if m == nil || state == nil || runID == 0 || state.streamRunID != runID {
		return
	}
	run := m.runs[runID]
	if run == nil || !run.observed || (state.running && state.activeRunID != runID) {
		return
	}
	if state.running {
		return
	}
	state.running = true
	state.runCancelling = false
	state.activeRunID = runID
	state.workingFrame = 0
	state.cancelRun = nil
	state.streamTurn++
	state.status = "working"
	state.err = nil
	state.updatedAt = time.Now()
	m.runByState[state.key] = runID
}

func (m *model) finishObservedConversationRun(state *conversationState, runID int, event chat.ChatEvent) (tea.Cmd, bool) {
	if m == nil || state == nil || runID == 0 || state.streamRunID != runID {
		return nil, false
	}
	active := state == m.conversationState
	wasRunning := state.running && state.activeRunID == runID
	wasCancelled := state.runCancelling || event.Cancelled
	var promptFocusCmd tea.Cmd
	if state.activeUIPrompt != nil && state.activeUIPrompt.origin == uiPromptExtension && state.activeUIPrompt.runID == runID {
		promptFocusCmd = m.resolveUIPromptForState(state, extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
	}

	currentState := m.conversationState
	m.conversationState = state
	if wasRunning {
		m.finishActiveBlocks()
		m.running = false
		m.runCancelling = false
		m.cancelRun = nil
		m.activeRunID = 0
		if event.Kind == "error" && !wasCancelled {
			message := strings.TrimSpace(event.Error)
			if message == "" {
				message = "remote conversation failed"
			}
			m.err = errors.New(message)
			m.status = "error"
		} else if wasCancelled {
			m.status = "cancelled"
		} else {
			m.status = "ready"
		}
		m.clearActiveAssistantEntry()
		m.updatedAt = time.Now()
	} else if event.Kind == "error" {
		message := strings.TrimSpace(event.Error)
		if message == "" {
			message = "remote conversation failed"
		}
		m.err = errors.New(message)
		m.status = "error"
	}
	turn := m.streamTurn
	runSucceeded := event.Kind == "done" && !wasCancelled
	queuedFollowUp := ""
	if wasRunning && runSucceeded && !m.quitAfterRun && len(m.queuedFollowUps) > 0 {
		queuedFollowUp = m.queuedFollowUps[0]
		m.queuedFollowUps = m.queuedFollowUps[1:]
	} else if wasRunning && !runSucceeded {
		m.queuedFollowUps = nil
	}
	m.conversationState = currentState
	if m.runByState[state.key] == runID {
		delete(m.runByState, state.key)
	}
	if active && queuedFollowUp == "" {
		m.refreshViewport(state.autoFollow)
	}
	if m.quitAfterRun && wasRunning {
		m.quitAfterRun = false
		m.cancel()
		return promptFocusCmd, true
	}
	if queuedFollowUp != "" {
		return tea.Batch(promptFocusCmd, m.startConversationRunPreservingComposer(state, queuedFollowUp)), false
	}
	if runSucceeded {
		return tea.Batch(promptFocusCmd, refreshConversationHistoryFromSource(m.ctx, state.key, state.conversationID, runID, turn, m.conversationSource)), false
	}
	return promptFocusCmd, false
}

func (m *model) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	inputOuterHeight := inputHeight + 2
	historySearchHeight := m.historySearchHeight()
	slashCommandHeight := m.slashCommandSuggestionsHeight()
	settingsPickerHeight := m.profilePickerHeight() + m.reasoningPickerHeight()
	extensionWidgetHeight := m.extensionWidgetsHeight(extensions.UIWidgetPlacementAboveComposer) + m.extensionWidgetsHeight(extensions.UIWidgetPlacementBelowComposer)
	footerHeight := 0
	viewportHeight := m.height - inputOuterHeight - historySearchHeight - slashCommandHeight - settingsPickerHeight - extensionWidgetHeight - footerHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport.SetWidth(m.contentWidth())
	m.viewport.SetHeight(viewportHeight)
	m.textarea.SetWidth(max(1, m.inputContentWidth()))
	m.textarea.SetHeight(inputHeight)
	if m.activeUIPrompt != nil && m.activeUIPrompt.mode == uiPromptInput {
		m.activeUIPrompt.input.SetWidth(uiPromptTextInputWidth(m.uiDialogInputWidth()))
	}
}

func (m *model) submit() tea.Cmd {
	message := strings.TrimSpace(m.textarea.Value())
	if message == "" {
		return nil
	}
	if cmd, handled := m.handleLocalSlashCommand(message); handled {
		return cmd
	}
	if m.running {
		return nil
	}
	if m.initialHistoryPending && m.deferSubmitUntilHistory {
		if strings.TrimSpace(m.submitAfterHistoryLoad) == "" {
			m.submitAfterHistoryLoad = message
			m.submitAfterHistoryLoadPreserveComposer = false
		}
		m.status = "loading conversation"
		return nil
	}
	if m.extensionLifecyclePending {
		if strings.TrimSpace(m.submitAfterExtensionLifecycle) == "" {
			m.submitAfterExtensionLifecycle = message
			m.submitAfterExtensionLifecyclePreserveComposer = false
		}
		m.status = "restoring extensions"
		return nil
	}
	return m.startConversationRun(m.conversationState, message)
}

func (m *model) submitShortcutMessage(state *conversationState, message string) tea.Cmd {
	message = strings.TrimSpace(message)
	if state == nil || message == "" {
		return nil
	}
	active := state == m.conversationState
	if state.running {
		state.queuedFollowUps = append(state.queuedFollowUps, message)
		state.steerError = ""
		state.status = "command queued"
		if active {
			m.refreshViewport(true)
		}
		return nil
	}
	if state.initialHistoryPending && state.deferSubmitUntilHistory {
		if strings.TrimSpace(state.submitAfterHistoryLoad) == "" {
			state.submitAfterHistoryLoad = message
			state.submitAfterHistoryLoadPreserveComposer = true
			state.status = "loading conversation"
		} else {
			state.queuedFollowUps = append(state.queuedFollowUps, message)
			state.status = "command queued"
		}
		return nil
	}
	if state.extensionLifecyclePending {
		if strings.TrimSpace(state.submitAfterExtensionLifecycle) == "" {
			state.submitAfterExtensionLifecycle = message
			state.submitAfterExtensionLifecyclePreserveComposer = true
			state.status = "restoring extensions"
		} else {
			state.queuedFollowUps = append(state.queuedFollowUps, message)
			state.status = "command queued"
		}
		return nil
	}
	return m.startConversationRunPreservingComposer(state, message)
}

func (m *model) startConversationRun(state *conversationState, message string) tea.Cmd {
	return m.startConversationRunWithComposer(state, message, true)
}

func (m *model) startConversationRunPreservingComposer(state *conversationState, message string) tea.Cmd {
	return m.startConversationRunWithComposer(state, message, false)
}

func (m *model) startConversationRunWithComposer(state *conversationState, message string, clearComposer bool) tea.Cmd {
	message = strings.TrimSpace(message)
	if state == nil || message == "" || state.running {
		return nil
	}
	active := state == m.conversationState
	conversationID := m.ensureConversationID(state)
	if conversationID == "" {
		return nil
	}
	m.stopConversationStream(state)
	conversationKey := state.key
	if strings.TrimSpace(state.title) == "" {
		state.title = conversations.NormalizeConversationName(userDisplayMessage(message))
	}
	state.updatedAt = time.Now()

	currentState := m.conversationState
	m.conversationState = state
	m.profilePickerOpen = false
	m.reasoningPickerOpen = false
	if clearComposer {
		if active {
			m.dismissSlashCommandSuggestions()
			m.textarea.Reset()
		} else {
			m.slashCommandIndex = -1
			m.slashDismissedDraft = ""
		}
		m.draft = ""
	}
	m.appendSubmittedMessageToHistory(message)
	persistMessageHistory := m.persistSubmittedMessageCommandForState(state, message)
	m.clearActiveAssistantEntry()
	m.entries = append(m.entries, chatEntry{kind: entryUser, content: userDisplayMessage(message)})
	m.running = true
	m.workingFrame = 0
	m.nextRunID++
	m.activeRunID = m.nextRunID
	m.status = "working"
	m.err = nil

	runCtx, cancel := context.WithCancel(contextWithTUIConversation(m.ctx, conversationKey))
	m.cancelRun = cancel
	runID := m.activeRunID
	turnID := ""
	if m.remote {
		turnID = convtypes.GenerateID()
	}
	if m.runs == nil {
		m.runs = map[int]*conversationRun{}
	}
	if m.runByState == nil {
		m.runByState = map[string]int{}
	}
	m.runs[runID] = &conversationRun{conversationKey: conversationKey, turnID: turnID, cancel: cancel}
	m.runByState[conversationKey] = runID
	m.conversationState = currentState
	if active {
		m.refreshViewport(true)
	}

	runCh := m.runCh
	runner := m.runner
	uiDone := m.ctx.Done()
	uiBroker := newTUIUIBrokerForConversation(runCh, runID, conversationKey)
	runCtx = extensions.ContextWithUIInputBroker(runCtx, uiBroker)
	req := chat.ChatRequest{
		Message:        message,
		ConversationID: conversationID,
		TurnID:         turnID,
		Profile:        profileForRequest(state.profile),
		CWD:            state.requestedCWD,
	}
	if !state.conversationWasResumed {
		req.ReasoningEffort = state.reasoningEffort
		if m.remote {
			req.EnvironmentProfile = m.environmentProfile
		}
	}

	return func() tea.Msg {
		if persistMessageHistory != nil {
			_ = persistMessageHistory()
		}
		go func() {
			defer uiBroker.close()
			conversationID, err := runner.Run(runCtx, req, tuiSink{ch: runCh, runID: runID, conversationKey: conversationKey, done: uiDone})
			select {
			case runCh <- chatDoneMsg{runID: runID, conversationKey: conversationKey, conversationID: strings.TrimSpace(conversationID), err: err}:
			case <-uiDone:
			}
		}()
		return nil
	}
}

func (m model) slashCommandQuery() (string, bool) {
	draft := strings.TrimLeft(m.textarea.Value(), " \t\r\n")
	if !strings.HasPrefix(draft, "/") {
		return "", false
	}
	withoutSlash := strings.TrimPrefix(draft, "/")
	if strings.ContainsAny(withoutSlash, " \t\r\n") {
		return "", false
	}
	return strings.ToLower(withoutSlash), true
}

func (m model) slashCommandSuggestionsOpen() bool {
	if m.profilePickerOpen || m.reasoningPickerOpen || m.historySearch != nil {
		return false
	}
	if m.textarea.Value() == m.slashDismissedDraft {
		return false
	}
	_, ok := m.slashCommandQuery()
	return ok && len(m.filteredSlashCommands()) > 0
}

func (m model) filteredSlashCommands() []slashcommands.Command {
	query, ok := m.slashCommandQuery()
	if !ok {
		return nil
	}
	commands := make([]slashcommands.Command, 0, len(m.slashCommands))
	for _, command := range m.slashCommands {
		name := strings.ToLower(command.Name)
		description := strings.ToLower(command.Description)
		if query == "" || strings.Contains(name, query) || strings.Contains(description, query) {
			commands = append(commands, command)
		}
	}
	return commands
}

func (m *model) resetSlashCommandIndex() {
	if len(m.filteredSlashCommands()) == 0 {
		m.slashCommandIndex = -1
		return
	}
	if m.slashCommandIndex >= len(m.filteredSlashCommands()) {
		m.slashCommandIndex = -1
	}
}

func (m *model) dismissSlashCommandSuggestions() {
	m.slashCommandIndex = -1
	m.slashDismissedDraft = m.textarea.Value()
}

func (m *model) moveSlashCommandSelection(delta int) {
	suggestions := m.filteredSlashCommands()
	if len(suggestions) == 0 {
		m.slashCommandIndex = -1
		return
	}
	next := m.slashCommandIndex + delta
	if delta > 0 {
		if next >= len(suggestions) {
			next = -1
		}
	} else if delta < 0 {
		if m.slashCommandIndex < 0 {
			next = len(suggestions) - 1
		} else if next < 0 {
			next = -1
		}
	}
	m.slashCommandIndex = next
}

func (m *model) selectSlashCommand() {
	suggestions := m.filteredSlashCommands()
	if len(suggestions) == 0 {
		return
	}
	index := m.slashCommandIndex
	if index < 0 || index >= len(suggestions) {
		index = 0
	}
	m.textarea.SetValue(insertSlashCommand(m.textarea.Value(), suggestions[index].Name))
	m.slashCommandIndex = -1
	m.slashDismissedDraft = ""
}

func insertSlashCommand(draft, commandName string) string {
	leadingWhitespaceLength := 0
	for leadingWhitespaceLength < len(draft) {
		switch draft[leadingWhitespaceLength] {
		case ' ', '\t', '\r', '\n':
			leadingWhitespaceLength++
		default:
			return draft[:leadingWhitespaceLength] + "/" + strings.TrimSpace(commandName) + " "
		}
	}
	return draft[:leadingWhitespaceLength] + "/" + strings.TrimSpace(commandName) + " "
}

func userDisplayMessage(message string) string {
	command, args, found := slashcommands.Parse(message)
	if !found {
		return strings.TrimSpace(message)
	}

	update, handled, err := goals.ParseSlashCommand(command, args, time.Now())
	if handled && err == nil {
		return update.Display
	}

	return strings.TrimSpace(message)
}

func mergeSlashCommands(base, additions []slashcommands.Command) []slashcommands.Command {
	merged := make([]slashcommands.Command, 0, len(base)+len(additions))
	seen := map[string]struct{}{}
	for _, command := range base {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, command)
	}
	for _, command := range additions {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, command)
	}
	return merged
}

func (m *model) submitSteering() tea.Cmd {
	message := strings.TrimSpace(m.textarea.Value())
	if message == "" {
		return nil
	}
	if _, _, found := slashcommands.Parse(message); found {
		m.queueFollowUpCommand(message)
		return nil
	}

	if len(message) > steer.MaxMessageLength {
		m.err = errors.New("steering message too long")
		m.steerError = "Steering message must be less than 10,000 characters."
		m.status = "steering failed"
		m.refreshViewport(true)
		return nil
	}

	if strings.TrimSpace(m.conversationID) == "" {
		m.conversationID = convtypes.GenerateID()
	}
	if m.remote {
		controller, ok := m.runner.(interface {
			SteerConversation(context.Context, string, string, []string) (bool, error)
		})
		if !ok {
			m.err = errors.New("remote chat runner does not support steering")
			m.steerError = "Remote steering is unavailable."
			m.status = "steering failed"
			m.refreshViewport(true)
			return nil
		}
		conversationID := m.conversationID
		conversationKey := m.activeConversationKey
		m.textarea.Reset()
		m.steerError = ""
		m.status = "queuing steering"
		m.refreshViewport(true)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), 5*time.Second)
			defer cancel()
			queued, err := controller.SteerConversation(ctx, conversationID, message, nil)
			return remoteSteerMsg{conversationKey: conversationKey, message: message, queued: queued, err: err}
		}
	}

	steerStore, err := steer.NewSteerStore(m.ctx)
	if err != nil {
		m.err = errors.Wrap(err, "failed to initialize steer store")
		m.steerError = fmt.Sprintf("Failed to queue steering: %v", err)
		m.status = "steering failed"
		m.refreshViewport(true)
		return nil
	}
	defer steerStore.Close()

	if _, err := steerStore.Enqueue(m.ctx, m.conversationID, message, nil); err != nil {
		m.err = errors.Wrap(err, "failed to write steering message")
		m.steerError = fmt.Sprintf("Failed to queue steering: %v", err)
		m.status = "steering failed"
		m.refreshViewport(true)
		return nil
	}

	m.textarea.Reset()
	m.queuedSteering = append(m.queuedSteering, message)
	m.steerError = ""
	m.status = "steering queued"
	m.refreshViewport(true)
	return nil
}

func (m *model) queueFollowUpCommand(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	m.textarea.Reset()
	m.queuedFollowUps = append(m.queuedFollowUps, message)
	m.steerError = ""
	m.status = "command queued"
	m.refreshViewport(true)
}

func (m *model) stopRun() {
	if m.cancelRun != nil {
		m.cancelRun()
		m.cancelRun = nil
	}
}

func (m *model) cancelActiveRun() tea.Cmd {
	if m.runCancelling {
		return nil
	}
	var stopCmd tea.Cmd
	if m.remote && strings.TrimSpace(m.conversationID) != "" {
		conversationID := m.conversationID
		conversationKey := m.activeConversationKey
		turnID := ""
		if run := m.runs[m.activeRunID]; run != nil {
			turnID = strings.TrimSpace(run.turnID)
		}
		var stop func(context.Context) error
		if turnID != "" {
			if controller, ok := m.runner.(interface {
				StopConversationTurn(context.Context, string, string) error
			}); ok {
				stop = func(ctx context.Context) error {
					return controller.StopConversationTurn(ctx, conversationID, turnID)
				}
			}
		}
		if stop == nil {
			if controller, ok := m.runner.(interface {
				StopConversation(context.Context, string) error
			}); ok {
				stop = func(ctx context.Context) error {
					return controller.StopConversation(ctx, conversationID)
				}
			}
		}
		if stop != nil {
			cancelRun := m.cancelRun
			m.cancelRun = nil
			stopCmd = func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), 5*time.Second)
				err := stop(ctx)
				cancel()
				if cancelRun != nil {
					cancelRun()
				}
				return remoteStopMsg{conversationKey: conversationKey, err: err}
			}
		} else {
			m.stopRun()
		}
	} else {
		m.stopRun()
	}
	var focusCmd tea.Cmd
	if m.activeUIPrompt != nil {
		focusCmd = m.resolveUIPrompt(extensions.UIInputResponse{Status: extensions.UIInputStatusDismissed})
	}
	m.finishActiveBlocks()
	m.status = "cancelling"
	m.runCancelling = true
	m.running = true
	m.refreshViewport(true)
	return tea.Batch(focusCmd, stopCmd)
}

func (m *model) finishActiveBlocks() {
	for entryIdx := range m.entries {
		if m.entries[entryIdx].kind != entryAssistant {
			continue
		}
		for blockIdx := range m.entries[entryIdx].blocks {
			block := &m.entries[entryIdx].blocks[blockIdx]
			switch block.kind {
			case blockThoughts:
				for thoughtIdx := range block.thoughts {
					block.thoughts[thoughtIdx].done = true
				}
			case blockTools:
				for toolIdx := range block.tools {
					block.tools[toolIdx].done = true
				}
			}
		}
	}
}

func (m *model) insertTextareaNewline() tea.Cmd {
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}
