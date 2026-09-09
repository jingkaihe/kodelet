package client

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func embeddedConfigTestSettings(cwd string) map[string]any {
	return map[string]any{
		"tool_mode":              "patch",
		"sysprompt":              filepath.Join(cwd, "prompt.tmpl"),
		"sysprompt_args":         map[string]any{"origin": "daemon", "purpose": "review"},
		"enable_fs_search_tools": true,
		"bash":                   map[string]any{"timeout": "45s"},
		"context":                map[string]any{"patterns": []string{"AGENTS.md", "CONTRIBUTING.md"}},
		"skills":                 map[string]any{"enabled": true, "allowed": []string{"review", "build"}},
		"extensions": map[string]any{
			"enabled":         true,
			"allow":           []string{"inspect", "format"},
			"deny":            []string{"unsafe"},
			"global_dir":      filepath.Join(cwd, "global-extensions"),
			"local_dir":       filepath.Join(cwd, "local-extensions"),
			"max_output_size": 4096,
			"tools": map[string]any{
				"inspect": map[string]any{"enabled": true},
				"write":   map[string]any{"enabled": false},
			},
		},
		"allowed_domains_file": filepath.Join(cwd, "domains.txt"),
		"allowed_tools":        []string{"file_read", "bash"},
		"allowed_commands":     []string{"git status", "git diff"},
		"environment_profiles": map[string]any{
			"review": map[string]any{
				"tool_mode":      "patch",
				"sysprompt_args": map[string]any{"origin": "environment"},
				"allowed_tools":  []string{"file_read"},
				"model":          "environment-model",
				"provider":       "environment-provider",
				"openai":         map[string]any{"api_key": "environment-secret"},
			},
		},
		"model":                "daemon-model",
		"weak_model":           "daemon-weak-model",
		"provider":             "daemon-provider",
		"max_tokens":           8192,
		"anthropic_api_access": "api-key",
		"anthropic_account":    "daemon-account",
		"aliases":              map[string]any{"private-alias": "daemon-model"},
		"openai":               map[string]any{"api_key": "openai-secret", "base_url": "https://openai.invalid"},
		"anthropic":            map[string]any{"api_key": "anthropic-secret", "base_url": "https://anthropic.invalid"},
	}
}

func assertEmbeddedConfigEnvironment(t *testing.T, config llmtypes.Config, cwd string) {
	t.Helper()
	assert.Equal(t, cwd, config.WorkingDirectory)
	assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
	assert.Equal(t, filepath.Join(cwd, "prompt.tmpl"), config.Sysprompt)
	assert.Equal(t, map[string]string{"origin": "daemon", "purpose": "review"}, config.SyspromptArgs)
	assert.True(t, config.EnableFSSearchTools)
	assert.Equal(t, 45*time.Second, config.BashTimeout())
	require.NotNil(t, config.Context)
	assert.Equal(t, []string{"AGENTS.md", "CONTRIBUTING.md"}, config.Context.Patterns)
	require.NotNil(t, config.Skills)
	assert.True(t, config.Skills.Enabled)
	assert.Equal(t, []string{"review", "build"}, config.Skills.Allowed)
	extensionsConfig, err := extensions.LoadConfigFromSettings(config.ExtensionSettings)
	require.NoError(t, err)
	assert.Equal(t, extensions.Config{
		Enabled:       true,
		Allow:         []string{"inspect", "format"},
		Deny:          []string{"unsafe"},
		GlobalDir:     filepath.Join(cwd, "global-extensions"),
		LocalDir:      filepath.Join(cwd, "local-extensions"),
		MaxOutputSize: 4096,
		Tools: map[string]extensions.ToolConfig{
			"inspect": {Enabled: new(true)},
			"write":   {Enabled: new(false)},
		},
	}, extensionsConfig)
	assert.Equal(t, filepath.Join(cwd, "domains.txt"), config.AllowedDomainsFile)
	assert.Equal(t, []string{"file_read", "bash"}, config.AllowedTools)
	assert.Equal(t, []string{"git status", "git diff"}, config.AllowedCommands)
	require.NotNil(t, config.ExecutionOptions)
	assert.Equal(t, new([]string{"file_read", "bash"}), config.ExecutionOptions.AllowedTools)
	assert.Equal(t, new([]string{"git status", "git diff"}), config.ExecutionOptions.AllowedCommands)
	require.Contains(t, config.EnvironmentProfiles, "review")
	assert.Equal(t, "patch", config.EnvironmentProfiles["review"]["tool_mode"])
	for _, key := range []string{"model", "provider", "openai"} {
		assert.NotContains(t, config.EnvironmentProfiles["review"], key)
	}
	assert.Empty(t, config.Model)
	assert.Empty(t, config.WeakModel)
	assert.Empty(t, config.Provider)
	assert.Zero(t, config.MaxTokens)
	assert.Empty(t, config.Profile)
	assert.Empty(t, config.Profiles)
	assert.Empty(t, config.AnthropicAccount)
	assert.NotEqual(t, llmtypes.AnthropicAPIAccessAPIKey, config.AnthropicAPIAccess)
	assert.NotContains(t, config.Aliases, "private-alias")
	assert.Nil(t, config.OpenAI)
	assert.Nil(t, config.Anthropic)
	assert.Nil(t, config.Extensions, "loading settings must not start an extension runtime")
}

