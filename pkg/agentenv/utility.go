package agentenv

import (
	"context"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// UtilityEnvironment is the explicit workspace-free boundary for internal model
// utilities with complete in-memory inputs. It performs no discovery, opens no
// runner lease, and cannot execute tools or extension lifecycle callbacks.
type UtilityEnvironment struct {
	open bool
}

func (e *UtilityEnvironment) Open(ctx context.Context, _ RunSpec) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	e.open = true
	return e.Manifest(), nil
}

func (e *UtilityEnvironment) IsOpen() bool { return e.open }

func (*UtilityEnvironment) Manifest() Manifest { return Manifest{} }

func (*UtilityEnvironment) ExecuteCommand(context.Context, CommandRequest) (CommandResult, error) {
	return CommandResult{}, errors.New("internal model utilities cannot execute commands")
}

func (*UtilityEnvironment) ProcessUserMessage(ctx context.Context, message string) (string, error) {
	return message, ctx.Err()
}

func (*UtilityEnvironment) DispatchAgentStart(ctx context.Context) error { return ctx.Err() }

func (*UtilityEnvironment) DispatchTurnStart(ctx context.Context, _ int) error { return ctx.Err() }

func (*UtilityEnvironment) ProcessAgentInit(ctx context.Context, prompt string, _ []string) (AgentInitDecision, error) {
	return AgentInitDecision{SystemPrompt: prompt, AllowedTools: []string{}, ToolsModified: true}, ctx.Err()
}

func (*UtilityEnvironment) DispatchTurnEnd(ctx context.Context, _ string, _ int) error {
	return ctx.Err()
}

func (*UtilityEnvironment) DispatchAgentEnd(ctx context.Context, _ []llmtypes.Message) ([]string, error) {
	return []string{}, ctx.Err()
}

func (*UtilityEnvironment) DispatchToolCall(context.Context, ToolRequest) (ToolCallDecision, error) {
	return ToolCallDecision{Blocked: true, Reason: "internal model utilities cannot execute tools"}, nil
}

func (*UtilityEnvironment) DispatchToolUpdate(context.Context, ToolOutputRequest) (ToolOutputDecision, error) {
	return ToolOutputDecision{}, errors.New("internal model utilities cannot execute tools")
}

func (*UtilityEnvironment) DispatchToolResult(context.Context, ToolOutputRequest) (ToolOutputDecision, error) {
	return ToolOutputDecision{}, errors.New("internal model utilities cannot execute tools")
}

func (*UtilityEnvironment) CanStreamToolUpdates() bool { return false }

func (*UtilityEnvironment) ExecuteTool(context.Context, ToolRequest, ToolUpdateSink) (ToolExecution, error) {
	return ToolExecution{}, errors.New("internal model utilities cannot execute tools")
}

func (e *UtilityEnvironment) Close(context.Context) error {
	e.open = false
	return nil
}

var _ Environment = (*UtilityEnvironment)(nil)
