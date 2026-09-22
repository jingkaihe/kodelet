package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/llm/openai/copilotdefaults"
	codexpreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/codex"
	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// applyCodexRestrictions modifies request parameters for Codex API compatibility.
// The Codex API doesn't support max_output_tokens on this path, and Codex also
// expects runtime sections as developer messages rather than top-level
// instructions only.
// This method centralizes all Codex-specific parameter restrictions in one place.
func (t *Thread) applyCodexRestrictions(params *responses.ResponseNewParams) {
	if !t.isCodex {
		return
	}
	// Codex uses prompt_cache_key plus full input replay on this path, not
	// server-side stored conversation state.
	params.Store = param.NewOpt(false)
	params.MaxOutputTokens = param.Opt[int64]{}

	if base.EnvironmentForThread(t) != nil {
		contexts := base.EnvironmentContexts(t)
		promptCtx := sysprompt.BuildRuntimeContext(t.Config, contexts)

		renderer, err := sysprompt.ResolveRendererForConfig(t.Config)
		if err != nil {
			logger.G(context.Background()).WithError(err).Warn("failed to load custom sysprompt template for codex runtime sections, using default")
		}

		devMessages := sysprompt.RenderRuntimeSections(promptCtx, renderer)

		// prepend dev messages to params' input
		for i := len(devMessages) - 1; i >= 0; i-- {
			params.Input.OfInputItemList = append([]responses.ResponseInputItemUnionParam{
				{
					OfMessage: &responses.EasyInputMessageParam{
						Role:    responses.EasyInputMessageRoleDeveloper,
						Content: responses.EasyInputMessageContentUnionParam{OfString: param.NewOpt(devMessages[i])},
					},
				},
			}, params.Input.OfInputItemList...)
		}
	}
}

func applyPromptCacheOptions(params *responses.ResponseNewParams, config llmtypes.Config, model string) {
	if resolvePlatformName(config) != defaultOpenAIPlatform || !supportsPromptCacheOptions(model) {
		return
	}

	params.PromptCacheOptions = responses.ResponseNewParamsPromptCacheOptions{
		Mode: "implicit",
		Ttl:  "30m",
	}
}

func supportsPromptCacheOptions(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-6-astra" || model == "gpt-6-sol" || model == "gpt-6-luna" || model == "gpt-5.6" || strings.HasPrefix(model, "gpt-5.6-")
}

func openAIReasoningEffortForRequest(model string, effort shared.ReasoningEffort) shared.ReasoningEffort {
	normalized := shared.ReasoningEffort(strings.ToLower(strings.TrimSpace(string(effort))))
	if strings.EqualFold(strings.TrimSpace(model), "gpt-6-astra") && (normalized == shared.ReasoningEffortNone || normalized == shared.ReasoningEffortMinimal) {
		return shared.ReasoningEffortLow
	}
	return normalized
}

type codexResponsesRequestMetadata struct {
	clientMetadata map[string]string
	turnMetadata   string
	windowID       string
}

type codexTurnMetadataPayload struct {
	InstallationID string                          `json:"installation_id,omitempty"`
	SessionID      string                          `json:"session_id"`
	ThreadID       string                          `json:"thread_id"`
	TurnID         string                          `json:"turn_id"`
	WindowID       string                          `json:"window_id"`
	RequestKind    string                          `json:"request_kind"`
	Compaction     *codexCompactionMetadataPayload `json:"compaction,omitempty"`
}

type codexCompactionMetadataPayload struct {
	Trigger        string `json:"trigger"`
	Reason         string `json:"reason"`
	Implementation string `json:"implementation"`
	Phase          string `json:"phase"`
	Strategy       string `json:"strategy"`
}

type codexTurnIDContextKey struct{}

func withCodexTurnID(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if codexTurnIDFromContext(ctx) != "" {
		return ctx
	}
	return context.WithValue(ctx, codexTurnIDContextKey{}, convtypes.GenerateID())
}

func codexTurnIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	turnID, _ := ctx.Value(codexTurnIDContextKey{}).(string)
	return strings.TrimSpace(turnID)
}

