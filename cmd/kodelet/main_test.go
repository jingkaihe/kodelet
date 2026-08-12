package main

import (
	"os"
	"path/filepath"
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
	previous := userConfiguredServer
	userConfiguredServer = value
	t.Cleanup(func() {
		userConfiguredServer = previous
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
	setServerConfigForTest(t, "")
	t.Cleanup(viper.Reset)
	viper.Reset()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	viper.SetDefault("provider", "openai")
	viper.SetDefault("model", "default-model")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".kodelet", "config.yaml"), []byte("server: https://global.example/control\n"), 0o644))

	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte("model: repo-model\nserver: https://repo.example/control\n"), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"provider":"anthropic","extensions":{"local_dir":"/tmp/sdk-extensions"}}`), 0o644))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, configFileModeMerge)

	loadConfigFiles()

	assert.Equal(t, "anthropic", viper.GetString("provider"))
	assert.Equal(t, "repo-model", viper.GetString("model"))
	assert.Equal(t, "https://repo.example/control", viper.GetString("server"))
	assert.Equal(t, "/tmp/sdk-extensions", viper.GetString("extensions.local_dir"))
	assert.Equal(t, "https://global.example/control", userConfiguredServer)
}

func TestLoadConfigFilesCanUseIsolatedOverrideConfigFile(t *testing.T) {
	setServerConfigForTest(t, "")
	t.Cleanup(viper.Reset)
	viper.Reset()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	viper.SetDefault("provider", "openai")
	viper.SetDefault("model", "default-model")

	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte("model: repo-model\n"), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"provider":"anthropic","server":"https://override.example/control"}`), 0o644))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, configFileModeIsolate)

	loadConfigFiles()

	assert.Equal(t, "anthropic", viper.GetString("provider"))
	assert.Equal(t, "default-model", viper.GetString("model"))
	assert.Equal(t, "https://override.example/control", userConfiguredServer)
}

func TestLoadConfigFilesDoesNotTrustRepositoryServer(t *testing.T) {
	setServerConfigForTest(t, "")
	t.Cleanup(viper.Reset)
	viper.Reset()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(controlPlaneServerEnv, "")
	t.Setenv(configFileEnv, "")
	t.Setenv(configFileModeEnv, configFileModeMerge)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte("server: https://repo.example/control\n"), 0o644))

	loadConfigFiles()

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("server", defaultRunnerServer, "")
	server, configured := serverFlagOrConfig(cmd)
	assert.Equal(t, defaultRunnerServer, server)
	assert.False(t, configured)
	assert.Equal(t, "https://repo.example/control", viper.GetString("server"))
}
