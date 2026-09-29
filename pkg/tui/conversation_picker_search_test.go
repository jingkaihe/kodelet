package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

type searchingConversationSource struct {
	conversationSourceRunner
	results map[string][]convtypes.ConversationSummary
	err     error
	queries []string
	limits  []int
}

func (s *searchingConversationSource) SearchConversations(_ context.Context, query string, limit int) ([]convtypes.ConversationSummary, error) {
	s.queries = append(s.queries, query)
	s.limits = append(s.limits, limit)
	return s.results[query], s.err
}

func searchResult(id, title string, updatedAt time.Time, snippet string) convtypes.ConversationSummary {
	return convtypes.ConversationSummary{
		ID: id, FirstMessage: title, UpdatedAt: updatedAt,
		Search: &convtypes.ConversationSearchResult{MatchCount: 1, Matches: []convtypes.SearchMatch{{Kind: "text", Role: "user", Snippet: snippet}}},
	}
}

func newSearchPickerModel(t *testing.T, source *searchingConversationSource) model {
	t.Helper()
	m := newModel(context.Background(), Config{Remote: true, Runner: source})
	t.Cleanup(m.cancel)
	m.width = 110
	m.height = 24
	m.resize()
	m.openConversationPicker("")
	m.applyConversationList(conversationListMsg{requestID: m.conversationPicker.requestID, summaries: source.summaries})
	return m
}

func typeConversationPickerQuery(t *testing.T, m model, text string) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, r := range text {
		var updated tea.Model
		updated, cmd = m.Update(textKeyPress(string(r)))
		m = updated.(model)
	}
	return m, cmd
}

// runConversationSearch delivers the debounce tick without waiting for it.
func runConversationSearch(t *testing.T, m model) model {
	t.Helper()
	updated, cmd := m.Update(conversationSearchDueMsg{requestID: m.conversationPicker.searchRequestID})
	m = updated.(model)
	require.NotNil(t, cmd)
	msg, ok := cmd().(conversationSearchMsg)
	require.True(t, ok)
	updated, _ = m.Update(msg)
	return updated.(model)
}

func TestConversationPickerSearchesDaemonHistory(t *testing.T) {
	now := time.Now()
	source := &searchingConversationSource{
		conversationSourceRunner: conversationSourceRunner{summaries: []convtypes.ConversationSummary{
			{ID: "recent-release", FirstMessage: "Release checklist", UpdatedAt: now},
			{ID: "recent-prerelease", FirstMessage: "Prerelease cleanup", UpdatedAt: now.Add(-time.Minute)},
			{ID: "recent-other", FirstMessage: "Unrelated work", UpdatedAt: now.Add(-2 * time.Minute)},
		}},
		results: map[string][]convtypes.ConversationSummary{"release": {
			searchResult("recent-release", "Release checklist", now, "**Release** checklist"),
			searchResult("archived", "Old notes", now.Add(-90*24*time.Hour), "draft the **release**\nnotes"),
		}},
	}
	m := newSearchPickerModel(t, source)

	m, cmd := typeConversationPickerQuery(t, m, "release")
	require.NotNil(t, cmd, "typing schedules a debounced search")
	assert.True(t, m.conversationPicker.searching)
	assert.Equal(t, []string{"recent-release", "recent-prerelease"}, itemConversationIDs(m.filteredConversationPickerItems()),
		"loaded conversations are filtered locally while the search runs")
	assert.Contains(t, xansi.Strip(m.renderConversationPicker()), "Searching saved conversations")
	assert.Empty(t, source.queries, "nothing is searched before typing pauses")
	due, ok := cmd().(conversationSearchDueMsg)
	require.True(t, ok, "the command is the debounce tick")
	assert.Equal(t, m.conversationPicker.searchRequestID, due.requestID)

	m = runConversationSearch(t, m)
	assert.Equal(t, []string{"release"}, source.queries)
	assert.Equal(t, []int{conversationPickerLimit}, source.limits)
	assert.False(t, m.conversationPicker.searching)
	items := m.filteredConversationPickerItems()
	assert.Equal(t, []string{"recent-release", "archived"}, itemConversationIDs(items),
		"daemon results replace the local filter, including older conversations")
	selected := m.conversationPickerSelectedIndex(items)
	assert.Equal(t, "recent-release", items[selected].id, "the selection survives when it still matches")

	rendered := xansi.Strip(m.renderConversationPicker())
	assert.NotContains(t, rendered, "Searching")
	assert.Contains(t, rendered, "Old notes")
	assert.Contains(t, rendered, "Match: Release checklist")

	updated, _ := m.Update(keyPress(tea.KeyDown))
	m = updated.(model)
	assert.Contains(t, xansi.Strip(m.renderConversationPicker()), "Match: draft the release notes")

	// Long matches keep their label and are clipped at the end.
	m.conversationPicker.search.summaries[1].Search.Matches[0].Snippet = "draft the **release** notes " + strings.Repeat("tail ", 60)
	rendered = xansi.Strip(m.renderConversationPicker())
	assert.Contains(t, rendered, "│ Match: draft the release notes tail")
	assert.Contains(t, rendered, "… │\n│ Enter open")

	// Clearing the query returns to recent conversations without searching.
	updated, cmd = m.Update(keyPressWithMod('u', tea.ModCtrl))
	m = updated.(model)
	assert.Nil(t, cmd)
	assert.False(t, m.conversationPicker.searching)
	assert.Equal(t, []string{"recent-release", "recent-prerelease", "recent-other"}, itemConversationIDs(m.filteredConversationPickerItems()))
	assert.Len(t, source.queries, 1)
}

