package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
)

func loadProfileOptions() []string {
	globalProfiles := loadProfilesFromConfigFile(filepath.Join(userHomeDir(), ".kodelet", "config.yaml"))
	repoProfiles := loadProfilesFromConfigFile("kodelet-config.yaml")
	options := make([]string, 0, len(globalProfiles)+len(repoProfiles)+1)
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
	names := make([]string, 0, len(globalProfiles)+len(repoProfiles))
	for name := range globalProfiles {
		names = append(names, name)
	}
	for name := range repoProfiles {
		if _, exists := globalProfiles[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		appendOption(name)
	}
	return options
}

func loadProfilesFromConfigFile(path string) map[string]llmtypes.ProfileConfig {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil
	}
	return profilesFromViper(v)
}

func profilesFromViper(v *viper.Viper) map[string]llmtypes.ProfileConfig {
	if v == nil || !v.IsSet("profiles") {
		return nil
	}
	profilesMap := v.GetStringMap("profiles")
	profiles := make(map[string]llmtypes.ProfileConfig)
	for name, profileData := range profilesMap {
		if strings.EqualFold(name, "default") {
			continue
		}
		profileMap, ok := profileData.(map[string]any)
		if !ok {
			continue
		}
		profiles[name] = llmtypes.ProfileConfig(profileMap)
	}
	if len(profiles) == 0 {
		return nil
	}
	return profiles
}

func userHomeDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return homeDir
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
