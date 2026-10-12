package agentenv

import (
	"context"
	"encoding/json"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// ReloadResourcesToolName is a host-owned, model-only control tool.
const ReloadResourcesToolName = "reload_resources"

func reloadResourcesDefinition() ToolDefinition {
	wire := runnerpayload.ToolDefinition{
		Name:        ReloadResourcesToolName,
		Description: "Request a reload of this conversation's workspace resources after the current tool batch finishes. Reloads extensions, tools and their schemas, skills, recipes, context files and environment settings before the next model request, without ending the run. Returns an acknowledgement, not completion; a subsequent message reports the outcome. Retained background workers and SDK-attached extensions prevent reload. Cannot be called from code_execute.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		ModelOnly:   true,
	}
	return ToolDefinition{
		Name: wire.Name, Description: wire.Description, InputSchema: wire.InputSchema,
		ModelOnly: true, Placement: ToolPlacementControlPlane, Tool: newRemoteToolProxy(wire),
	}
}

// requestReload completes the old environment's tool hooks before queuing a
// host-owned request. It must never wait for reload from inside a tool batch.
func (e *RemoteEnvironment) requestReload(ctx context.Context, request ToolRequest) (ToolExecution, error) {
	definition, allowed := e.Manifest().ToolDefinition(ReloadResourcesToolName)
	result := tooltypes.BaseToolResult{}
	if !allowed || definition.Placement != ToolPlacementControlPlane {
		result.Error = "resource reload is not available or permitted for this run"
		structured := result.StructuredData()
		structured.ToolName = ReloadResourcesToolName
		return ToolExecution{Input: request.Input, Result: result, StructuredResult: structured}, nil
	}
	decision, err := e.DispatchToolCall(ctx, request)
	if err != nil {
		return ToolExecution{}, err
	}
	request.Input = decision.Input
	var input map[string]json.RawMessage
	switch {
	case decision.Blocked:
		result.Error = decision.Reason
		if result.Error == "" {
			result.Error = "resource reload blocked by extension policy"
		}
	case json.Unmarshal([]byte(request.Input), &input) != nil || input == nil || len(input) != 0:
		result.Error = "reload_resources expects an empty object"
	default:
		result.Result = "Reload requested. The current tool batch will finish before resources are replaced; the next message will report the outcome."
	}
	structured := result.StructuredData()
	structured.ToolName = ReloadResourcesToolName
	output, err := e.DispatchToolResult(ctx, ToolOutputRequest{
		Name: request.Name, Input: request.Input, ToolCallID: request.ToolCallID, StructuredResult: structured,
	})
	if err != nil {
		return ToolExecution{}, err
	}
	if !result.IsError() {
		e.mu.Lock()
		if err := ctx.Err(); err != nil {
			e.mu.Unlock()
			return ToolExecution{}, err
		}
		if !e.opened || e.closing || e.reloading || e.reloadFailed {
			e.mu.Unlock()
			return ToolExecution{}, errors.New("remote environment cannot queue a reload")
		}
		e.reloadPending = true
		e.mu.Unlock()
	}
	return ToolExecution{
		Input: request.Input, Result: result, StructuredResult: output.StructuredResult, Modified: output.Modified,
	}, nil
}

// HasPendingReload reports a request awaiting the next between-turn checkpoint.
func (e *RemoteEnvironment) HasPendingReload() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.opened && !e.closing && e.reloadPending
}

// ReloadPendingResources replaces only this run's snapshot. A refusal leaves
// the old environment usable; every uncertain/destructive failure is fatal.
// The provider calls this only after all tool results and turn.end hooks finish.
func (e *RemoteEnvironment) ReloadPendingResources(ctx context.Context) (reloaded bool, message string, err error) {
	e.mu.Lock()
	if !e.reloadPending {
		e.mu.Unlock()
		return false, "", nil
	}
	if !e.opened || e.closing || e.reloading || e.reloadFailed {
		e.mu.Unlock()
		return false, "", errors.New("remote environment cannot reload")
	}
	e.reloadPending = false
	e.reloading = true
	params := protocol.RunReloadParams{RunID: e.runID, ExpectedEnvironmentRevision: e.wireManifest.EnvironmentRevision}
	config := e.hostConfig.Clone()
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.reloading = false
		e.reloadFailed = err != nil
		e.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	result, err := e.controller.ReloadRun(ctx, params)
	if err != nil {
		return false, "", errors.Wrap(err, "resource reload failed; the run cannot safely continue")
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	if result.Refusal != "" {
		return false, "Resource reload refused: " + result.Refusal, nil
	}
	manifest, err := e.convertManifest(result.Manifest, config)
	if err != nil {
		return false, "", errors.Wrap(err, "invalid reloaded environment")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	if !e.opened || e.closing || e.runID != params.RunID {
		return false, "", errors.New("run stopped while reloading resources")
	}
	e.manifest = manifest
	e.wireManifest = result.Manifest
	return true, "Workspace resources reloaded. Use the refreshed tools, schemas and context for the rest of this run.", nil
}
