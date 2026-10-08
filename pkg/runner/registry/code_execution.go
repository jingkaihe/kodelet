package registry

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/codemode"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

type codeParentRegistration struct {
	ctx      context.Context
	cancel   context.CancelFunc
	allowed  map[string]bool
	children map[string]bool // false retains a completed-child tombstone.
}

func (r *Registry) registerCodeParent(ctx context.Context, params runnerpayload.ToolExecuteParams) (func(), error) {
	if params.CallableTools == nil || params.ManifestDigest == "" {
		return nil, errors.New("code execution requires host-owned callable tools and manifest digest")
	}
	key := modelHelperKey{params.RunID, params.ToolCallID}
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[key.runID]
	if run == nil || run.Status != RunStatusRunning || !run.codeExecution || run.codeStopping || ctx.Err() != nil {
		return nil, errors.New("code execution requires an active negotiated run")
	}
	if _, err := r.activeRunLinkLocked(params.RunID, false); err != nil {
		return nil, err
	}
	if run.ManifestDigest != params.ManifestDigest {
		return nil, errors.New("code execution manifest digest does not match the pinned run")
	}
	var manifest runnerpayload.Manifest
	if err := json.Unmarshal([]byte(run.ManifestJSON), &manifest); err != nil {
		return nil, errors.Wrap(err, "invalid pinned code execution manifest")
	}
	if !manifest.Capabilities.CodeExecution {
		return nil, errors.New("runner does not support code execution")
	}
	available := make(map[string]bool, len(manifest.Tools))
	for _, definition := range manifest.Tools {
		// Model-only tools are declared directly and never callable from scripts.
		available[definition.Name] = definition.Placement == "environment" && !definition.ModelOnly &&
			manifest.Config.Options.ToolAllowed(definition.Name)
	}
	if !available["code_execute"] {
		return nil, errors.New("code execution is not available in the pinned manifest")
	}
	allowed := make(map[string]bool, len(*params.CallableTools))
	for _, name := range *params.CallableTools {
		if name == "code_execute" || !available[name] {
			return nil, errors.New("code execution callable set exceeds the pinned manifest")
		}
		allowed[name] = true
	}
	if r.codeParents == nil {
		r.codeParents = make(map[modelHelperKey]*codeParentRegistration)
	}
	if _, exists := r.codeParents[key]; exists {
		return nil, errors.New("code execution parent is already active")
	}
	parentCtx, cancel := context.WithCancel(ctx)
	parent := &codeParentRegistration{
		ctx:      parentCtx,
		cancel:   cancel,
		allowed:  allowed,
		children: make(map[string]bool),
	}
	r.codeParents[key] = parent
	cleanup := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.codeParents[key] == parent {
			r.clearCodeParentLocked(key, parent)
		}
	}
	stop := context.AfterFunc(parentCtx, cleanup)
	return func() {
		stop()
		cleanup()
	}, nil
}

