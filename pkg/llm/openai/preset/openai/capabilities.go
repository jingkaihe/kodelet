package openai

import "strings"

// responsesModel describes request handling for a model that only works with
// the Responses API.
type responsesModel struct {
	// rejectsNoneReasoning is set when the API rejects the `none` reasoning
	// effort, so `none` and `minimal` must be sent as `low`.
	rejectsNoneReasoning bool
}

// responsesModels lists GPT-6 models that need the Responses API and accept
// prompt_cache_options. GPT-5.6 models are matched by prefix in
// lookupResponsesModel.
var responsesModels = map[string]responsesModel{
	"gpt-6.2-astra": {rejectsNoneReasoning: true},
	"gpt-6.2-sol":   {rejectsNoneReasoning: true},
	"gpt-6.1-terra": {rejectsNoneReasoning: true},
	"gpt-6.1-sol":   {rejectsNoneReasoning: true},
	"gpt-6-astra":   {rejectsNoneReasoning: true},
	"gpt-6-sol":     {},
	"gpt-6-luna":    {},
}

func lookupResponsesModel(model string) (responsesModel, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if capabilities, ok := responsesModels[model]; ok {
		return capabilities, true
	}
	if model == "gpt-5.6" || strings.HasPrefix(model, "gpt-5.6-") {
		return responsesModel{}, true
	}
	return responsesModel{}, false
}

// RequiresResponsesAPI reports whether model must be called through the
// Responses API rather than Chat Completions.
func RequiresResponsesAPI(model string) bool {
	_, ok := lookupResponsesModel(model)
	return ok
}

// SupportsPromptCacheOptions reports whether model accepts the Responses API
// prompt_cache_options parameter.
func SupportsPromptCacheOptions(model string) bool {
	_, ok := lookupResponsesModel(model)
	return ok
}

// RejectsNoneReasoningEffort reports whether the API rejects the `none`
// reasoning effort for model.
func RejectsNoneReasoningEffort(model string) bool {
	capabilities, _ := lookupResponsesModel(model)
	return capabilities.rejectsNoneReasoning
}
