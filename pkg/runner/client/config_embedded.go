package client

import (
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// ProfileConfigLoader resolves a run's environment using separate model and
// runner profile names. Only embedded runners install this trusted local hook.
// No model configuration or credentials are accepted from the runner protocol.
type ProfileConfigLoader func(cwd, modelProfile, environmentProfile string) (llmtypes.Config, error)

// NewEmbeddedConfigLoader pins the environment-only projection of the daemon's
// defaults and profiles. Explicit runner settings override inherited preferences;
// the ordinary workspace loader then applies repository policy and runner profiles.
func NewEmbeddedConfigLoader(defaults, overrides map[string]any) (ProfileConfigLoader, error) {
	type settingsSnapshot struct {
		Defaults      map[string]any
		Overrides     map[string]any
		Profiles      map[string]map[string]any
		ActiveProfile string
	}
	settings := settingsSnapshot{
		Defaults:  environmentSettings(defaults),
		Overrides: environmentSettings(overrides),
		Profiles:  make(map[string]map[string]any),
	}
	settings.ActiveProfile, _ = defaults["profile"].(string)
	if profiles, ok := defaults["profiles"].(map[string]any); ok {
		for name, raw := range profiles {
			if profile, ok := raw.(map[string]any); ok {
				settings.Profiles[name] = environmentSettings(profile)
			}
		}
	}
	// JSON cloning both pins caller-owned maps and strips non-environment data
	// before it can be retained by the embedded runner's loader.
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, errors.Wrap(err, "failed to snapshot embedded runner settings")
	}
	return func(cwd, modelProfile, environmentProfile string) (llmtypes.Config, error) {
		var settings settingsSnapshot
		if err := json.Unmarshal(data, &settings); err != nil {
			return llmtypes.Config{}, errors.Wrap(err, "failed to decode embedded runner settings")
		}
		profile := strings.TrimSpace(modelProfile)
		if profile == "" {
			profile = strings.TrimSpace(settings.ActiveProfile)
		}
		v := viper.New()
		if err := v.MergeConfigMap(settings.Defaults); err != nil {
			return llmtypes.Config{}, errors.Wrap(err, "failed to load embedded runner defaults")
		}
		if profile != "" && !strings.EqualFold(profile, "default") {
			values, ok := settings.Profiles[profile]
			if !ok {
				return llmtypes.Config{}, errors.Errorf("embedded runner model profile %q not found; restart the server after changing profiles", profile)
			}
			if err := v.MergeConfigMap(values); err != nil {
				return llmtypes.Config{}, errors.Wrap(err, "failed to apply embedded runner model profile")
			}
		}
		// The daemon already applies these permission ceilings to ordinary
		// remote runs. Preserve them during discovery too: runner preferences
		// may override model preferences, but cannot relax model permissions.
		inherited, err := llm.GetConfigFromSettingsWithEnvironmentProfile(v.AllSettings(), "")
		if err != nil {
			return llmtypes.Config{}, err
		}
		if err := v.MergeConfigMap(settings.Overrides); err != nil {
			return llmtypes.Config{}, errors.Wrap(err, "failed to apply embedded runner overrides")
		}
		// Use the shared loader to keep workspace validation and environment
		// profile semantics identical for standalone and embedded runners.
		loader, err := NewWorkspaceConfigLoader(v.AllSettings())
		if err != nil {
			return llmtypes.Config{}, err
		}
		config, err := loader(cwd, environmentProfile)
		if err != nil {
			return llmtypes.Config{}, err
		}
		// Intersect the effective runner policy with inherited daemon ceilings,
		// including discovery where no model turn supplies request options.
		return llmtypes.ApplyEnvironmentOptions(config, inherited.EnvironmentOptions())
	}, nil
}
