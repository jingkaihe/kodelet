package responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openaipreset "github.com/jingkaihe/kodelet/pkg/llm/openai/preset/openai"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/openai/openai-go/v3/responses"
)

const openAISearchToolName = "openai_web_search"

func shouldEnableNativeOpenAISearch(config llmtypesConfig) bool {
	platform := normalizeSearchPlatformName(config.platform)
	if platform != "openai" && platform != "codex" {
		return false
	}

	if config.useCopilot {
		return false
	}

	if platform == "codex" {
		if config.enableSearch != nil {
			return *config.enableSearch
		}
		return true
	}

	if config.baseURL != "" && !strings.EqualFold(strings.TrimRight(config.baseURL, "/"), strings.TrimRight(openaipreset.BaseURL, "/")) {
		return false
	}

	if config.enableSearch != nil {
		return *config.enableSearch
	}

	return true
}

type llmtypesConfig struct {
	platform     string
	baseURL      string
	useCopilot   bool
	enableSearch *bool
	allowedFile  string
	allowedTools []string
}

func normalizeSearchPlatformName(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}

func buildNativeOpenAISearchTool(config llmtypesConfig) responses.ToolUnionParam {
	tool := responses.WebSearchToolParam{
		Type:              responses.WebSearchToolTypeWebSearch,
		SearchContextSize: responses.WebSearchToolSearchContextSizeMedium,
	}

	if filters, ok := buildWebSearchFilters(config.allowedFile); ok {
		tool.Filters = filters
	}

	return responses.ToolUnionParam{OfWebSearch: &tool}
}

func buildWebSearchFilters(allowedDomainsFile string) (responses.WebSearchToolFiltersParam, bool) {
	if strings.TrimSpace(allowedDomainsFile) == "" {
		return responses.WebSearchToolFiltersParam{}, false
	}

	filter := osutil.NewDomainFilter(allowedDomainsFile)
	allowedDomains := filter.GetAllowedDomains()
	if len(allowedDomains) == 0 {
		return responses.WebSearchToolFiltersParam{}, false
	}

	return responses.WebSearchToolFiltersParam{AllowedDomains: allowedDomains}, true
}

func webSearchStatusMessage(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "searching":
		return "searching"
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	default:
		return "in progress"
	}
}

