package llm

import (
	"github.com/jingkaihe/kodelet/pkg/agentenv"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// SetEnvironment attaches an agent environment to a provider thread.
func SetEnvironment(thread llmtypes.Thread, environment agentenv.Environment) error {
	if thread == nil {
		return errors.New("thread is required")
	}
	setter, ok := thread.(interface{ SetEnvironment(agentenv.Environment) })
	if !ok {
		return errors.New("thread does not support agent environments")
	}
	setter.SetEnvironment(environment)
	return nil
}
