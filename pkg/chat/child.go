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
		return config, nil, errors.New("delegated children require a remote runner")
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
	tools := manifest.ToolNames()
	if policy.AllowedTools != nil {
		tools = slices.DeleteFunc(tools, func(name string) bool { return !slices.Contains(*policy.AllowedTools, name) })
	}
	policy.AllowedTools = &tools
	config.ExecutionOptions = mergeChildOptions(config.ExecutionOptions, policy)
	depth := 0
	if state, ok := ctx.Value(childContextKey{}).(*childContext); ok {
		depth = state.depth
	}
	return delegation.WithPrepare(ctx, func(_ context.Context, request delegation.Request, preset delegation.Preset, identity delegation.Identity) (delegation.Run, error) {
		if depth >= 8 {
			return nil, errors.New("child nesting limit exceeded")
		}
		childConfig, err := childConfiguration(config, preset.Options, request.Options)
		if err != nil {
			return nil, err
		}
		cwd := request.CWD
		if cwd == "" {
			cwd = manifest.WorkingDirectory
		}
		rel, err := filepath.Rel(manifest.WorkingDirectory, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("child working directory exceeds parent workspace")
		}
		childConfig.WorkingDirectory = cwd
		prompt := preset.SystemPrompt
		if request.SystemPrompt != "" {
			prompt = request.SystemPrompt
		}
		state := &childContext{config: childConfig, identity: identity, prompt: prompt, depth: depth + 1}
		if request.LeaseID == "" {
			if aggregate, ok := parent.(interface{ AggregateSubagentUsage(llmtypes.Usage) }); ok {
				state.aggregate = aggregate.AggregateSubagentUsage
			}
		}
		return func(ctx context.Context, emit func(delegation.Event)) error {
			ctx = context.WithValue(ctx, childContextKey{}, state)
			childReq := ChatRequest{
				Message: request.Message, ConversationID: identity.ConversationID, TurnID: identity.RunID,
				RunnerID: req.RunnerID, EnvironmentProfile: req.EnvironmentProfile, CWD: cwd, Options: childConfig.ExecutionOptions.Clone(),
			}
			_, err := runDefaultChat(ctx, childReq, childEventSink{emit}, "", nil, nil, resolver)
			return err
		}, nil
	})
}

// Model validation stays in W1's applyExecutionOptions. The additional child
// ceiling can narrow (never widen) the parent's effective environment policy.
func childConfiguration(parent llmtypes.Config, preset, overrides *llmtypes.ExecutionOptions) (llmtypes.Config, error) {
	config := parent.Clone()
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
	s.emit(delegation.Event{Kind: event.Kind, Text: text, ToolName: event.ToolName, ToolCallID: event.ToolCallID})
	return nil
}

// Save the child's identity before runner/provider side effects and preserve it
// even when opening fails or cancellation arrives immediately after admission.
func persistChildIdentity(ctx context.Context, state *childContext, runnerID, environmentProfile string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	thread, err := llm.NewThread(state.config.Clone())
	if err != nil {
		return err
	}
	defer func() { _ = llm.CloseThread(thread) }()
	thread.SetConversationID(state.identity.ConversationID)
	thread.EnablePersistence(ctx, true)
	thread.SetMetadataValue("delegation", state.identity)
	thread.SetMetadataValue("execution_preset", childPresetSnapshot{Name: state.identity.Profile, Options: state.config.ExecutionOptions, SystemPrompt: state.prompt})
	thread.SetMetadataValue(RunnerIDMetadataKey, runnerID)
	thread.SetMetadataValue(EnvironmentProfileMetadataKey, environmentProfile)
	return thread.SaveConversation(ctx)
}
