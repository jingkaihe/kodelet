package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/goals"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testMetadataStore struct {
	metadata map[string]any
}

func (s *testMetadataStore) GetMetadata() map[string]any {
	copy := make(map[string]any, len(s.metadata))
	for key, value := range s.metadata {
		copy[key] = value
	}
	return copy
}

func (s *testMetadataStore) SetMetadataValue(key string, value any) {
	if s.metadata == nil {
		s.metadata = map[string]any{}
	}
	s.metadata[key] = value
}

func TestGetGoalToolReturnsCurrentGoal(t *testing.T) {
	store := &testMetadataStore{metadata: map[string]any{goals.MetadataKey: goals.New("ship goal support", time.Now())}}
	ctx := ContextWithToolContext(context.Background(), ToolContext{MetadataStore: store})

	result := NewGetGoalTool().Execute(ctx, NewBasicState(context.Background()), `{}`)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "ship goal support")
	structured := result.StructuredData()
	metadata, ok := structured.Metadata.(tooltypes.GetGoalMetadata)
	require.True(t, ok)
	assert.True(t, metadata.Active)
	assert.Equal(t, "active", metadata.Status)
}

func TestGetGoalToolSchemaDescriptionAndEmptyStates(t *testing.T) {
	tool := NewGetGoalTool()
	assert.Equal(t, "get_goal", tool.Name())
	assert.Contains(t, tool.Description(), "current goal")
	require.NotNil(t, tool.GenerateSchema())
	assert.NoError(t, tool.ValidateInput(nil, ""))
	assert.Error(t, tool.ValidateInput(nil, `{`))
	kvs, err := tool.TracingKVs(`{}`)
	assert.NoError(t, err)
	assert.Nil(t, kvs)

	missingStore := tool.Execute(context.Background(), NewBasicState(context.Background()), `{}`)
	require.True(t, missingStore.IsError())
	assert.Contains(t, missingStore.GetError(), "goal metadata is unavailable")

	store := &testMetadataStore{}
	ctx := ContextWithToolContext(context.Background(), ToolContext{MetadataStore: store})
	result := tool.Execute(ctx, NewBasicState(context.Background()), `{}`)
	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "No goal")
	assert.Contains(t, result.AssistantFacing(), "No goal")

	metadata, ok := result.StructuredData().Metadata.(tooltypes.GetGoalMetadata)
	require.True(t, ok)
	assert.False(t, metadata.Active)
	assert.Empty(t, metadata.Objective)
}

func TestUpdateGoalToolMarksGoalComplete(t *testing.T) {
	store := &testMetadataStore{metadata: map[string]any{goals.MetadataKey: goals.New("ship goal support", time.Now())}}
	ctx := ContextWithToolContext(context.Background(), ToolContext{MetadataStore: store})

	result := NewUpdateGoalTool().Execute(ctx, NewBasicState(context.Background()), `{"status":"complete","reason":"tests pass"}`)

	require.False(t, result.IsError())
	goal, ok := goals.FromMetadata(store.GetMetadata())
	require.True(t, ok)
	assert.Equal(t, goals.StatusComplete, goal.Status)
	assert.Equal(t, "tests pass", goal.Reason)

	structured := result.StructuredData()
	metadata, ok := structured.Metadata.(tooltypes.UpdateGoalMetadata)
	require.True(t, ok)
	assert.Equal(t, "complete", metadata.Status)
}

func TestUpdateGoalToolRequiresTerminalStatus(t *testing.T) {
	result := NewUpdateGoalTool().Execute(context.Background(), NewBasicState(context.Background()), `{"status":"invalid"}`)

	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "complete")
}

func TestUpdateGoalToolSchemaValidationAndTracing(t *testing.T) {
	tool := NewUpdateGoalTool()
	assert.Equal(t, "update_goal", tool.Name())
	assert.Contains(t, tool.Description(), "blocked threshold")
	require.NotNil(t, tool.GenerateSchema())
	assert.NoError(t, tool.ValidateInput(nil, `{"status":"paused"}`))
	assert.Error(t, tool.ValidateInput(nil, `{`))
	assert.Error(t, tool.ValidateInput(nil, `{"status":"invalid"}`))

	kvs, err := tool.TracingKVs(`{"status":"complete"}`)
	require.NoError(t, err)
	require.Len(t, kvs, 1)
	assert.Equal(t, "status", string(kvs[0].Key))
	assert.Equal(t, "complete", kvs[0].Value.AsString())
	_, err = tool.TracingKVs(`{`)
	assert.Error(t, err)
}

func TestUpdateGoalToolErrorPaths(t *testing.T) {
	invalidJSON := NewUpdateGoalTool().Execute(context.Background(), NewBasicState(context.Background()), `{`)
	require.True(t, invalidJSON.IsError())
	assert.NotEmpty(t, invalidJSON.GetError())

	missingStore := NewUpdateGoalTool().Execute(context.Background(), NewBasicState(context.Background()), `{"status":"complete"}`)
	require.True(t, missingStore.IsError())
	assert.Contains(t, missingStore.GetError(), "goal metadata is unavailable")

	store := &testMetadataStore{}
	ctx := ContextWithToolContext(context.Background(), ToolContext{MetadataStore: store})
	noGoal := NewUpdateGoalTool().Execute(ctx, NewBasicState(context.Background()), `{"status":"complete"}`)
	require.True(t, noGoal.IsError())
	assert.Contains(t, noGoal.GetError(), "no goal")
}

func TestUpdateGoalToolPausesAndResumesGoal(t *testing.T) {
	store := &testMetadataStore{metadata: map[string]any{goals.MetadataKey: goals.New("ship goal support", time.Now())}}
	ctx := ContextWithToolContext(context.Background(), ToolContext{MetadataStore: store})

	paused := NewUpdateGoalTool().Execute(ctx, NewBasicState(context.Background()), `{"status":"paused","reason":"user asked to pause"}`)
	require.False(t, paused.IsError())
	goal, ok := goals.FromMetadata(store.GetMetadata())
	require.True(t, ok)
	assert.Equal(t, goals.StatusPaused, goal.Status)

	resumed := NewUpdateGoalTool().Execute(ctx, NewBasicState(context.Background()), `{"status":"active","reason":"user asked to resume"}`)
	require.False(t, resumed.IsError())
	goal, ok = goals.FromMetadata(store.GetMetadata())
	require.True(t, ok)
	assert.Equal(t, goals.StatusActive, goal.Status)
}

func TestGoalToolResultStructuredDataAndJSON(t *testing.T) {
	goal := goals.New("ship", time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC))
	result := &GoalToolResult{toolName: "update_goal", goal: &goal, content: "Goal marked active."}

	structured := result.StructuredData()
	assert.Equal(t, "update_goal", structured.ToolName)
	assert.True(t, structured.Success)
	meta, ok := structured.Metadata.(tooltypes.UpdateGoalMetadata)
	require.True(t, ok)
	assert.Equal(t, "ship", meta.Objective)

	data, err := json.Marshal(structured)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"metadataType":"update_goal"`)
}
