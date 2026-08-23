package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
)

var hardReservedExtensionShortcutKeys = map[string]string{
	"ctrl+c":    "cancel the active run or quit",
	"ctrl+d":    "cancel the active run or quit",
	"ctrl+j":    "insert a newline",
	"ctrl+l":    "browse and switch conversations",
	"alt+enter": "insert a newline",
}

var overridableTUIShortcutKeys = map[string]string{
	"ctrl+g": "edit the draft in $EDITOR",
	"ctrl+o": "toggle thought and tool details",
	"ctrl+r": "search previous sent messages",
	"ctrl+t": "change profile before starting",
	"ctrl+y": "change reasoning effort before starting",
}

func effectiveExtensionShortcuts(ctx context.Context, shortcuts []extensions.Shortcut) []extensions.Shortcut {
	effective := make([]extensions.Shortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		key, err := extensions.NormalizeShortcutKey(shortcut.Key)
		if err != nil {
			reportTUIShortcutDiagnostic(ctx, shortcut.ExtensionID, errors.Wrap(err, "invalid extension shortcut").Error())
			continue
		}
		shortcut.Key = key
		if description, reserved := hardReservedExtensionShortcutKeys[key]; reserved {
			reportTUIShortcutDiagnostic(ctx, shortcut.ExtensionID, fmt.Sprintf(
				"shortcut %q is reserved to %s; skipping",
				key,
				description,
			))
			continue
		}
		if description, conflicts := overridableTUIShortcutKeys[key]; conflicts {
			reportTUIShortcutDiagnostic(ctx, shortcut.ExtensionID, fmt.Sprintf(
				"shortcut %q overrides the built-in binding to %s",
				key,
				description,
			))
		}
		effective = append(effective, shortcut)
	}
	return effective
}

func reportTUIShortcutDiagnostic(ctx context.Context, extensionID, message string) {
	if sink, ok := extensions.DiagnosticSinkFromContext(ctx); ok {
		sink.ReportDiagnostic(ctx, extensions.Diagnostic{
			Level:     extensions.DiagnosticLevelWarning,
			Extension: extensionID,
			Message:   message,
		})
	}
}

func (m model) extensionShortcutForKey(key string) (extensions.Shortcut, bool) {
	if m.profilePickerOpen || m.reasoningPickerOpen || m.historySearch != nil || m.slashCommandSuggestionsOpen() {
		return extensions.Shortcut{}, false
	}
	normalized, err := extensions.NormalizeShortcutKey(key)
	if err != nil {
		return extensions.Shortcut{}, false
	}
	if _, reserved := hardReservedExtensionShortcutKeys[normalized]; reserved {
		return extensions.Shortcut{}, false
	}
	for _, shortcut := range m.extensionShortcuts {
		if shortcut.Key == normalized {
			return shortcut, true
		}
	}
	return extensions.Shortcut{}, false
}

func (m *model) startExtensionShortcut(shortcut extensions.Shortcut) tea.Cmd {
	if m == nil || m.conversationState == nil || m.extensionRuntimes == nil {
		return nil
	}
	state := m.conversationState
	m.nextRunID++
	callID := m.nextRunID
	conversationKey := state.key
	conversationID := state.conversationID
	profile := state.profile
	reasoningEffort := state.reasoningEffort
	if state.conversationWasResumed {
		reasoningEffort = ""
	}
	cwd := state.requestedCWD
	if strings.TrimSpace(cwd) == "" {
		cwd = state.cwd
	}

	baseCtx := m.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	callCtx, cancel := context.WithCancel(contextWithTUIConversation(baseCtx, conversationKey))
	broker := newTUIUIBrokerForConversation(m.runCh, callID, conversationKey)
	callCtx = extensions.ContextWithUIInputBroker(callCtx, broker)
	if m.shortcutCalls == nil {
		m.shortcutCalls = map[int]*extensionShortcutCall{}
	}
	m.shortcutCalls[callID] = &extensionShortcutCall{conversationKey: conversationKey, cancel: cancel}
	runtimeManager := m.extensionRuntimes

	return func() tea.Msg {
		defer broker.close()
		defer cancel()
		llmConfig, resolvedCWD, err := chat.ResolveConfigWithReasoning(
			callCtx,
			conversationID,
			profileForRequest(profile),
			reasoningEffort,
			cwd,
			"",
		)
		matched := false
		if err == nil {
			extensionCallContext := extensions.ExtensionCallContext{
				ConversationID: strings.TrimSpace(conversationID),
				CWD:            resolvedCWD,
				Provider:       strings.TrimSpace(llmConfig.Provider),
				Model:          strings.TrimSpace(llmConfig.Model),
				Profile:        strings.TrimSpace(llmConfig.Profile),
				InvokedBy:      "main",
			}
			extensionRuntime, runtimeErr := runtimeManager.RuntimeWithCallContext(callCtx, resolvedCWD, extensionCallContext)
			if runtimeErr != nil {
				err = errors.Wrap(runtimeErr, "failed to initialize extensions for shortcut")
			} else {
				matched, err = extensionRuntime.ExecuteShortcut(callCtx, shortcut.Key, extensionCallContext)
			}
		}
		return extensionShortcutDoneMsg{
			callID:          callID,
			conversationKey: conversationKey,
			key:             shortcut.Key,
			extensionID:     shortcut.ExtensionID,
			matched:         matched,
			err:             err,
		}
	}
}

func formatShortcutKey(key string) string {
	parts := strings.Split(strings.TrimSpace(key), "+")
	for index, part := range parts {
		switch strings.ToLower(part) {
		case "ctrl":
			parts[index] = "Ctrl"
		case "alt":
			parts[index] = "Alt"
		case "shift":
			parts[index] = "Shift"
		case "esc":
			parts[index] = "Esc"
		case "pgup":
			parts[index] = "PgUp"
		case "pgdown":
			parts[index] = "PgDown"
		default:
			parts[index] = strings.ToUpper(part)
		}
	}
	return strings.Join(parts, "+")
}

func extensionShortcutDescription(shortcut extensions.Shortcut) string {
	description := strings.TrimSpace(shortcut.Description)
	if description == "" {
		description = "Run extension shortcut"
	}
	if extensionID := strings.TrimSpace(shortcut.ExtensionID); extensionID != "" {
		description += " · " + extensionID
	}
	return description
}
