package anthropic

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

const anthropicSearchToolName = "anthropic_web_search"

func (t *Thread) requestTools(opt llmtypes.MessageOpt) ([]anthropic.ToolUnionParam, error) {
	tools := toAnthropicTools(t.tools(opt), t.useSubscription)
	// Explicit opt-in lets an extension profile search without enabling it for the parent.
	if opt.NoToolUse || !slices.Contains(t.Config.AllowedTools, anthropicSearchToolName) ||
		!base.ToolAllowedForThread(t, anthropicSearchToolName) {
		return tools, nil
	}
	if t.useCopilot {
		return nil, errors.New("anthropic_web_search is not supported by GitHub Copilot")
	}
	return append(tools, anthropic.ToolUnionParam{
		OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{MaxUses: anthropic.Int(5)},
	}), nil
}

func webSearchResponseError(response *anthropic.Message) error {
	for _, block := range response.Content {
		if result, ok := block.AsAny().(anthropic.WebSearchToolResultBlock); ok && result.Content.ErrorCode != "" {
			return errors.Errorf("Anthropic web search failed: %s", result.Content.ErrorCode)
		}
	}
	return nil
}

// Native searches run at the provider, but use the same progress events as local tools.
func handleWebSearchProgress(handler llmtypes.MessageHandler, block anthropic.ContentBlockUnion) {
	switch variant := block.AsAny().(type) {
	case anthropic.ServerToolUseBlock:
		if variant.Name == "web_search" {
			handler.HandleToolUse(variant.ID, "web_search", variant.JSON.Input.Raw())
		}
	case anthropic.WebSearchToolResultBlock:
		result := tooltypes.BaseToolResult{}
		if variant.Content.ErrorCode != "" {
			result.Error = "Anthropic web search failed: " + string(variant.Content.ErrorCode)
		} else {
			result.Result = fmt.Sprintf("Found %d search results", len(variant.Content.OfWebSearchResultBlockArray))
		}
		handler.HandleToolResult(variant.ToolUseID, "web_search", result)
	}
}

// webSearchCitationLinks renders sources without changing the stored API content.
func webSearchCitationLinks(citations []anthropic.TextCitationParamUnion) string {
	var links strings.Builder
	seen := make(map[string]bool)
	for _, citation := range citations {
		raw := citation.GetURL()
		if raw == nil || seen[*raw] {
			continue
		}
		u, err := url.Parse(*raw)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			continue
		}
		seen[*raw] = true
		safeURL := strings.NewReplacer("<", "%3C", ">", "%3E", " ", "%20", "\\", "%5C").Replace(u.String())
		links.WriteString(" [source](<" + safeURL + ">)")
	}
	return links.String()
}
