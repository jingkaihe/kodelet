package tools

import (
	"strings"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

func findTool(name string, state tooltypes.State) (tooltypes.Tool, error) {
	if normalized, found := resolveCustomToolAlias(name, state); found {
		name = normalized
	}
	for _, tool := range state.Tools() {
		if tool.Name() == name {
			return tool, nil
		}
	}
	return nil, errors.Errorf("tool %s not found", name)
}

func resolveCustomToolAlias(name string, state tooltypes.State) (string, bool) {
	if state == nil {
		return "", false
	}
	if !strings.HasPrefix(name, customToolAliasPrefix) && !strings.HasPrefix(name, pluginToolAliasPrefix) && !strings.Contains(name, "/") && !strings.Contains(name, "@") {
		return "", false
	}

	for _, tool := range state.Tools() {
		customTool, ok := tool.(*CustomTool)
		if !ok {
			continue
		}
		canonical := customTool.canonical
		if canonical == "" {
			canonical = customTool.name
		}
		if name == canonical || name == strings.Replace(canonical, "/", "@", 1) {
			return customTool.Name(), true
		}
		prefix := ""
		if idx := strings.LastIndex(canonical, "/"); idx > 0 {
			prefix = canonical[:idx] + "/"
		}
		aliases := aliasesForTool(customTool.name, prefix)
		for _, alias := range aliases.Aliases {
			if alias == name {
				return customTool.Name(), true
			}
		}
	}

	return "", false
}
