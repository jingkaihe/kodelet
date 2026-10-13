package agentenv

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// ReloadResourcesToolName is a host-owned, model-only control tool.
const ReloadResourcesToolName = "reload_resources"

// ResourcesReloadedDisplay labels a completed reload in user-facing transcripts.
const ResourcesReloadedDisplay = "Resources reloaded"

// ReloadOutcome reports a between-turn reload to the model and to the user.
type ReloadOutcome struct {
	// Reloaded is false when the previous environment was kept.
	Reloaded bool
	// Message is the model-facing notice.
	Message string
	// Display is the user-facing transcript marker text.
	Display string
}

// reloadResourcesDescription tells the model when a reload helps, not how it works.
const reloadResourcesDescription = `Reload this workspace's Kodelet resources so the rest of this run uses your changes: extensions and their tools, skills, recipes, AGENTS.md and other context files, and kodelet-config.yaml settings. Global Kodelet configuration and model settings are not reloaded.

Call it once after editing these resources, rebuilding compiled extensions first. Ordinary file changes don't need it.

The reload runs after the current tool batch; your next turn has the refreshed tools and starts with a message reporting the outcome. If refused, the old resources stay. Extensions that fail to start are listed; fix them and reload again.`

func reloadResourcesDefinition() ToolDefinition {
	wire := runnerpayload.ToolDefinition{
		Name:        ReloadResourcesToolName,
		Description: reloadResourcesDescription,
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
func (e *RemoteEnvironment) ReloadPendingResources(ctx context.Context) (outcome ReloadOutcome, err error) {
	e.mu.Lock()
	if !e.reloadPending {
		e.mu.Unlock()
		return ReloadOutcome{}, nil
	}
	if !e.opened || e.closing || e.reloading || e.reloadFailed {
		e.mu.Unlock()
		return ReloadOutcome{}, errors.New("remote environment cannot reload")
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
		return ReloadOutcome{}, err
	}
	result, err := e.controller.ReloadRun(ctx, params)
	if err != nil {
		return ReloadOutcome{}, errors.Wrap(err, "resource reload failed; the run cannot safely continue")
	}
	if err := ctx.Err(); err != nil {
		return ReloadOutcome{}, err
	}
	if result.Refusal != "" {
		refusal := "Resource reload refused: " + result.Refusal
		return ReloadOutcome{Message: refusal, Display: refusal}, nil
	}
	manifest, err := e.convertManifest(result.Manifest, config)
	if err != nil {
		return ReloadOutcome{}, errors.Wrap(err, "invalid reloaded environment")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ReloadOutcome{}, err
	}
	if !e.opened || e.closing || e.runID != params.RunID {
		return ReloadOutcome{}, errors.New("run stopped while reloading resources")
	}
	e.manifest = manifest
	e.wireManifest = result.Manifest
	outcome = ReloadOutcome{
		Reloaded: true,
		Message:  "Workspace resources reloaded. Use the refreshed tools, schemas and context for the rest of this run.",
		Display:  ResourcesReloadedDisplay,
	}
	if warning := strings.TrimSpace(result.Warning); warning != "" {
		outcome.Message = "Workspace resources reloaded, but some extensions failed to start and are unavailable:\n" + warning +
			"\nFix them and call reload_resources again to load them. Use the refreshed tools, schemas and context for the rest of this run."
		outcome.Display = "Resources reloaded; some extensions failed to start"
	}
	return outcome, nil
}
