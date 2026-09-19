package llm

import (
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// GetConfigFromViper loads the selected named model profile and shared settings,
// then resolves model aliases. A profile selection is required.
func GetConfigFromViper() (llmtypes.Config, error) {
	return GetConfigFromViperWithCmd(nil)
}

// GetConfigFromViperWithoutProfile loads only shared configuration. It neither
// requires nor applies model profiles, and returns no model settings.
func GetConfigFromViperWithoutProfile() (llmtypes.Config, error) {
	return loadSharedConfigFromSettings(settingsFromViper())
}

// GetConfigFromViperWithProfile loads configuration from Viper while applying the
// provided profile name instead of the globally active viper profile. This is
// useful for request-scoped profile selection (for example, in the web UI)
// without mutating shared process-wide viper state. An empty name uses the
// configured profile selection; "default" is an ordinary profile name.
func GetConfigFromViperWithProfile(profileName string) (llmtypes.Config, error) {
	return getConfigFromViper(profileName, nil)
}

// GetConfigFromViperWithEnvironmentProfile loads runner-owned configuration from
// the separate environment_profiles namespace without applying a model profile.
func GetConfigFromViperWithEnvironmentProfile(profileName string) (llmtypes.Config, error) {
	return GetConfigFromSettingsWithEnvironmentProfile(settingsFromViper(), profileName)
}

// GetConfigFromSettingsWithEnvironmentProfile resolves a runner-owned settings
// snapshot without consulting or mutating the process-global Viper instance.
func GetConfigFromSettingsWithEnvironmentProfile(settings map[string]any, profileName string) (llmtypes.Config, error) {
	settings = cloneSettings(settings)
	profileName = strings.TrimSpace(profileName)
	if strings.EqualFold(profileName, "default") {
		profileName = ""
	}
	if profileName != "" {
		profiles, _ := settingValueMap(settings["environment_profiles"])
		rawProfile, exists := profiles[profileName]
		if !exists {
			return llmtypes.Config{}, errors.Errorf("failed to apply environment profile: profile '%s' not found", profileName)
		}
		profile, ok := settingValueMap(rawProfile)
		if !ok {
			return llmtypes.Config{}, errors.Errorf("environment profile '%s' must be a mapping", profileName)
		}
		mergeSettings(settings, profile)
	}

	return loadSharedConfigFromSettings(settings)
}

// GetConfigFromViperWithCmd loads the LLM configuration from Viper with command context.
// When a cobra.Command is provided, CLI flags that were explicitly changed take priority
// over profile settings.
func GetConfigFromViperWithCmd(cmd *cobra.Command) (llmtypes.Config, error) {
	return getConfigFromViper("", cmd)
}

// modelSettingKeys belong exclusively to named model profiles. Provider
// connections, authentication, platform selection, model registries and pricing
// remain shared settings and may be overridden by a profile.
var modelSettingKeys = []string{
	"provider",
	"model",
	"weak_model",
	"max_tokens",
	"weak_model_max_tokens",
	"thinking_budget_tokens",
	"reasoning_effort",
	"allowed_reasoning_efforts",
	"openai.api_mode",
	"openai.text_verbosity",
	"openai.enable_search",
	"openai.websocket_mode",
	"openai.service_tier",
	"openai.manual_cache",
	"anthropic.adaptive_thinking",
}

// ValidateModelProfiles validates daemon model configuration at startup. Clients,
// runners and saved-conversation shared loaders must not call this: they do not
// need a default model profile. Removed top-level file and environment settings
// are rejected; explicit model flags remain per-request overrides.
func ValidateModelProfiles() error {
	for _, key := range modelSettingKeys {
		if viper.InConfig(key) {
			return errors.Errorf("top-level model setting %q is not supported; move it into profiles.<name>", key)
		}
		envKey := "KODELET_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		if _, configured := os.LookupEnv(envKey); configured {
			return errors.Errorf("model environment setting %s is not supported; configure %s in profiles.<name> instead", envKey, key)
		}
	}
	if _, err := GetConfigFromViper(); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(viper.GetStringMap("profiles"))) {
		if strings.TrimSpace(name) == "" {
			return errors.New("model profile names must not be empty")
		}
		if _, err := GetConfigFromViperWithProfile(name); err != nil {
			return errors.Wrapf(err, "invalid model profile %q", name)
		}
	}
	return nil
}

// HasConfiguredProfile reports whether a named profile exists in the merged
// Viper configuration.
func HasConfiguredProfile(profileName string) bool {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return false
	}
	_, exists := viper.GetStringMap("profiles")[profileName]
	return exists
}

// IsProfileHidden reports presentation metadata, not a restriction on explicit selection.
func IsProfileHidden(profileName string) bool {
	profile, ok := viper.GetStringMap("profiles")[profileName].(map[string]any)
	if !ok {
		return false
	}
	hidden, _ := profile["hidden"].(bool)
	return hidden
}