func TestEmbeddedConfigInheritsEnvironmentOnly(t *testing.T) {
	for _, source := range []string{"defaults", "model profile", "serve overrides"} {
		t.Run(source, func(t *testing.T) {
			cwd := t.TempDir()
			settings := embeddedConfigTestSettings(cwd)
			var defaults, overrides map[string]any
			modelProfile := ""
			switch source {
			case "defaults":
				defaults = settings
			case "model profile":
				defaults = map[string]any{"profiles": map[string]any{"deep": settings}}
				modelProfile = "deep"
			case "serve overrides":
				overrides = settings
			}
			loader, err := NewEmbeddedConfigLoader(defaults, overrides)
			require.NoError(t, err)
			config, err := loader(cwd, modelProfile, "")
			require.NoError(t, err)
			assertEmbeddedConfigEnvironment(t, config, cwd)
			assert.Equal(t, embeddedConfigTestSettings(cwd), settings, "loading must not mutate caller settings")
		})
	}
}

func TestEmbeddedConfigModelProfilesConcurrent(t *testing.T) {
	// Any accidental fallback to user configuration must stay inside this test.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("KODELET_MODEL", "process-model-must-not-be-used")
	t.Setenv("KODELET_PROFILE", "process-profile-must-not-be-used")
	processCWD, err := os.Getwd()
	require.NoError(t, err)
	globalSettings := viper.AllSettings()
	defaults := map[string]any{
		"profile":                "deep",
		"tool_mode":              "full",
		"enable_fs_search_tools": false,
		"sysprompt_args":         map[string]any{"origin": "base", "shared": "pinned"},
		"profiles": map[string]any{
			"deep": map[string]any{
				"tool_mode": "patch", "enable_fs_search_tools": false,
				"sysprompt_args": map[string]any{"origin": "deep"},
			},
			"flair": map[string]any{
				"tool_mode": "full", "enable_fs_search_tools": true,
				"sysprompt_args": map[string]any{"origin": "flair"},
			},
			"default": map[string]any{"sysprompt_args": map[string]any{"origin": "not-the-default"}},
		},
	}
	loader, err := NewEmbeddedConfigLoader(defaults, nil)
	require.NoError(t, err)
	directories := []string{t.TempDir(), t.TempDir()}
	for _, cwd := range directories {
		require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"),
			[]byte("sysprompt_args:\n  directory: "+cwd+"\n"), 0o600))
	}
	cases := []struct {
		profile string
		origin  string
		mode    llmtypes.ToolMode
		search  bool
	}{
		{"", "deep", llmtypes.ToolModePatch, false},
		{"  ", "deep", llmtypes.ToolModePatch, false},
		{"default", "base", llmtypes.ToolModeFull, false},
		{" DEFAULT ", "base", llmtypes.ToolModeFull, false},
		{"deep", "deep", llmtypes.ToolModePatch, false},
		{"flair", "flair", llmtypes.ToolModeFull, true},
	}
	var wg sync.WaitGroup
	for i := range 48 {
		wg.Go(func() {
			test := cases[i%len(cases)]
			cwd := directories[(i/len(cases))%len(directories)]
			config, err := loader(cwd, test.profile, "")
			if !assert.NoError(t, err, "model profile %q", test.profile) {
				return
			}
			assert.Equal(t, cwd, config.WorkingDirectory)
			assert.Equal(t, test.mode, config.ToolMode, "model profile %q", test.profile)
			assert.Equal(t, test.search, config.EnableFSSearchTools, "model profile %q", test.profile)
			assert.Equal(t, map[string]string{"origin": test.origin, "shared": "pinned", "directory": cwd}, config.SyspromptArgs)
			assert.Empty(t, config.Model)
			assert.Empty(t, config.Profile)
			if config.SyspromptArgs != nil {
				config.SyspromptArgs["shared"] = "mutated result"
			}
		})
	}
	wg.Wait()
	currentCWD, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, processCWD, currentCWD)
	assert.Equal(t, globalSettings, viper.AllSettings())
}

