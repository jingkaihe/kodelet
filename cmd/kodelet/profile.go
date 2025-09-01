package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
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
		// Check repo config first, then global
		repoProfile := getRepoProfileSetting()
		globalProfile := getGlobalProfileSetting()
		
		if repoProfile == "default" || repoProfile == "" {
			if globalProfile == "default" || globalProfile == "" {
				presenter.Info("Using default configuration (no profile active)")
			} else {
				presenter.Success(fmt.Sprintf("Current profile: %s (from global config)", globalProfile))
			}
		} else {
			presenter.Success(fmt.Sprintf("Current profile: %s (from repository config)", repoProfile))
		}
		return nil
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available profiles from both global and repository configs",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get merged profiles from both configs
		globalProfiles := getGlobalProfiles()
		repoProfiles := getRepoProfiles()
		mergedProfiles := mergeProfiles(globalProfiles, repoProfiles)
		
		if len(mergedProfiles) == 0 {
			presenter.Info("No profiles defined")
			return nil
		}
		
		activeProfile := viper.GetString("profile")
		// Treat "default" as no active profile
		if activeProfile == "default" {
			activeProfile = ""
		}
		
		presenter.Section("Available Profiles")
		
		for name, source := range mergedProfiles {
			marker := ""
			if name == activeProfile {
				marker = "* "
			}
			
			location := ""
			switch source {
			case "both":
				location = " (repo overrides global)"
			case "global":
				location = " (global)"
			default:
				location = " (repo)"
			}
			
			if marker != "" {
				presenter.Success(fmt.Sprintf("%s%s%s", marker, name, location))
			} else {
				presenter.Info(fmt.Sprintf("  %s%s", name, location))
			}
		}
		return nil
	},
}

var profileShowCmd = &cobra.Command{
	Use:   "show [profile-name]",
	Short: "Show merged configuration for a specific profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName := args[0]
		
		// Handle special case: "default" shows base configuration
		if profileName == "default" {
			presenter.Section("Default Configuration (base config without profile)")
			// Get base configuration without any profile applied
			baseConfig := getBaseConfig()
			yamlOutput, _ := yaml.Marshal(baseConfig)
			presenter.Info(string(yamlOutput))
			return nil
		}
		
		// Get the merged profile configuration
		mergedConfig := getMergedProfileConfig(profileName)
		if mergedConfig == nil {
			return fmt.Errorf("profile '%s' not found", profileName)
		}
		
		// Display the configuration in YAML format
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
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName := args[0]
		global, _ := cmd.Flags().GetBool("global")
		
		// Handle special case: "default" means use base configuration
		if profileName == "default" {
			var configFile string
			if global {
				configFile = "~/.kodelet/config.yaml"
			} else {
				configFile = "./kodelet-config.yaml"
			}
			
			// Update with "default" profile (means no profile)
			if err := updateProfileInConfig(configFile, "default"); err != nil {
				return err
			}
			
			location := "repository"
			if global {
				location = "global"
			}
			presenter.Success(fmt.Sprintf("Switched to default configuration in %s config", location))
			return nil
		}
		
		// Validate profile exists in merged view
		mergedProfiles := getMergedProfiles()
		if _, exists := mergedProfiles[profileName]; !exists {
			return fmt.Errorf("profile '%s' not found", profileName)
		}
		
		// Determine target config file
		var configFile string
		if global {
			configFile = "~/.kodelet/config.yaml"
		} else {
			configFile = "./kodelet-config.yaml"
		}
		
		// Update configuration file
		if err := updateProfileInConfig(configFile, profileName); err != nil {
			return err
		}
		
		location := "repository"
		if global {
			location = "global"
		}
		presenter.Success(fmt.Sprintf("Switched to profile '%s' in %s config", profileName, location))
		return nil
	},
}

func init() {
	profileCmd.AddCommand(profileCurrentCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileShowCmd)
	profileCmd.AddCommand(profileUseCmd)
	
	// Add global flag for use command
	profileUseCmd.Flags().BoolP("global", "g", false, "Update global config instead of repository config")
}

// Helper functions for profile management

