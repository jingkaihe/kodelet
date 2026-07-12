package tui

import (
	"slices"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/chat"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func normalizeReasoningEffort(effort string) string {
	normalized, err := llmtypes.NormalizeReasoningEffort(effort)
	if err != nil || normalized == "" {
		return llmtypes.DefaultReasoningEffort
	}
	return normalized
}

func normalizeReasoningEffortOptions(options []string, selected string) []string {
	selected = normalizeReasoningEffort(selected)
	seen := make(map[string]struct{}, len(options)+1)
	normalized := make([]string, 0, len(options)+1)
	for _, raw := range options {
		effort, err := llmtypes.NormalizeReasoningEffort(raw)
		if err != nil || effort == "" {
			continue
		}
		if _, exists := seen[effort]; exists {
			continue
		}
		seen[effort] = struct{}{}
		normalized = append(normalized, effort)
	}
	if _, exists := seen[selected]; !exists {
		normalized = append(normalized, selected)
	}
	return normalized
}

func reasoningEffortOptionIndex(options []string, selected string) int {
	selected = normalizeReasoningEffort(selected)
	for index, option := range options {
		if normalizeReasoningEffort(option) == selected {
			return index
		}
	}
	return -1
}

func resolveReasoningSettings(profile, requested string) (string, []string, bool, error) {
	config, err := chat.ResolveConfigForNewConversation(profileForRequest(profile), requested)
	if err != nil {
		return "", nil, false, err
	}
	return config.ReasoningEffort, llmtypes.ReasoningEffortOptions(config), len(config.AllowedReasoningEfforts) > 0, nil
}

func (m model) canChangeReasoningEffort() bool {
	return strings.TrimSpace(m.conversationID) == "" && !m.running && len(m.reasoningEffortOptions) > 1
}

func (m *model) setReasoningEffort(effort string, explicit bool) {
	effort = normalizeReasoningEffort(effort)
	m.reasoningEffortOptions = normalizeReasoningEffortOptions(m.reasoningEffortOptions, effort)
	m.reasoningEffort = effort
	m.reasoningEffortIndex = reasoningEffortOptionIndex(m.reasoningEffortOptions, effort)
	if m.reasoningEffortIndex < 0 {
		m.reasoningEffortIndex = 0
	}
	m.reasoningPickerIndex = m.reasoningEffortIndex
	m.reasoningEffortExplicit = explicit
}

func (m *model) refreshReasoningSettingsForProfile() {
	profileDefault, options, restricted, err := resolveReasoningSettings(m.profile, "")
	if err != nil {
		return
	}
	if m.reasoningEffortExplicit && (!restricted || slices.Contains(options, m.reasoningEffort)) {
		m.reasoningEffortOptions = normalizeReasoningEffortOptions(options, m.reasoningEffort)
		m.setReasoningEffort(m.reasoningEffort, true)
		return
	}
	m.reasoningEffortOptions = normalizeReasoningEffortOptions(options, profileDefault)
	m.setReasoningEffort(profileDefault, false)
}

func (m *model) openReasoningPicker() {
	if !m.canChangeReasoningEffort() {
		return
	}
	m.profilePickerOpen = false
	m.reasoningPickerOpen = true
	m.reasoningPickerIndex = m.reasoningEffortIndex
}

func (m *model) closeReasoningPicker() {
	m.reasoningPickerOpen = false
	m.reasoningPickerIndex = m.reasoningEffortIndex
}

func (m *model) toggleReasoningPickerFromKeyboard() {
	if m.reasoningPickerOpen {
		m.selectReasoningPickerOption(m.reasoningPickerIndex)
		return
	}
	m.openReasoningPicker()
}

func (m *model) toggleReasoningPickerFromClick() {
	if m.reasoningPickerOpen {
		m.closeReasoningPicker()
		return
	}
	m.openReasoningPicker()
}

func (m *model) moveReasoningPicker(delta int) {
	if !m.reasoningPickerOpen || len(m.reasoningEffortOptions) == 0 {
		return
	}
	m.reasoningPickerIndex = (m.reasoningPickerIndex + delta) % len(m.reasoningEffortOptions)
	if m.reasoningPickerIndex < 0 {
		m.reasoningPickerIndex += len(m.reasoningEffortOptions)
	}
}

func (m *model) selectReasoningPickerOption(index int) {
	if !m.reasoningPickerOpen || index < 0 || index >= len(m.reasoningEffortOptions) {
		return
	}
	m.setReasoningEffort(m.reasoningEffortOptions[index], true)
	m.reasoningPickerOpen = false
}