func (t *Thread) buildCodexResponsesRequestMetadata(
	turnID string,
	windowGeneration uint64,
	requestKind string,
	compaction *codexCompactionMetadata,
) (codexResponsesRequestMetadata, error) {
	if !t.isCodex {
		return codexResponsesRequestMetadata{}, nil
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = convtypes.GenerateID()
	}
	windowID := fmt.Sprintf("%s:%d", t.ConversationID, windowGeneration)
	payload := codexTurnMetadataPayload{
		InstallationID: t.codexInstallationID,
		SessionID:      t.ConversationID,
		ThreadID:       t.ConversationID,
		TurnID:         turnID,
		WindowID:       windowID,
		RequestKind:    requestKind,
	}
	if compaction != nil {
		payload.Compaction = &codexCompactionMetadataPayload{
			Trigger:        compaction.trigger,
			Reason:         compaction.reason,
			Implementation: compaction.implementation,
			Phase:          compaction.phase,
			Strategy:       compaction.strategy,
		}
	}
	turnMetadata, err := json.Marshal(payload)
	if err != nil {
		return codexResponsesRequestMetadata{}, errors.Wrap(err, "failed to encode codex turn metadata")
	}
	clientMetadata := map[string]string{
		"session_id":                 t.ConversationID,
		"thread_id":                  t.ConversationID,
		"turn_id":                    turnID,
		auth.CodexWindowIDHeader:     windowID,
		auth.CodexTurnMetadataHeader: string(turnMetadata),
	}
	if t.codexInstallationID != "" {
		clientMetadata[auth.CodexInstallationIDHeader] = t.codexInstallationID
	}
	return codexResponsesRequestMetadata{
		clientMetadata: clientMetadata,
		turnMetadata:   string(turnMetadata),
		windowID:       windowID,
	}, nil
}

func (m codexResponsesRequestMetadata) apply(params *responses.ResponseNewParams) {
	if params == nil || len(m.clientMetadata) == 0 {
		return
	}
	params.SetExtraFields(map[string]any{"client_metadata": maps.Clone(m.clientMetadata)})
}

// codexResponsesHeaders keeps HTTP and WebSocket metadata in the same order.
func (t *Thread) codexResponsesHeaders(metadata codexResponsesRequestMetadata) [][2]string {
	if !t.isCodex {
		return nil
	}
	headers := [][2]string{
		{auth.CodexBetaFeaturesHeader, auth.CodexBetaFeatures},
		{"session-id", t.ConversationID},
		{"thread-id", t.ConversationID},
	}
	if t.codexInstallationID != "" {
		headers = append(headers, [2]string{auth.CodexInstallationIDHeader, t.codexInstallationID})
	}
	if metadata.windowID != "" {
		headers = append(headers, [2]string{auth.CodexWindowIDHeader, metadata.windowID})
	}
	if metadata.turnMetadata != "" {
		headers = append(headers, [2]string{auth.CodexTurnMetadataHeader, metadata.turnMetadata})
	}
	return headers
}

func (t *Thread) codexResponsesRequestOptions(metadata codexResponsesRequestMetadata) []option.RequestOption {
	var opts []option.RequestOption
	for _, header := range t.codexResponsesHeaders(metadata) {
		opts = append(opts, option.WithHeader(header[0], header[1]))
	}
	return opts
}

func (t *Thread) codexResponsesWebSocketHeaders(metadata codexResponsesRequestMetadata) []string {
	var headers []string
	for _, header := range t.codexResponsesHeaders(metadata) {
		headers = append(headers, header[0]+": "+header[1])
	}
	return headers
}

const defaultOpenAIPlatform = "openai"

func normalizePlatformName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func resolvePlatformName(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	return defaultOpenAIPlatform
}

func resolvePlatformForLoading(config llmtypes.Config) string {
	if config.OpenAI == nil {
		return defaultOpenAIPlatform
	}

	if platform := normalizePlatformName(config.OpenAI.Platform); platform != "" {
		return platform
	}

	if config.OpenAI.Models == nil && config.OpenAI.Pricing == nil {
		return defaultOpenAIPlatform
	}

	return ""
}

