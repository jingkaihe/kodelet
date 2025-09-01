# ADR 017: Profile System for Model Configuration Management

## Status
Proposed

## Context
Kodelet currently supports flexible configuration of LLM models through config files (`~/.kodelet/config.yaml` and `kodelet-config.yaml`), including support for different providers, model aliases, and mix-and-match configurations for agents and subagents. However, users who frequently experiment with different model configurations face several challenges:

1. **Tedious Configuration Switching**: Users must manually edit configuration files each time they want to test a different model setup
2. **Error-Prone Manual Editing**: Repeated manual editing increases the risk of syntax errors or misconfiguration
3. **No Easy Comparison**: Difficult to quickly switch between configurations for A/B testing or performance comparison
4. **Configuration Sprawl**: No organized way to maintain multiple tested configurations for different use cases

Users need a streamlined way to:
- Define multiple named configurations (profiles)
- Switch between profiles quickly via CLI
- Override profiles for specific commands
- Maintain different profiles for different use cases (e.g., "premium" for best quality, "fast" for quick tasks)

## Decision
We will implement a profile system that allows users to:

1. Define multiple named profiles within configuration files
2. Switch between profiles using CLI commands
3. Override the active profile for individual commands
4. Manage profiles at both global and repository levels

The profile system will integrate seamlessly with the existing configuration hierarchy and maintain backward compatibility.

### Key Behaviors
- **Profile visibility**: All profiles from both global and repository configs are visible and usable
- **Profile precedence**: Repository profiles override global profiles with the same name
- **Profile switching**: `use` without `-g` modifies local config, with `-g` modifies global config
- **Profile listing**: Always shows merged view with source indicators
- **Profile showing**: Always displays the merged configuration
- **Profile clearing**: Use `kodelet profile use default` to use base configuration without any profile
- **Reserved name**: "default" is a reserved profile name that means "use base configuration"

## Architecture Details

### Configuration Structure

#### Profile Definition in Config Files
```yaml
# Root level configuration (base config used when profile is "default" or not set)
provider: "anthropic"
model: "claude-sonnet-4-20250514"
weak_model: "claude-3-5-haiku-20241022"
max_tokens: 16000
weak_model_max_tokens: 8192
thinking_budget_tokens: 8000

# Active profile selection
profile: "premium"  # Optional: specify the active profile
# Use profile: "default" or omit to use base configuration

# Profile definitions (note: "default" is reserved and cannot be defined here)
profiles:
  premium:
    model: "opus-41"           # Uses alias system
    weak_model: "sonnet-4"
    max_tokens: 16000
    weak_model_max_tokens: 8192
    thinking_budget_tokens: 8000

  fast:
    model: "haiku-35"
    weak_model: "haiku-35"
    max_tokens: 4096
    weak_model_max_tokens: 4096
    thinking_budget_tokens: 2000

  openai:
    provider: "openai"
    use_copilot: true
    model: "gpt-4.1"
    weak_model: "gpt-4.1-mini"
    max_tokens: 16000
    reasoning_effort: "medium"

  xai:
    provider: "openai"
    model: "grok-3"
    weak_model: "grok-3-mini"
    max_tokens: 16000
    reasoning_effort: "none"
    openai:
      preset: "xai"

  mix-n-match:
    # Main agent uses Claude
    provider: "anthropic"
    model: "sonnet-4"
    weak_model: "haiku-35"
    max_tokens: 16000
    
    # Subagent uses OpenAI o3 for complex reasoning
    subagent:
      provider: "openai"
      model: "o3"
      reasoning_effort: "high"
      allowed_tools: ["file_read", "glob_tool", "grep_tool", "thinking"]

# Aliases work across all profiles
aliases:
  sonnet-4: "claude-sonnet-4-20250514"
  haiku-35: "claude-3-5-haiku-20241022"
  opus-4: "claude-opus-4-20250514"
  opus-41: "claude-opus-4-1-20250805"
```

### CLI Commands

