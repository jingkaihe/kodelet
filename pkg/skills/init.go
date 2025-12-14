package skills

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/logger"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
)

// Initialize discovers and configures skills based on configuration and CLI flags.
// It reads skills.enabled from config and respects the --no-skills flag (bound to no_skills in viper).
// Returns the discovered skills and whether skills are enabled.
func Initialize(ctx context.Context, llmConfig llmtypes.Config) (map[string]*Skill, bool) {
	// Check if disabled via CLI flag (--no-skills sets no_skills to true)
	noSkillsFlag := viper.GetBool("no_skills")

	// skills.enabled defaults to true when Skills config is nil
	enabled := (llmConfig.Skills == nil || llmConfig.Skills.Enabled) && !noSkillsFlag
	if !enabled {
		return nil, false
	}

	discovery, err := NewDiscovery()
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to create skill discovery")
		return nil, false
	}

	allSkills, err := discovery.DiscoverSkills()
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to discover skills")
		return nil, false
	}

	if llmConfig.Skills != nil && len(llmConfig.Skills.Allowed) > 0 {
		allSkills = FilterByAllowlist(allSkills, llmConfig.Skills.Allowed)
	}

	return allSkills, true
}
