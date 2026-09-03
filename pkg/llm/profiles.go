package llm

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// ProfileSource identifies the highest-precedence configuration layer for a profile.
type ProfileSource string

const (
	// ConfigFileEnv points at an additional trusted configuration file that is
	// layered on top of the global config, or replaces it entirely in isolated mode.
	ConfigFileEnv = "KODELET_CONFIG_FILE"
	// ConfigFileModeEnv selects how ConfigFileEnv is applied.
	ConfigFileModeEnv = "KODELET_CONFIG_FILE_MODE"
	// ConfigFileModeMerge layers the override file on top of the global config.
	ConfigFileModeMerge = "merge"
	// ConfigFileModeIsolated makes the override file the only configuration source.
	ConfigFileModeIsolated = "isolated"
	// RepoConfigFile is the repository-local configuration file name.
	RepoConfigFile = "kodelet-config.yaml"

	ProfileSourceGlobal   ProfileSource = "global"
	ProfileSourceRepo     ProfileSource = "repo"
	ProfileSourceOverride ProfileSource = "override"
)

// UsesIsolatedConfigFile reports whether an explicit override replaces both
// the home and repository configuration layers.
func UsesIsolatedConfigFile() bool {
	return overrideConfigFile() != "" && strings.EqualFold(strings.TrimSpace(os.Getenv(ConfigFileModeEnv)), ConfigFileModeIsolated)
}

// GlobalProfiles returns the profiles declared by the global configuration
// layer. The built-in "default" profile is excluded, and nil is returned when
// no profile is declared or an isolated override disables the layer.
func GlobalProfiles() map[string]llmtypes.ProfileConfig {
	if UsesIsolatedConfigFile() {
		return nil
	}
	return profilesFromViper(readGlobalConfig())
}

// RepoProfiles returns the profiles declared by the repository-local config.
func RepoProfiles() map[string]llmtypes.ProfileConfig {
	if UsesIsolatedConfigFile() {
		return nil
	}
	return profilesFromConfigFiles(RepoConfigFile)
}

// OverrideProfiles returns the profiles declared by KODELET_CONFIG_FILE.
func OverrideProfiles() map[string]llmtypes.ProfileConfig {
	return profilesFromViper(readOverrideConfig())
}

// ActiveProfileSetting returns the profile selected by the highest-precedence
// configuration file together with that file's source.
func ActiveProfileSetting() (string, ProfileSource) {
	if profile, configured := OverrideProfileSetting(); configured {
		return profile, ProfileSourceOverride
	}
	if profile, configured := repoProfileSetting(); configured {
		return profile, ProfileSourceRepo
	}
	if profile, configured := globalProfileSetting(); configured {
		return profile, ProfileSourceGlobal
	}
	return "", ""
}

// GlobalProfileSetting returns the active profile selected by the global configuration layer.
func GlobalProfileSetting() string {
	profile, _ := globalProfileSetting()
	return profile
}

// RepoProfileSetting returns the active profile selected by the repository-local config.
func RepoProfileSetting() string {
	profile, _ := repoProfileSetting()
	return profile
}

// OverrideProfileSetting returns the profile selected by KODELET_CONFIG_FILE
// and whether that file explicitly contains a profile setting.
func OverrideProfileSetting() (string, bool) {
	return profileSettingFromViper(readOverrideConfig())
}

func globalProfileSetting() (string, bool) {
	if UsesIsolatedConfigFile() {
		return "", false
	}
	return profileSettingFromViper(readGlobalConfig())
}

func repoProfileSetting() (string, bool) {
	if UsesIsolatedConfigFile() {
		return "", false
	}
	return profileSettingFromViper(readConfigFiles(RepoConfigFile))
}

func overrideConfigFile() string {
	return strings.TrimSpace(os.Getenv(ConfigFileEnv))
}

func readGlobalConfig() *viper.Viper {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(filepath.Join(homeDir, ".kodelet"))
	if err := v.ReadInConfig(); err != nil {
		return nil
	}
	return v
}

func readOverrideConfig() *viper.Viper {
	path := overrideConfigFile()
	if path == "" {
		return nil
	}

	v := viper.New()
	v.SetConfigFile(path)
	if !UsesIsolatedConfigFile() {
		// Startup has already selected YAML before merging the override. Preserve
		// that behavior, including support for extensionless override files.
		v.SetConfigType("yaml")
	}
	if err := v.ReadInConfig(); err != nil {
		return nil
	}
	return v
}

func profilesFromConfigFiles(paths ...string) map[string]llmtypes.ProfileConfig {
	return profilesFromViper(readConfigFiles(paths...))
}

func profilesFromViper(v *viper.Viper) map[string]llmtypes.ProfileConfig {
	if v == nil || !v.IsSet("profiles") {
		return nil
	}

	profiles := make(map[string]llmtypes.ProfileConfig)
	for name, profileData := range v.GetStringMap("profiles") {
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

func profileSettingFromViper(v *viper.Viper) (string, bool) {
	if v == nil {
		return "", false
	}
	for _, key := range v.AllKeys() {
		if strings.EqualFold(key, "profile") {
			return v.GetString("profile"), true
		}
	}
	return "", false
}

// readConfigFiles layers the supplied config files in order, ignoring the ones
// that are missing or unreadable. It returns nil when none could be read.
func readConfigFiles(paths ...string) *viper.Viper {
	v := viper.New()
	loaded := false
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		v.SetConfigFile(path)

		var err error
		if loaded {
			err = v.MergeInConfig()
		} else {
			err = v.ReadInConfig()
		}
		if err != nil {
			continue
		}
		loaded = true
	}

	if !loaded {
		return nil
	}
	return v
}
