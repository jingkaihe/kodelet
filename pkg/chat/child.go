package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/steer"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

type (
	childContextKey struct{}
	childContext    struct {
		config    llmtypes.Config
		identity  delegation.Identity
		prompt    string
		depth     int
		aggregate func(llmtypes.Usage)
		record    *convtypes.ConversationRecord
		resume    bool
	}
)

type childPresetSnapshot struct {
	Name         string                     `json:"name"`
	Options      *llmtypes.ExecutionOptions `json:"options"`
	SystemPrompt string                     `json:"system_prompt"`
}

// A later user turn may narrow the child's policy, never turn a search-only
// child into an unrestricted agent or re-read a changed extension prompt file.
func restoreChildPreset(ctx context.Context, req ChatRequest, config llmtypes.Config) (llmtypes.Config, *string, error) {
	if req.ConversationID == "" {
		return config, nil, nil
	}
	service, err := conversationservice.GetDefaultConversationService(ctx)
	if err != nil {
		return config, nil, err
	}
	defer func() { _ = service.Close() }()
	record, err := service.GetConversation(ctx, req.ConversationID)
	if errors.Is(err, convtypes.ErrConversationNotFound) {
		return config, nil, nil
	}
	if err != nil {
		return config, nil, err
	}
	raw, exists := record.Metadata["execution_preset"]
	if !exists {
		return config, nil, nil
	}
	if req.RunnerID == "" {
		return config, nil, errors.New("delegated tasks require a runner connected to the server")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return config, nil, err
	}
	var preset childPresetSnapshot
	if err := json.Unmarshal(data, &preset); err != nil {
		return config, nil, errors.Wrap(err, "invalid stored child preset")
	}
	config, err = applyChildPresetSnapshot(config, req.Options, preset)
	return config, &preset.SystemPrompt, err
}

func applyChildPresetSnapshot(config llmtypes.Config, requested *llmtypes.ExecutionOptions, preset childPresetSnapshot) (llmtypes.Config, error) {
	if preset.Options == nil {
		return config, errors.New("stored child policy is missing")
	}
	// W1 has already restored/locked the model snapshot. Restore the environment
	// and turn ceiling without introducing another provider-configuration path.
	config.ExecutionOptions = preset.Options.Clone()
	overrides := requested.Restrictions()
	if requested != nil && requested.MaxTurns != nil {
		if overrides == nil {
			overrides = &llmtypes.ExecutionOptions{}
		}
		overrides.MaxTurns = requested.MaxTurns
	}
	return childConfiguration(config, nil, overrides)
}

