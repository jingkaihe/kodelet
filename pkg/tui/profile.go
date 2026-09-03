package tui

import (
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
)

func loadProfileOptions() []string {
	globalProfiles := llm.GlobalProfiles()
	repoProfiles := llm.RepoProfiles()
	overrideProfiles := llm.OverrideProfiles()
	options := make([]string, 0, len(globalProfiles)+len(repoProfiles)+len(overrideProfiles)+1)
	seen := map[string]bool{}

	appendOption := func(profile string) {
		profile = displayProfile(profile)
		key := strings.ToLower(profile)
		if seen[key] {
			return
		}
		seen[key] = true
		options = append(options, profile)
	}

	appendOption("default")
	names := make([]string, 0, len(globalProfiles)+len(repoProfiles)+len(overrideProfiles))
	for name := range globalProfiles {
		names = append(names, name)
	}
	for name := range repoProfiles {
		names = append(names, name)
	}
	for name := range overrideProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		appendOption(name)
	}
	return options
}

func (m model) canChangeProfile() bool {
	return strings.TrimSpace(m.conversationID) == "" && !m.running && len(m.profileOptions) > 1
}

func (m *model) setProfile(profile string) {
	profile = displayProfile(profile)
	m.profileOptions = normalizeProfileOptions(m.profileOptions, profile)
	m.profile = profile
	m.profileIndex = profileOptionIndex(m.profileOptions, profile)
	if m.profileIndex < 0 {
		m.profileIndex = 0
	}
	m.profilePickerIndex = m.profileIndex
}

func (m *model) toggleProfilePickerFromKeyboard() {
	if m.profilePickerOpen {
		m.selectProfilePickerOption(m.profilePickerIndex)
		return
	}
	m.openProfilePicker()
}

func (m *model) toggleProfilePickerFromClick() {
	if m.profilePickerOpen {
		m.closeProfilePicker()
		return
	}
	m.openProfilePicker()
}

func (m *model) openProfilePicker() {
	if !m.canChangeProfile() {
		return
	}
	m.reasoningPickerOpen = false
	m.profilePickerOpen = true
	m.profilePickerIndex = m.profileIndex
}

func (m *model) closeProfilePicker() {
	m.profilePickerOpen = false
	m.profilePickerIndex = m.profileIndex
}

func (m *model) moveProfilePicker(delta int) {
	if !m.profilePickerOpen || len(m.profileOptions) == 0 {
		return
	}
	m.profilePickerIndex = (m.profilePickerIndex + delta) % len(m.profileOptions)
	if m.profilePickerIndex < 0 {
		m.profilePickerIndex += len(m.profileOptions)
	}
}

func (m *model) selectProfilePickerOption(index int) {
	if !m.profilePickerOpen || index < 0 || index >= len(m.profileOptions) {
		return
	}
	m.setProfile(m.profileOptions[index])
	m.refreshReasoningSettingsForProfile()
	m.profilePickerOpen = false
}
