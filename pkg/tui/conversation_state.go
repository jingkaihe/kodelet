package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
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
	m.viewportOffset = m.viewport.YOffset
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
	m.nextConversationKey++
	key := fmt.Sprintf("new:%d", m.nextConversationKey)
	state := newConversationState(key, "", false, m.conversationDefaults)
	state.messageHistoryScopeCWD, _ = messagehistory.ResolveScopeCWD(state.cwd)
	m.conversations[key] = state
	_, activateCmd := m.activateConversation(key)
	return tea.Batch(
		activateCmd,
		m.closeConversationPicker(),
		loadSlashCommandsForConversation(m.ctx, key, m.slashCommandCWD()),
		loadMessageHistoryForConversation(m.ctx, key, m.messageHistoryStore, state.messageHistoryScopeCWD),
		textarea.Blink,
	)
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

func (m *model) cancelAllRuns() {
	for _, run := range m.runs {
		if run != nil && run.cancel != nil {
			run.cancel()
		}
	}
}