#### Profile Management Commands
```bash
# Show current active profile
kodelet profile current

# List all available profiles (always shows merged view)
kodelet profile list              # Shows all profiles from both configs
                                 # Repository profiles override global ones with same name

# Switch to a different profile
kodelet profile use premium       # Switch profile in ./kodelet-config.yaml
kodelet profile use openai -g    # Switch profile in ~/.kodelet/config.yaml

# Use default configuration (no profile)
kodelet profile use default      # Use base config in ./kodelet-config.yaml
kodelet profile use default -g   # Use base config in ~/.kodelet/config.yaml

# Show profile details (always shows merged configuration)
kodelet profile show premium      # Display merged configuration for a profile
```

#### Profile Override in Commands
```bash
# Use a specific profile for a single command
kodelet run --profile premium "explain this architecture"
kodelet chat --profile fast
kodelet commit --profile fast

# Profile flag takes precedence over configured profile

# Special case: use "default" to explicitly use base configuration
kodelet run --profile default "use base configuration without any profile"
```

### Implementation Changes

#### 1. Configuration Type Extensions
```go
// pkg/types/llm/config.go

type Config struct {
    // Existing fields...
    
    // Profile configuration
    Profile  string                    `mapstructure:"profile"`
    Profiles map[string]ProfileConfig `mapstructure:"profiles"`
}

type ProfileConfig map[string]interface{} // Flexible to support all config fields
```

#### 2. Profile Loading Logic
```go
// pkg/llm/config.go

func GetConfigFromViper() Config {
    var config Config
    
    // Load base configuration
    if err := viper.Unmarshal(&config); err != nil {
        return config
    }
    
    // Validate that no profile is named "default" (reserved)
    if config.Profiles != nil {
        if _, exists := config.Profiles["default"]; exists {
            logger.Warn("Profile named 'default' is reserved and will be ignored")
            delete(config.Profiles, "default")
        }
    }
    
    // Apply profile if specified
    profileName := getActiveProfile()
    if profileName != "" && config.Profiles != nil {
        if profile, exists := config.Profiles[profileName]; exists {
            applyProfile(&config, profile)
        }
    }
    
    // Apply model aliases
    config.Model = resolveModelAlias(config.Model, config.Aliases)
    config.WeakModel = resolveModelAlias(config.WeakModel, config.Aliases)
    
    return config
}

func getActiveProfile() string {
    // Priority order:
    // 1. Command-line flag (--profile)
    // 2. Environment variable (KODELET_PROFILE)
    // 3. Config file setting (profile: "name")
    
    profile := viper.GetString("profile")
    
    // "default" is a reserved name that means no profile
    if profile == "default" || profile == "" {
        return ""
    }
    return profile
}

func applyProfile(config *Config, profile ProfileConfig) {
    // Deep merge profile settings into config
    // Profile settings override base configuration
    for key, value := range profile {
        viper.Set(key, value)
    }
    
    // Re-unmarshal with profile applied
    viper.Unmarshal(config)
}
```