func TestEmbeddedConfigSnapshotsDoNotLeak(t *testing.T) {
	cwd := t.TempDir()
	defaults := embeddedConfigTestSettings(cwd)
	profile := map[string]any{"tool_mode": "patch", "sysprompt_args": map[string]any{"origin": "daemon"}}
	defaults["profile"] = "deep"
	defaults["profiles"] = map[string]any{"deep": profile}
	overrides := map[string]any{
		"sysprompt_args": map[string]any{"origin": "daemon"},
		"extensions":     map[string]any{"max_output_size": 4096},
	}
	loader, err := NewEmbeddedConfigLoader(defaults, overrides)
	require.NoError(t, err)

	defaults["profile"] = "missing"
	defaults["tool_mode"] = "full"
	defaults["sysprompt_args"].(map[string]any)["purpose"] = "changed input"
	defaults["bash"].(map[string]any)["timeout"] = "1s"
	defaults["context"].(map[string]any)["patterns"].([]string)[0] = "CHANGED.md"
	defaults["skills"].(map[string]any)["allowed"].([]string)[0] = "changed-skill"
	defaults["allowed_tools"].([]string)[0] = "changed-tool"
	defaults["allowed_commands"].([]string)[0] = "changed-command"
	defaultExtensions := defaults["extensions"].(map[string]any)
	defaultExtensions["allow"].([]string)[0] = "changed-extension"
	defaultExtensions["deny"].([]string)[0] = "changed-denial"
	defaultExtensions["tools"].(map[string]any)["write"].(map[string]any)["enabled"] = true
	defaults["environment_profiles"].(map[string]any)["review"].(map[string]any)["tool_mode"] = "full"
	profile["tool_mode"] = "full"
	profile["sysprompt_args"].(map[string]any)["origin"] = "changed-profile"
	overrides["sysprompt_args"].(map[string]any)["origin"] = "changed-override"
	overrides["extensions"].(map[string]any)["max_output_size"] = 1

	config, err := loader(cwd, "", "")
	require.NoError(t, err)
	assertEmbeddedConfigEnvironment(t, config, cwd)
	config.SyspromptArgs["purpose"] = "changed result"
	config.Bash.Timeout = time.Second
	config.Context.Patterns[0] = "CHANGED.md"
	config.Skills.Allowed[0] = "changed-skill"
	config.AllowedTools[0] = "changed-tool"
	config.AllowedCommands[0] = "changed-command"
	require.NotNil(t, config.ExecutionOptions.AllowedTools)
	(*config.ExecutionOptions.AllowedTools)[0] = "changed-execution-tool"
	clear(config.ExtensionSettings["tools"].(map[string]any))
	config.ExtensionSettings["max_output_size"] = 1
	config.EnvironmentProfiles["review"]["tool_mode"] = "full"
	config.EnvironmentProfiles["review"]["sysprompt_args"].(map[string]any)["origin"] = "changed environment result"

	again, err := loader(cwd, "deep", "")
	require.NoError(t, err)
	assertEmbeddedConfigEnvironment(t, again, cwd)
	review, err := loader(cwd, "deep", "review")
	require.NoError(t, err)
	assert.Equal(t, "environment", review.SyspromptArgs["origin"])
	assert.Equal(t, []string{"file_read"}, review.AllowedTools)
}

