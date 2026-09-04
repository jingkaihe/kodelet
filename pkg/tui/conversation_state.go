package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

func contextWithTUIConversation(ctx context.Context, conversationKey string) context.Context {
	return extensions.ContextWithExtensionUIScope(ctx, conversationKey)
}

func tuiConversationKeyFromContext(ctx context.Context) string {
	return extensions.ExtensionUIScopeFromContext(ctx)
}

func newConversationState(key, conversationID string, resumed bool, defaults conversationDefaults) *conversationState {
	profile := displayProfile(defaults.profile)
	profileOptions := normalizeProfileOptions(append([]string(nil), defaults.profileOptions...), profile)
	profileIndex := profileOptionIndex(profileOptions, profile)
	if profileIndex < 0 {
		profileIndex = 0
	}

	reasoningEffort := normalizeReasoningEffort(defaults.reasoningEffort)
	reasoningOptions := normalizeReasoningEffortOptions(append([]string(nil), defaults.reasoningEffortOptions...), reasoningEffort)
	reasoningIndex := reasoningEffortOptionIndex(reasoningOptions, reasoningEffort)
	if reasoningIndex < 0 {
		reasoningIndex = 0
	}

	status := "ready"
	if resumed {
		status = "loading"
	}
	return &conversationState{
		key:                     strings.TrimSpace(key),
		conversationID:          strings.TrimSpace(conversationID),
		conversationWasResumed:  resumed,
		loaded:                  !resumed,
		profile:                 profile,
		profileOptions:          profileOptions,
		profileIndex:            profileIndex,
		profilePickerIndex:      profileIndex,
		reasoningEffort:         reasoningEffort,
		reasoningEffortOptions:  reasoningOptions,
		reasoningEffortIndex:    reasoningIndex,
		reasoningPickerIndex:    reasoningIndex,
		reasoningEffortExplicit: defaults.reasoningEffortExplicit,
		cwd:                     defaults.cwd,
		requestedCWD:            defaults.requestedCWD,
		slashCommands:           append([]slashcommands.Command(nil), defaults.slashCommands...),
		slashCommandIndex:       -1,
		autoFollow:              true,
		status:                  status,
	}
}

func (m *model) stateForKey(key string) *conversationState {
	if m == nil {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return m.conversationState
	}
	return m.conversations[key]
}

func (m *model) stateForRun(runID int) *conversationState {
	if m == nil || runID == 0 {
		return nil
	}
	if run := m.runs[runID]; run != nil {
		return m.conversations[run.conversationKey]
	}
	// Preserve compatibility with tests and injected runners that set the legacy
	// active fields directly without registering a run.
	if m.conversationState != nil && runID == m.activeRunID {
		return m.conversationState
	}
	return nil
}

func (m *model) saveActiveConversationPresentation() {
	if m == nil || m.conversationState == nil {
		return
	}
	m.draft = m.textarea.Value()
	m.viewportOffset = m.viewport.YOffset()
}

func (m *model) activateConversation(key string) (bool, tea.Cmd) {
	state := m.stateForKey(key)
	if state == nil {
		return false, nil
	}
	if m.conversationState == state {
		state.unread = false
		return true, nil
	}
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}

	m.saveActiveConversationPresentation()
	m.activeConversationKey = state.key
	m.conversationState = state
	state.unread = false
	m.pendingRefresh = false
	m.pendingRefreshBottom = false
	m.textarea.SetValue(state.draft)
	m.resize()
	cmds := m.updateExtensionSurfaceLayouts()
	cmds = append(cmds, m.extensionSurfaceFocusTransitionCommands(oldFocusKey, oldFocused, oldFocus)...)
	m.refreshViewport(false)
	if state.autoFollow {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(state.viewportOffset)
	}
	return true, tea.Sequence(cmds...)
}

func (m *model) createNewConversation() tea.Cmd {
	return m.createNewConversationAt("")
}

func (m *model) createNewConversationAt(cwd string) tea.Cmd {
	m.nextConversationKey++
	key := fmt.Sprintf("new:%d", m.nextConversationKey)
	defaults := m.conversationDefaults
	if m.remote {
		defaults.cwd = m.remoteDefaultCWD
		defaults.requestedCWD = ""
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		defaults.cwd = cwd
		defaults.requestedCWD = cwd
	}
	state := newConversationState(key, "", false, defaults)
	if !m.remote {
		state.messageHistoryScopeCWD, _ = messagehistory.ResolveScopeCWD(state.cwd)
	}
	state.extensionDiscoveryBlocked = m.remote
	m.conversations[key] = state
	_, activateCmd := m.activateConversation(key)
	cmds := []tea.Cmd{
		activateCmd,
		m.closeConversationPicker(),
		textarea.Blink,
	}
	if !m.remote {
		cmds = append(cmds,
			loadSlashCommandsForConversation(m.ctx, key, m.slashCommandCWD()),
			loadMessageHistoryForConversation(m.ctx, key, m.messageHistoryStore, state.messageHistoryScopeCWD),
		)
	}
	return tea.Batch(cmds...)
}