#### 3. Profile Command Implementation
```go
// cmd/kodelet/profile.go

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    
    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "gopkg.in/yaml.v3"
    "github.com/pkg/errors"
    
    "github.com/jingkaihe/kodelet/pkg/presenter"
    "github.com/jingkaihe/kodelet/pkg/llm"
)

var profileCmd = &cobra.Command{
    Use:   "profile",
    Short: "Manage configuration profiles",
    Long:  "Manage named configuration profiles for different model setups",
}

func init() {
    profileCmd.AddCommand(profileCurrentCmd)
    profileCmd.AddCommand(profileListCmd)
    profileCmd.AddCommand(profileShowCmd)
    profileCmd.AddCommand(profileUseCmd)
    
    // Add global flag for use command
    profileUseCmd.Flags().BoolP("global", "g", false, "Update global config instead of repository config")
    
    rootCmd.AddCommand(profileCmd)
}

var profileCurrentCmd = &cobra.Command{
    Use:   "current",
    Short: "Show the current active profile",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Check repo config first, then global
        repoProfile := getRepoProfileSetting()
        globalProfile := getGlobalProfileSetting()
        
        if repoProfile == "default" || repoProfile == "" {
            presenter.Info("Using default configuration (no profile active)")
        } else if repoProfile != "" {
            presenter.Success(fmt.Sprintf("Current profile: %s (from repository config)", repoProfile))
        } else if globalProfile == "default" || globalProfile == "" {
            presenter.Info("Using default configuration (no profile active)")
        } else if globalProfile != "" {
            presenter.Success(fmt.Sprintf("Current profile: %s (from global config)", globalProfile))
        } else {
            presenter.Info("Using default configuration (no profile active)")
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

var profileListCmd = &cobra.Command{
    Use:   "list",
    Short: "List all available profiles from both global and repository configs",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Get merged profiles from both configs
        globalProfiles := getGlobalProfiles()
        repoProfiles := getRepoProfiles()
        mergedProfiles := mergeProfiles(globalProfiles, repoProfiles)
        
        // Remove "default" if it exists (it's reserved)
        delete(mergedProfiles, "default")
        
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
            if source == "both" {
                location = " (repo overrides global)"
            } else if source == "global" {
                location = " (global)"
            } else {
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

// Helper function to update profile in configuration file
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
    
    // Update or add the profile field
    // "default" is a reserved value (means no profile)
    config["profile"] = profileName
    
    // Write back to file
    return writeYAMLConfig(configPath, config)
}

// Helper function to write YAML configuration
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
    
    return nil
}

// Alternative implementation using Viper for better integration
func updateProfileInConfigWithViper(configPath string, profileName string) error {
    // Create a new viper instance for this specific config file
    v := viper.New()
    v.SetConfigType("yaml")
    
    // Expand path
    if strings.HasPrefix(configPath, "~") {
        home, err := os.UserHomeDir()
        if err != nil {
            return errors.Wrap(err, "failed to get home directory")
        }
        configPath = filepath.Join(home, configPath[1:])
    }
    
    // Set config file path
    v.SetConfigFile(configPath)
    
    // Read existing config if it exists
    if err := v.ReadInConfig(); err != nil {
        if !os.IsNotExist(err) {
            // Only return error if it's not a "file not found" error
            return errors.Wrap(err, "failed to read config")
        }
        // File doesn't exist, will be created on write
    }
    
    // Set the profile value ("default" is reserved - means no profile)
    v.Set("profile", profileName)
    
    // Write the configuration
    if err := v.WriteConfig(); err != nil {
        // If file doesn't exist, use WriteConfigAs to create it
        if os.IsNotExist(err) {
            if err := v.WriteConfigAs(configPath); err != nil {
                return errors.Wrap(err, "failed to create config file")
            }
        } else {
            return errors.Wrap(err, "failed to write config")
        }
    }
    
    return nil
}
```

### Configuration File Update Implementation

The `updateProfileInConfig` function is critical for modifying configuration files while preserving their structure. Two implementation approaches are provided:

#### Approach 1: Direct YAML Manipulation
This approach directly reads and writes YAML files, providing more control over the file structure:

**Advantages:**
- Simpler implementation with fewer dependencies on Viper internals
- More predictable behavior with file creation and updates
- Easier to preserve YAML formatting and structure
- Can handle edge cases like missing directories

**Disadvantages:**
- Doesn't preserve comments in YAML files
- May not maintain exact formatting preferences
- Requires manual path expansion and directory creation

#### Approach 2: Viper-based Implementation
This approach uses Viper's built-in configuration management:

**Advantages:**
- Better integration with existing Viper configuration system
- Maintains consistency with how configs are read elsewhere
- Potentially better at preserving complex nested structures

**Disadvantages:**
- Viper's WriteConfig can be unpredictable with formatting
- May reorder keys or change formatting significantly
- Less control over error handling for file creation

**Recommendation:** Use the direct YAML manipulation approach (Approach 1) for the initial implementation, as it provides more predictable behavior and better control over file operations. The implementation can be refined later based on user feedback.

### Key Implementation Considerations

