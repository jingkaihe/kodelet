package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/llm/openai"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// ConversationAdoptionRequest selects an explicit environment. Confirmation is
// the digest returned by a prior preview, not permission to replace a record.
type ConversationAdoptionRequest struct {
	RunnerID           string `json:"runnerId"`
	EnvironmentProfile string `json:"environmentProfile,omitempty"`
	Confirmation       string `json:"confirmation,omitempty"`
}

// ConversationAdoptionResult contains no credentials or raw conversation data.
type ConversationAdoptionResult struct {
	ConversationID     string `json:"conversationId"`
	RunnerID           string `json:"runnerId"`
	RunnerName         string `json:"runnerName,omitempty"`
	HostInstanceID     string `json:"hostInstanceId"`
	Hostname           string `json:"hostname"`
	Generation         int64  `json:"generation"`
	CWD                string `json:"cwd"`
	EnvironmentProfile string `json:"environmentProfile,omitempty"`
	ModelProfile       string `json:"modelProfile"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	Confirmation       string `json:"confirmation"`
	Adopted            bool   `json:"adopted"`
}

// AdoptConversation previews or confirms a daemon-owned legacy conversation.
// It never retries a mutation after an uncertain transport outcome.
func (r *Client) AdoptConversation(ctx context.Context, id string, params ConversationAdoptionRequest) (ConversationAdoptionResult, error) {
	var result ConversationAdoptionResult
	if strings.TrimSpace(id) == "" || strings.TrimSpace(params.RunnerID) == "" {
		return result, errors.New("conversation ID and explicit runner ID are required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", id, "adopt")
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(params)
	if err != nil {
		return result, errors.Wrap(err, "failed to encode adoption request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errors.Wrap(err, "failed to create adoption request")
	}
	request.Header.Set("Content-Type", "application/json")
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "could not confirm the runner assignment; check 'kodelet conversation show' before trying again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return result, errors.Wrap(err, "received an invalid response; check the conversation's saved runner before trying adoption again")
	}
	return result, nil
}

// ValidateConversationAdoptionConfig checks stored model semantics against live
// daemon policy and locally available credentials, without a provider request,
// credential refresh, or thread construction. It does not rewrite the snapshot.
func ValidateConversationAdoptionConfig(record *conversations.GetConversationResponse) (llmtypes.Config, error) {
	stored, err := ResolveConfigForExistingConversation(record)
	if err != nil {
		return llmtypes.Config{}, err
	}
	profile := NormalizeRequestedProfile(stored.Profile)
	var policy llmtypes.Config
	if profile != "" {
		if !llm.HasConfiguredProfile(profile) {
			return llmtypes.Config{}, errors.Errorf("stored model profile %q is unavailable on the server", profile)
		}
		policy, err = llm.GetConfigFromViperWithProfile(profile)
	} else {
		policy, err = llm.GetConfigFromViperWithoutProfile()
	}
	if err != nil {
		return llmtypes.Config{}, err
	}
	if stored.Provider != policy.Provider || (record.Provider != "" && record.Provider != stored.Provider) {
		return llmtypes.Config{}, errors.New("stored provider is incompatible with the server model profile")
	}
	if stored.Provider == "anthropic" && stored.Anthropic != nil {
		platform := ""
		if policy.Anthropic != nil {
			platform = policy.Anthropic.Platform
		}
		if stored.Anthropic.Platform != platform {
			return llmtypes.Config{}, errors.New("stored Anthropic platform is incompatible with server policy")
		}
	}
	if stored.Provider == "openai" && stored.OpenAI != nil {
		platform := "openai"
		if policy.OpenAI != nil && policy.OpenAI.Platform != "" {
			platform = policy.OpenAI.Platform
		}
		if stored.OpenAI.Platform != "" && stored.OpenAI.Platform != platform {
			return llmtypes.Config{}, errors.New("stored OpenAI platform is incompatible with server policy")
		}
	}
	if !executionModelAllowed(policy, stored.Model) || (stored.WeakModel != "" && !executionModelAllowed(policy, stored.WeakModel)) {
		return llmtypes.Config{}, errors.New("stored model is not allowed by the model profile or provider catalog")
	}
	stored.AllowedReasoningEfforts = policy.AllowedReasoningEfforts
	if _, err := applyExecutionOptions(stored, &llmtypes.ExecutionOptions{}, nil); err != nil {
		return llmtypes.Config{}, err
	}
	if err := adoptionCredentialsAvailable(stored); err != nil {
		return llmtypes.Config{}, err
	}
	return stored, nil
}

func adoptionCredentialsAvailable(config llmtypes.Config) error {
	if (config.Anthropic != nil && config.Anthropic.Platform == "copilot" && config.Provider == "anthropic") ||
		(config.OpenAI != nil && config.OpenAI.Platform == "copilot" && config.Provider == "openai") {
		exists, err := auth.GetCopilotCredentialsExists()
		if err == nil && exists {
			return nil
		}
		return errors.New("server Copilot credentials are unavailable; connect the provider before adoption")
	}
	switch config.Provider {
	case "anthropic":
		if config.AnthropicAPIAccess != llmtypes.AnthropicAPIAccessAPIKey {
			credentials, err := auth.GetAnthropicCredentialsByAlias(config.AnthropicAccount)
			if err == nil && credentials.AccessToken != "" && (credentials.ExpiresAt > time.Now().Unix() || credentials.RefreshToken != "") {
				return nil
			}
			if config.AnthropicAPIAccess == llmtypes.AnthropicAPIAccessSubscription || config.AnthropicAccount != "" {
				return errors.New("server Anthropic subscription account is unavailable; connect the account before adoption")
			}
		}
		if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			return nil
		}
	case "openai":
		if config.OpenAI != nil && config.OpenAI.Platform == "codex" {
			credentials, err := auth.GetCodexCredentials()
			if err == nil && credentials.AccessToken != "" && (credentials.ExpiresAt > time.Now().Unix() || credentials.RefreshToken != "") {
				return nil
			}
			return errors.New("server Codex credentials are unavailable; connect the provider before adoption")
		}
		if strings.TrimSpace(os.Getenv(openai.GetAPIKeyEnvVar(config))) != "" {
			return nil
		}
	}
	return errors.New("server provider credentials are unavailable; configure authentication before adoption")
}
