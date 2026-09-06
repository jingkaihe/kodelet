package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/pkg/errors"
)

// ExecutionOptions is the credential-free request contract shared by clients,
// execution presets, and the runner protocol. Pointers distinguish omission
// from explicit false, zero, and empty allowlists. These are not Config values.
type ExecutionOptions struct {
	Provider             *string   `json:"provider,omitempty"`
	Model                *string   `json:"model,omitempty"`
	WeakModel            *string   `json:"weakModel,omitempty"`
	MaxTokens            *int      `json:"maxTokens,omitempty"`
	WeakModelMaxTokens   *int      `json:"weakModelMaxTokens,omitempty"`
	ThinkingBudgetTokens *int      `json:"thinkingBudgetTokens,omitempty"`
	ReasoningEffort      *string   `json:"reasoningEffort,omitempty"`
	MaxTurns             *int      `json:"maxTurns,omitempty"`
	UseWeakModel         *bool     `json:"useWeakModel,omitempty"`
	NoTools              *bool     `json:"noTools,omitempty"`
	NoExtensions         *bool     `json:"noExtensions,omitempty"`
	NoSkills             *bool     `json:"noSkills,omitempty"`
	AllowedTools         *[]string `json:"allowedTools,omitempty"` // Explicit catalog selection within inherited policy, independent of tool mode.
	AllowedCommands      *[]string `json:"allowedCommands,omitempty"`
	EnableFSSearchTools  *bool     `json:"enableFSSearchTools,omitempty"`
}

// MarshalJSON preserves deny-all when a Go caller supplies a non-nil pointer
// to a nil slice. Such lists are empty restrictions, never null/inheritance.
func (o ExecutionOptions) MarshalJSON() ([]byte, error) {
	type wireOptions ExecutionOptions
	return json.Marshal(wireOptions(*o.Clone()))
}

// UnmarshalJSON rejects unknown options rather than silently dropping client
// settings, including removed persistence flags and arbitrary configuration.
func (o *ExecutionOptions) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("execution options must be an object, not null; omit options to inherit")
	}
	type wireOptions ExecutionOptions
	var value wireOptions
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return errors.Wrap(err, "invalid execution options")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing execution options data")
	}
	// Null is not a synonym for an omitted restriction. Requiring callers to
	// omit absent fields prevents null/empty-array mistakes from widening tools.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.Errorf("execution option %s must not be null; omit it to inherit", name)
		}
	}
	options := ExecutionOptions(value)
	if err := options.Validate(); err != nil {
		return err
	}
	*o = options
	return nil
}

// Clone returns a fully independent copy, preserving explicit empty lists.
func (o *ExecutionOptions) Clone() *ExecutionOptions {
	if o == nil {
		return nil
	}
	c := *o
	c.Provider = cloneOption(o.Provider)
	c.Model = cloneOption(o.Model)
	c.WeakModel = cloneOption(o.WeakModel)
	c.MaxTokens = cloneOption(o.MaxTokens)
	c.WeakModelMaxTokens = cloneOption(o.WeakModelMaxTokens)
	c.ThinkingBudgetTokens = cloneOption(o.ThinkingBudgetTokens)
	c.ReasoningEffort = cloneOption(o.ReasoningEffort)
	c.MaxTurns = cloneOption(o.MaxTurns)
	c.UseWeakModel = cloneOption(o.UseWeakModel)
	c.NoTools = cloneOption(o.NoTools)
	c.NoExtensions = cloneOption(o.NoExtensions)
	c.NoSkills = cloneOption(o.NoSkills)
	c.EnableFSSearchTools = cloneOption(o.EnableFSSearchTools)
	if o.AllowedTools != nil {
		values := append([]string{}, (*o.AllowedTools)...)
		c.AllowedTools = &values
	}
	if o.AllowedCommands != nil {
		values := append([]string{}, (*o.AllowedCommands)...)
		c.AllowedCommands = &values
	}
	return &c
}