// Delegation never carries the parent's environment/thread ownership. Children
// have separate durable IDs and use the ordinary persisted remote chat path.
func contextWithCentralChildren(ctx context.Context, parent llmtypes.Thread, environment agentenv.Environment, req ChatRequest, resolver EnvironmentResolver) context.Context {
	config := parent.GetConfig().Clone()
	config.Extensions = nil
	manifest := environment.Manifest()
	policy := config.EnvironmentOptions()
	// The manifest is a presentation snapshot, not an implicit permission ceiling.
	config.ExecutionOptions = mergeChildOptions(config.ExecutionOptions, policy)
	depth := 0
	if state, ok := ctx.Value(childContextKey{}).(*childContext); ok {
		depth = state.depth
	}
	return delegation.WithPrepare(ctx, func(ctx context.Context, request delegation.Request, preset delegation.Preset, identity delegation.Identity) (delegation.Run, error) {
		if depth >= 8 {
			return nil, errors.New("child nesting limit exceeded")
		}
		childPolicy := config.Clone()
		// agent.init runs after this context is installed. Its explicit tool-list
		// patch (including an empty list) must constrain later child admissions.
		if raw := parent.GetMetadata()["allowed_tools"]; raw != nil {
			var allowed []string
			if err := decodeChildMetadata(raw, &allowed); err != nil {
				return nil, errors.Wrap(err, "invalid parent agent.init tool policy")
			}
			childPolicy.ExecutionOptions = intersectChildOptions(childPolicy.ExecutionOptions, &llmtypes.ExecutionOptions{AllowedTools: &allowed})
		}
		state, err := prepareChild(ctx, parent, childPolicy, request, preset, identity, req.EnvironmentProfile)
		if err != nil {
			return nil, err
		}
		childConfig := state.config
		cwd := childConfig.WorkingDirectory
		if cwd == "" {
			cwd = manifest.WorkingDirectory
		}
		rel, err := filepath.Rel(manifest.WorkingDirectory, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("the child task's working directory must be inside the parent workspace")
		}
		childConfig.WorkingDirectory = cwd
		state.config, state.depth = childConfig, depth+1
		if request.LeaseID == "" {
			if aggregate, ok := parent.(interface{ AggregateSubagentUsage(llmtypes.Usage) }); ok {
				state.aggregate = aggregate.AggregateSubagentUsage
			}
		}
		return func(ctx context.Context, emit func(delegation.Event)) (runErr error) {
			defer func() {
				finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				store, err := conversationservice.GetConversationStore(finishCtx)
				if err == nil {
					defer func() { _ = store.Close() }()
					if finisher, ok := store.(interface {
						FinishChildTurn(context.Context, string, string, bool, error) error
					}); ok {
						err = finisher.FinishChildTurn(finishCtx, identity.ConversationID, identity.RunID, ctx.Err() != nil, runErr)
					} else {
						err = errors.New("conversation storage does not support saving child task results")
					}
				}
				if runErr == nil {
					runErr = err
				}
			}()
			ctx = context.WithValue(ctx, childContextKey{}, state)
			ctx = steer.WithChildRun(ctx, identity.RunID)
			childReq := ChatRequest{
				Message: request.Message, ConversationID: identity.ConversationID, TurnID: identity.RunID,
				RunnerID: req.RunnerID, EnvironmentProfile: req.EnvironmentProfile, CWD: cwd, Options: childConfig.ExecutionOptions.Clone(),
			}
			_, err := runDefaultChat(ctx, childReq, childEventSink{emit}, "", nil, nil, resolver)
			return err
		}, nil
	})
}

