package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// NewWorkspaceConfigLoader pins runner defaults and resolves each execution's
// environment settings from its own directory. It never loads model credentials,
// daemon endpoints, or authentication policy from repository configuration.
func NewWorkspaceConfigLoader(defaults map[string]any) (WorkspaceConfigLoader, error) {
	snapshot, err := json.Marshal(environmentSettings(defaults))
	if err != nil {
		return nil, errors.Wrap(err, "failed to snapshot runner settings")
	}
	return func(cwd, profile string) (llmtypes.Config, error) {
		var settings map[string]any
		if err := json.Unmarshal(snapshot, &settings); err != nil {
			return llmtypes.Config{}, errors.Wrap(err, "failed to decode runner settings snapshot")
		}
		host, err := llm.GetConfigFromSettingsWithEnvironmentProfile(settings, profile)
		if err != nil {
			return llmtypes.Config{}, err
		}
		v := viper.New()
		if err := v.MergeConfigMap(settings); err != nil {
			return llmtypes.Config{}, errors.Wrap(err, "failed to load runner defaults")
		}
		workspace := viper.New()
		workspace.SetConfigFile(filepath.Join(cwd, "kodelet-config.yaml"))
		if err := workspace.ReadInConfig(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return llmtypes.Config{}, errors.Wrap(err, "failed to load workspace environment settings")
		}
		workspaceSettings := environmentSettings(workspace.AllSettings())
		// Never merge repository profile definitions: Viper merges nested maps in
		// place, so restoring settings afterward would restore the mutated policy.
		delete(workspaceSettings, "environment_profiles")
		if err := v.MergeConfigMap(workspaceSettings); err != nil {
			return llmtypes.Config{}, errors.Wrap(err, "failed to merge workspace environment settings")
		}
		config, err := llm.GetConfigFromSettingsWithEnvironmentProfile(v.AllSettings(), profile)
		if err != nil {
			return llmtypes.Config{}, err
		}
		if err := validateWorkspacePolicy(host, config); err != nil {
			return llmtypes.Config{}, err
		}
		config.WorkingDirectory = cwd
		return config, nil
	}, nil
}

func environmentSettings(settings map[string]any) map[string]any {
	result := make(map[string]any)
	for _, key := range []string{"allowed_commands", "allowed_domains_file", "allowed_tools", "tool_mode", "bash", "sysprompt", "sysprompt_args", "skills", "context", "extensions", "enable_fs_search_tools", "environment_profiles"} {
		if value, ok := settings[key]; ok {
			result[key] = value
		}
	}
	// Profiles use the same ownership boundary as defaults.
	if profiles, ok := result["environment_profiles"].(map[string]any); ok {
		projected := make(map[string]any, len(profiles))
		for name, value := range profiles {
			if profile, ok := value.(map[string]any); ok {
				projected[name] = environmentSettings(profile)
			}
		}
		result["environment_profiles"] = projected
	}
	return result
}

func validateWorkspacePolicy(host, config llmtypes.Config) error {
	for name, pair := range map[string][2][]string{
		"allowed_tools":    {host.AllowedTools, config.AllowedTools},
		"allowed_commands": {host.AllowedCommands, config.AllowedCommands},
	} {
		if len(pair[0]) == 0 {
			continue
		}
		if len(pair[1]) == 0 {
			return errors.Errorf("workspace %s cannot remove runner restrictions", name)
		}
		for _, value := range pair[1] {
			if !slices.Contains(pair[0], value) {
				return errors.Errorf("workspace %s cannot widen runner restrictions", name)
			}
		}
	}
	if strings.TrimSpace(host.AllowedDomainsFile) != "" && config.AllowedDomainsFile != host.AllowedDomainsFile {
		return errors.New("workspace allowed_domains_file cannot replace runner policy")
	}
	if enabled, ok := host.ExtensionSettings["enabled"].(bool); ok && !enabled {
		if enabled, ok := config.ExtensionSettings["enabled"].(bool); !ok || enabled {
			return errors.New("workspace cannot enable extensions disabled by runner policy")
		}
	}
	if host.Skills != nil && !host.Skills.Enabled && (config.Skills == nil || config.Skills.Enabled) {
		return errors.New("workspace cannot enable skills disabled by runner policy")
	}
	return nil
}
