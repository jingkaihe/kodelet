package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthTokenFlagOrEnvironment(t *testing.T) {
	const environmentName = "KODELET_TEST_AUTH_TOKEN"
	t.Setenv(environmentName, " environment-secret ")

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("auth-token", "", "")
	assert.Equal(t, "environment-secret", authTokenFlagOrEnvironment(cmd, environmentName))

	require.NoError(t, cmd.Flags().Set("auth-token", " flag-secret "))
	assert.Equal(t, "flag-secret", authTokenFlagOrEnvironment(cmd, environmentName))

	emptyCmd := &cobra.Command{Use: "test"}
	emptyCmd.Flags().String("auth-token", "", "")
	require.NoError(t, emptyCmd.Flags().Set("auth-token", ""))
	assert.Empty(t, authTokenFlagOrEnvironment(emptyCmd, environmentName))
}

func TestServerFlagOrConfig(t *testing.T) {
	newCommand := func() *cobra.Command {
		cmd := &cobra.Command{Use: "test"}
		cmd.Flags().String("server", defaultRunnerServer, "")
		return cmd
	}

	t.Run("uses flag default without enabling server mode", func(t *testing.T) {
		setServerConfigForTest(t, "")
		t.Setenv(controlPlaneServerEnv, "")

		server, configured := serverFlagOrConfig(newCommand())

		assert.Equal(t, defaultRunnerServer, server)
		assert.False(t, configured)
	})

	t.Run("uses configured server", func(t *testing.T) {
		setServerConfigForTest(t, " https://config.example/control ")
		t.Setenv(controlPlaneServerEnv, "")

		server, configured := serverFlagOrConfig(newCommand())

		assert.Equal(t, "https://config.example/control", server)
		assert.True(t, configured)
	})

	t.Run("environment overrides config", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, " https://environment.example ")

		server, configured := serverFlagOrConfig(newCommand())

		assert.Equal(t, "https://environment.example", server)
		assert.True(t, configured)
	})

	t.Run("flag overrides environment and config", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, "https://environment.example")
		cmd := newCommand()
		require.NoError(t, cmd.Flags().Set("server", " https://flag.example "))

		server, configured := serverFlagOrConfig(cmd)

		assert.Equal(t, "https://flag.example", server)
		assert.True(t, configured)
	})
}

func TestServerResolutionUsesActualCommandPaths(t *testing.T) {
	t.Run("chat stays local without a configured server", func(t *testing.T) {
		setServerConfigForTest(t, "")
		t.Setenv(controlPlaneServerEnv, "")
		cmd := parseActualCommandForTest(t, chatCmd)

		config := getChatConfigFromFlags(cmd)

		assert.Equal(t, defaultRunnerServer, config.Server)
		assert.False(t, usesControlPlaneChat(config))
	})

	t.Run("chat flag overrides environment and user config", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, "https://environment.example")
		cmd := parseActualCommandForTest(t, chatCmd, "--server", "https://flag.example")

		config := getChatConfigFromFlags(cmd)

		assert.Equal(t, "https://flag.example", config.Server)
		assert.True(t, usesControlPlaneChat(config))
	})

	t.Run("ACP uses the user-configured server", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, "")
		cmd := parseActualCommandForTest(t, acpCmd)

		server, configured := serverFlagOrConfig(cmd)

		assert.Equal(t, "https://config.example", server)
		assert.True(t, configured)
	})

	t.Run("ACP environment overrides user config", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, "https://environment.example")
		cmd := parseActualCommandForTest(t, acpCmd)

		server, configured := serverFlagOrConfig(cmd)

		assert.Equal(t, "https://environment.example", server)
		assert.True(t, configured)
	})

	t.Run("runner start uses the user-configured server", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, "")
		cmd := parseActualCommandForTest(t, runnerStartCmd)

		assert.Equal(t, "https://config.example", runnerStartConfigFromFlags(cmd).Server)
	})

	t.Run("runner query flag overrides environment and user config", func(t *testing.T) {
		setServerConfigForTest(t, "https://config.example")
		t.Setenv(controlPlaneServerEnv, "https://environment.example")
		cmd := parseActualCommandForTest(t, runnerListCmd, "--server", "https://flag.example")

		assert.Equal(t, "https://flag.example", runnerQueryConfigFromFlags(cmd).Server)
	})
}