1. **Path Expansion**: Both implementations handle `~` in paths by expanding to the user's home directory.

2. **File Creation**: If the config file doesn't exist, the function creates it with just the profile setting, allowing gradual configuration building.

3. **Directory Creation**: The implementation ensures parent directories exist before writing files.

4. **Error Handling**: Comprehensive error wrapping provides clear context for debugging issues.

5. **Atomicity**: Consider using atomic file writes (write to temp, then rename) for production to prevent corruption during updates.

6. **Comment Preservation**: Neither approach perfectly preserves YAML comments. For production, consider using a YAML library that maintains comments (e.g., `github.com/goccy/go-yaml`).

### Configuration Precedence

The profile system integrates with the existing configuration hierarchy:

1. **Command-line flags** (highest priority)
   - Including `--profile` flag for temporary override
2. **Environment variables**
   - Including `KODELET_PROFILE` for profile selection
3. **Active profile configuration**
   - Settings from the selected profile
4. **Repository configuration** (`kodelet-config.yaml`)
   - Base configuration and profile definitions
5. **Global configuration** (`~/.kodelet/config.yaml`)
   - Base configuration and profile definitions
6. **Default values** (lowest priority)

### Profile Resolution Rules

1. **Profile Selection Priority**:
   - Command-line `--profile` flag (highest)
   - `KODELET_PROFILE` environment variable
   - `profile` field in repository config (`kodelet-config.yaml`)
   - `profile` field in global config (`~/.kodelet/config.yaml`) (lowest)

2. **Profile Definition Priority**:
   - Repository-level profiles override global profiles with the same name
   - When listing/showing profiles, both are visible but repo takes precedence
   - Profile settings override base configuration
   - Undefined fields in profile inherit from base configuration

3. **Profile Availability**:
   - All profiles from both global and repository configs are available for use
   - `profile list` shows merged view with source indicators
   - `profile use` can activate any available profile regardless of source
   - The profile setting itself is stored in the targeted config file (`-g` for global)

4. **Reserved Names**:
   - "default" is a reserved profile name that means "use base configuration"
   - Users cannot create a profile named "default" in their configuration
   - Validation should prevent defining `profiles.default` in config files

5. **Alias Resolution**:
   - Aliases are resolved after profile application
   - Aliases work consistently across all profiles
   - Repository aliases override global aliases with the same name

### Migration and Backward Compatibility

1. **Zero Breaking Changes**: Users without profiles continue to work exactly as before
2. **Gradual Adoption**: Users can start with no profiles and add them as needed
3. **Config File Compatibility**: Existing config files remain valid without modification
4. **Environment Variable Support**: All existing environment variables continue to work

## Practical Example

Consider a user with both global and repository configurations:

**Global config (`~/.kodelet/config.yaml`)**:
```yaml
profiles:
  standard:
    provider: "anthropic"
    model: "sonnet-4"
  premium:
    provider: "anthropic"
    model: "opus-41"
  openai:
    provider: "openai"
    model: "gpt-4.1"
```

**Repository config (`./kodelet-config.yaml`)**:
```yaml
profile: "fast"
profiles:
  fast:
    provider: "anthropic"
    model: "haiku-35"
  premium:  # Overrides global premium
    provider: "anthropic"
    model: "opus-4"
    thinking_budget_tokens: 12000
```

**Command behaviors**:
```bash
# Lists all profiles: standard (global), premium (repo overrides global), openai (global), fast (repo)
kodelet profile list

# Shows "fast (from repository config)" since repo config takes precedence
kodelet profile current

# Updates ./kodelet-config.yaml to set profile: "premium"
kodelet profile use premium

# Updates ~/.kodelet/config.yaml to set profile: "openai"
kodelet profile use openai -g

# Shows the merged configuration for premium (repo version with 12000 thinking tokens)
kodelet profile show premium

# Uses openai profile temporarily without changing any config
kodelet run --profile openai "analyze this"

# Switch to default configuration (base config without any profile)
kodelet profile use default      # Sets profile: "default" in ./kodelet-config.yaml
kodelet profile use default -g   # Sets profile: "default" in ~/.kodelet/config.yaml
```