func (m *model) openNewConversationPrompt(initialCWD string) tea.Cmd {
	cancelButtonText := "Cancel"
	if m.conversationPicker != nil {
		cancelButtonText = "Back"
	}

	baseCWD := m.newConversationDefaultCWD()
	if m.remote {
		value := strings.TrimSpace(initialCWD)
		helpText := "Leave blank to use the execution environment's default working directory. Relative paths and ~ are resolved on the execution host."
		if strings.TrimSpace(baseCWD) != "" {
			helpText = fmt.Sprintf("Leave blank to use %s. Relative paths and ~ are resolved on the execution host.", displayCWD(baseCWD))
		}
		return m.openUIPrompt(uiPromptState{
			mode:                   uiPromptInput,
			origin:                 uiPromptNewConversation,
			title:                  "New conversation",
			message:                "Working directory",
			helpText:               helpText,
			defaultValue:           value,
			submitButtonText:       "Create",
			cancelButtonText:       cancelButtonText,
			newConversationCWDBase: baseCWD,
		})
	}

	value := strings.TrimSpace(initialCWD)
	if value == "" {
		value = displayCWD(baseCWD)
	}
	return m.openUIPrompt(uiPromptState{
		mode:                   uiPromptInput,
		origin:                 uiPromptNewConversation,
		title:                  "New conversation",
		message:                "Working directory",
		helpText:               "Relative paths are resolved from the current workspace.",
		defaultValue:           value,
		submitButtonText:       "Create",
		cancelButtonText:       cancelButtonText,
		required:               true,
		newConversationCWDBase: baseCWD,
	})
}

func (m model) newConversationDefaultCWD() string {
	if m.remote {
		return strings.TrimSpace(m.remoteDefaultCWD)
	}
	if !m.remote && m.conversationState != nil {
		if cwd := strings.TrimSpace(slashCommandCWDForState(m.conversationState)); cwd != "" {
			return cwd
		}
	}
	return strings.TrimSpace(m.conversationDefaults.cwd)
}

func (m *model) refreshNewConversationPromptCWD(cwd string) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return
	}

	for _, prompt := range []*uiPromptState{m.activeUIPrompt, m.suspendedUIPrompt} {
		if prompt == nil || prompt.origin != uiPromptNewConversation || prompt.mode != uiPromptInput {
			continue
		}
		inputUnchanged := prompt.input.Value() == prompt.defaultValue
		prompt.newConversationCWDBase = cwd
		if m.remote {
			continue
		}
		prompt.defaultValue = displayCWD(cwd)
		if inputUnchanged {
			prompt.input.SetValue(prompt.defaultValue)
		}
	}
}

func resolveNewConversationCWD(input, baseCWD string) (string, error) {
	processCWD, err := chat.ResolveConfiguredDefaultCWD("")
	if err != nil {
		return "", err
	}

	expandedBase := strings.TrimSpace(baseCWD)
	if expandedBase == "" {
		expandedBase = processCWD
	} else {
		expandedBase, err = chat.ExpandCWDInput(expandedBase, processCWD)
		if err != nil {
			return "", err
		}
		if normalizedBase, normalizeErr := conversations.NormalizeCWD(expandedBase); normalizeErr == nil {
			expandedBase = normalizedBase
		}
	}

	expandedCWD, err := chat.ExpandCWDInput(input, expandedBase)
	if err != nil {
		return "", err
	}
	return conversations.NormalizeCWD(expandedCWD)
}

func newConversationCWDErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	const missingDirectoryPrefix = "cwd directory does not exist:"
	if path, found := strings.CutPrefix(message, missingDirectoryPrefix); found {
		return "Directory does not exist:" + path
	}
	return message
}

func (m *model) ensureConversationID(state *conversationState) string {
	if m == nil || state == nil {
		return ""
	}
	if conversationID := strings.TrimSpace(state.conversationID); conversationID != "" {
		return conversationID
	}
	return m.setConversationID(state, convtypes.GenerateID())
}

func (m *model) setConversationID(state *conversationState, conversationID string) string {
	if m == nil || state == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return strings.TrimSpace(state.conversationID)
	}
	state.conversationID = conversationID
	return conversationID
}

func (m *model) startConversationStream(state *conversationState) tea.Cmd {
	if m == nil || state == nil || !m.remote || m.conversationStream == nil || state.running || state.streamRunID != 0 {
		return nil
	}
	conversationID := strings.TrimSpace(state.conversationID)
	if conversationID == "" || !state.loaded {
		return nil
	}

	m.nextRunID++
	runID := m.nextRunID
	conversationKey := state.key
	streamCtx, cancel := context.WithCancel(contextWithTUIConversation(m.ctx, conversationKey))
	uiBroker := newTUIUIBrokerForConversation(m.runCh, runID, conversationKey)
	streamCtx = extensions.ContextWithUIInputBroker(streamCtx, uiBroker)
	state.streamRunID = runID
	state.cancelStream = cancel
	m.runs[runID] = &conversationRun{
		conversationKey: conversationKey,
		cancel:          cancel,
		observed:        true,
	}

	streamer := m.conversationStream
	runCh := m.runCh
	uiDone := m.ctx.Done()
	return func() tea.Msg {
		go func() {
			defer uiBroker.close()
			err := streamer.StreamConversation(streamCtx, conversationID, tuiSink{
				ch:              runCh,
				runID:           runID,
				conversationKey: conversationKey,
				done:            streamCtx.Done(),
			})
			select {
			case runCh <- conversationStreamDoneMsg{runID: runID, conversationKey: conversationKey, err: err}:
			case <-uiDone:
			}
		}()
		return nil
	}
}

func (m *model) stopConversationStream(state *conversationState) {
	if m == nil || state == nil || state.streamRunID == 0 {
		return
	}
	runID := state.streamRunID
	if state.cancelStream != nil {
		state.cancelStream()
	}
	state.streamRunID = 0
	state.cancelStream = nil
	if run := m.runs[runID]; run != nil && run.observed {
		delete(m.runs, runID)
	}
	if m.runByState[state.key] == runID {
		delete(m.runByState, state.key)
	}
}

func (m *model) cancelAllRuns() {
	for _, run := range m.runs {
		if run != nil && run.cancel != nil {
			run.cancel()
		}
	}
}