func TestEmbeddedConfigServeOverridesWinModelProfile(t *testing.T) {
	cwd := t.TempDir()
	defaults := map[string]any{
		"profile":        "deep",
		"sysprompt_args": map[string]any{"base_only": "retained"},
		"profiles": map[string]any{
			"deep": map[string]any{
				"tool_mode": "full", "enable_fs_search_tools": true,
				"sysprompt": "model.tmpl", "bash": map[string]any{"timeout": "90s"},
				"sysprompt_args": map[string]any{"origin": "model", "model_only": "retained"},
				"context":        map[string]any{"patterns": []string{"MODEL.md"}},
				"allowed_tools":  []string{"bash"}, "allowed_commands": []string{"git diff"},
				"skills": map[string]any{"enabled": true, "allowed": []string{"build"}},
				"extensions": map[string]any{
					"enabled": true, "allow": []string{"format"}, "deny": []string{"unsafe"},
					"tools": map[string]any{"write": map[string]any{"enabled": true}},
				},
			},
		},
	}
	overrides := embeddedConfigTestSettings(cwd)
	overrides["enable_fs_search_tools"] = false
	loader, err := NewEmbeddedConfigLoader(defaults, overrides)
	require.NoError(t, err)
	for _, profile := range []string{"", "deep"} {
		config, err := loader(cwd, profile, "")
		require.NoError(t, err)
		assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
		assert.False(t, config.EnableFSSearchTools, "explicit false must override a profile's true")
		assert.Equal(t, filepath.Join(cwd, "prompt.tmpl"), config.Sysprompt)
		assert.Equal(t, map[string]string{
			"origin": "daemon", "purpose": "review", "base_only": "retained", "model_only": "retained",
		}, config.SyspromptArgs)
		assert.Equal(t, 45*time.Second, config.BashTimeout())
		require.NotNil(t, config.Context)
		assert.Equal(t, []string{"AGENTS.md", "CONTRIBUTING.md"}, config.Context.Patterns)
		assert.Equal(t, []string{"file_read", "bash"}, config.AllowedTools)
		assert.Equal(t, []string{"git diff"}, config.AllowedCommands)
		assert.Equal(t, new([]string{"bash"}), config.ExecutionOptions.AllowedTools, "runner overrides cannot relax inherited model permissions")
		require.NotNil(t, config.Skills)
		assert.Equal(t, []string{"review", "build"}, config.Skills.Allowed)
		extensionConfig, err := extensions.LoadConfigFromSettings(config.ExtensionSettings)
		require.NoError(t, err)
		assert.Equal(t, []string{"inspect", "format"}, extensionConfig.Allow)
		assert.Equal(t, new(false), extensionConfig.Tools["write"].Enabled)
	}
}