func prepareChild(ctx context.Context, parent llmtypes.Thread, config llmtypes.Config, request delegation.Request, preset delegation.Preset, identity delegation.Identity, environmentProfile string) (*childContext, error) {
	state := &childContext{identity: identity, prompt: preset.SystemPrompt, resume: request.Resume != ""}
	var err error
	if state.resume {
		store, err := conversationservice.GetConversationStore(ctx)
		if err != nil {
			return nil, err
		}
		defer func() { _ = store.Close() }()
		record, err := store.Load(ctx, request.Resume)
		if err != nil {
			return nil, err
		}
		var origin delegation.Identity
		var saved childPresetSnapshot
		if err := decodeChildMetadata(record.Metadata["delegation"], &origin); err != nil {
			return nil, err
		}
		if err := decodeChildMetadata(record.Metadata["execution_preset"], &saved); err != nil {
			return nil, err
		}
		if origin.ParentConversationID != identity.ParentConversationID || origin.ExtensionID != identity.ExtensionID || origin.RunnerID != identity.RunnerID || origin.HostInstanceID == "" || origin.HostInstanceID != identity.HostInstanceID || origin.ConversationID != identity.ConversationID || saved.Name != request.Profile || origin.Profile != saved.Name {
			return nil, errors.New("the child task cannot resume with a different parent, host or execution preset")
		}
		if record.Metadata[RunnerIDMetadataKey] != identity.RunnerID || record.Metadata[EnvironmentProfileMetadataKey] != environmentProfile || (request.CWD != "" && request.CWD != record.CWD) || (request.SystemPrompt != "" && request.SystemPrompt != saved.SystemPrompt) {
			return nil, errors.New("child resume cannot change its runner, environment, working directory or prompt")
		}
		snapshot, ok, err := conversationservice.ConfigSnapshotFromMetadata(record.Metadata)
		if err != nil {
			return nil, err
		}
		if !ok || saved.Options == nil {
			return nil, errors.New("the child task cannot resume without its saved settings and permissions")
		}
		livePolicy := config.EnvironmentOptions()
		if config.ExecutionOptions != nil {
			livePolicy.MaxTurns = config.ExecutionOptions.MaxTurns
		}
		config, err = snapshot.Apply(config)
		if err != nil {
			return nil, err
		}
		config.ExecutionOptions = intersectChildOptions(saved.Options, livePolicy)
		// Validate supplied model fields against the saved identity, then narrow
		// the saved policy. Changed extension registrations never replace it.
		if _, err := applyExecutionOptions(config, request.Options, &conversationservice.GetConversationResponse{ID: record.ID, Metadata: record.Metadata}); err != nil {
			return nil, err
		}
		state.config, err = childConfiguration(config, nil, request.Options)
		if err != nil {
			return nil, err
		}
		state.config.WorkingDirectory = record.CWD
		state.record, state.prompt = &record, saved.SystemPrompt
		return state, nil
	}
	state.config, err = childConfiguration(config, preset.Options, request.Options)
	if err != nil {
		return nil, err
	}
	state.config.WorkingDirectory = request.CWD
	if request.SystemPrompt != "" {
		state.prompt = request.SystemPrompt
	}
	if request.ContextMode == "fork" {
		forker, ok := parent.(interface {
			SnapshotConversationFork(context.Context) (convtypes.ConversationRecord, error)
		})
		if !ok {
			return nil, errors.New("provider does not support a safe live context fork")
		}
		source, err := forker.SnapshotConversationFork(ctx)
		if err != nil {
			return nil, err
		}
		if source.ID != identity.ParentConversationID || source.Provider != state.config.Provider {
			return nil, errors.New("child fork must preserve the authenticated parent provider")
		}
		record := convtypes.ForkConversationRecordWithOptions(source, convtypes.ConversationForkOptions{Mode: convtypes.ConversationForkModeLiveSnapshot})
		record.ID = identity.ConversationID
		state.record = &record
	}
	return state, nil
}