func cloneOption[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}

// Validate validates values without accessing credentials, files, or providers.
func (o *ExecutionOptions) Validate() error {
	if o == nil {
		return nil
	}
	for name, value := range map[string]*string{"provider": o.Provider, "model": o.Model, "weakModel": o.WeakModel, "reasoningEffort": o.ReasoningEffort} {
		if value != nil && (strings.TrimSpace(*value) == "" || strings.ContainsRune(*value, '\x00')) {
			return errors.Errorf("execution option %s must be nonempty and contain no NUL", name)
		}
	}
	if o.Provider != nil && *o.Provider != "openai" && *o.Provider != "anthropic" {
		return errors.Errorf("unsupported execution provider %q", *o.Provider)
	}
	for name, value := range map[string]*int{"maxTokens": o.MaxTokens, "weakModelMaxTokens": o.WeakModelMaxTokens} {
		if value != nil && *value <= 0 {
			return errors.Errorf("execution option %s must be positive", name)
		}
	}
	for name, value := range map[string]*int{"maxTurns": o.MaxTurns, "thinkingBudgetTokens": o.ThinkingBudgetTokens} {
		if value != nil && *value < 0 {
			return errors.Errorf("execution option %s must not be negative", name)
		}
	}
	if o.ReasoningEffort != nil {
		if _, err := NormalizeReasoningEffort(*o.ReasoningEffort); err != nil {
			return err
		}
	}
	for name, values := range map[string]*[]string{"allowedTools": o.AllowedTools, "allowedCommands": o.AllowedCommands} {
		if values == nil {
			continue
		}
		for _, value := range *values {
			if strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00') {
				return errors.Errorf("execution option %s contains an empty or invalid entry", name)
			}
		}
	}
	return nil
}

// HasModelOptions reports fields locked by conversation model snapshots.
func (o *ExecutionOptions) HasModelOptions() bool {
	return o != nil && (o.Provider != nil || o.Model != nil || o.WeakModel != nil || o.MaxTokens != nil || o.WeakModelMaxTokens != nil || o.ThinkingBudgetTokens != nil || o.ReasoningEffort != nil)
}

// Restrictions strips central model/turn choices from runner-bound options.
func (o *ExecutionOptions) Restrictions() *ExecutionOptions {
	if o == nil {
		return nil
	}
	c := o.Clone()
	c.Provider, c.Model, c.WeakModel, c.ReasoningEffort = nil, nil, nil, nil
	c.MaxTokens, c.WeakModelMaxTokens, c.ThinkingBudgetTokens, c.MaxTurns = nil, nil, nil, nil
	c.UseWeakModel = nil
	return c
}

// ToolsDisabled includes the explicit-empty allowlist form of tool-free runs.
func (o *ExecutionOptions) ToolsDisabled() bool {
	return o != nil && ((o.NoTools != nil && *o.NoTools) || (o.AllowedTools != nil && len(*o.AllowedTools) == 0))
}

// ToolAllowed enforces request restrictions independently of discovery and
// extension tool-list patches. An empty command list also disables bash.
func (o *ExecutionOptions) ToolAllowed(name string) bool {
	if o == nil {
		return true
	}
	if o.ToolsDisabled() || (name == "bash" && o.AllowedCommands != nil && len(*o.AllowedCommands) == 0) {
		return false
	}
	if name == "skill" && o.NoSkills != nil && *o.NoSkills {
		return false
	}
	if (name == "glob_tool" || name == "grep_tool") && o.EnableFSSearchTools != nil && !*o.EnableFSSearchTools {
		return false
	}
	return o.AllowedTools == nil || slices.Contains(*o.AllowedTools, name)
}

