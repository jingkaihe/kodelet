package responses

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	openairesponses "github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeOpenAISearchConfigHelpers(t *testing.T) {
	enabled := true
	disabled := false

	assert.True(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: " openai "}))
	assert.True(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "codex"}))
	assert.True(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "codex", enableSearch: &enabled}))
	assert.False(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "codex", enableSearch: &disabled}))
	assert.False(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "fireworks"}))
	assert.False(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "openai", useCopilot: true}))
	assert.False(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "openai", baseURL: "https://example.test/v1"}))
	assert.False(t, shouldEnableNativeOpenAISearch(llmtypesConfig{platform: "openai", enableSearch: &disabled}))
	assert.Equal(t, "openai", normalizeSearchPlatformName(" OpenAI "))

	domainsFile := filepath.Join(t.TempDir(), "domains.txt")
	require.NoError(t, os.WriteFile(domainsFile, []byte("# comment\nExample.com\nhttps://docs.example.com/path\n*.golang.org\n"), 0o644))
	filters, ok := buildWebSearchFilters(domainsFile)
	require.True(t, ok)
	sort.Strings(filters.AllowedDomains)
	assert.Equal(t, []string{"*.golang.org", "docs.example.com", "example.com"}, filters.AllowedDomains)

	_, ok = buildWebSearchFilters(" ")
	assert.False(t, ok)
	_, ok = buildWebSearchFilters(filepath.Join(t.TempDir(), "missing.txt"))
	assert.False(t, ok)

	tool := buildNativeOpenAISearchTool(llmtypesConfig{allowedFile: domainsFile})
	require.NotNil(t, tool.OfWebSearch)
	assert.NotEmpty(t, tool.OfWebSearch.Filters.AllowedDomains)
}

func TestWebSearchStatusInputAndDetails(t *testing.T) {
	assert.Equal(t, "searching", webSearchStatusMessage(" SEARCHING "))
	assert.Equal(t, "completed", webSearchStatusMessage("completed"))
	assert.Equal(t, "failed", webSearchStatusMessage("failed"))
	assert.Equal(t, "in progress", webSearchStatusMessage("queued"))

	searchAction := webSearchActionFromJSON(t, `{"type":"search","query":" fallback ","queries":[" one ",""," two "]}`)
	details := webSearchDetailsFromAction(searchAction)
	assert.Equal(t, []string{"one", "two"}, details.queries)
	assert.JSONEq(t, `{"queries":["one","two"],"status":"completed","type":"search"}`, webSearchInputJSON(searchAction, "completed"))

	openAction := webSearchActionFromJSON(t, `{"type":"open_page","url":" https://example.com/page "}`)
	details = webSearchDetailsFromAction(openAction)
	assert.Equal(t, "https://example.com/page", details.url)
	assert.JSONEq(t, `{"status":"in progress","type":"open_page","url":"https://example.com/page"}`, webSearchInputJSON(openAction, "other"))

	findAction := webSearchActionFromJSON(t, `{"type":"find_in_page","url":" https://example.com/page ","pattern":" needle "}`)
	details = webSearchDetailsFromAction(findAction)
	assert.Equal(t, "https://example.com/page", details.url)
	assert.Equal(t, "needle", details.pattern)
	assert.JSONEq(t, `{"status":"failed","type":"find_in_page","url":"https://example.com/page","pattern":"needle"}`, webSearchInputJSON(findAction, "failed"))

	assert.Nil(t, searchQueries(" ", nil))
	assert.Equal(t, []string{"fallback"}, searchQueries(" fallback ", nil))
}