func parseActualCommandForTest(t *testing.T, cmd *cobra.Command, args ...string) *cobra.Command {
	t.Helper()
	flag := cmd.Flags().Lookup("server")
	require.NotNil(t, flag)
	previousValue := flag.Value.String()
	previousChanged := flag.Changed
	require.NoError(t, flag.Value.Set(flag.DefValue))
	flag.Changed = false
	t.Cleanup(func() {
		require.NoError(t, flag.Value.Set(previousValue))
		flag.Changed = previousChanged
	})
	require.NoError(t, cmd.ParseFlags(args))
	return cmd
}

func setServerConfigForTest(t *testing.T, value string) {
	t.Helper()
	previous := viper.Get("server")
	wasSet := viper.IsSet("server")
	viper.Set("server", value)
	t.Cleanup(func() {
		if wasSet {
			viper.Set("server", previous)
			return
		}
		viper.Set("server", nil)
	})
}

func TestAuthTokenFlagsDoNotCaptureEnvironmentDefaults(t *testing.T) {
	commands := []*cobra.Command{runnerStartCmd, runnerListCmd, runnerInspectCmd, runnerRemoveCmd, chatCmd}
	for _, command := range commands {
		t.Run(command.CommandPath(), func(t *testing.T) {
			flag := command.Flags().Lookup("auth-token")
			require.NotNil(t, flag)
			assert.Empty(t, flag.DefValue)
		})
	}
}

func TestLoadConfigFilesMergesOverrideConfigFile(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	viper.SetDefault("provider", "openai")
	viper.SetDefault("model", "default-model")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
	globalConfig := `
server: https://global.example/control
profile: global-profile
serve:
  web_auth_mode: oidc
  oidc:
    issuer: https://global-issuer.example
`
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte(globalConfig), 0o644))

	repositoryConfig := `
model: repo-model
server: https://repo.example/control
serve.web_auth_mode: none
serve:
  web_auth_mode: none
  skip_auth: true
  auth_token: repo-secret
  oidc:
    issuer: http://repo-issuer.example
profile: null
extensions:
  tools:
    dangerous.tool:
      enabled: false
`
	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte(repositoryConfig), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"provider":"anthropic","serve":{"runner_auth_mode":"enrollment"},"extensions":{"local_dir":"/tmp/sdk-extensions"}}`), 0o644))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, configFileModeMerge)

	require.NoError(t, loadConfigFiles())

	assert.Equal(t, "anthropic", viper.GetString("provider"))
	assert.Equal(t, "repo-model", viper.GetString("model"))
	assert.Equal(t, "https://global.example/control", viper.GetString("server"))
	assert.Nil(t, viper.Get("profile"))
	assert.Equal(t, "/tmp/sdk-extensions", viper.GetString("extensions.local_dir"))
	assert.Equal(t, "oidc", viper.GetString("serve.web_auth_mode"))
	assert.Equal(t, "enrollment", viper.GetString("serve.runner_auth_mode"))
	assert.False(t, viper.GetBool("serve.skip_auth"))
	assert.Empty(t, viper.GetString("serve.auth_token"))
	assert.Equal(t, "https://global-issuer.example", viper.GetString("serve.oidc.issuer"))
	tools := viper.GetStringMap("extensions.tools")
	require.Contains(t, tools, "dangerous.tool")
	toolConfig, ok := tools["dangerous.tool"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, toolConfig["enabled"])
}

func TestLoadConfigFilesCanUseIsolatedOverrideConfigFile(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	viper.SetDefault("provider", "openai")
	viper.SetDefault("model", "default-model")

	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte("model: global-model\nserve:\n  web_auth_mode: none\n"), 0o644))
	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte("model: repo-model\n"), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"provider":"anthropic","server":"https://override.example/control","serve":{"web_auth_mode":"oidc","oidc":{"issuer":"https://override-issuer.example"}}}`), 0o644))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, configFileModeIsolate)

	require.NoError(t, loadConfigFiles())

	assert.Equal(t, "anthropic", viper.GetString("provider"))
	assert.Equal(t, "default-model", viper.GetString("model"))
	assert.Equal(t, "https://override.example/control", viper.GetString("server"))
	assert.Equal(t, "oidc", viper.GetString("serve.web_auth_mode"))
	assert.Equal(t, "https://override-issuer.example", viper.GetString("serve.oidc.issuer"))
}