// ProviderPlatform returns the normalized platform for a provider's configuration block.
func ProviderPlatform(config llmtypes.Config, provider string) string {
	platform := ""
	switch provider {
	case "openai":
		if config.OpenAI != nil {
			platform = config.OpenAI.Platform
		}
	case "anthropic":
		if config.Anthropic != nil {
			platform = config.Anthropic.Platform
		}
	}
	if platform = strings.ToLower(strings.TrimSpace(platform)); platform == "" {
		return provider
	}
	return platform
}

func getConfigFromViper(profileName string, cmd *cobra.Command) (llmtypes.Config, error) {
	settings := settingsFromViper()
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = getActiveProfile()
	}
	if profileName == "" {
		return llmtypes.Config{}, errors.New("no model profile selected; set profile: <name> or use --profile <name>")
	}

	profiles, _ := settingValueMap(settings["profiles"])
	rawProfile, exists := profiles[profileName]
	if !exists {
		return llmtypes.Config{}, errors.Errorf("failed to apply configuration profile: profile '%s' not found", profileName)
	}
	profile, ok := settingValueMap(rawProfile)
	if !ok {
		return llmtypes.Config{}, errors.Errorf("model profile '%s' must be a mapping", profileName)
	}
	if err := validateModelIdentity(profile); err != nil {
		return llmtypes.Config{}, errors.Wrapf(err, "invalid model profile %q", profileName)
	}

	clearModelSettings(settings)
	mergeSettings(settings, profile)
	if cmd != nil {
		applyExplicitFlagsToSettings(cmd, settings)
	}
	settings["profile"] = profileName
	return GetConfigFromProfile(settings)
}

func validateModelIdentity(settings map[string]any) error {
	for _, key := range []string{"provider", "model"} {
		value, ok := settings[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return errors.Errorf("%s is required and must be a non-empty string in each model profile", key)
		}
	}
	switch strings.ToLower(strings.TrimSpace(settings["provider"].(string))) {
	case "openai", "anthropic":
		return nil
	default:
		return errors.Errorf("unsupported provider %q; supported providers are openai and anthropic", settings["provider"])
	}
}

// GetConfigFromProfile decodes a model profile with field defaults, without
// consulting daemon or workspace settings.
func GetConfigFromProfile(settings llmtypes.ProfileConfig) (llmtypes.Config, error) {
	if err := validateModelIdentity(settings); err != nil {
		return llmtypes.Config{}, err
	}
	defaults := map[string]any{
		"max_tokens":             8192,
		"weak_model_max_tokens":  8192,
		"thinking_budget_tokens": 4048,
	}
	if strings.EqualFold(strings.TrimSpace(settings["provider"].(string)), "openai") {
		defaults["openai"] = map[string]any{
			"api_mode":       "responses",
			"enable_search":  true,
			"websocket_mode": true,
		}
	}
	mergeSettings(defaults, settings)
	config, err := loadConfigFromSettings(defaults)
	if err != nil {
		return config, err
	}
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	if err := llmtypes.NormalizeReasoningConfig(&config); err != nil {
		return config, err
	}

	config.Aliases = withDefaultModelAliases(config.Aliases)
	config.Model = resolveModelAlias(strings.TrimSpace(config.Model), config.Aliases)
	config.WeakModel = resolveModelAlias(strings.TrimSpace(config.WeakModel), config.Aliases)
	config.ModelAliasesResolved = true
	return config, nil
}

func settingsFromViper() map[string]any {
	settings := cloneSettings(viper.AllSettings())
	// Read these maps directly: AllSettings splits literal dots in model IDs.
	for _, path := range []string{"aliases", "openai.pricing", "profiles", "environment_profiles"} {
		if value := viper.Get(path); value != nil {
			setSetting(settings, path, value)
		}
	}
	return settings
}

func loadSharedConfigFromSettings(settings map[string]any) (llmtypes.Config, error) {
	clearModelSettings(settings)
	delete(settings, "profile")
	delete(settings, "profiles")
	config, err := loadConfigFromSettings(settings)
	if err != nil {
		return config, err
	}
	config.Aliases = withDefaultModelAliases(config.Aliases)
	return config, nil
}

func clearModelSettings(settings map[string]any) {
	for _, key := range modelSettingKeys {
		parent, child, nested := strings.Cut(key, ".")
		if !nested {
			delete(settings, key)
			continue
		}
		if values, ok := settingValueMap(settings[parent]); ok {
			delete(values, child)
			if len(values) == 0 {
				delete(settings, parent)
			} else {
				settings[parent] = values
			}
		}
	}
}