func decodeChildMetadata(raw any, target any) error {
	if raw == nil {
		return errors.New("child delegation metadata is missing")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func intersectChildOptions(saved, current *llmtypes.ExecutionOptions) *llmtypes.ExecutionOptions {
	result := saved.Clone()
	if result == nil {
		result = &llmtypes.ExecutionOptions{}
	}
	if current == nil {
		return result
	}
	for _, pair := range []struct {
		to      **bool
		ceiling *bool
	}{{&result.NoTools, current.NoTools}, {&result.NoSkills, current.NoSkills}, {&result.NoExtensions, current.NoExtensions}} {
		if pair.ceiling != nil && *pair.ceiling {
			*pair.to = new(true)
		}
	}
	for _, pair := range []struct {
		to      **[]string
		ceiling *[]string
	}{{&result.AllowedTools, current.AllowedTools}, {&result.AllowedCommands, current.AllowedCommands}} {
		if pair.ceiling == nil {
			continue
		}
		allowed := slices.Clone(*pair.ceiling)
		if *pair.to != nil {
			allowed = slices.DeleteFunc(allowed, func(name string) bool { return !slices.Contains(**pair.to, name) })
		}
		*pair.to = &allowed
	}
	if current.EnableFSSearchTools != nil && !*current.EnableFSSearchTools {
		result.EnableFSSearchTools = new(false)
	}
	if current.MaxTurns != nil && *current.MaxTurns > 0 && (result.MaxTurns == nil || *result.MaxTurns == 0 || *result.MaxTurns > *current.MaxTurns) {
		result.MaxTurns = new(*current.MaxTurns)
	}
	return result
}

// Model validation stays in W1's applyExecutionOptions. The additional child
// ceiling can narrow (never widen) the parent's effective environment policy.
func childConfiguration(parent llmtypes.Config, preset, overrides *llmtypes.ExecutionOptions) (llmtypes.Config, error) {
	config := parent.Clone()
	// This is the parent's conversation-selection policy, not a child ceiling.
	// Delegated presets may choose a different model and provider-supported effort.
	config.AllowedReasoningEfforts = nil
	for _, requested := range []*llmtypes.ExecutionOptions{preset, overrides} {
		if requested == nil {
			continue
		}
		options := mergeChildOptions(config.ExecutionOptions, requested)
		host := config.EnvironmentOptions()
		for _, restriction := range []struct {
			parent, child *bool
			name          string
		}{
			{host.NoTools, requested.NoTools, "noTools"}, {host.NoSkills, requested.NoSkills, "noSkills"}, {host.NoExtensions, requested.NoExtensions, "noExtensions"},
		} {
			if restriction.parent != nil && *restriction.parent && restriction.child != nil && !*restriction.child {
				return llmtypes.Config{}, errors.Errorf("child cannot relax parent %s", restriction.name)
			}
		}
		for _, list := range []struct {
			parent, child *[]string
			name          string
		}{{host.AllowedTools, requested.AllowedTools, "tools"}, {host.AllowedCommands, requested.AllowedCommands, "commands"}} {
			if list.parent != nil && list.child != nil {
				for _, name := range *list.child {
					if !slices.Contains(*list.parent, name) {
						return llmtypes.Config{}, errors.Errorf("child %s exceed parent policy: %s", list.name, name)
					}
				}
			}
		}
		if requested.EnableFSSearchTools != nil && *requested.EnableFSSearchTools && host.EnableFSSearchTools != nil && !*host.EnableFSSearchTools {
			return llmtypes.Config{}, errors.New("child cannot enable parent-disabled filesystem search")
		}
		for _, limit := range []struct {
			parent int
			child  *int
			name   string
		}{{config.MaxTokens, requested.MaxTokens, "maxTokens"}, {config.WeakModelMaxTokens, requested.WeakModelMaxTokens, "weakModelMaxTokens"}, {config.ThinkingBudgetTokens, requested.ThinkingBudgetTokens, "thinkingBudgetTokens"}} {
			if limit.child != nil && *limit.child > limit.parent {
				return llmtypes.Config{}, errors.Errorf("child %s exceeds parent ceiling", limit.name)
			}
		}
		if config.ExecutionOptions != nil && config.ExecutionOptions.MaxTurns != nil && *config.ExecutionOptions.MaxTurns > 0 && requested.MaxTurns != nil && (*requested.MaxTurns == 0 || *requested.MaxTurns > *config.ExecutionOptions.MaxTurns) {
			return llmtypes.Config{}, errors.New("child maxTurns exceeds parent ceiling")
		}
		var err error
		config, err = applyExecutionOptions(config, options, nil)
		if err != nil {
			return llmtypes.Config{}, err
		}
	}
	return config, nil
}

func mergeChildOptions(parent, child *llmtypes.ExecutionOptions) *llmtypes.ExecutionOptions {
	result := parent.Clone()
	if result == nil {
		result = &llmtypes.ExecutionOptions{}
	}
	if child == nil {
		return result
	}
	// ExecutionOptions deliberately contains only optional pointer fields. Copy
	// those present rather than serializing Config or creating a parallel schema.
	to, from := reflect.ValueOf(result).Elem(), reflect.ValueOf(child.Clone()).Elem()
	for i := 0; i < from.NumField(); i++ {
		if !from.Field(i).IsNil() {
			to.Field(i).Set(from.Field(i))
		}
	}
	return result
}

type childEventSink struct{ emit func(delegation.Event) }

func (s childEventSink) Send(event ChatEvent) error {
	text, _ := event.Content.(string)
	if event.Delta != "" {
		text = event.Delta
	}
	if event.Result != nil {
		text = *event.Result
	}
	childEvent := delegation.Event{Kind: event.Kind, Text: text, ToolName: event.ToolName, ToolCallID: event.ToolCallID}
	switch event.Kind {
	case "tool-use":
		childEvent.Input = event.Input
	case "tool-update", "tool-result":
		childEvent.ToolOutput = event.ToolOutput
		if event.Kind == "tool-result" && event.ToolResult != nil {
			childEvent.Success = new(event.ToolResult.Success)
			childEvent.Error = event.ToolResult.Error
		}
	}
	s.emit(childEvent)
	return nil
}

// Save the child's identity before runner/provider side effects and preserve it
// even when opening fails or cancellation arrives immediately after admission.
func persistChildIdentity(ctx context.Context, state *childContext, runnerID, environmentProfile string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	admission := convtypes.ChildAdmission{TurnAdmission: convtypes.TurnAdmission{ConversationID: state.identity.ConversationID, RunID: state.identity.RunID, RunnerID: runnerID, EnvironmentProfile: environmentProfile}}
	if state.resume {
		admission.ExpectedUpdatedAt = state.record.UpdatedAt
	}
	ctx = convtypes.ContextWithChildAdmission(ctx, admission)
	if state.record != nil {
		record := *state.record
		record.CWD = state.config.WorkingDirectory
		var err error
		record.Metadata, err = conversationservice.AddConfigSnapshot(record.Metadata, state.config)
		if err != nil {
			return err
		}
		if !state.resume {
			record.Metadata["delegation"] = state.identity
		}
		record.Metadata["execution_preset"] = childPresetSnapshot{Name: state.identity.Profile, Options: state.config.ExecutionOptions.Clone(), SystemPrompt: state.prompt}
		record.Metadata[RunnerIDMetadataKey], record.Metadata[EnvironmentProfileMetadataKey] = runnerID, environmentProfile
		store, err := conversationservice.GetConversationStore(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		return store.Save(ctx, record)
	}
	thread, err := llm.NewThread(state.config.Clone())
	if err != nil {
		return err
	}
	defer func() { _ = llm.CloseThread(thread) }()
	thread.SetConversationID(state.identity.ConversationID)
	thread.EnablePersistence(ctx, true)
	if !thread.IsPersisted() {
		return errors.New("child tasks require conversation storage")
	}
	thread.SetMetadataValue("delegation", state.identity)
	thread.SetMetadataValue("execution_preset", childPresetSnapshot{Name: state.identity.Profile, Options: state.config.ExecutionOptions, SystemPrompt: state.prompt})
	thread.SetMetadataValue(RunnerIDMetadataKey, runnerID)
	thread.SetMetadataValue(EnvironmentProfileMetadataKey, environmentProfile)
	return thread.SaveConversation(ctx)
}

// A resumed child's lifetime usage remains in its own record; the foreground
// parent is charged only for this execution, never for previous child turns.
func childTurnUsage(total, initial llmtypes.Usage) llmtypes.Usage {
	return llmtypes.Usage{
		InputTokens: total.InputTokens - initial.InputTokens, OutputTokens: total.OutputTokens - initial.OutputTokens,
		CacheCreationInputTokens: total.CacheCreationInputTokens - initial.CacheCreationInputTokens, CacheReadInputTokens: total.CacheReadInputTokens - initial.CacheReadInputTokens,
		InputCost: total.InputCost - initial.InputCost, OutputCost: total.OutputCost - initial.OutputCost,
		CacheCreationCost: total.CacheCreationCost - initial.CacheCreationCost, CacheReadCost: total.CacheReadCost - initial.CacheReadCost,
	}
}
