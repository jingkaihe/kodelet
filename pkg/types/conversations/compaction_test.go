package conversations

import (
	"encoding/json"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactionHistoryRepeatedBoundaries(t *testing.T) {
	var history *CompactionHistory
	first := json.RawMessage(`[{"role":"user","content":"first"},{"role":"assistant","content":"answer"}]`)
	visible, err := history.VisibleMessages(first)
	require.NoError(t, err)
	assert.Equal(t, first, visible)
	history, err = history.Append(first, 2, llmtypes.CompactionMarker{ID: "one", Method: "api"})
	require.NoError(t, err)
	second := json.RawMessage(`[
		{"role":"user","content":"retained first"},
		{"type":"compaction","encrypted_content":"opaque-one"},
		{"role":"user","content":"second"},
		{"role":"assistant","content":"second answer"}
	]`)
	next, err := history.Append(second, 1, llmtypes.CompactionMarker{ID: "two", Method: "summary", Summary: "new summary"})
	require.NoError(t, err)
	require.Len(t, next.Segments, 2)
	assert.JSONEq(t, string(first), string(next.Segments[0].RawMessages))
	assert.JSONEq(t, `[{"role":"user","content":"second"},{"role":"assistant","content":"second answer"}]`, string(next.Segments[1].RawMessages))
	assert.Equal(t, 1, next.ActiveDisplayStart)
	assert.Empty(t, next.Segments[0].Marker.Summary)
	assert.Equal(t, "new summary", next.Segments[1].Marker.Summary)
	assert.Len(t, history.Segments, 1, "installing a replacement must not mutate an earlier snapshot")
	assert.Equal(t, 2, history.ActiveDisplayStart)

	visible, err = next.VisibleMessages(json.RawMessage(`[{"role":"user","content":"new summary"},{"role":"user","content":"third"}]`))
	require.NoError(t, err)
	assert.JSONEq(t, `[{"role":"user","content":"third"}]`, string(visible))
	next.Segments[0].RawMessages[0] = ' '
	assert.Equal(t, byte('['), history.Segments[0].RawMessages[0])
}

func TestCompactionHistoryInvalidBoundary(t *testing.T) {
	for _, start := range []int{-1, 2} {
		history := &CompactionHistory{ActiveDisplayStart: start}
		require.Error(t, history.Validate(json.RawMessage(`[{}]`)))
		_, err := history.Append(json.RawMessage(`[{}]`), 1, llmtypes.CompactionMarker{})
		require.Error(t, err)
		assert.Empty(t, history.Segments)
	}
	history := &CompactionHistory{ActiveDisplayStart: 1}
	visible, err := history.VisibleMessages(json.RawMessage(`[{}]`))
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(visible))
	require.Error(t, history.Validate(json.RawMessage(`invalid`)))
	_, err = history.Append(json.RawMessage(`[{}]`), 0, llmtypes.CompactionMarker{})
	require.Error(t, err)
}

func TestForkCompactionHistoryIsIsolated(t *testing.T) {
	source := NewConversationRecord("source")
	var err error
	source.CompactionHistory, err = source.CompactionHistory.Append(json.RawMessage(`[{}]`), 1, llmtypes.CompactionMarker{ID: "one"})
	require.NoError(t, err)
	source.RawMessages = json.RawMessage(`[{}]`)
	fork := ForkConversationRecord(source)
	require.Equal(t, source.CompactionHistory, fork.CompactionHistory)
	fork.CompactionHistory.ActiveDisplayStart++
	fork.CompactionHistory.Segments[0].RawMessages[0] = ' '
	assert.Equal(t, 1, source.CompactionHistory.ActiveDisplayStart)
	assert.Equal(t, byte('['), source.CompactionHistory.Segments[0].RawMessages[0])
}

func TestCompactedConversationSummaryUsesDisplayHistory(t *testing.T) {
	for _, original := range []json.RawMessage{
		json.RawMessage(`[{"role":"user","content":"original question"},{"role":"assistant","content":"answer"}]`),
		json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"original question"}]},{"role":"assistant","content":[{"type":"text","text":"answer"}]}]`),
	} {
		record := NewConversationRecord("compact")
		var err error
		record.CompactionHistory, err = record.CompactionHistory.Append(original, 1, llmtypes.CompactionMarker{ID: "one"})
		require.NoError(t, err)
		record.RawMessages = json.RawMessage(`[{"role":"user","content":"replacement"},{"role":"user","content":"follow-up"}]`)
		summary := record.ToSummary()
		assert.Equal(t, "original question", summary.FirstMessage)
		assert.Equal(t, 3, summary.MessageCount)
	}
}
