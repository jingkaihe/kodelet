package client

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceConfigLoaderConcurrentSnapshots(t *testing.T) {
	processCWD, err := os.Getwd()
	require.NoError(t, err)
	globalSettings := viper.AllSettings()
	t.Setenv("KODELET_MODEL", "not-a-runner-model")
	defaults := map[string]any{
		"allowed_tools": []string{"file_read", "bash"},
		"extensions":    map[string]any{"enabled": false},
		"skills":        map[string]any{"enabled": false},
		"sysprompt_args": map[string]any{
			"origin": "runner", "default_only": "pinned",
		},
		"environment_profiles": map[string]any{
			"review": map[string]any{
				"allowed_tools":  []string{"file_read"},
				"sysprompt_args": map[string]any{"origin": "profile"},
				"model":          "untrusted-profile-model",
			},
		},
		"model": "not-a-runner-model",
	}
	loader, err := NewWorkspaceConfigLoader(defaults)
	require.NoError(t, err)
	// Neither later caller mutations nor returned configurations may change the snapshot.
	defaults["sysprompt_args"].(map[string]any)["default_only"] = "changed"
	defaults["environment_profiles"].(map[string]any)["review"].(map[string]any)["allowed_tools"] = []string{"bash"}
	directories := []string{t.TempDir(), t.TempDir()}
	for _, cwd := range directories {
		contents := "sysprompt_args:\n  origin: workspace\n  directory: " + cwd + "\nbash:\n  timeout: 30s\nmodel: repository-model\nprovider: repository-provider\nprofile: repository-profile\nserver: https://repository.invalid\nserve:\n  runner_auth_mode: none\nenvironment_profiles:\n  review:\n    allowed_tools: [bash]\n"
		require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte(contents), 0o600))
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			cwd := directories[i%2]
			profile, origin := "", "workspace"
			if i%2 == 1 {
				profile, origin = "review", "profile"
			}
			config, err := loader(cwd, profile)
			if !assert.NoError(t, err) {
				return
			}
			assert.Equal(t, cwd, config.WorkingDirectory)
			assert.Equal(t, cwd, config.SyspromptArgs["directory"])
			assert.Equal(t, origin, config.SyspromptArgs["origin"])
			assert.Equal(t, "pinned", config.SyspromptArgs["default_only"])
			assert.Equal(t, 30*time.Second, config.BashTimeout())
			assert.NotContains(t, []string{"repository-model", "not-a-runner-model", "untrusted-profile-model"}, config.Model)
			assert.NotEqual(t, "repository-provider", config.Provider)
			assert.Empty(t, config.Profile)
			if profile == "review" {
				assert.Equal(t, []string{"file_read"}, config.AllowedTools)
			} else {
				assert.Equal(t, []string{"file_read", "bash"}, config.AllowedTools)
			}
			config.SyspromptArgs["default_only"] = "mutated result"
			config.AllowedTools[0] = "mutated result"
		})
	}
	wg.Wait()
	currentCWD, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, processCWD, currentCWD)
	assert.Equal(t, globalSettings, viper.AllSettings())
	assert.Equal(t, "not-a-runner-model", os.Getenv("KODELET_MODEL"))
}

func TestWorkspaceConfigLoaderReloadOnlyAffectsLaterSnapshots(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "kodelet-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("sysprompt_args:\n  version: before\n"), 0o600))
	loader, err := NewWorkspaceConfigLoader(nil)
	require.NoError(t, err)
	before, err := loader(cwd, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("sysprompt_args:\n  version: after\n"), 0o600))
	after, err := loader(cwd, "")
	require.NoError(t, err)
	assert.Equal(t, "before", before.SyspromptArgs["version"])
	assert.Equal(t, "after", after.SyspromptArgs["version"])
}

func TestWorkspaceConfigLoaderEnforcesHostPolicy(t *testing.T) {
	for _, test := range []struct {
		name     string
		defaults map[string]any
		yaml     string
		wantErr  string
	}{
		{"widen tools", map[string]any{"allowed_tools": []string{"file_read"}}, "allowed_tools: [file_read, bash]", "cannot widen runner restrictions"},
		{"clear tools", map[string]any{"allowed_tools": []string{"file_read"}}, "allowed_tools: []", "cannot remove runner restrictions"},
		{"widen commands", map[string]any{"allowed_commands": []string{"git status"}}, "allowed_commands: ['*']", "cannot widen runner restrictions"},
		{"clear commands", map[string]any{"allowed_commands": []string{"git status"}}, "allowed_commands: []", "cannot remove runner restrictions"},
		{"replace domains", map[string]any{"allowed_domains_file": "/trusted/domains"}, "allowed_domains_file: /repo/domains", "cannot replace runner policy"},
		{"enable extensions", map[string]any{"extensions": map[string]any{"enabled": false}}, "extensions:\n  enabled: true", "cannot enable extensions"},
		{"enable skills", map[string]any{"skills": map[string]any{"enabled": false}}, "skills:\n  enabled: true", "cannot enable skills"},
		{"narrow tools", map[string]any{"allowed_tools": []string{"file_read", "bash"}}, "allowed_tools: [file_read]", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			cwd := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte(test.yaml), 0o600))
			loader, err := NewWorkspaceConfigLoader(test.defaults)
			require.NoError(t, err)
			_, err = loader(cwd, "")
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
		})
	}
}

func TestWorkspaceConfigLoaderErrors(t *testing.T) {
	_, err := NewWorkspaceConfigLoader(map[string]any{"sysprompt_args": make(chan string)})
	require.ErrorContains(t, err, "snapshot runner settings")
	loader, err := NewWorkspaceConfigLoader(nil)
	require.NoError(t, err)
	cwd := t.TempDir()
	_, err = loader(cwd, "")
	require.NoError(t, err, "workspace YAML is optional")
	_, err = loader(cwd, "missing")
	require.ErrorContains(t, err, "profile 'missing' not found")
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "kodelet-config.yaml"), []byte("extensions: ["), 0o600))
	_, err = loader(cwd, "")
	require.ErrorContains(t, err, "load workspace environment settings")
}
