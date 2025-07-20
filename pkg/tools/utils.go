package tools

import (
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

func findTool(name string, state tooltypes.State) (tooltypes.Tool, error) {
	for _, tool := range state.Tools() {
		if tool.Name() == name {
			return tool, nil
		}
	}
	return nil, errors.Errorf("tool %s not found", name)
}