func TestWebSearchStructuredResultAndMetadataExtensions(t *testing.T) {
	searchItem := webSearchItemFromJSON(t, `{
		"id":"ws-1",
		"type":"web_search_call",
		"status":"completed",
		"action":{"type":"search","queries":["coverage"],"sources":[{"type":"url","url":"https://example.com/a"},{"type":"url","url":""}]}
	}`)
	result := webSearchStructuredResult("ws-1", searchItem)
	assert.Equal(t, openAISearchToolName, result.ToolName)
	assert.True(t, result.Success)
	metadata := result.Metadata.(tooltypes.OpenAIWebSearchMetadata)
	assert.Equal(t, []string{"coverage"}, metadata.Queries)
	assert.Equal(t, []string{"https://example.com/a"}, metadata.Sources)

	failedItem := webSearchItemFromJSON(t, `{"id":"ws-2","type":"web_search_call","status":"failed","action":{"type":"open_page","url":"https://example.com"}}`)
	failed := webSearchStructuredResult("ws-2", failedItem)
	assert.False(t, failed.Success)
	assert.Equal(t, "OpenAI web search failed", failed.Error)
	failedMetadata := failed.Metadata.(tooltypes.OpenAIWebSearchMetadata)
	assert.Equal(t, "https://example.com", failedMetadata.URL)

	metadataPtr := &tooltypes.OpenAIWebSearchMetadata{}
	extendWebSearchMetadataFromRawItem(metadataPtr, []byte(`{"content":"fallback query","action":{"type":"search"},"results":[{"url":"https://example.com/r"},{"url":""}]}`))
	assert.Equal(t, []string{"fallback query"}, metadataPtr.Queries)
	assert.Equal(t, []string{"https://example.com/r"}, metadataPtr.Results)

	extendWebSearchMetadataFromRawItem(metadataPtr, nil)
	extendWebSearchMetadataFromRawItem(nil, []byte(`{}`))
	extendWebSearchMetadataFromRawItem(&tooltypes.OpenAIWebSearchMetadata{}, []byte(`{`))
}

func TestWebSearchDetailsFromRawStoredAndStoredParam(t *testing.T) {
	rawOpen := []byte(`{"content":" https://fallback.example/open ","action":{"type":"open_page"}}`)
	details := webSearchDetailsFromRawItem(rawOpen)
	assert.Equal(t, "https://fallback.example/open", details.url)

	rawFind := []byte(`{"content":" https://fallback.example/find ","action":{"type":"find_in_page","pattern":" needle "}}`)
	details = webSearchDetailsFromRawItem(rawFind)
	assert.Equal(t, "https://fallback.example/find", details.url)
	assert.Equal(t, "needle", details.pattern)

	details = webSearchDetailsFromStoredItem(StoredInputItem{Action: "search", Content: "stored query", RawItem: json.RawMessage(`{"action":{"type":"search","queries":["raw"]}}`)})
	assert.Equal(t, []string{"stored query"}, details.queries)

	openParam := webSearchActionParamFromStoredItem(StoredInputItem{Action: "open_page", Content: "https://example.com/open"})
	require.NotNil(t, openParam.OfOpenPage)
	assert.Equal(t, "https://example.com/open", openParam.OfOpenPage.URL.Value)

	findParam := webSearchActionParamFromStoredItem(StoredInputItem{Action: "find_in_page", Content: "https://example.com/find", Arguments: "needle"})
	require.NotNil(t, findParam.OfFind)
	assert.Equal(t, "https://example.com/find", findParam.OfFind.URL)
	assert.Equal(t, "needle", findParam.OfFind.Pattern)

	searchParam := webSearchActionParamFromStoredItem(StoredInputItem{Action: "search", Content: "coverage"})
	require.NotNil(t, searchParam.OfSearch)
	assert.Equal(t, []string{"coverage"}, searchParam.OfSearch.Queries)
}

func webSearchActionFromJSON(t *testing.T, raw string) openairesponses.ResponseFunctionWebSearchActionUnion {
	t.Helper()

	var action openairesponses.ResponseFunctionWebSearchActionUnion
	require.NoError(t, json.Unmarshal([]byte(raw), &action))
	return action
}

func webSearchItemFromJSON(t *testing.T, raw string) openairesponses.ResponseFunctionWebSearch {
	t.Helper()

	var item openairesponses.ResponseFunctionWebSearch
	require.NoError(t, json.Unmarshal([]byte(raw), &item))
	return item
}