// codeChild registers capabilities atomically under one lock. No RPC, tool work,
// or waiting occurs under that lock. Authority derives from the parent context,
// never the short-lived reverse-RPC context.
func (r *Registry) codeChild(ctx context.Context, identity UIRequestIdentity, params runnerpayload.ToolChildParams, begin bool) (any, *protocol.RPCError) {
	fail := func(code int, message string) (any, *protocol.RPCError) {
		return nil, &protocol.RPCError{Code: code, Message: message}
	}
	for _, id := range []string{params.RunID, params.ParentToolCallID, params.ToolCallID} {
		if id == "" || strings.TrimSpace(id) != id || len(id) > 256 {
			return fail(protocol.ErrorCodeInvalidParams, "child ownership requires valid run, parent, and tool call IDs")
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(protocol.ErrorCodeUnavailable, err.Error())
	}
	key := modelHelperKey{params.RunID, params.ParentToolCallID}
	childKey := modelHelperKey{params.RunID, params.ToolCallID}
	r.mu.Lock()
	defer r.mu.Unlock()
	runner, err := r.currentRunnerLocked(identity.RunnerID, identity.ConnectionID, identity.Generation)
	run := r.runs[params.RunID]
	parent := r.codeParents[key]
	if err != nil || run == nil || run.Status != RunStatusRunning || run.codeStopping ||
		run.RunnerID != identity.RunnerID || run.connectionID != identity.ConnectionID || run.generation != identity.Generation ||
		!runnerHasActiveRun(runner, params.RunID) || parent == nil || parent.ctx.Err() != nil {
		return fail(protocol.ErrorCodeStale, "child ownership requires an active code execution parent and runner generation")
	}
	if !begin {
		if _, exists := parent.children[params.ToolCallID]; !exists {
			return fail(protocol.ErrorCodeInvalidParams, "tool call is not a child of this parent")
		}
		r.clearCodeChildLocked(childKey)
		parent.children[params.ToolCallID] = false
		return struct{}{}, nil
	}
	if params.Name == "code_execute" || !parent.allowed[params.Name] {
		return fail(protocol.ErrorCodeInvalidParams, "child tool is not in the authorized catalog")
	}
	if params.ToolCallID == params.ParentToolCallID ||
		r.artifactTools[childKey] != nil || run.codeChildIDs[params.ToolCallID] != "" {
		return fail(protocol.ErrorCodeConflict, "child tool call ID has already been used")
	}
	if len(parent.children) >= 128 {
		return fail(protocol.ErrorCodeConflict, "code execution child call limit exceeded")
	}
	active := 0
	for _, running := range parent.children {
		if running {
			active++
		}
	}
	if active >= codemode.MaxConcurrentToolCalls {
		return fail(protocol.ErrorCodeConflict, "code execution active child limit exceeded")
	}
	if run.codeChildIDs == nil {
		run.codeChildIDs = make(map[string]string)
	}
	run.codeChildIDs[params.ToolCallID] = params.ParentToolCallID
	parent.children[params.ToolCallID] = true
	artifactCtx, artifactCancel := context.WithCancel(parent.ctx)
	if r.artifactTools == nil {
		r.artifactTools = make(map[modelHelperKey]*artifactToolRegistration)
	}
	r.artifactTools[childKey] = &artifactToolRegistration{ctx: artifactCtx, cancel: artifactCancel}
	if params.Name == "web_fetch" && tooltypes.ModelHelperFromContext(parent.ctx) != nil {
		helperCtx, helperCancel := context.WithCancel(parent.ctx)
		if r.modelHelpers == nil {
			r.modelHelpers = make(map[modelHelperKey]*modelHelperRegistration)
		}
		r.modelHelpers[childKey] = &modelHelperRegistration{ctx: helperCtx, cancel: helperCancel}
	}
	if forker, ok := tools.ToolContextFromContext(parent.ctx).MetadataStore.(llmtypes.ConversationForker); ok {
		if r.toolForkers == nil {
			r.toolForkers = make(map[toolForkKey]*toolForkRegistration)
		}
		r.toolForkers[toolForkKey(childKey)] = &toolForkRegistration{
			forker:   forker,
			toolName: params.Name,
			ctx:      artifactCtx,
		}
	}
	return struct{}{}, nil
}

func (r *Registry) clearCodeChildLocked(key modelHelperKey) {
	if registration := r.artifactTools[key]; registration != nil {
		registration.cancel()
		delete(r.artifactTools, key)
	}
	if registration := r.modelHelpers[key]; registration != nil {
		registration.cancel()
		delete(r.modelHelpers, key)
	}
	delete(r.toolForkers, toolForkKey(key))
}

func (r *Registry) clearCodeParentLocked(key modelHelperKey, parent *codeParentRegistration) {
	parent.cancel()
	for childID, active := range parent.children {
		if active {
			r.clearCodeChildLocked(modelHelperKey{key.runID, childID})
		}
	}
	delete(r.codeParents, key)
}

func (r *Registry) clearRunCodeParentsLocked(runID string) {
	if run := r.runs[runID]; run != nil {
		run.codeStopping = true
		run.codeChildIDs = nil
	}
	for key, parent := range r.codeParents {
		if key.runID == runID {
			r.clearCodeParentLocked(key, parent)
		}
	}
}