// applyExplicitFlagsToSettings sets explicitly changed CLI flag values into a local settings map.
func applyExplicitFlagsToSettings(cmd *cobra.Command, settings map[string]any) {
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		viperKey := explicitFlagViperKey(flag.Name)
		if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
			setSetting(settings, viperKey, sliceValue.GetSlice())
			return
		}
		if flag.Value.Type() == "stringToString" {
			if mapValue, err := cmd.Flags().GetStringToString(flag.Name); err == nil {
				setSetting(settings, viperKey, mapValue)
				return
			}
		}
		setSetting(settings, viperKey, flag.Value.String())
	})
}

func explicitFlagViperKey(flagName string) string {
	if viperKey, ok := explicitFlagKeyOverrides[flagName]; ok {
		return viperKey
	}
	return strings.ReplaceAll(flagName, "-", "_")
}

var explicitFlagKeyOverrides = map[string]string{
	"enable-openai-search":       "openai.enable_search",
	"context-patterns":           "context.patterns",
	"tracing-enabled":            "tracing.enabled",
	"tracing-sampler":            "tracing.sampler",
	"tracing-ratio":              "tracing.ratio",
	"tracing-capture-content":    "tracing.capture_content",
	"tracing-internal-rpc-spans": "tracing.internal_rpc_spans",
	"sysprompt":                  "sysprompt",
	"sysprompt-arg":              "sysprompt_args",
}

func loadConfigFromSettings(settings map[string]any) (llmtypes.Config, error) {
	var config llmtypes.Config
	// Model aliases and pricing registries use literal model IDs such as
	// "gpt-5.6" as keys, rather than dot-delimited configuration paths.
	v := viper.NewWithOptions(viper.KeyDelimiter("::"))
	for key, value := range settings {
		v.Set(key, value)
	}

	// Use viper's automatic unmarshaling with mapstructure tags
	if err := v.Unmarshal(&config); err != nil {
		return config, errors.Wrap(err, "failed to unmarshal configuration")
	}
	if config.OpenAI != nil {
		if err := llmtypes.NormalizeOpenAITextVerbosity(&config); err != nil {
			return config, err
		}
	}

	if config.Bash == nil {
		config.Bash = &llmtypes.BashConfig{Timeout: llmtypes.DefaultBashTimeout}
	} else if config.Bash.Timeout == 0 {
		config.Bash.Timeout = llmtypes.DefaultBashTimeout
	}
	if config.Bash.Timeout < llmtypes.MinBashTimeout {
		return config, errors.Errorf("bash.timeout must be at least %s", llmtypes.MinBashTimeout)
	}

	// Set default anthropic_api_access if empty
	if config.AnthropicAPIAccess == "" {
		config.AnthropicAPIAccess = llmtypes.AnthropicAPIAccessAuto
	}

	if _, ok := settings["compact_ratio"]; !ok {
		config.CompactRatio = llmtypes.DefaultCompactRatio
	}
	if err := validateCompactRatio(config.CompactRatio); err != nil {
		return config, err
	}

	// Apply retry defaults if not set
	if config.Retry.Attempts == 0 {
		config.Retry = llmtypes.DefaultRetryConfig
	}

	return config, nil
}

func validateCompactRatio(ratio float64) error {
	if ratio <= 0.0 || ratio > 1.0 {
		return errors.New("compact_ratio must be greater than 0.0 and less than or equal to 1.0")
	}
	return nil
}

func cloneSettings(settings map[string]any) map[string]any {
	cloned := make(map[string]any, len(settings))
	for key, value := range settings {
		cloned[key] = cloneSettingValue(value)
	}
	return cloned
}

func cloneSettingValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneSettings(typed)
	case llmtypes.ProfileConfig:
		return cloneSettings(map[string]any(typed))
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for k, v := range typed {
			cloned[k] = v
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneSettingValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func mergeSettings(settings map[string]any, overrides map[string]any) {
	for key, value := range overrides {
		if value == nil {
			continue
		}

		existing, exists := settings[key]
		if exists {
			if merged, ok := mergeSettingValue(existing, value); ok {
				settings[key] = merged
				continue
			}
		}

		settings[key] = cloneSettingValue(value)
	}
}

func mergeSettingValue(base any, override any) (map[string]any, bool) {
	baseMap, ok := settingValueMap(base)
	if !ok {
		return nil, false
	}

	overrideMap, ok := settingValueMap(override)
	if !ok {
		return nil, false
	}

	merged := cloneSettings(baseMap)
	mergeSettings(merged, overrideMap)

	return merged, true
}

func settingValueMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case llmtypes.ProfileConfig:
		return map[string]any(typed), true
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = item
		}
		return converted, true
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	converted := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		converted[iter.Key().String()] = iter.Value().Interface()
	}

	return converted, true
}

func setSetting(settings map[string]any, key string, value any) {
	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		settings[key] = cloneSettingValue(value)
		return
	}

	current := settings
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok || next == nil {
			next = make(map[string]any)
			current[part] = next
		}
		current = next
	}

	current[parts[len(parts)-1]] = cloneSettingValue(value)
}

func getActiveProfile() string {
	return strings.TrimSpace(viper.GetString("profile"))
}
