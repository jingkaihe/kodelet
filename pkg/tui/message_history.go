package tui

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
)

func loadMessageHistory(ctx context.Context, store *messagehistory.Store, scopeCWD string) tea.Cmd {
	return func() tea.Msg {
		scopeCWD = strings.TrimSpace(scopeCWD)
		if store == nil || scopeCWD == "" {
			return messageHistoryMsg{scopeCWD: scopeCWD}
		}

		entries, err := store.List(ctx, scopeCWD, messagehistory.MaxEntriesPerScope)
		if err != nil {
			return messageHistoryMsg{scopeCWD: scopeCWD, err: err}
		}

		messages := make([]string, 0, len(entries))
		for _, entry := range entries {
			if text := strings.TrimSpace(entry.Text); text != "" {
				messages = append(messages, text)
			}
		}
		return messageHistoryMsg{scopeCWD: scopeCWD, messages: messages}
	}
}

func (m *model) updateMessageHistoryScope(cwd string) tea.Cmd {
	scopeCWD, err := messagehistory.ResolveScopeCWD(cwd)
	if err != nil || strings.TrimSpace(scopeCWD) == "" || scopeCWD == m.messageHistoryScopeCWD {
		return nil
	}
	m.messageHistoryScopeCWD = scopeCWD
	m.messageHistory = nil
	m.historySearch = nil
	return loadMessageHistory(m.ctx, m.messageHistoryStore, m.messageHistoryScopeCWD)
}

func (m *model) appendSubmittedMessageToHistory(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	if strings.TrimSpace(m.messageHistoryScopeCWD) == "" && !m.initialHistoryPending {
		if scopeCWD, err := messagehistory.ResolveScopeCWD(m.cwd); err == nil {
			m.messageHistoryScopeCWD = scopeCWD
		}
	}
	m.appendMessageHistoryTexts([]string{message})
}

func (m *model) persistSubmittedMessageCommand(message string) tea.Cmd {
	message = strings.TrimSpace(message)
	store := m.messageHistoryStore
	scopeCWD := strings.TrimSpace(m.messageHistoryScopeCWD)
	if store == nil || message == "" {
		return nil
	}
	conversationID := strings.TrimSpace(m.conversationID)
	profile := strings.TrimSpace(m.profile)

	return func() tea.Msg {
		if scopeCWD == "" {
			scopeCWD = resolveStoredMessageHistoryScope(context.Background(), conversationID)
		}
		if scopeCWD == "" {
			return nil
		}
		entry := messagehistory.Entry{
			CreatedAt:      time.Now().UTC(),
			ScopeCWD:       scopeCWD,
			ConversationID: conversationID,
			Profile:        profile,
			Source:         "tui",
			Text:           message,
		}
		_ = store.Append(context.Background(), entry)
		return nil
	}
}

func resolveStoredMessageHistoryScope(ctx context.Context, conversationID string) string {
	if strings.TrimSpace(conversationID) != "" {
		service, err := conversations.GetDefaultConversationService(ctx)
		if err == nil {
			response, loadErr := service.GetConversation(ctx, conversationID)
			_ = service.Close()
			if loadErr == nil && strings.TrimSpace(response.CWD) != "" {
				if scopeCWD, err := messagehistory.ResolveScopeCWD(response.CWD); err == nil {
					return scopeCWD
				}
			}
		}
	}
	return ""
}

func (m *model) prependMessageHistoryTexts(messages []string) {
	if len(messages) == 0 {
		return
	}
	merged := make([]string, 0, len(messages)+len(m.messageHistory))
	merged = appendMessageHistoryTexts(merged, messages)
	merged = appendMessageHistoryTexts(merged, m.messageHistory)
	m.messageHistory = trimMessageHistory(merged)
}

func (m *model) appendMessageHistoryTexts(messages []string) {
	m.messageHistory = appendMessageHistoryTexts(m.messageHistory, messages)
	m.messageHistory = trimMessageHistory(m.messageHistory)
}

func appendMessageHistoryTexts(history []string, messages []string) []string {
	for _, message := range messages {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		if len(history) > 0 && history[len(history)-1] == message {
			continue
		}
		history = append(history, message)
	}
	return history
}

func trimMessageHistory(history []string) []string {
	if len(history) <= messagehistory.MaxEntriesPerScope {
		return history
	}
	return history[len(history)-messagehistory.MaxEntriesPerScope:]
}

