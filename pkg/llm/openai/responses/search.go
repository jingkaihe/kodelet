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
	payload := map[string]any{
		"status": webSearchStatusMessage(status),
		"type":   action.Type,
	}

	switch action.Type {
	case "search":
		search := action.AsSearch()
		queries := searchQueries(search.Query, search.Queries)
		if len(queries) > 0 {
			payload["queries"] = queries
		}
	case "open_page":
		openPage := action.AsOpenPage()
		if openPage.URL != "" {
			payload["url"] = openPage.URL
		}
	case "find_in_page":
		find := action.AsFind()
		if find.URL != "" {
			payload["url"] = find.URL
		}
		if find.Pattern != "" {
			payload["pattern"] = find.Pattern
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"status":%q}`, webSearchStatusMessage(status))
	}
	return string(data)
}

func webSearchStructuredResult(callID string, item responses.ResponseFunctionWebSearch) tooltypes.StructuredToolResult {
	metadata := tooltypes.OpenAIWebSearchMetadata{
		CallID: callID,
		Status: string(item.Status),
		Action: item.Action.Type,
	}

	switch item.Action.Type {
	case "search":
		search := item.Action.AsSearch()
		metadata.Queries = searchQueries(search.Query, search.Queries)
		for _, source := range search.Sources {
			if strings.TrimSpace(source.URL) != "" {
				metadata.Sources = append(metadata.Sources, source.URL)
			}
		}
	case "open_page":
		openPage := item.Action.AsOpenPage()
		metadata.URL = openPage.URL
	case "find_in_page":
		find := item.Action.AsFind()
		metadata.URL = find.URL
		metadata.Pattern = find.Pattern
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
		return append([]string(nil), queries...)
	}
	if strings.TrimSpace(query) == "" {
		return nil
	}
	return []string{query}
}
