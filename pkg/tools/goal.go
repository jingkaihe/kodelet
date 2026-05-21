package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/goals"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

// GetGoalTool returns the current thread goal.
type GetGoalTool struct{}

// UpdateGoalTool marks the current thread goal complete or blocked.
type UpdateGoalTool struct{}

// GetGoalInput reuses the shared get_goal input schema while preserving pkg/tools schema IDs.
type GetGoalInput tooltypes.GetGoalInput

// UpdateGoalInput reuses the shared update_goal input schema while preserving pkg/tools schema IDs.
type UpdateGoalInput tooltypes.UpdateGoalInput

// GoalToolResult represents a get_goal or update_goal result.
type GoalToolResult struct {
	toolName string
	goal     *goals.Goal
	content  string
	err      string
}

func NewGetGoalTool() *GetGoalTool {
	return &GetGoalTool{}
}

func NewUpdateGoalTool() *UpdateGoalTool {
	return &UpdateGoalTool{}
}

func (t *GetGoalTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[GetGoalInput]()
}

func (t *GetGoalTool) Name() string {
	return "get_goal"
}

func (t *GetGoalTool) Description() string {
	return "Get the current goal for this thread, including status and objective."
}

func (t *GetGoalTool) ValidateInput(_ tooltypes.State, parameters string) error {
	if strings.TrimSpace(parameters) == "" {
		return nil
	}
	input := &GetGoalInput{}
	return json.Unmarshal([]byte(parameters), input)
}

func (t *GetGoalTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	if err := t.ValidateInput(nil, parameters); err != nil {
		return &GoalToolResult{toolName: t.Name(), err: err.Error()}
	}

	store := toolContextFromContext(ctx).MetadataStore
	if store == nil {
		return &GoalToolResult{toolName: t.Name(), err: "goal metadata is unavailable"}
	}

	goal, ok := goals.FromMetadata(store.GetMetadata())
	if !ok {
		return &GoalToolResult{
			toolName: t.Name(),
			content:  "No goal is currently defined for this thread.",
		}
	}

	content := formatGoal(goal)
	return &GoalToolResult{toolName: t.Name(), goal: &goal, content: content}
}

func (t *GetGoalTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	return nil, nil
}

func (t *UpdateGoalTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[UpdateGoalInput]()
}

func (t *UpdateGoalTool) Name() string {
	return "update_goal"
}

func (t *UpdateGoalTool) Description() string {
	return `Update the existing goal.
Use this tool to mark the goal achieved or genuinely blocked, or when the user explicitly asks to pause, resume, or clear the goal.
Set status to "active" only to resume a paused or blocked goal after the user asks to resume it.
Set status to "paused" only when the user asks to pause or stop automatic goal continuation.
Set status to "complete" only when the objective has actually been achieved and no required work remains.
Set status to "blocked" only when the same blocking condition has repeated for at least three consecutive goal turns, counting the original/user-triggered turn and any automatic continuations, and the agent cannot make meaningful progress without user input or an external-state change.
Set status to "cleared" only when the user asks to clear or remove the current goal.
If the user resumes a goal that was previously marked "blocked", treat the resumed run as a fresh blocked audit. If the same blocking condition then repeats for at least three consecutive resumed goal turns, set status to "blocked" again.
Once the blocked threshold is satisfied, do not keep reporting that you are still blocked while leaving the goal active; set status to "blocked".
Do not use "blocked" merely because the work is hard, slow, uncertain, incomplete, or would benefit from clarification.
Do not mark a goal complete merely because you are stopping work.`
}

func (t *UpdateGoalTool) ValidateInput(_ tooltypes.State, parameters string) error {
	input := &UpdateGoalInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return err
	}

	status := goals.Status(strings.TrimSpace(input.Status))
	if !goals.IsUpdateStatus(status) {
		return errors.New(`status must be one of "active", "paused", "complete", "blocked", or "cleared"`)
	}
	return nil
}

func (t *UpdateGoalTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	input := &UpdateGoalInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return &GoalToolResult{toolName: t.Name(), err: err.Error()}
	}

	status := goals.Status(strings.TrimSpace(input.Status))
	if !goals.IsUpdateStatus(status) {
		return &GoalToolResult{toolName: t.Name(), err: `status must be one of "active", "paused", "complete", "blocked", or "cleared"`}
	}

	store := toolContextFromContext(ctx).MetadataStore
	if store == nil {
		return &GoalToolResult{toolName: t.Name(), err: "goal metadata is unavailable"}
	}

	goal, metadata, err := goals.UpdateStatus(store.GetMetadata(), status, input.Reason, time.Now())
	if err != nil {
		return &GoalToolResult{toolName: t.Name(), err: err.Error()}
	}
	for key, value := range metadata {
		store.SetMetadataValue(key, value)
	}

	content := fmt.Sprintf("Goal marked %s.", goal.Status)
	if goal.Reason != "" {
		content += "\nReason: " + goal.Reason
	}
	return &GoalToolResult{toolName: t.Name(), goal: &goal, content: content}
}

func (t *UpdateGoalTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &UpdateGoalInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return nil, err
	}
	return []attribute.KeyValue{
		attribute.String("status", strings.TrimSpace(input.Status)),
	}, nil
}

func (r *GoalToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.content, r.err)
}

func (r *GoalToolResult) IsError() bool {
	return r.err != ""
}

func (r *GoalToolResult) GetError() string {
	return r.err
}

func (r *GoalToolResult) GetResult() string {
	return r.content
}

func (r *GoalToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  r.toolName,
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}
	if r.IsError() {
		result.Error = r.err
	}

	if r.toolName == "get_goal" {
		result.Metadata = getGoalMetadata(r.goal)
	} else {
		result.Metadata = updateGoalMetadata(r.goal)
	}
	return result
}

func formatGoal(goal goals.Goal) string {
	lines := []string{
		"Current goal:",
		"Objective: " + goal.Objective,
		"Status: " + string(goal.Status),
	}
	if goal.Reason != "" {
		lines = append(lines, "Reason: "+goal.Reason)
	}
	return strings.Join(lines, "\n")
}

func getGoalMetadata(goal *goals.Goal) tooltypes.GetGoalMetadata {
	if goal == nil {
		return tooltypes.GetGoalMetadata{Active: false}
	}
	return tooltypes.GetGoalMetadata{
		Objective: goal.Objective,
		Status:    string(goal.Status),
		Reason:    goal.Reason,
		Active:    goal.Status == goals.StatusActive,
		CreatedAt: goal.CreatedAt,
		UpdatedAt: goal.UpdatedAt,
	}
}

func updateGoalMetadata(goal *goals.Goal) tooltypes.UpdateGoalMetadata {
	if goal == nil {
		return tooltypes.UpdateGoalMetadata{}
	}
	return tooltypes.UpdateGoalMetadata{
		Objective: goal.Objective,
		Status:    string(goal.Status),
		Reason:    goal.Reason,
		CreatedAt: goal.CreatedAt,
		UpdatedAt: goal.UpdatedAt,
	}
}
