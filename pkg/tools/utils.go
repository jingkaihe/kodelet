package tools

import (
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

func noNestedBatch(input BatchToolInput) error {
	for idx, invocation := range input.Invocations {
		if invocation.ToolName == "batch" {
			return errors.Wrapf(ErrNestedBatch, "invocation.%d is a batch tool", idx)
		}
	}
	return nil
}

func findTool(name string, state tooltypes.State) (tooltypes.Tool, error) {
	for _, tool := range state.Tools() {
		if tool.Name() == name {
			return tool, nil
		}
	}
	return nil, errors.Wrapf(ErrToolNotFound, "tool %s not found", name)
}