func parseAPIMode(raw string) (llmtypes.OpenAIAPIMode, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "-", "_")

	switch normalized {
	case "chat", "chat_completions", "chatcompletions":
		return llmtypes.OpenAIAPIModeChatCompletions, true
	case "responses":
		return llmtypes.OpenAIAPIModeResponses, true
	default:
		return "", false
	}
}

func normalizeServiceTier(config llmtypes.Config) llmtypes.OpenAIServiceTier {
	if config.OpenAI == nil {
		return ""
	}

	tier, ok := llmtypes.ParseOpenAIServiceTier(string(config.OpenAI.ServiceTier))
	if !ok {
		return ""
	}

	return tier
}

func shouldUseResponsesWebSocket(config llmtypes.Config) bool {
	if config.OpenAI != nil && config.OpenAI.WebSocketMode != nil {
		return *config.OpenAI.WebSocketMode
	}
	return true
}

func supportsResponsesWebSocket(config llmtypes.Config) bool {
	platform := resolvePlatformName(config)
	if platform != "openai" && platform != "codex" {
		return false
	}

	baseURL := strings.TrimRight(getBaseURL(config), "/")
	switch platform {
	case "openai":
		return baseURL == "" || strings.EqualFold(baseURL, strings.TrimRight(openaipreset.BaseURL, "/"))
	case "codex":
		return baseURL == "" || strings.EqualFold(baseURL, strings.TrimRight(codexpreset.BaseURL, "/"))
	default:
		return false
	}
}

func supportsRemoteCompactionV2(config llmtypes.Config) bool {
	platform := resolvePlatformName(config)
	return platform == "openai" || platform == "codex"
}

func getPlatformBaseURL(platform string) string {
	switch normalizePlatformName(platform) {
	case "codex":
		return codexpreset.BaseURL
	case "copilot":
		return auth.CopilotBaseURL
	case "openai":
		return openaipreset.BaseURL
	default:
		return ""
	}
}

func getAPIKeyEnvVar(config llmtypes.Config) string {
	if config.OpenAI != nil && config.OpenAI.APIKeyEnvVar != "" {
		return config.OpenAI.APIKeyEnvVar
	}
	return openaipreset.APIKeyEnvVar
}

func getBaseURL(config llmtypes.Config) string {
	if baseURL := os.Getenv("OPENAI_API_BASE"); baseURL != "" {
		return baseURL
	}
	if config.OpenAI != nil && config.OpenAI.BaseURL != "" {
		return config.OpenAI.BaseURL
	}
	return getPlatformBaseURL(resolvePlatformName(config))
}

// loadCustomConfiguration loads custom models and pricing from config.
// It processes platform defaults first, then applies custom overrides if provided.
func loadCustomConfiguration(config llmtypes.Config) (map[string]string, map[string]llmtypes.ModelPricing) {
	customModels := make(map[string]string)
	customPricing := make(map[string]llmtypes.ModelPricing)

	platformName := resolvePlatformForLoading(config)
	if platformName != "" {
		platformModels, platformPricing := loadPlatformDefaultsForConfig(platformName, config)
		for model, category := range platformModels {
			customModels[model] = category
		}
		for model, pricing := range platformPricing {
			customPricing[model] = pricing
		}
	}

	if config.OpenAI != nil {
		if config.OpenAI.Models != nil {
			for _, model := range config.OpenAI.Models.Reasoning {
				customModels[model] = "reasoning"
			}
			for _, model := range config.OpenAI.Models.NonReasoning {
				customModels[model] = "non-reasoning"
			}
		}

		if config.OpenAI.Pricing != nil {
			for k, v := range config.OpenAI.Pricing {
				customPricing[k] = v
			}
		}
	}

	return customModels, customPricing
}

type responsesAuthInfo struct {
	useCodex   bool
	useCopilot bool
	baseURL    string
	authorizer auth.HTTPAuthorizer
}