// EnvironmentOptions returns only non-secret environment restrictions, narrowed
// by the owning host's policy. Empty legacy Config allowlists mean unrestricted;
// empty ExecutionOptions allowlists mean deny all.
func (c Config) EnvironmentOptions() *ExecutionOptions {
	o := c.ExecutionOptions.Restrictions()
	if o == nil {
		o = &ExecutionOptions{}
	}
	o.AllowedTools = intersectPolicy(c.AllowedTools, o.AllowedTools)
	o.AllowedCommands = intersectPolicy(c.AllowedCommands, o.AllowedCommands)
	if c.Skills != nil && !c.Skills.Enabled {
		o.NoSkills = new(true)
	}
	if enabled, ok := c.ExtensionSettings["enabled"].(bool); ok && !enabled {
		o.NoExtensions = new(true)
	}
	return o
}

func intersectPolicy(host []string, requested *[]string) *[]string {
	if len(host) == 0 {
		return intersectRestrictions(nil, requested)
	}
	return intersectRestrictions(&host, requested)
}

func intersectRestrictions(host, requested *[]string) *[]string {
	if host == nil {
		if requested == nil {
			return nil
		}
		values := append([]string{}, (*requested)...)
		return &values
	}
	values := make([]string, 0, len(*host))
	for _, value := range *host {
		if requested == nil || slices.Contains(*requested, value) {
			values = append(values, value)
		}
	}
	return &values
}

// ApplyEnvironmentOptions snapshots runner settings and applies only narrowing
// restrictions before extension or tool discovery. Model settings stay central.
func ApplyEnvironmentOptions(config Config, options *ExecutionOptions) (Config, error) {
	if err := options.Validate(); err != nil {
		return Config{}, err
	}
	if options.HasModelOptions() || (options != nil && (options.MaxTurns != nil || options.UseWeakModel != nil)) {
		return Config{}, errors.New("runner options may contain only environment restrictions")
	}
	host := config.EnvironmentOptions()
	config = config.Clone()
	config.ExecutionOptions = options.Clone()
	if config.ExecutionOptions == nil {
		config.ExecutionOptions = &ExecutionOptions{}
	}
	o := config.ExecutionOptions
	o.AllowedTools = intersectRestrictions(host.AllowedTools, o.AllowedTools)
	o.AllowedCommands = intersectRestrictions(host.AllowedCommands, o.AllowedCommands)
	for _, restriction := range []struct {
		host   *bool
		target **bool
	}{{host.NoTools, &o.NoTools}, {host.NoExtensions, &o.NoExtensions}, {host.NoSkills, &o.NoSkills}} {
		if restriction.host != nil && *restriction.host {
			*restriction.target = new(true)
		}
	}
	if host.EnableFSSearchTools != nil && !*host.EnableFSSearchTools {
		config.EnableFSSearchTools = false
	}
	if o.EnableFSSearchTools != nil {
		// The raw config flag chooses the default tool presentation. Only an
		// explicit inherited restriction prevents selecting filesystem search.
		if *o.EnableFSSearchTools && host.EnableFSSearchTools != nil && !*host.EnableFSSearchTools {
			return Config{}, errors.New("enableFSSearchTools cannot enable a feature disabled by runner policy")
		}
		config.EnableFSSearchTools = *o.EnableFSSearchTools
	} else {
		o.EnableFSSearchTools = host.EnableFSSearchTools
	}
	if o.AllowedCommands != nil {
		// Bash consumes Config.AllowedCommands; keep the explicit-empty deny
		// marker in ExecutionOptions as legacy empty Config lists allow all.
		config.AllowedCommands = slices.Clone(*o.AllowedCommands)
	}
	if o.NoExtensions != nil && *o.NoExtensions {
		if config.ExtensionSettings == nil {
			config.ExtensionSettings = map[string]any{}
		}
		config.ExtensionSettings["enabled"] = false
		config.Extensions = nil
	}
	if o.NoSkills != nil && *o.NoSkills {
		config.Skills = &SkillsConfig{Enabled: false}
	}
	return config, nil
}
