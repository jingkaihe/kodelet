package tui

import (
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func structuredToolResultText(result *tooltypes.StructuredToolResult) string {
	if result == nil {
		return ""
	}
	rendered := renderers.NewRendererRegistry().Render(*result)
	if strings.TrimSpace(rendered) != "" {
		return rendered
	}
	if strings.TrimSpace(result.Error) != "" {
		return result.Error
	}
	return ""
}

func parseStructuredToolResult(content string) (*tooltypes.StructuredToolResult, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, false
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, false
	}
	if _, ok := raw["success"].(bool); !ok {
		return nil, false
	}

	var result tooltypes.StructuredToolResult
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false
	}
	if result.ToolName == "" && result.Metadata == nil {
		return nil, false
	}
	return &result, true
}