func TestLoadConfigFilesMergeOverrideCanSelectServer(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte("server: https://global.example/control\nserve:\n  web_auth_mode: token\n"), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"server":"https://override.example/control","serve":{"web_auth_mode":"oidc"}}`), 0o644))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, configFileModeMerge)

	require.NoError(t, loadConfigFiles())

	assert.Equal(t, "https://override.example/control", viper.GetString("server"))
	assert.Equal(t, "oidc", viper.GetString("serve.web_auth_mode"))
}

func TestLoadConfigFilesDoesNotTrustRepositoryControlPlaneSettings(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(controlPlaneServerEnv, "")
	t.Setenv(configFileEnv, "")
	t.Setenv(configFileModeEnv, configFileModeMerge)
	t.Chdir(t.TempDir())
	repositoryConfig := `
SeRvEr: https://repo.example/control
SeRvE:
  host: 0.0.0.0
  web_auth_mode: none
  runner_auth_mode: none
  skip_auth: true
  auth_token: repo-web-secret
  runner_auth_token: repo-runner-secret
  oidc:
    issuer: http://repo-issuer.example
SERVE.SKIP_AUTH: true
`
	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte(repositoryConfig), 0o644))

	require.NoError(t, loadConfigFiles())

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	server, configured := serverFlagOrConfig(cmd)
	assert.Equal(t, defaultRunnerServer, server)
	assert.False(t, configured)
	assert.Empty(t, viper.GetString("server"))
	assert.Empty(t, viper.GetStringMap("serve"))
}

func TestLoadConfigFilesFailsForInvalidExplicitConfigFile(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		contents *string
	}{
		{name: "missing merge override", mode: configFileModeMerge},
		{name: "missing isolated override", mode: configFileModeIsolate},
		{name: "malformed merge override", mode: configFileModeMerge, contents: stringPointer("serve: [")},
		{name: "malformed isolated override", mode: configFileModeIsolate, contents: stringPointer("serve: [")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Chdir(t.TempDir())
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte("serve:\n  skip_auth: true\n"), 0o644))

			configPath := filepath.Join(t.TempDir(), "explicit.yaml")
			if tt.contents != nil {
				require.NoError(t, os.WriteFile(configPath, []byte(*tt.contents), 0o600))
			}
			t.Setenv(configFileEnv, configPath)
			t.Setenv(configFileModeEnv, tt.mode)

			err := loadConfigFiles()
			require.Error(t, err)
			assert.Contains(t, err.Error(), configPath)
		})
	}
}

func TestLoadConfigFilesRejectsUnknownExplicitConfigMode(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	configPath := filepath.Join(t.TempDir(), "explicit.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("model: test\n"), 0o600))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, "isolate")

	err := loadConfigFiles()

	require.Error(t, err)
	assert.Contains(t, err.Error(), configFileModeEnv)
	assert.Contains(t, err.Error(), configFileModeIsolate)
}

func TestLoadConfigFilesRequiresPrivateModeForTrustedStaticTokens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACL validation rather than Unix mode bits")
	}
	for _, test := range []struct {
		name   string
		config string
	}{
		{name: "nested token", config: "serve:\n  auth_token: secret\n"},
		{name: "dotted token", config: "SERVE.RUNNER_AUTH_TOKEN: secret\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Chdir(t.TempDir())
			configDir := filepath.Join(home, ".kodelet")
			require.NoError(t, os.MkdirAll(configDir, 0o700))
			configPath := filepath.Join(configDir, "config.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(test.config), 0o600))
			require.NoError(t, os.Chmod(configPath, 0o644))

			err := loadConfigFiles()
			require.ErrorContains(t, err, "must not be accessible by group or other users")

			viper.Reset()
			require.NoError(t, os.Chmod(configPath, 0o600))
			require.NoError(t, loadConfigFiles())
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