func TestEmbeddedConfigEnvironmentProfiles(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte(`profile: repository-only
model: repository-model
provider: repository-provider
sysprompt_args:
  workspace: retained
profiles:
  deep:
    tool_mode: full
  repository-only:
    tool_mode: full
environment_profiles:
  review:
    allowed_tools: [bash]
    sysprompt_args:
      origin: repository
  repository-only:
    tool_mode: full
`), 0o600))
	defaults := map[string]any{
		"tool_mode": "full", "allowed_tools": []string{"file_read", "bash"},
		"environment_profiles": map[string]any{
			"review": map[string]any{
				"allowed_tools":  []string{"file_read"},
				"sysprompt_args": map[string]any{"origin": "inherited", "common": "retained"},
			},
		},
		"profiles": map[string]any{
			"deep": map[string]any{
				"tool_mode": "patch",
				"environment_profiles": map[string]any{
					"model-local": map[string]any{"sysprompt_args": map[string]any{"origin": "model-local"}},
				},
			},
		},
	}
	for _, override := range []bool{false, true} {
		name, origin := "inherited", "inherited"
		var overrides map[string]any
		if override {
			name, origin = "serve override", "serve"
			overrides = map[string]any{"environment_profiles": map[string]any{
				"review":      map[string]any{"sysprompt_args": map[string]any{"origin": "serve"}},
				"serve-local": map[string]any{"sysprompt_args": map[string]any{"origin": "serve-local"}},
			}}
		}
		t.Run(name, func(t *testing.T) {
			loader, err := NewEmbeddedConfigLoader(defaults, overrides)
			require.NoError(t, err)
			for _, modelProfile := range []string{"default", "deep"} {
				config, err := loader(cwd, modelProfile, "review")
				require.NoError(t, err)
				assert.Equal(t, origin, config.SyspromptArgs["origin"])
				assert.Equal(t, "retained", config.SyspromptArgs["common"])
				assert.Equal(t, "retained", config.SyspromptArgs["workspace"])
				assert.Equal(t, []string{"file_read"}, config.AllowedTools)
				assert.NotContains(t, config.EnvironmentProfiles, "repository-only")
				assert.Empty(t, config.Model)
				assert.Empty(t, config.Provider)
				if modelProfile == "deep" {
					assert.Equal(t, llmtypes.ToolModePatch, config.ToolMode)
				}
			}
			for _, environmentProfile := range []string{"", "default"} {
				config, err := loader(cwd, "deep", environmentProfile)
				require.NoError(t, err)
				assert.Equal(t, []string{"file_read", "bash"}, config.AllowedTools)
				assert.NotContains(t, config.SyspromptArgs, "origin")
			}
			localProfiles := []string{"model-local"}
			if override {
				localProfiles = append(localProfiles, "serve-local")
			}
			for _, environmentProfile := range localProfiles {
				config, err := loader(cwd, "deep", environmentProfile)
				require.NoError(t, err)
				assert.Equal(t, environmentProfile, config.SyspromptArgs["origin"])
			}
			for _, profiles := range [][2]string{{"repository-only", ""}, {"deep", "repository-only"}, {"review", ""}, {"deep", "deep"}} {
				_, err := loader(cwd, profiles[0], profiles[1])
				assert.Error(t, err, "model/environment profiles %q must not cross namespaces or use repository definitions", profiles)
			}
		})
	}
}