// getRepoProfileSetting gets the profile setting from repository config
func getRepoProfileSetting() string {
	// Create a new viper instance for repository config
	v := viper.New()
	v.SetConfigName("kodelet-config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	
	if err := v.ReadInConfig(); err != nil {
		return ""
	}
	
	return v.GetString("profile")
}

// getGlobalProfileSetting gets the profile setting from global config
func getGlobalProfileSetting() string {
	// Create a new viper instance for global config
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

// getGlobalProfiles gets profiles from global config
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
			// Skip reserved profile name
			continue
		}
		
		if profileMap, ok := profileData.(map[string]interface{}); ok {
			profiles[name] = llmtypes.ProfileConfig(profileMap)
		}
	}
	
	return profiles
}

// getRepoProfiles gets profiles from repository config
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
			// Skip reserved profile name
			continue
		}
		
		if profileMap, ok := profileData.(map[string]interface{}); ok {
			profiles[name] = llmtypes.ProfileConfig(profileMap)
		}
	}
	
	return profiles
}

// mergeProfiles merges global and repository profiles, with repo taking precedence
func mergeProfiles(globalProfiles, repoProfiles map[string]llmtypes.ProfileConfig) map[string]string {
	merged := make(map[string]string)
	
	// Add global profiles
	for name := range globalProfiles {
		merged[name] = "global"
	}
	
	// Add repo profiles, overriding globals with same name
	for name := range repoProfiles {
		if _, exists := merged[name]; exists {
			merged[name] = "both"
		} else {
			merged[name] = "repo"
		}
	}
	
	return merged
}

// getMergedProfiles gets all profiles from both configs
func getMergedProfiles() map[string]string {
	globalProfiles := getGlobalProfiles()
	repoProfiles := getRepoProfiles()
	return mergeProfiles(globalProfiles, repoProfiles)
}

// getBaseConfig gets the base configuration without any profile applied
func getBaseConfig() *llmtypes.Config {
	// Save current profile setting
	currentProfile := viper.GetString("profile")
	
	// Temporarily clear profile
	viper.Set("profile", "")
	
	// Get config without profile
	config := llm.GetConfigFromViper()
	
	// Restore original profile setting
	viper.Set("profile", currentProfile)
	
	return &config
}

// getMergedProfileConfig gets the merged configuration for a specific profile
func getMergedProfileConfig(profileName string) *llmtypes.Config {
	// Check if profile exists in either config
	globalProfiles := getGlobalProfiles()
	repoProfiles := getRepoProfiles()
	
	var profileConfig llmtypes.ProfileConfig
	var found bool
	
	// Repository profiles take precedence over global ones
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
	
	// Get base config
	baseConfig := getBaseConfig()
	
	// Apply profile to base config
	// Create a copy of the base config to avoid modifying the original
	mergedConfig := *baseConfig
	
	// Apply profile settings (this is a simplified version)
	if provider, ok := profileConfig["provider"].(string); ok {
		mergedConfig.Provider = provider
	}
	if model, ok := profileConfig["model"].(string); ok {
		mergedConfig.Model = model
	}
	if weakModel, ok := profileConfig["weak_model"].(string); ok {
		mergedConfig.WeakModel = weakModel
	}
	// Add other fields as needed...
	
	return &mergedConfig
}

// updateProfileInConfig updates the profile setting in a configuration file
func updateProfileInConfig(configPath string, profileName string) error {
	// Expand path if it contains ~
	if strings.HasPrefix(configPath, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return errors.Wrap(err, "failed to get home directory")
		}
		configPath = filepath.Join(home, configPath[1:])
	}
	
	// Read existing configuration file
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new config with just the profile setting
			newConfig := map[string]interface{}{
				"profile": profileName,
			}
			return writeYAMLConfig(configPath, newConfig)
		}
		return errors.Wrap(err, "failed to read config file")
	}
	
	// Parse YAML to preserve structure and comments
	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return errors.Wrap(err, "failed to parse config file")
	}
	
	// Initialize config if nil
	if config == nil {
		config = make(map[string]interface{})
	}
	
	// Update or add the profile field
	// "default" is a reserved value (means no profile)
	config["profile"] = profileName
	
	// Write back to file
	return writeYAMLConfig(configPath, config)
}

// writeYAMLConfig writes a YAML configuration to file
func writeYAMLConfig(configPath string, config map[string]interface{}) error {
	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrap(err, "failed to create config directory")
	}
	
	// Marshal to YAML with proper formatting
	data, err := yaml.Marshal(config)
	if err != nil {
		return errors.Wrap(err, "failed to marshal config")
	}
	
	// Write to file with appropriate permissions
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return errors.Wrap(err, "failed to write config file")
	}
	
	logger.G(context.TODO()).WithField("file", configPath).Debug("Profile configuration updated")
	return nil
}