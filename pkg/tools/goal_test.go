package tools

import (
	"context"
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