func TestEmbeddedConfigRepositoryRestrictions(t *testing.T) {
	for _, test := range []struct {
		name    string
		yaml    string
		wantErr bool
		check   func(*testing.T, llmtypes.Config)
	}{
		{name: "widen tools", yaml: "allowed_tools: [file_read, bash, file_write]", wantErr: true},
		{name: "clear tools", yaml: "allowed_tools: []", wantErr: true},
		{name: "widen commands", yaml: "allowed_commands: ['*']", wantErr: true},
		{name: "clear commands", yaml: "allowed_commands: []", wantErr: true},
		{name: "widen skills", yaml: "skills:\n  allowed: [review, build, deploy]", wantErr: true},
		{name: "clear skills", yaml: "skills:\n  allowed: []", wantErr: true},
		{name: "widen extensions", yaml: "extensions:\n  allow: [inspect, format, execute]", wantErr: true},
		{name: "clear extension allowlist", yaml: "extensions:\n  allow: []", wantErr: true},
		{name: "remove extension denial", yaml: "extensions:\n  deny: [other]", wantErr: true},
		{name: "clear extension denylist", yaml: "extensions:\n  deny: []", wantErr: true},
		{name: "enable disabled extension tool", yaml: "extensions:\n  tools:\n    write:\n      enabled: true", wantErr: true},
		{name: "replace domains file", yaml: "allowed_domains_file: repository-domains.txt", wantErr: true},
		{name: "clear domains file", yaml: "allowed_domains_file: ''", wantErr: true},
		{
			name: "narrow tools", yaml: "allowed_tools: [file_read]",
			check: func(t *testing.T, config llmtypes.Config) {
				assert.Equal(t, []string{"file_read"}, config.AllowedTools)
				require.NotNil(t, config.ExecutionOptions)
				assert.True(t, config.ExecutionOptions.ToolAllowed("file_read"))
				assert.False(t, config.ExecutionOptions.ToolAllowed("bash"))
			},
		},
		{
			name: "narrow commands", yaml: "allowed_commands: ['git status']",
			check: func(t *testing.T, config llmtypes.Config) {
				assert.Equal(t, []string{"git status"}, config.AllowedCommands)
			},
		},
		{
			name: "narrow skills", yaml: "skills:\n  allowed: [review]",
			check: func(t *testing.T, config llmtypes.Config) {
				require.NotNil(t, config.Skills)
				assert.Equal(t, []string{"review"}, config.Skills.Allowed)
			},
		},
		{
			name: "narrow extensions", yaml: "extensions:\n  allow: [inspect]\n  deny: [unsafe, format]\n  tools:\n    inspect:\n      enabled: false",
			check: func(t *testing.T, config llmtypes.Config) {
				extensionConfig, err := extensions.LoadConfigFromSettings(config.ExtensionSettings)
				require.NoError(t, err)
				assert.Equal(t, []string{"inspect"}, extensionConfig.Allow)
				assert.Equal(t, []string{"unsafe", "format"}, extensionConfig.Deny)
				assert.Equal(t, new(false), extensionConfig.Tools["inspect"].Enabled)
				assert.Equal(t, new(false), extensionConfig.Tools["write"].Enabled)
			},
		},
		{
			name: "disable skills and extensions", yaml: "skills:\n  enabled: false\nextensions:\n  enabled: false",
			check: func(t *testing.T, config llmtypes.Config) {
				require.NotNil(t, config.Skills)
				assert.False(t, config.Skills.Enabled)
				assert.Equal(t, false, config.ExtensionSettings["enabled"])
				require.NotNil(t, config.ExecutionOptions)
				assert.Equal(t, new(true), config.ExecutionOptions.NoSkills)
				assert.Equal(t, new(true), config.ExecutionOptions.NoExtensions)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cwd := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte(test.yaml), 0o600))
			// Restrictions inherited from the selected model profile remain host policy.
			loader, err := NewEmbeddedConfigLoader(map[string]any{
				"profiles": map[string]any{"deep": embeddedConfigTestSettings(cwd)},
			}, nil)
			require.NoError(t, err)
			config, err := loader(cwd, "deep", "")
			if test.wantErr {
				assert.Error(t, err, "repository settings must not widen inherited runner policy")
				return
			}
			require.NoError(t, err)
			test.check(t, config)
		})
	}
	for _, feature := range []string{"skills", "extensions"} {
		t.Run("cannot reenable "+feature, func(t *testing.T) {
			cwd := t.TempDir()
			loader, err := NewEmbeddedConfigLoader(map[string]any{feature: map[string]any{"enabled": false}}, nil)
			require.NoError(t, err)
			config, err := loader(cwd, "", "")
			require.NoError(t, err)
			require.NotNil(t, config.ExecutionOptions)
			if feature == "skills" {
				assert.Equal(t, new(true), config.ExecutionOptions.NoSkills)
			} else {
				assert.Equal(t, new(true), config.ExecutionOptions.NoExtensions)
			}
			require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte(feature+":\n  enabled: true\n"), 0o600))
			_, err = loader(cwd, "", "")
			assert.Error(t, err, "repository must not reenable a disabled feature")
		})
	}
}

func TestEmbeddedConfigErrors(t *testing.T) {
	_, err := NewEmbeddedConfigLoader(map[string]any{"sysprompt_args": make(chan string)}, nil)
	require.Error(t, err)
	_, err = NewEmbeddedConfigLoader(nil, map[string]any{"sysprompt_args": make(chan string)})
	require.Error(t, err)
	// Discarded provider settings need not even be serializable by the runner.
	loader, err := NewEmbeddedConfigLoader(map[string]any{
		"openai": make(chan string),
		"profiles": map[string]any{
			"deep": map[string]any{"anthropic": make(chan string)},
		},
	}, map[string]any{"openai": make(chan string)})
	require.NoError(t, err)
	cwd := t.TempDir()
	_, err = loader(cwd, "deep", "")
	require.NoError(t, err, "workspace config is optional and providers are not initialized")
	_, err = loader(cwd, "missing", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
	_, err = loader(cwd, "deep", "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte("extensions: ["), 0o600))
	_, err = loader(cwd, "deep", "")
	require.Error(t, err)
}
