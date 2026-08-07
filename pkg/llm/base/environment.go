package base

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

type environmentThread interface {
	GetEnvironment() agentenv.Environment
	SetEnvironment(agentenv.Environment)
	SetEnvironmentState(tooltypes.State)
}

type environmentConfigThread interface {
	ApplyEnvironmentConfig(agentenv.EnvironmentConfig)
}

// OpenEnvironment opens and pins the environment used by one top-level SendMessage call.
func OpenEnvironment(ctx context.Context, thread llmtypes.Thread) (agentenv.Manifest, error) {
	if thread == nil {
		return agentenv.Manifest{}, errors.New("thread is required")
	}
	host, ok := thread.(environmentThread)
	if !ok {
		return agentenv.Manifest{}, errors.New("thread does not support agent environments")
	}

	environment := host.GetEnvironment()
	if environment == nil {
		runtime, _ := thread.GetConfig().Extensions.(*extensions.Runtime)
		environment = agentenv.NewLocalEnvironmentFromState(thread.GetState(), runtime)
		host.SetEnvironment(environment)
	}

	manifest := environment.Manifest()
	if !environment.IsOpen() {
		var err error
		manifest, err = environment.Open(ctx, agentenv.RunSpec{
			ConversationID: thread.GetConversationID(),
			Config:         thread.GetConfig(),
			Metadata:       thread.GetMetadata(),
			InvokedBy:      "main",
		})
		if err != nil {
			return agentenv.Manifest{}, err
		}
	}
	if manifest.Config != nil {
		configHost, ok := thread.(environmentConfigThread)
		if !ok {
			_ = environment.Close(context.WithoutCancel(ctx))
			return agentenv.Manifest{}, errors.New("thread does not support environment configuration")
		}
		configHost.ApplyEnvironmentConfig(*manifest.Config)
	}
	if stateProvider, ok := environment.(agentenv.StateProvider); ok {
		host.SetEnvironmentState(stateProvider.State())
	}
	return manifest, nil
}

// CloseEnvironment closes the environment snapshot for one top-level SendMessage call.
func CloseEnvironment(ctx context.Context, thread llmtypes.Thread) error {
	return CloseEnvironmentWithError(ctx, thread, nil)
}

// CloseEnvironmentWithError closes an environment and reports the run outcome when supported.
func CloseEnvironmentWithError(ctx context.Context, thread llmtypes.Thread, runErr error) error {
	environment := EnvironmentForThread(thread)
	if environment == nil {
		return nil
	}
	var err error
	if closer, ok := environment.(agentenv.OutcomeCloser); ok {
		err = closer.CloseWithError(ctx, runErr)
	} else {
		err = environment.Close(ctx)
	}
	if err != nil {
		return err
	}
	if host, ok := thread.(environmentThread); ok {
		var state tooltypes.State
		if provider, ok := environment.(agentenv.StateProvider); ok {
			state = provider.State()
		}
		host.SetEnvironmentState(state)
	}
	return nil
}

// EnvironmentForThread returns the environment attached to a provider thread.
func EnvironmentForThread(thread llmtypes.Thread) agentenv.Environment {
	if thread == nil {
		return nil
	}
	host, ok := thread.(interface{ GetEnvironment() agentenv.Environment })
	if !ok {
		return nil
	}
	return host.GetEnvironment()
}

// EnvironmentIsOpen reports whether a thread has a pinned environment run.
func EnvironmentIsOpen(thread llmtypes.Thread) bool {
	environment := EnvironmentForThread(thread)
	return environment != nil && environment.IsOpen()
}

// EnvironmentManifest returns the currently pinned manifest for a thread.
func EnvironmentManifest(thread llmtypes.Thread) agentenv.Manifest {
	environment := EnvironmentForThread(thread)
	if environment == nil {
		return agentenv.Manifest{}
	}
	return environment.Manifest()
}

// EnvironmentContexts returns a defensive copy of the run-pinned context snapshot.
func EnvironmentContexts(thread llmtypes.Thread) map[string]string {
	if environment := EnvironmentForThread(thread); environment != nil && environment.IsOpen() {
		return environment.Manifest().Contexts
	}
	if state := threadState(thread); state != nil {
		return state.DiscoverContexts()
	}
	return nil
}