func webSearchInputJSON(action responses.ResponseFunctionWebSearchActionUnion, status string) string {
	details := webSearchDetailsFromAction(action)
	payload := map[string]any{
		"status": webSearchStatusMessage(status),
		"type":   action.Type,
	}

	switch action.Type {
	case "search":
		if len(details.queries) > 0 {
			payload["queries"] = details.queries
		}
	case "open_page":
		if details.url != "" {
			payload["url"] = details.url
		}
	case "find_in_page":
		if details.url != "" {
			payload["url"] = details.url
		}
		if details.pattern != "" {
			payload["pattern"] = details.pattern
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"status":%q}`, webSearchStatusMessage(status))
	}
	return string(data)
}

func webSearchStructuredResult(callID string, item responses.ResponseFunctionWebSearch) tooltypes.StructuredToolResult {
	details := webSearchDetailsFromAction(item.Action)
	metadata := tooltypes.OpenAIWebSearchMetadata{
		CallID: callID,
		Status: string(item.Status),
		Action: item.Action.Type,
	}

	switch item.Action.Type {
	case "search":
		search := item.Action.AsSearch()
		metadata.Queries = details.queries
		for _, source := range search.Sources {
			if strings.TrimSpace(source.URL) != "" {
				metadata.Sources = append(metadata.Sources, source.URL)
			}
		}
	case "open_page":
		metadata.URL = details.url
	case "find_in_page":
		metadata.URL = details.url
		metadata.Pattern = details.pattern
	}

	result := tooltypes.StructuredToolResult{
		ToolName:  openAISearchToolName,
		Success:   item.Status != responses.ResponseFunctionWebSearchStatusFailed,
		Timestamp: time.Now(),
		Metadata:  metadata,
	}
	if !result.Success {
		result.Error = "OpenAI web search failed"
	}

	return result
}

func extendWebSearchMetadataFromRawItem(metadata *tooltypes.OpenAIWebSearchMetadata, rawItem []byte) {
	if metadata == nil || len(rawItem) == 0 {
		return
	}

	details := webSearchDetailsFromRawItem(rawItem)
	if metadata.URL == "" && details.url != "" {
		metadata.URL = details.url
	}
	if metadata.Pattern == "" && details.pattern != "" {
		metadata.Pattern = details.pattern
	}
	if len(metadata.Queries) == 0 && len(details.queries) > 0 {
		metadata.Queries = details.queries
	}

	var payload struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rawItem, &payload); err != nil {
		return
	}

	for _, result := range payload.Results {
		if strings.TrimSpace(result.URL) != "" {
			metadata.Results = append(metadata.Results, result.URL)
		}
	}
}

func searchQueries(query string, queries []string) []string {
	if len(queries) > 0 {
		trimmedQueries := make([]string, 0, len(queries))
		for _, query := range queries {
			if trimmed := strings.TrimSpace(query); trimmed != "" {
				trimmedQueries = append(trimmedQueries, trimmed)
			}
		}
		return trimmedQueries
	}
	if strings.TrimSpace(query) == "" {
		return nil
	}
	return []string{strings.TrimSpace(query)}
}

type webSearchActionDetails struct {
	url     string
	pattern string
	queries []string
}

func webSearchDetailsFromAction(action responses.ResponseFunctionWebSearchActionUnion) webSearchActionDetails {
	details := webSearchActionDetails{}

	switch action.Type {
	case "search":
		search := action.AsSearch()
		details.queries = searchQueries("", search.Queries)
		if len(details.queries) == 0 {
			details.queries = searchQueries(action.Query, action.Queries)
		}
	case "open_page":
		openPage := action.AsOpenPage()
		details.url = strings.TrimSpace(openPage.URL)
		if details.url == "" {
			details.url = strings.TrimSpace(action.URL)
		}
	case "find_in_page":
		find := action.AsFind()
		details.url = strings.TrimSpace(find.URL)
		details.pattern = strings.TrimSpace(find.Pattern)
		if details.url == "" {
			details.url = strings.TrimSpace(action.URL)
		}
		if details.pattern == "" {
			details.pattern = strings.TrimSpace(action.Pattern)
		}
	}

	return details
}

func webSearchDetailsFromRawItem(rawItem []byte) webSearchActionDetails {
	var payload struct {
		Content string `json:"content"`
		Action  struct {
			Type    string   `json:"type"`
			URL     string   `json:"url"`
			Pattern string   `json:"pattern"`
			Query   string   `json:"query"`
			Queries []string `json:"queries"`
		} `json:"action"`
	}

	if err := json.Unmarshal(rawItem, &payload); err != nil {
		return webSearchActionDetails{}
	}

	details := webSearchActionDetails{
		url:     strings.TrimSpace(payload.Action.URL),
		pattern: strings.TrimSpace(payload.Action.Pattern),
		queries: searchQueries(payload.Action.Query, payload.Action.Queries),
	}

	if details.url == "" && (payload.Action.Type == "open_page" || payload.Action.Type == "find_in_page") {
		details.url = strings.TrimSpace(payload.Content)
	}
	if len(details.queries) == 0 && payload.Action.Type == "search" && strings.TrimSpace(payload.Content) != "" {
		details.queries = []string{strings.TrimSpace(payload.Content)}
	}

	return details
}

func webSearchDetailsFromStoredItem(item StoredInputItem) webSearchActionDetails {
	details := webSearchDetailsFromRawItem(item.RawItem)
	content := strings.TrimSpace(item.Content)
	arguments := strings.TrimSpace(item.Arguments)

	switch item.Action {
	case "open_page":
		if content != "" {
			details.url = content
		}
	case "find_in_page":
		if content != "" {
			details.url = content
		}
		if arguments != "" {
			details.pattern = arguments
		}
	default:
		if content != "" {
			details.queries = []string{content}
		}
	}

	return details
}