// buildClientOptions constructs the OpenAI client options based on authentication mode.
// Returns the SDK options plus transport/auth metadata used by WebSocket mode.
func buildClientOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, responsesAuthInfo, error) {
	useCodex := resolvePlatformName(config) == "codex"
	useCopilot := resolvePlatformName(config) == "copilot"
	authInfo := responsesAuthInfo{
		useCodex:   useCodex,
		useCopilot: useCopilot,
		baseURL:    getBaseURL(config),
	}

	var opts []option.RequestOption
	var err error

	if useCopilot {
		opts, authInfo.authorizer, err = buildCopilotAuthOptions(config, log)
	} else if useCodex {
		opts, authInfo.authorizer = buildCodexAuthOptions(config, log)
	} else {
		opts, authInfo.authorizer, err = buildAPIKeyAuthOptions(config, log)
	}
	if err != nil {
		return nil, authInfo, err
	}

	opts = append(opts, errorLoggingMiddleware(log))

	return opts, authInfo, nil
}

func buildCopilotAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, auth.HTTPAuthorizer, error) {
	copilotCredsExists, _ := auth.GetCopilotCredentialsExists()
	if !copilotCredsExists {
		return nil, nil, errors.New("GitHub Copilot credentials not found, run 'kodelet copilot-login'")
	}

	log.Debug("using GitHub Copilot authentication for Responses API")
	authorizer := auth.CopilotAuthorizer()
	opts := auth.OpenAIRequestOptionsWithAuthorizer(authorizer)
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	} else {
		opts = append(opts, option.WithBaseURL(auth.CopilotBaseURL))
	}

	return opts, authorizer, nil
}

func (t *Thread) requestOptions(opt llmtypes.MessageOpt) []option.RequestOption {
	if !t.useCopilot {
		return nil
	}

	return auth.CopilotOpenAIRequestOptions(opt)
}

// buildCodexAuthOptions returns client options for Codex CLI authentication.
func buildCodexAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, auth.HTTPAuthorizer) {
	log.Debug("using Codex authentication for Responses API")
	authorizer := auth.CodexAuthorizer()
	opts := auth.OpenAIRequestOptionsWithAuthorizer(authorizer)
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	} else {
		opts = append(opts, option.WithBaseURL(auth.CodexAPIBaseURL))
	}
	return opts, authorizer
}

// buildAPIKeyAuthOptions returns client options for standard API key authentication.
func buildAPIKeyAuthOptions(config llmtypes.Config, log *logrus.Entry) ([]option.RequestOption, auth.HTTPAuthorizer, error) {
	apiKeyEnvVar := getAPIKeyEnvVar(config)
	authorizer, err := auth.OpenAIAPIKeyAuthorizerFromEnv(apiKeyEnvVar)
	if err != nil {
		return nil, nil, err
	}

	log.WithField("api_key_env_var", apiKeyEnvVar).Debug("using OpenAI API key for Responses API")

	opts := auth.OpenAIRequestOptionsWithAuthorizer(authorizer)
	if baseURL := getBaseURL(config); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	return opts, authorizer, nil
}

// errorLoggingMiddleware returns a middleware that logs error response bodies for debugging.
func errorLoggingMiddleware(log *logrus.Entry) option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil {
			return resp, err
		}

		// Log response body for non-2xx status codes
		if resp != nil && resp.StatusCode >= 400 {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				log.WithError(readErr).Debug("failed to read error response body")
				return resp, err
			}

			log.WithField("status_code", resp.StatusCode).
				WithField("response_body", string(body)).
				Debug("API error response")

			// Restore the body so the SDK can still read it
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}

		return resp, err
	})
}