func matchingConversationPickerKeys(items []conversationPickerItem) []string {
	keys := []string{}
	for _, item := range items {
		if item.matchesQuery && !item.isNew {
			keys = append(keys, item.key)
		}
	}
	return keys
}

func TestConversationPickerSearchKeepsUnsavedConversations(t *testing.T) {
	now := time.Now()
	source := &searchingConversationSource{
		conversationSourceRunner: conversationSourceRunner{summaries: []convtypes.ConversationSummary{
			{ID: "saved", FirstMessage: "Unrelated work", UpdatedAt: now},
		}},
		results: map[string][]convtypes.ConversationSummary{"release": {
			searchResult("archived", "Old notes", now.Add(-time.Hour), "**release** notes"),
		}},
	}
	m := newSearchPickerModel(t, source)
	// A parked, never-submitted conversation exists only in the TUI.
	m.conversations["new:9"] = &conversationState{key: "new:9", draft: "release notes draft", updatedAt: now}

	m, _ = typeConversationPickerQuery(t, m, "release")
	assert.Equal(t, []string{"new:9"}, matchingConversationPickerKeys(m.filteredConversationPickerItems()))

	m = runConversationSearch(t, m)
	assert.ElementsMatch(t, []string{"new:9", "archived"}, matchingConversationPickerKeys(m.filteredConversationPickerItems()),
		"daemon results do not hide unsaved local matches")
}

