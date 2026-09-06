package chat

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdoptionModelPolicyPreservesSnapshot(t *testing.T) {
	previous := viper.AllSettings()
	t.Cleanup(func() { viper.Reset(); require.NoError(t, viper.MergeConfigMap(previous)) })
	t.Setenv("HOME", t.TempDir())
	for _, scenario := range []string{"snapshot", "legacy", "provider mismatch", "missing profile", "model policy", "reasoning policy", "platform policy", "invalid snapshot", "missing credentials"} {
		t.Run(scenario, func(t *testing.T) {
			viper.Reset()
			viper.Set("provider", "anthropic")
			viper.Set("model", "adoption-main")
			viper.Set("weak_model", "adoption-weak")
			viper.Set("anthropic_api_access", "api-key")
			t.Setenv("ANTHROPIC_API_KEY", "test-local-credential")
			config, err := ResolveConfigForNewConversation("default")
			require.NoError(t, err)
			metadata, err := conversations.AddConfigSnapshot(map[string]any{"custom": "preserved"}, config)
			require.NoError(t, err)
			record := &conversations.GetConversationResponse{Provider: "anthropic", Metadata: metadata}
			snapshot := metadata[conversations.ConfigSnapshotMetadataKey].(map[string]any)
			switch scenario {
			case "legacy":
				delete(metadata, conversations.ConfigSnapshotMetadataKey)
				metadata["model"], metadata["profile"] = config.Model, "default"
			case "provider mismatch":
				record.Provider = "openai"
			case "missing profile":
				snapshot["profile"] = "removed-profile"
			case "model policy":
				snapshot["model"] = "not-allowed-by-daemon"
			case "reasoning policy":
				viper.Set("allowed_reasoning_efforts", []string{"low"})
			case "platform policy":
				snapshot["anthropic"] = map[string]any{"platform": "copilot"}
			case "invalid snapshot":
				snapshot["version"] = 999
			case "missing credentials":
				t.Setenv("ANTHROPIC_API_KEY", "")
			}
			before, err := json.Marshal(metadata)
			require.NoError(t, err)
			resolved, err := ValidateConversationAdoptionConfig(record)
			if scenario == "snapshot" || scenario == "legacy" {
				require.NoError(t, err)
				assert.Equal(t, config.Model, resolved.Model)
			} else {
				require.Error(t, err)
			}
			after, err := json.Marshal(metadata)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "validation never rewrites stored config")
		})
	}
}

func TestAdoptionCredentialChecksAreLocalAndAccountScoped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-api-key")
	config := llmtypes.Config{Provider: "anthropic", AnthropicAPIAccess: llmtypes.AnthropicAPIAccessAPIKey}
	require.NoError(t, adoptionCredentialsAvailable(config))
	config.AnthropicAPIAccess, config.AnthropicAccount = llmtypes.AnthropicAPIAccessSubscription, "work"
	require.ErrorContains(t, adoptionCredentialsAvailable(config), "subscription account")
	path, err := auth.SaveAnthropicCredentialsWithAlias("work", &auth.AnthropicCredentials{
		AccessToken: "expired-token", RefreshToken: "refresh-only-on-real-provider-call", ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	})
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, adoptionCredentialsAvailable(config), "available refresh credentials require no network validation")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "adoption must not refresh or rewrite credentials")
	config.AnthropicAccount = "other"
	require.ErrorContains(t, adoptionCredentialsAvailable(config), "subscription account")
	config = llmtypes.Config{Provider: "openai", OpenAI: &llmtypes.OpenAIConfig{Platform: "custom", APIKeyEnvVar: "ADOPTION_CUSTOM_API_KEY"}}
	t.Setenv("ADOPTION_CUSTOM_API_KEY", "")
	require.Error(t, adoptionCredentialsAvailable(config))
	t.Setenv("ADOPTION_CUSTOM_API_KEY", "local-provider-key")
	require.NoError(t, adoptionCredentialsAvailable(config))
	config.OpenAI.Platform = "codex"
	require.ErrorContains(t, adoptionCredentialsAvailable(config), "Codex credentials")
	_, err = auth.SaveCodexCredentials(&auth.CodexCredentials{AccessToken: "codex-token", AccountID: "test-account", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	require.NoError(t, adoptionCredentialsAvailable(config))
}
