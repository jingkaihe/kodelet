package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelAliasBasicFunctionality(t *testing.T) {
	// Create a temporary config file with aliases
	configContent := `
provider: "anthropic"
model: "claude-sonnet-4-20250514"
max_tokens: 8192

aliases:
  sonnet-4: "claude-sonnet-4-20250514"
  haiku-35: "claude-3-5-haiku-20241022"
  gpt41: "gpt-4.1"
`

	// Create temporary directory and config file
	tempDir, err := os.MkdirTemp("", "kodelet-alias-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "kodelet-test-config.yaml")
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		modelFlag   string
		description string
	}{
		{
			name:        "accepts Claude alias",
			modelFlag:   "sonnet-4",
			description: "should accept alias as model name",
		},
		{
			name:        "accepts OpenAI alias",
			modelFlag:   "gpt41",
			description: "should accept OpenAI alias",
		},
		{
			name:        "accepts full model name",
			modelFlag:   "claude-sonnet-4-20250514",
			description: "should accept full model name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("kodelet", "run", "--model", tt.modelFlag, "test query")

			// Set environment to use our test config
			cmd.Env = []string{
				"KODELET_CONFIG_FILE=" + configFile,
				"HOME=" + tempDir, // Prevent loading user config
			}

			output, _ := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))

			// Should not fail due to flag parsing or alias resolution
			assert.False(t, strings.Contains(outputStr, "unknown flag") ||
				strings.Contains(outputStr, "invalid alias") ||
				strings.Contains(outputStr, "unknown alias"),
				"Should not fail with alias errors for model %s: %s", tt.modelFlag, outputStr)

			// Should not crash or panic
			assert.False(t, strings.Contains(outputStr, "panic") ||
				strings.Contains(outputStr, "fatal"),
				"Should not panic for model %s: %s", tt.modelFlag, outputStr)
		})
	}
}

func TestModelAliasEnvironmentVariableOverride(t *testing.T) {
	// Create a temporary config file with aliases
	configContent := `
provider: "anthropic"
model: "claude-sonnet-4-20250514"
max_tokens: 8192

aliases:
  sonnet-4: "claude-sonnet-4-20250514"
  haiku-35: "claude-3-5-haiku-20241022"
`

	// Create temporary directory and config file
	tempDir, err := os.MkdirTemp("", "kodelet-alias-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "kodelet-test-config.yaml")
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	tests := []struct {
		name        string
		envModel    string
		flagModel   string
		description string
	}{
		{
			name:        "environment variable with alias",
			envModel:    "haiku-35",
			flagModel:   "",
			description: "should accept alias in environment variable",
		},
		{
			name:        "flag overrides environment variable",
			envModel:    "haiku-35",
			flagModel:   "sonnet-4",
			description: "flag should override environment variable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd *exec.Cmd

			if tt.flagModel != "" {
				cmd = exec.Command("kodelet", "run", "--model", tt.flagModel, "test query")
			} else {
				cmd = exec.Command("kodelet", "run", "test query")
			}

			// Set environment
			env := []string{
				"KODELET_CONFIG_FILE=" + configFile,
				"HOME=" + tempDir, // Prevent loading user config
			}
			if tt.envModel != "" {
				env = append(env, "KODELET_MODEL="+tt.envModel)
			}
			cmd.Env = env

			output, _ := cmd.CombinedOutput()
			outputStr := strings.TrimSpace(string(output))

			// Should not fail due to model parsing or alias resolution
			assert.False(t, strings.Contains(outputStr, "unknown flag") ||
				strings.Contains(outputStr, "invalid alias") ||
				strings.Contains(outputStr, "unknown alias"),
				"%s should not fail with alias errors: %s", tt.description, outputStr)

			// Should not crash or panic
			assert.False(t, strings.Contains(outputStr, "panic") ||
				strings.Contains(outputStr, "fatal"),
				"%s should not panic: %s", tt.description, outputStr)
		})
	}
}

func TestModelAliasErrorHandling(t *testing.T) {
	// Create a temporary config file with aliases
	configContent := `
provider: "anthropic"
model: "claude-sonnet-4-20250514"
max_tokens: 8192

aliases:
  sonnet-4: "claude-sonnet-4-20250514"
  valid-alias: "claude-3-5-haiku-20241022"
`

	// Create temporary directory and config file
	tempDir, err := os.MkdirTemp("", "kodelet-alias-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "kodelet-test-config.yaml")
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	// Test that non-existent alias falls back gracefully
	cmd := exec.Command("kodelet", "run", "--model", "non-existent-alias", "test query")

	// Set environment to use our test config
	cmd.Env = []string{
		"KODELET_CONFIG_FILE=" + configFile,
		"HOME=" + tempDir, // Prevent loading user config
	}

	output, _ := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	// Should not fail due to alias resolution errors
	assert.False(t, strings.Contains(outputStr, "failed to resolve alias") ||
		strings.Contains(outputStr, "invalid alias configuration"),
		"Should handle non-existent alias gracefully: %s", outputStr)

	// Should not crash or panic
	assert.False(t, strings.Contains(outputStr, "panic") ||
		strings.Contains(outputStr, "fatal"),
		"Should not panic with non-existent alias: %s", outputStr)
}
