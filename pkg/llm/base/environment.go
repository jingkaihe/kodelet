package base

import (
	"context"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
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

// HandlePendingEnvironmentReload runs only between complete model/tool rounds.
// A refusal is feedback for the model; an uncertain handoff must stop the run.
func HandlePendingEnvironmentReload(ctx context.Context, thread llmtypes.Thread, handler llmtypes.MessageHandler, noSave bool) error {
	environment := EnvironmentForThread(thread)
	reloader, ok := environment.(interface {
		HasPendingReload() bool
		ReloadPendingResources(context.Context) (agentenv.ReloadOutcome, error)
	})
	if !ok || !environment.IsOpen() || !reloader.HasPendingReload() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	outcome, err := reloader.ReloadPendingResources(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to reload environment")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	kind := conversations.MessageDisplayKindResourceReloadRefused
	if outcome.Reloaded {
		// OpenEnvironment reapplies the already-open environment's new snapshot
		// without replacing the thread, model client, or run identity.
		if _, err := OpenEnvironment(ctx, thread); err != nil {
			return err
		}
		clearAllowedToolsMetadata(thread)
		kind = conversations.MessageDisplayKindResourceReload
	}
	if strings.TrimSpace(outcome.Message) == "" {
		return nil
	}
	display := strings.TrimSpace(outcome.Display)
	if display == "" {
		display = agentenv.ResourcesReloadedDisplay
	}
	// Keep the outcome in model context, but project it as a host marker in
	// transcripts rather than as user input. A unique message keeps repeated
	// outcomes from sharing a marker ID.
	message := outcome.Message + "\n\n[Resource reload ID: " + convtypes.GenerateID() + "]"
	metadata := conversations.AddMessageDisplay(thread.GetMetadata(), message, display, kind, "")
	thread.SetMetadataValue(conversations.MessageDisplayMetadataKey, metadata[conversations.MessageDisplayMetadataKey])
	thread.AddUserMessage(ctx, message)
	if thread.IsPersisted() && !noSave {
		// As with compaction markers, save before announcing the outcome so a
		// later turn hook failure does not erase the marker on history refresh.
		if err := thread.SaveConversation(context.WithoutCancel(ctx)); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to save resource reload marker")
		}
	}
	if reloadHandler, ok := handler.(llmtypes.ResourceReloadMessageHandler); ok {
		reloadHandler.HandleResourceReload(kind, conversations.MessageDisplayKey(message), display)
	}
	return nil
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
			InvokedBy:      invokedByForThread(thread),
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

func invokedByForThread(thread llmtypes.Thread) string {
	if thread == nil {
		return "main"
	}
	return ExtensionInvokedByFromMetadata(thread.GetMetadata())
}

// ExtensionInvokedByFromMetadata returns the extension origin for a persisted conversation fork.
func ExtensionInvokedByFromMetadata(metadata map[string]any) string {
	initiator, ok := convtypes.ConversationForkInitiatorFromMetadata(metadata)
	if !ok || initiator.Type != convtypes.ConversationForkInitiatorTypeExtensionTool {
		return "main"
	}
	if toolName := strings.TrimSpace(initiator.ToolName); toolName != "" {
		return toolName
	}
	if extensionID := strings.TrimSpace(initiator.ExtensionID); extensionID != "" {
		return extensionID
	}
	return "main"
}

// CloseEnvironment closes the environment snapshot for one top-level SendMessage call.
func CloseEnvironment(ctx context.Context, thread llmtypes.Thread) error {
	return CloseEnvironmentWithError(ctx, thread, nil)
}

// CloseEnvironmentWithError closes an environment and reports the run outcome when supported.
func CloseEnvironmentWithError(ctx context.Context, thread llmtypes.Thread, runErr error) error {
	return CloseEnvironmentWithOutcome(ctx, thread, llmtypes.NewAgentRunOutcome(runErr, ""))
}

// CloseEnvironmentWithOutcome finalizes one run, including non-error stop reasons.
func CloseEnvironmentWithOutcome(ctx context.Context, thread llmtypes.Thread, outcome llmtypes.AgentRunOutcome) error {
	environment := EnvironmentForThread(thread)
	if environment == nil {
		return nil
	}
	var err error
	if closer, ok := environment.(agentenv.OutcomeCloser); ok {
		err = closer.CloseWithOutcome(ctx, outcome)
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
