package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const (
	ScopeBuiltIn       = "built-in"
	ScopeRepo          = "repo"
	ScopeGlobal        = "global"
	ScopeRepoOverrides = "repo (overrides global)"
	ScopeSourceRepo    = "repo"
	ScopeSourceGlobal  = "global"
	ScopeSourceBoth    = "both"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage configuration profiles",
	Long:  "Manage named configuration profiles for different model setups",
}

var profileCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current active profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoProfile := getRepoProfileSetting()
		globalProfile := getGlobalProfileSetting()

		if repoProfile == "default" || repoProfile == "" {
			if globalProfile == "default" || globalProfile == "" {
				presenter.Info("Using default configuration (no profile active)")
			} else {
				presenter.Success(fmt.Sprintf("Current profile: %s (from global config)", globalProfile))
			}
		} else {
			presenter.Success(fmt.Sprintf("Current profile: %s (from repo config)", repoProfile))
		}
		return nil
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available profiles from both global and repo configs",
	RunE: func(cmd *cobra.Command, args []string) error {
		globalProfiles := getGlobalProfiles()
		repoProfiles := getRepoProfiles()
		mergedProfiles := mergeProfiles(globalProfiles, repoProfiles)

		activeProfile := viper.GetString("profile")
		activeProfileName := activeProfile
		if activeProfile == "default" {
			activeProfileName = ""
		}

		presenter.Section("Available Profiles")

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		fmt.Fprintln(tw, "NAME\tSCOPE\tSTATUS")
		fmt.Fprintln(tw, "----\t-----\t------")

		status := ""
		if activeProfileName == "" {
			status = "ACTIVE"
		}
		fmt.Fprintf(tw, "default\t%s\t%s\n", ScopeBuiltIn, status)

		if len(mergedProfiles) > 0 {
			for name, source := range mergedProfiles {
				status := ""
				if name == activeProfileName {
					status = "ACTIVE"
				}

				scope := ""
				switch source {
				case ScopeSourceBoth:
					scope = ScopeRepoOverrides
				case ScopeSourceGlobal:
					scope = ScopeGlobal
				default:
					scope = ScopeRepo
				}

				fmt.Fprintf(tw, "%s\t%s\t%s\n", name, scope, status)
			}
		}

		return tw.Flush()
	},
}

var profileShowCmd = &cobra.Command{
	Use:   "show [profile-name]",
	Short: "Show merged configuration for a specific profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName := args[0]

		if profileName == "default" {
			presenter.Section("Default Configuration (base config without profile)")
			baseConfig := getBaseConfig()
			yamlOutput, _ := yaml.Marshal(baseConfig)
			presenter.Info(string(yamlOutput))
			return nil
		}

		mergedConfig := getMergedProfileConfig(profileName)
		if mergedConfig == nil {
			return fmt.Errorf("profile '%s' not found", profileName)
		}

		presenter.Section(fmt.Sprintf("Profile: %s", profileName))
		yamlOutput, _ := yaml.Marshal(mergedConfig)
		presenter.Info(string(yamlOutput))

		return nil
	},
}

