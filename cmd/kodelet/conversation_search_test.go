package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

func TestConversationSearchOutputRendersMatches(t *testing.T) {
	updated := time.Date(2026, 9, 29, 18, 23, 12, 0, time.UTC)
	output := NewConversationSearchOutput(conversations.ListConversationsResponse{
		Conversations: []convtypes.ConversationSummary{
			{
				ID:           "named",
				CWD:          "/srv/project",
				Summary:      "Automatic summary",
				FirstMessage: "First message",
				Metadata:     conversations.SetConversationName(nil, "Search design"),
				UpdatedAt:    updated,
				Search: &convtypes.ConversationSearchResult{MatchCount: 4, Matches: []convtypes.SearchMatch{
					{EntryIndex: 2, Role: "user", Kind: convtypes.SearchEntryKindText, Snippet: "add a **search** tool"},
					{EntryIndex: 5, Role: "assistant", Kind: convtypes.SearchEntryKindToolUse, Snippet: "bash command: rg **search**"},
					{EntryIndex: 9, Role: "assistant", Kind: convtypes.SearchEntryKindCompaction, Snippet: "…the **search** index…"},
				}},
			},
			{
				ID:           "unnamed",
				FirstMessage: strings.Repeat("long ", 40),
				UpdatedAt:    updated,
				Search: &convtypes.ConversationSearchResult{MatchCount: 1, Matches: []convtypes.SearchMatch{
					{EntryIndex: -1, Kind: convtypes.SearchEntryKindTitle, Snippet: "**search** notes"},
				}},
			},
		},
		Total:         5,
		SearchPending: 1,
	})
	require.Len(t, output.Conversations, 2)
	assert.Equal(t, "Search design", output.Conversations[0].Title)
	assert.Len(t, []rune(output.Conversations[1].Title), conversationSearchTitleLimit)
	assert.True(t, strings.HasSuffix(output.Conversations[1].Title, "…"))

	var rendered bytes.Buffer
	require.NoError(t, output.Render(&rendered, TableFormat))
	assert.Equal(t, `named  2026-09-29T18:23:12Z  /srv/project
  Search design
  - user: add a **search** tool
  - tool input: bash command: rg **search**
  - compaction summary: …the **search** index…
  (1 more match)

unnamed  2026-09-29T18:23:12Z
  `+output.Conversations[1].Title+`
  - title: **search** notes

Showing 2 of 5 matching conversations.

The search index is still catching up (1 conversation pending); results may be incomplete.
`, rendered.String())

	rendered.Reset()
	require.NoError(t, NewConversationSearchOutput(conversations.ListConversationsResponse{}).Render(&rendered, JSONFormat))
	assert.JSONEq(t, `{"conversations": [], "total": 0}`, rendered.String())
}