func TestConversationPickerSearchIgnoresStaleResults(t *testing.T) {
	now := time.Now()
	source := &searchingConversationSource{
		conversationSourceRunner: conversationSourceRunner{summaries: []convtypes.ConversationSummary{
			{ID: "recent", FirstMessage: "Release checklist", UpdatedAt: now},
		}},
		results: map[string][]convtypes.ConversationSummary{
			"rel":     {searchResult("stale", "Stale", now, "**rel**")},
			"release": {searchResult("recent", "Release checklist", now, "**Release** checklist")},
		},
	}
	m := newSearchPickerModel(t, source)

	m, _ = typeConversationPickerQuery(t, m, "rel")
	first := m.conversationPicker.searchRequestID
	m, _ = typeConversationPickerQuery(t, m, "ease")
	require.Greater(t, m.conversationPicker.searchRequestID, first)

	updated, cmd := m.Update(conversationSearchDueMsg{requestID: first})
	m = updated.(model)
	assert.Nil(t, cmd, "superseded ticks do not search")

	// Reopening the picker never reuses a request ID from an earlier opening.
	current := m.conversationPicker.searchRequestID
	m.conversationPicker = nil
	m.openConversationPicker("release")
	assert.Greater(t, m.conversationPicker.searchRequestID, current)
	updated, _ = m.Update(conversationSearchMsg{requestID: current, query: "release", summaries: source.results["rel"]})
	m = updated.(model)
	assert.Nil(t, m.conversationPicker.search, "results from an earlier opening are dropped")
	m.applyConversationList(conversationListMsg{requestID: m.conversationPicker.requestID, summaries: source.summaries})
	updated, _ = m.Update(conversationSearchMsg{requestID: first, query: "rel", summaries: source.results["rel"]})
	m = updated.(model)
	assert.Nil(t, m.conversationPicker.search, "superseded results are dropped")
	assert.True(t, m.conversationPicker.searching)

	m = runConversationSearch(t, m)
	assert.Equal(t, []string{"release"}, source.queries)
	assert.Equal(t, []string{"recent"}, itemConversationIDs(m.filteredConversationPickerItems()))
}

func TestConversationPickerSearchFailureKeepsLocalFilter(t *testing.T) {
	source := &searchingConversationSource{
		conversationSourceRunner: conversationSourceRunner{summaries: []convtypes.ConversationSummary{
			{ID: "recent", FirstMessage: "Release checklist", UpdatedAt: time.Now()},
			{ID: "other", FirstMessage: "Other", UpdatedAt: time.Now()},
		}},
		err: errors.New("daemon unavailable"),
	}
	m := newSearchPickerModel(t, source)

	m, _ = typeConversationPickerQuery(t, m, "release")
	m = runConversationSearch(t, m)

	assert.False(t, m.conversationPicker.searching)
	assert.Equal(t, []string{"recent"}, itemConversationIDs(m.filteredConversationPickerItems()))
	assert.Contains(t, xansi.Strip(m.renderConversationPicker()), "failed to search conversations: daemon unavailable")
}

func TestConversationPickerWithoutDaemonSearchFiltersLocally(t *testing.T) {
	runner := &conversationSourceRunner{summaries: []convtypes.ConversationSummary{
		{ID: "recent", FirstMessage: "Release checklist", UpdatedAt: time.Now()},
		{ID: "other", FirstMessage: "Other", UpdatedAt: time.Now()},
	}}
	m := newModel(context.Background(), Config{Remote: true, Runner: runner})
	t.Cleanup(m.cancel)
	m.openConversationPicker("")
	m.applyConversationList(conversationListMsg{requestID: m.conversationPicker.requestID, summaries: runner.summaries})

	m, cmd := typeConversationPickerQuery(t, m, "release")
	assert.Nil(t, cmd)
	assert.False(t, m.conversationPicker.searching)
	assert.Equal(t, []string{"recent"}, itemConversationIDs(m.filteredConversationPickerItems()))
}

func TestSessionsCommandSearchesInitialQuery(t *testing.T) {
	source := &searchingConversationSource{results: map[string][]convtypes.ConversationSummary{
		"deploy": {searchResult("match", "Deploy notes", time.Now(), "**deploy** notes")},
	}}
	m := newModel(context.Background(), Config{Remote: true, Runner: source})
	t.Cleanup(m.cancel)

	require.NotNil(t, m.openConversationPicker(" deploy "))
	assert.True(t, m.conversationPicker.searching)
	m = runConversationSearch(t, m)
	assert.Equal(t, []string{"deploy"}, source.queries)
	assert.Equal(t, []string{"match"}, itemConversationIDs(m.filteredConversationPickerItems()))
}
