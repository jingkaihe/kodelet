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
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	viper.SetDefault("provider", "openai")
	viper.SetDefault("model", "default-model")

	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte("model: repo-model\n"), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"provider":"anthropic","extensions":{"local_dir":"/tmp/sdk-extensions"}}`), 0o644))
	t.Setenv(configFileEnv, configPath)

	loadConfigFiles()

	assert.Equal(t, "anthropic", viper.GetString("provider"))
	assert.Equal(t, "repo-model", viper.GetString("model"))
	assert.Equal(t, "/tmp/sdk-extensions", viper.GetString("extensions.local_dir"))
}

func TestLoadConfigFilesCanUseIsolatedOverrideConfigFile(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	viper.SetDefault("provider", "openai")
	viper.SetDefault("model", "default-model")

	require.NoError(t, os.WriteFile("kodelet-config.yaml", []byte("model: repo-model\n"), 0o644))

	configPath := filepath.Join(t.TempDir(), "kodelet-config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"provider":"anthropic"}`), 0o644))
	t.Setenv(configFileEnv, configPath)
	t.Setenv(configFileModeEnv, configFileModeIsolate)

	loadConfigFiles()

	assert.Equal(t, "anthropic", viper.GetString("provider"))
	assert.Equal(t, "default-model", viper.GetString("model"))
}