func userMessagesFromEntries(entries []chatEntry) []string {
	messages := make([]string, 0)
	for _, entry := range entries {
		if entry.kind != entryUser {
			continue
		}
		if text := strings.TrimSpace(entry.content); text != "" {
			messages = append(messages, text)
		}
	}
	return messages
}

func (m *model) openHistorySearch() {
	m.profilePickerOpen = false
	m.dismissSlashCommandSuggestions()
	m.shortcutsOpen = false
	m.historySearch = &historySearchState{originalDraft: m.textarea.Value()}
	m.applyHistorySearchQuery()
}

func (m *model) updateHistorySearchKey(msg tea.KeyMsg) bool {
	if m.historySearch == nil {
		return false
	}

	switch msg.String() {
	case "ctrl+r", "up":
		m.moveHistorySearchSelection(1)
		m.refreshViewport(false)
		return true
	case "down":
		m.moveHistorySearchSelection(-1)
		m.refreshViewport(false)
		return true
	case "enter":
		if m.acceptHistorySearch() {
			m.resize()
			m.refreshViewport(false)
		}
		return true
	case "esc", "ctrl+c":
		m.cancelHistorySearch()
		m.resize()
		m.refreshViewport(false)
		return true
	case "backspace", "ctrl+h":
		m.deleteHistorySearchRune()
		m.refreshViewport(false)
		return true
	case "ctrl+u":
		m.historySearch.query = ""
		m.applyHistorySearchQuery()
		m.refreshViewport(false)
		return true
	}

	if msg.Type == tea.KeySpace && !msg.Alt {
		m.historySearch.query += " "
		m.applyHistorySearchQuery()
		m.refreshViewport(false)
		return true
	}

	if msg.Type == tea.KeyRunes && !msg.Alt {
		m.historySearch.query += string(msg.Runes)
		m.applyHistorySearchQuery()
		m.refreshViewport(false)
		return true
	}

	return true
}

func (m *model) applyHistorySearchQuery() {
	if m.historySearch == nil {
		return
	}
	query := m.historySearch.query
	m.historySearch.matches = m.historySearchMatches(query)
	m.historySearch.selected = 0
	if len(m.historySearch.matches) == 0 {
		m.textarea.SetValue(m.historySearch.originalDraft)
		return
	}
	m.textarea.SetValue(m.historySearch.matches[0])
}

func (m *model) historySearchMatches(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	lowerQuery := strings.ToLower(query)
	seen := map[string]struct{}{}
	matches := make([]string, 0)
	for i := len(m.messageHistory) - 1; i >= 0; i-- {
		message := strings.TrimSpace(m.messageHistory[i])
		if message == "" || !strings.Contains(strings.ToLower(message), lowerQuery) {
			continue
		}
		if _, ok := seen[message]; ok {
			continue
		}
		seen[message] = struct{}{}
		matches = append(matches, message)
	}
	return matches
}

func (m *model) moveHistorySearchSelection(delta int) {
	if m.historySearch == nil || len(m.historySearch.matches) == 0 || delta == 0 {
		return
	}
	next := m.historySearch.selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.historySearch.matches) {
		next = len(m.historySearch.matches) - 1
	}
	m.historySearch.selected = next
	m.textarea.SetValue(m.historySearch.matches[next])
}

func (m *model) acceptHistorySearch() bool {
	if m.historySearch == nil || len(m.historySearch.matches) == 0 {
		return false
	}
	selected := m.historySearch.selected
	if selected < 0 || selected >= len(m.historySearch.matches) {
		selected = 0
	}
	message := m.historySearch.matches[selected]
	m.historySearch = nil
	m.textarea.SetValue(message)
	return true
}

func (m *model) cancelHistorySearch() {
	if m.historySearch == nil {
		return
	}
	originalDraft := m.historySearch.originalDraft
	m.historySearch = nil
	m.textarea.SetValue(originalDraft)
}

func (m *model) deleteHistorySearchRune() {
	if m.historySearch == nil || m.historySearch.query == "" {
		return
	}
	_, size := utf8.DecodeLastRuneInString(m.historySearch.query)
	if size <= 0 {
		m.historySearch.query = ""
	} else {
		m.historySearch.query = m.historySearch.query[:len(m.historySearch.query)-size]
	}
	m.applyHistorySearchQuery()
}