var profileUseCmd = &cobra.Command{
	Use:   "use [profile-name]",
	Short: "Switch to a different profile",
	Long: `Switch to a different profile. 
Without -g flag: updates ./kodelet-config.yaml
With -g flag: updates ~/.kodelet/config.yaml

Use "default" to use base configuration without any profile.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName := args[0]
		global, _ := cmd.Flags().GetBool("global")

		// Validate non-default profiles exist
		if profileName != "default" {
			mergedProfiles := getMergedProfiles()
			if _, exists := mergedProfiles[profileName]; !exists {
				return fmt.Errorf("profile '%s' not found", profileName)
			}
		}

		if err := updateProfileInConfig(global, profileName); err != nil {
			return err
		}

		presenter.Success(getProfileSwitchMessage(profileName, global))
		return nil
	},
}

func init() {
	profileCmd.AddCommand(profileCurrentCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileShowCmd)
	profileCmd.AddCommand(profileUseCmd)

	profileUseCmd.Flags().BoolP("global", "g", false, "Update global config instead of repo config")
}

func getRepoProfileSetting() string {
	v := viper.New()
	v.SetConfigName("kodelet-config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return ""
	}

	return v.GetString("profile")
}

func getGlobalProfileSetting() string {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	v.AddConfigPath(filepath.Join(homeDir, ".kodelet"))

	if err := v.ReadInConfig(); err != nil {
		return ""
	}

	return v.GetString("profile")
}

func getGlobalProfiles() map[string]llmtypes.ProfileConfig {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	v.AddConfigPath(filepath.Join(homeDir, ".kodelet"))

	if err := v.ReadInConfig(); err != nil {
		return nil
	}

	if !v.IsSet("profiles") {
		return nil
	}

	profilesMap := v.GetStringMap("profiles")
	profiles := make(map[string]llmtypes.ProfileConfig)

	for name, profileData := range profilesMap {
		if name == "default" {
			continue
		}

		if profileMap, ok := profileData.(map[string]interface{}); ok {
			profiles[name] = llmtypes.ProfileConfig(profileMap)
		}
	}

	return profiles
}

func getRepoProfiles() map[string]llmtypes.ProfileConfig {
	v := viper.New()
	v.SetConfigName("kodelet-config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return nil
	}

	if !v.IsSet("profiles") {
		return nil
	}

	profilesMap := v.GetStringMap("profiles")
	profiles := make(map[string]llmtypes.ProfileConfig)

	for name, profileData := range profilesMap {
		if name == "default" {
			continue
		}

		if profileMap, ok := profileData.(map[string]interface{}); ok {
			profiles[name] = llmtypes.ProfileConfig(profileMap)
		}
	}

	return profiles
}

func mergeProfiles(globalProfiles, repoProfiles map[string]llmtypes.ProfileConfig) map[string]string {
	merged := make(map[string]string)

	for name := range globalProfiles {
		merged[name] = ScopeSourceGlobal
	}

	for name := range repoProfiles {
		if _, exists := merged[name]; exists {
			merged[name] = ScopeSourceBoth
		} else {
			merged[name] = ScopeSourceRepo
		}
	}

	return merged
}

func getMergedProfiles() map[string]string {
	globalProfiles := getGlobalProfiles()
	repoProfiles := getRepoProfiles()
	return mergeProfiles(globalProfiles, repoProfiles)
}

func getBaseConfig() *llmtypes.Config {
	currentProfile := viper.GetString("profile")

	viper.Set("profile", "")

	config := llm.GetConfigFromViper()

	viper.Set("profile", currentProfile)

	return &config
}

func getMergedProfileConfig(profileName string) *llmtypes.Config {
	globalProfiles := getGlobalProfiles()
	repoProfiles := getRepoProfiles()

	var profileConfig llmtypes.ProfileConfig
	var found bool

	if repoProfiles != nil {
		if profile, exists := repoProfiles[profileName]; exists {
			profileConfig = profile
			found = true
		}
	}

	if !found && globalProfiles != nil {
		if profile, exists := globalProfiles[profileName]; exists {
			profileConfig = profile
			found = true
		}
	}

	if !found {
		return nil
	}

	baseConfig := getBaseConfig()

	mergedConfig := *baseConfig

	if provider, ok := profileConfig["provider"].(string); ok {
		mergedConfig.Provider = provider
	}
	if model, ok := profileConfig["model"].(string); ok {
		mergedConfig.Model = model
	}
	if weakModel, ok := profileConfig["weak_model"].(string); ok {
		mergedConfig.WeakModel = weakModel
	}

	return &mergedConfig
}

func getConfigFilePath(global bool) (string, error) {
	if global {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", errors.Wrap(err, "failed to get home directory")
		}
		return filepath.Join(homeDir, ".kodelet", "config.yaml"), nil
	}
	return "./kodelet-config.yaml", nil
}

func getProfileSwitchMessage(profileName string, global bool) string {
	location := "repo"
	if global {
		location = "global"
	}

	if profileName == "default" {
		return fmt.Sprintf("Switched to default configuration in %s config", location)
	}
	return fmt.Sprintf("Switched to profile '%s' in %s config", profileName, location)
}

func updateProfileInConfig(global bool, profileName string) error {
	configPath, err := getConfigFilePath(global)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			newConfig := map[string]interface{}{
				"profile": profileName,
			}
			return writeYAMLConfig(configPath, newConfig)
		}
		return errors.Wrap(err, "failed to read config file")
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return errors.Wrap(err, "failed to parse config file")
	}

	if config == nil {
		config = make(map[string]interface{})
	}

	config["profile"] = profileName

	return writeYAMLConfig(configPath, config)
}

func writeYAMLConfig(configPath string, config map[string]interface{}) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrap(err, "failed to create config directory")
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return errors.Wrap(err, "failed to marshal config")
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return errors.Wrap(err, "failed to write config file")
	}

	logger.G(context.TODO()).WithField("file", configPath).Debug("Profile configuration updated")
	return nil
}
