package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const (
	ScopeBuiltIn       = "built-in"
	ScopeRepo          = "repo"
	ScopeGlobal        = "global"
	ScopeOverride      = "override"
	ScopeRepoOverrides = "repo (overrides global)"
	profileEnv         = "KODELET_PROFILE"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage configuration profiles",
	Long:  "Manage named configuration profiles for different model setups",
}

var profileCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current active profile",
	RunE: func(cmd *cobra.Command, _ []string) error {
		profile, location := effectiveProfileSetting(cmd)
		if profile == "" || strings.EqualFold(profile, "default") {
			presenter.Info("Using default configuration (no profile active)")
			return nil
		}

		if location == "" {
			presenter.Success(fmt.Sprintf("Current profile: %s", profile))
			return nil
		}
		presenter.Success(fmt.Sprintf("Current profile: %s (from %s)", profile, location))
		return nil
	},
}

func effectiveProfileSetting(cmd *cobra.Command) (string, string) {
	profile := strings.TrimSpace(viper.GetString("profile"))
	if cmd != nil {
		if flag := cmd.Flags().Lookup("profile"); flag != nil && flag.Changed {
			return profile, "command-line flag"
		}
	}
	if strings.TrimSpace(os.Getenv(profileEnv)) != "" {
		return profile, "environment"
	}

	_, source := llm.ActiveProfileSetting()
	switch source {
	case llm.ProfileSourceGlobal:
		return profile, "global config"
	case llm.ProfileSourceRepo:
		return profile, "repo config"
	case llm.ProfileSourceOverride:
		return profile, "override config"
	default:
		return profile, ""
	}
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available configuration profiles",
	RunE: func(_ *cobra.Command, _ []string) error {
		profileSources := llm.ProfileSources()

		activeProfile := strings.TrimSpace(viper.GetString("profile"))
		activeProfileName := activeProfile
		if strings.EqualFold(activeProfile, "default") {
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

		if len(profileSources) > 0 {
			for name, source := range profileSources {
				status := ""
				if name == activeProfileName {
					status = "ACTIVE"
				}

				scope := ""
				switch source {
				case llm.ProfileSourceRepoOverridesGlobal:
					scope = ScopeRepoOverrides
				case llm.ProfileSourceGlobal:
					scope = ScopeGlobal
				case llm.ProfileSourceOverride:
					scope = ScopeOverride
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
		format, _ := cmd.Flags().GetString("format")

		var (
			config llmtypes.Config
			err    error
		)
		if profileName == "default" {
			config, err = llm.GetConfigFromViperWithoutProfile()
		} else {
			if !llm.HasConfiguredProfile(profileName) {
				return fmt.Errorf("profile '%s' not found", profileName)
			}
			config, err = llm.GetConfigFromViperWithProfile(profileName)
		}
		if err != nil {
			return errors.Wrap(err, "failed to load configuration")
		}

		config.Profile = ""
		config.Profiles = nil
		config.Aliases = nil

		var output []byte

		switch format {
		case "yaml":
			output, err = yaml.Marshal(config)
		case "json":
			output, err = json.MarshalIndent(config, "", "  ")
		default:
			return fmt.Errorf("unsupported format '%s'. Supported formats: json, yaml", format)
		}

		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		fmt.Print(string(output))
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

		if err := ensureProfileSelectionWritable(global); err != nil {
			return err
		}

		if profileName != "default" {
			if _, exists := llm.ProfileSources()[profileName]; !exists {
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

	profileShowCmd.Flags().StringP("format", "f", "json", "Output format (json, yaml)")
	profileUseCmd.Flags().BoolP("global", "g", false, "Update global config instead of repo config")
}

func ensureProfileSelectionWritable(global bool) error {
	overridePath := strings.TrimSpace(os.Getenv(llm.ConfigFileEnv))
	if overridePath == "" {
		return nil
	}
	targetPath, err := getConfigFilePath(global)
	if err != nil {
		return err
	}
	if sameConfigFile(overridePath, targetPath) {
		return nil
	}
	if llm.UsesIsolatedConfigFile() {
		return errors.Errorf("cannot switch profiles while %s is %q; update %q directly", llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated, overridePath)
	}
	if _, configured := llm.OverrideProfileSetting(); configured {
		return errors.Errorf("cannot switch profiles because %q sets the active profile and overrides repo and global configuration; update it directly", overridePath)
	}
	return nil
}

func sameConfigFile(first, second string) bool {
	firstPath, firstErr := filepath.Abs(first)
	secondPath, secondErr := filepath.Abs(second)
	if firstErr != nil || secondErr != nil {
		return false
	}
	firstInfo, firstErr := os.Stat(firstPath)
	secondInfo, secondErr := os.Stat(secondPath)
	if firstErr == nil && secondErr == nil {
		return os.SameFile(firstInfo, secondInfo)
	}
	return filepath.Clean(firstPath) == filepath.Clean(secondPath)
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
			newConfig := map[string]any{
				"profile": profileName,
			}
			return writeYAMLConfig(configPath, newConfig, global)
		}
		return errors.Wrap(err, "failed to read config file")
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return errors.Wrap(err, "failed to parse config file")
	}

	if config == nil {
		config = make(map[string]any)
	}

	config["profile"] = profileName

	return writeYAMLConfig(configPath, config, global)
}

func writeYAMLConfig(configPath string, config map[string]any, private bool) error {
	dir := filepath.Dir(configPath)
	if private {
		if err := osutil.EnsurePrivateDir(dir); err != nil {
			return errors.Wrap(err, "failed to create private config directory")
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create config directory")
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return errors.Wrap(err, "failed to marshal config")
	}

	mode := os.FileMode(0o644)
	if private {
		mode = 0o600
	}
	if err := os.WriteFile(configPath, data, mode); err != nil {
		return errors.Wrap(err, "failed to write config file")
	}
	if private {
		if err := osutil.EnsurePrivateFile(configPath); err != nil {
			return errors.Wrap(err, "failed to secure config file")
		}
	}

	logger.G(context.TODO()).WithField("file", configPath).Debug("Profile configuration updated")
	return nil
}
