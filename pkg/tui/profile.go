package tui

import (
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
)

func loadProfileOptions() []string {
	profileSources := llm.ProfileSources()
	options := make([]string, 0, len(profileSources))
	seen := map[string]bool{}

	appendOption := func(profile string) {
		profile = displayProfile(profile)
		key := strings.ToLower(profile)
		if profile == "" || seen[key] {
			return
		}
		seen[key] = true
		options = append(options, profile)
	}

	names := make([]string, 0, len(profileSources))
	for name := range profileSources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !llm.IsProfileHidden(name) {
			appendOption(name)
		}
	}
	return options
}

func (m *model) setProfile(profile string) {
	profile = displayProfile(profile)
	m.profileOptions = normalizeProfileOptions(m.profileOptions, profile)
	m.profile = profile
	m.profileIndex = profileOptionIndex(m.profileOptions, profile)
	if m.profileIndex < 0 {
		m.profileIndex = 0
	}
}