## Example Use Cases

### Use Case 1: Development vs Production
```yaml
profiles:
  development:
    model: "haiku-35"          # Fast, cost-effective for development
    max_tokens: 4096
    thinking_budget_tokens: 1000
    
  production:
    model: "opus-41"           # Best quality for production work
    max_tokens: 16000
    thinking_budget_tokens: 8000
```

### Use Case 2: Provider Comparison
```yaml
profiles:
  claude-test:
    provider: "anthropic"
    model: "sonnet-4"
    
  openai-test:
    provider: "openai"
    model: "gpt-4.1"
    
  xai-test:
    provider: "openai"
    model: "grok-3"
    openai:
      preset: "xai"
```

### Use Case 3: Task-Specific Profiles
```yaml
profiles:
  code-review:
    model: "opus-41"
    thinking_budget_tokens: 12000  # More thinking for complex reviews
    
  documentation:
    model: "sonnet-4"
    max_tokens: 8192
    
  quick-fix:
    model: "haiku-35"
    max_tokens: 2048
    thinking_budget_tokens: 500
```

## Alternatives Considered

1. **Separate Profile Files**:
   - Store each profile in a separate file (e.g., `~/.kodelet/profiles/premium.yaml`)
   - Rejected: Adds complexity, harder to compare profiles, more files to manage

2. **Profile Inheritance**:
   - Allow profiles to inherit from other profiles
   - Rejected: Adds complexity, potential for circular dependencies, harder to understand

3. **Profile Templates**:
   - Provide built-in profile templates for common use cases
   - Deferred: Can be added later as a enhancement without breaking changes

4. **Auto-Profile Selection**:
   - Automatically select profiles based on task type or repository
   - Rejected: Too magical, reduces user control, hard to predict behavior

## Consequences

### Positive
- **Rapid Experimentation**: Switch between model configurations instantly
- **Organized Configuration**: Named profiles for different use cases
- **Reduced Errors**: Less manual editing of configuration files  
- **Better Testing**: Easy A/B testing of different models
- **Team Collaboration**: Share profile definitions with team via repository config
- **Backward Compatible**: No impact on existing users

### Negative
- **Added Complexity**: New concept for users to learn
- **Configuration Duplication**: Some settings may be repeated across profiles
- **Potential Confusion**: Multiple ways to set configuration values
- **Maintenance Overhead**: Profiles need to be kept up-to-date with model changes

## Implementation Plan

1. **Phase 1: Core Profile System**
   - Add profile types to configuration structures
   - Implement profile loading and merging logic
   - Add profile selection via config file
   - Add validation to prevent "default" profile definition
   - Handle "default" as reserved name for base configuration

2. **Phase 2: CLI Commands**
   - Implement `kodelet profile` command group
   - Add `current`, `list`, `show`, and `use` subcommands
   - Add `--profile` flag to existing commands
   - Add profile validation and error handling
   - Implement config file update logic with proper YAML handling

3. **Phase 3: Enhanced Features**
   - Profile usage statistics and recommendations
   - Profile export/import functionality
   - Profile validation against current API availability
   - Comment preservation in YAML updates

4. **Phase 4: Documentation**
   - Update configuration documentation
   - Add profile examples to config.sample.yaml
   - Create profile best practices guide
   - Document "default" as reserved profile name

## Security Considerations

1. **API Key Management**: Profiles should not store API keys directly
2. **Sensitive Data**: Warn users against storing sensitive data in profiles
3. **Profile Sharing**: Document safe ways to share profiles without exposing secrets

## Future Enhancements

1. **Profile Conditions**: Auto-select profiles based on conditions (e.g., repository, time of day)
2. **Profile Composition**: Combine multiple profiles for complex scenarios
3. **Profile Marketplace**: Community-contributed profiles for specific use cases
4. **Profile Analytics**: Track which profiles are most effective for different tasks
5. **Dynamic Profiles**: Profiles that adapt based on usage patterns and feedback