// disableSDKServerOverloadRetries leaves ordinary SDK retries unchanged while
// reserving structured overload retries for the outer Responses retry loop.
func disableSDKServerOverloadRetries() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil || resp == nil || resp.Body == nil || resp.StatusCode < http.StatusBadRequest {
			return resp, err
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if readErr != nil {
			return resp, err
		}
		if isResponsesServerOverloadedCode(responsesAPIErrorCodeFromBody(string(body))) {
			if resp.Header == nil {
				resp.Header = make(http.Header)
			}
			resp.Header.Set(responsesServerOverloadHeader, "true")
			resp.Header.Set("x-should-retry", "false")
		}
		return resp, err
	})
}

func loadPlatformDefaultsForConfig(platformName string, config llmtypes.Config) (map[string]string, map[string]llmtypes.ModelPricing) {
	return loadPlatformDefaultsForServiceTier(platformName, normalizeServiceTier(config))
}

func loadPlatformDefaultsForServiceTier(platformName string, serviceTier llmtypes.OpenAIServiceTier) (map[string]string, map[string]llmtypes.ModelPricing) {
	switch normalizePlatformName(platformName) {
	case "openai":
		return loadPlatformDefaultsFromConfig(openaipreset.Models, openaipreset.PricingForServiceTier(serviceTier))
	case "codex":
		return loadPlatformDefaultsFromConfig(codexpreset.Models, codexpreset.PricingForServiceTier(serviceTier))
	case "copilot":
		models, pricing, err := copilotdefaults.LoadPlatformDefaults(context.Background())
		if err == nil {
			return loadPlatformDefaultsFromConfig(*models, pricing)
		}
		return loadPlatformDefaultsFromConfig(openaipreset.Models, openaipreset.Pricing)
	default:
		return nil, nil
	}
}

// loadPlatformDefaultsFromConfig converts platform model and pricing defaults into the internal format.
func loadPlatformDefaultsFromConfig(platformModels llmtypes.CustomModels, platformPricing llmtypes.CustomPricing) (map[string]string, map[string]llmtypes.ModelPricing) {
	models := make(map[string]string)
	pricing := make(map[string]llmtypes.ModelPricing)

	for _, model := range platformModels.Reasoning {
		models[model] = "reasoning"
	}
	for _, model := range platformModels.NonReasoning {
		models[model] = "non-reasoning"
	}

	for model, p := range platformPricing {
		pricing[model] = p
	}

	return models, pricing
}

// getPricing returns the pricing information for a model, checking custom pricing first.
func (t *Thread) getPricing(model string) llmtypes.ModelPricing {
	// Check custom pricing first
	if t.customPricing != nil {
		if pricing, ok := t.customPricing[model]; ok {
			return pricing
		}
	}

	// Return default pricing as fallback
	return llmtypes.ModelPricing{
		Input:         0.000002,  // $2.00 per million tokens (GPT-4.1 default)
		CachedInput:   0.0000005, // $0.50 per million tokens
		Output:        0.000008,  // $8.00 per million tokens
		ContextWindow: 1047576,
	}
}

// getPricingForServiceTier selects built-in pricing using the processing tier
// reported by the API. Explicit pricing from configuration remains authoritative.
func (t *Thread) getPricingForServiceTier(model string, serviceTier llmtypes.OpenAIServiceTier) llmtypes.ModelPricing {
	if t.Config.OpenAI != nil && t.Config.OpenAI.Pricing != nil {
		if pricing, ok := t.Config.OpenAI.Pricing[model]; ok {
			return pricing
		}
	}

	tier, ok := llmtypes.ParseOpenAIServiceTier(string(serviceTier))
	if !ok {
		return t.getPricing(model)
	}

	platformName := resolvePlatformForLoading(t.Config)
	switch normalizePlatformName(platformName) {
	case "openai", "codex":
		_, tierPricing := loadPlatformDefaultsForServiceTier(platformName, tier)
		if pricing, ok := tierPricing[model]; ok {
			return pricing
		}
	}

	return t.getPricing(model)
}

// isReasoningModelDynamic checks if a model supports reasoning using loaded platform defaults/config.
func (t *Thread) isReasoningModelDynamic(model string) bool {
	if t.customModels != nil {
		if category, ok := t.customModels[model]; ok {
			return category == "reasoning"
		}
	}
	return false
}
