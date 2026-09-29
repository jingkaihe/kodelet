package sqlite

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conversations "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

func newSearchTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "search.db")
	setupTestDB(t, dbPath)
	store, err := NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func saveSearchConversation(t *testing.T, store *Store, id, cwd string) {
	t.Helper()
	record := conversations.NewConversationRecord(id)
	record.CWD = cwd
	record.Provider = "anthropic"
	record.RawMessages = json.RawMessage(`[]`)
	require.NoError(t, store.Save(t.Context(), record))
}

func searchDocument(t *testing.T, store *Store, id string, entries ...conversations.SearchEntry) conversations.SearchDocument {
	t.Helper()
	source, err := store.LoadSearchSource(t.Context(), id)
	require.NoError(t, err)
	return conversations.SearchDocument{
		ConversationID:  id,
		SourceUpdatedAt: source.UpdatedAt,
		Version:         1,
		ContentHash:     fmt.Sprint(entries),
		Entries:         entries,
	}
}

func indexSearchConversation(t *testing.T, store *Store, id string, entries ...conversations.SearchEntry) {
	t.Helper()
	stored, err := store.ReplaceSearchIndex(t.Context(), searchDocument(t, store, id, entries...))
	require.NoError(t, err)
	require.True(t, stored)
}

func titleEntry(payload string) conversations.SearchEntry {
	return conversations.SearchEntry{EntryIndex: -1, Kind: conversations.SearchEntryKindTitle, Payload: payload}
}

func textEntry(index int, role, payload string) conversations.SearchEntry {
	return conversations.SearchEntry{EntryIndex: index, Role: role, Kind: conversations.SearchEntryKindText, Payload: payload}
}

func toolEntry(index int, payload string) conversations.SearchEntry {
	return conversations.SearchEntry{EntryIndex: index, Role: "assistant", Kind: conversations.SearchEntryKindToolUse, Payload: payload}
}

func searchIDs(t *testing.T, store *Store, options conversations.QueryOptions) []string {
	t.Helper()
	result, err := store.Query(t.Context(), options)
	require.NoError(t, err)
	ids := make([]string, 0, len(result.ConversationSummaries))
	for _, summary := range result.ConversationSummaries {
		ids = append(ids, summary.ID)
	}
	return ids
}

func searchEntryIDs(t *testing.T, store *Store, conversationID string) []int64 {
	t.Helper()
	var ids []int64
	require.NoError(t, store.db.SelectContext(t.Context(), &ids,
		`SELECT id FROM conversation_search_entries WHERE conversation_id = ? ORDER BY id`, conversationID))
	return ids
}

func TestSearchMatchExpression(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "search index", want: `"search"* AND "index"*`},
		{input: `"exact phrase" word`, want: `"exact phrase" AND "word"*`},
		{input: `before"inside"after`, want: `"before"* AND "inside" AND "after"*`},
		{input: `unbalanced "quote here`, want: `"unbalanced"* AND "quote"* AND "here"*`},
		{input: "~/workspace/kodelet", want: `"~/workspace/kodelet"*`},
		{input: "NOT OR AND NEAR(x)", want: `"NOT"* AND "OR"* AND "AND"* AND "NEAR(x)"*`},
		{input: "café 日本", want: `"café"* AND "日本"*`},
		{input: `-- ** ~ ""`, want: ""},
		{input: "   ", want: ""},
	} {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, searchMatchExpression(tt.input))
		})
	}
}

func TestQuerySearchesIndexedEntries(t *testing.T) {
	store := newSearchTestStore(t)
	saveSearchConversation(t, store, "conv-a", "/workspace/alpha")
	indexSearchConversation(t, store, "conv-a",
		titleEntry("Alpha design\n/workspace/alpha\nconv-a"),
		textEntry(0, "user", "We should add a search_conversation tool"),
		toolEntry(1, "bash\ncommand: rg fts5"),
	)
	saveSearchConversation(t, store, "conv-b", "/workspace/beta")
	indexSearchConversation(t, store, "conv-b",
		titleEntry("Beta notes\n/workspace/beta\nconv-b"),
		textEntry(0, "assistant", "The search index uses FTS5 with bm25 ranking"),
	)
	saveSearchConversation(t, store, "conv-c", "/workspace/alpha")
	indexSearchConversation(t, store, "conv-c",
		titleEntry("Groceries\n/workspace/alpha\nconv-c"),
		textEntry(0, "user", "Book a Waitrose delivery slot"),
	)
	// Saved but not yet indexed conversations are not matched.
	saveSearchConversation(t, store, "conv-d", "/workspace/alpha")

	assert.ElementsMatch(t, []string{"conv-a", "conv-b"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "search"}))
	assert.ElementsMatch(t, []string{"conv-a", "conv-b"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "SEAR"}),
		"bare words match case-insensitive prefixes")
	assert.Equal(t, []string{"conv-a"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "conversat"}),
		"identifiers are split into searchable words")
	assert.Equal(t, []string{"conv-b"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "search fts5"}),
		"all terms must appear in the same entry")
	assert.Equal(t, []string{"conv-b"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: `"search index"`}))
	assert.Empty(t, searchIDs(t, store, conversations.QueryOptions{SearchTerm: `"index search"`}))
	assert.Equal(t, []string{"conv-c"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "waitrose"}))

	assert.Equal(t, []string{"conv-b"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "~/workspace/beta"}))
	assert.Equal(t, []string{"conv-a"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "conv-a"}))
	assert.Equal(t, []string{"conv-c"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "groceries"}))

	result, err := store.Query(t.Context(), conversations.QueryOptions{
		SearchTerm: "search",
		CWD:        "/workspace/beta",
		Limit:      1,
	})
	require.NoError(t, err)
	require.Len(t, result.ConversationSummaries, 1)
	assert.Equal(t, "conv-b", result.ConversationSummaries[0].ID)
	assert.Equal(t, 1, result.Total)

	result, err = store.Query(t.Context(), conversations.QueryOptions{SearchTerm: "search", Limit: 1})
	require.NoError(t, err)
	assert.Len(t, result.ConversationSummaries, 1)
	assert.Equal(t, 2, result.Total)

	result, err = store.Query(t.Context(), conversations.QueryOptions{SearchTerm: "search", Limit: 1, Offset: 5})
	require.NoError(t, err)
	assert.Empty(t, result.ConversationSummaries)
	assert.Equal(t, 2, result.Total)

	result, err = store.Query(t.Context(), conversations.QueryOptions{SearchTerm: "~ --", SortBy: "relevance"})
	require.NoError(t, err)
	assert.Empty(t, result.ConversationSummaries)
	assert.Zero(t, result.Total)
	assert.Equal(t, []string{"/workspace/alpha", "/workspace/beta"}, result.CWDs)
}

func TestQuerySearchReturnsRankedHighlightedMatches(t *testing.T) {
	store := newSearchTestStore(t)
	saveSearchConversation(t, store, "tool-match", "/workspace")
	indexSearchConversation(t, store, "tool-match",
		titleEntry("Tool match"),
		toolEntry(3, "fts5 ranking notes"),
	)
	saveSearchConversation(t, store, "prose-match", "/workspace")
	indexSearchConversation(t, store, "prose-match",
		titleEntry("Prose match"),
		textEntry(1, "user", "Ranking with fts5 first"),
		textEntry(2, "assistant", "fts5 ranking notes"),
		textEntry(4, "user", "More fts5 ranking detail"),
	)

	result, err := store.Query(t.Context(), conversations.QueryOptions{
		SearchTerm:    "fts5 ranking",
		SearchMatches: 2,
		SortBy:        "relevance",
	})
	require.NoError(t, err)
	require.Len(t, result.ConversationSummaries, 2)

	// Identical prose outranks the demoted tool input.
	prose := result.ConversationSummaries[0]
	assert.Equal(t, "prose-match", prose.ID)
	require.NotNil(t, prose.Search)
	assert.Equal(t, 3, prose.Search.MatchCount)
	require.Len(t, prose.Search.Matches, 2, "matches are capped per conversation")
	assert.Equal(t, conversations.SearchMatch{
		EntryIndex: 2,
		Role:       "assistant",
		Kind:       conversations.SearchEntryKindText,
		Snippet:    "**fts5** **ranking** notes",
	}, prose.Search.Matches[0])

	tool := result.ConversationSummaries[1]
	assert.Equal(t, "tool-match", tool.ID)
	require.NotNil(t, tool.Search)
	assert.Equal(t, 1, tool.Search.MatchCount)
	assert.Equal(t, []conversations.SearchMatch{{
		EntryIndex: 3,
		Role:       "assistant",
		Kind:       conversations.SearchEntryKindToolUse,
		Snippet:    "**fts5** **ranking** notes",
	}}, tool.Search.Matches)

	result, err = store.Query(t.Context(), conversations.QueryOptions{
		SearchTerm: "fts5 ranking",
		SortBy:     "relevance",
		SortOrder:  "asc",
	})
	require.NoError(t, err)
	require.Len(t, result.ConversationSummaries, 2)
	assert.Equal(t, "tool-match", result.ConversationSummaries[0].ID)
	assert.Empty(t, result.ConversationSummaries[0].Search.Matches)
	assert.Equal(t, 1, result.ConversationSummaries[0].Search.MatchCount)

	saveSearchConversation(t, store, "long", "/workspace")
	long := "prefix " + repeatWords("filler", 100) + " needle " + repeatWords("tail", 100)
	indexSearchConversation(t, store, "long", textEntry(0, "user", long))
	result, err = store.Query(t.Context(), conversations.QueryOptions{SearchTerm: "needle", SearchMatches: 1})
	require.NoError(t, err)
	require.Len(t, result.ConversationSummaries, 1)
	snippet := result.ConversationSummaries[0].Search.Matches[0].Snippet
	assert.Contains(t, snippet, "**needle**")
	assert.Contains(t, snippet, "…")
	assert.Less(t, len(snippet), 400)

	result, err = store.Query(t.Context(), conversations.QueryOptions{})
	require.NoError(t, err)
	for _, summary := range result.ConversationSummaries {
		assert.Nil(t, summary.Search)
	}
	_, err = store.Query(t.Context(), conversations.QueryOptions{SortBy: "relevance"})
	assert.ErrorContains(t, err, "requires a search term")
}

func repeatWords(word string, count int) string {
	words := make([]byte, 0, (len(word)+1)*count)
	for index := range count {
		if index > 0 {
			words = append(words, ' ')
		}
		words = append(words, word...)
	}
	return string(words)
}

func TestQuerySearchReadsOneSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "search.db")
	setupTestDB(t, dbPath)
	store, err := NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	// A second store has its own connection, like another daemon or process.
	writer, err := NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, writer.Close()) })

	saveSearchConversation(t, store, "conv", "/workspace")
	indexSearchConversation(t, store, "conv",
		textEntry(0, "user", "original needle question"),
		toolEntry(1, "bash\ncommand: echo needle"),
	)

	// Re-index between ranking and snippets. The replacement reuses the freed
	// row IDs for different text, roles, and kinds.
	replaced := false
	searchMatchesRankedHook = func() {
		replaced = true
		indexSearchConversation(t, writer, "conv",
			toolEntry(5, "bash\ncommand: replacement needle"),
			textEntry(6, "assistant", "replacement needle answer"),
		)
	}
	t.Cleanup(func() { searchMatchesRankedHook = nil })

	result, err := store.Query(t.Context(), conversations.QueryOptions{SearchTerm: "needle", SearchMatches: 2, SortBy: "relevance"})
	require.NoError(t, err)
	require.True(t, replaced)
	require.Len(t, result.ConversationSummaries, 1)
	search := result.ConversationSummaries[0].Search
	require.NotNil(t, search)
	assert.Equal(t, 2, search.MatchCount)
	assert.ElementsMatch(t, []conversations.SearchMatch{
		{EntryIndex: 0, Role: "user", Kind: conversations.SearchEntryKindText, Snippet: "original **needle** question"},
		{EntryIndex: 1, Role: "assistant", Kind: conversations.SearchEntryKindToolUse, Snippet: "bash command: echo **needle**"},
	}, search.Matches, "snippets and labels come from the same snapshot")

	searchMatchesRankedHook = nil
	result, err = store.Query(t.Context(), conversations.QueryOptions{SearchTerm: "replacement", SearchMatches: 1})
	require.NoError(t, err)
	require.Len(t, result.ConversationSummaries, 1)
}

func TestPendingSearchIndexTracksSavesAndVersions(t *testing.T) {
	store := newSearchTestStore(t)
	saveSearchConversation(t, store, "older", "/workspace")
	saveSearchConversation(t, store, "newer", "/workspace")

	pending, err := store.PendingSearchIndex(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, pending, 2)
	assert.Equal(t, "newer", pending[0].ID, "most recently updated first")
	assert.Equal(t, "older", pending[1].ID)

	indexSearchConversation(t, store, "older", textEntry(0, "user", "hello"))
	pending, err = store.PendingSearchIndex(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "newer", pending[0].ID)

	record, err := store.Load(t.Context(), "older")
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), record))
	pending, err = store.PendingSearchIndex(t.Context(), 1)
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	indexSearchConversation(t, store, "older", textEntry(0, "user", "hello"))
	indexSearchConversation(t, store, "newer", textEntry(0, "user", "hello"))
	pending, err = store.PendingSearchIndex(t.Context(), 1)
	require.NoError(t, err)
	assert.Empty(t, pending)
	pending, err = store.PendingSearchIndex(t.Context(), 2)
	require.NoError(t, err)
	assert.Len(t, pending, 2)
}

func TestReplaceSearchIndexGuardsAgainstStaleDocuments(t *testing.T) {
	store := newSearchTestStore(t)
	saveSearchConversation(t, store, "conv", "/workspace")
	stale := searchDocument(t, store, "conv", textEntry(0, "user", "stale words"))

	record, err := store.Load(t.Context(), "conv")
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), record))
	stored, err := store.ReplaceSearchIndex(t.Context(), stale)
	require.NoError(t, err)
	assert.False(t, stored, "a document extracted before the latest save is discarded")
	assert.Empty(t, searchEntryIDs(t, store, "conv"))

	indexSearchConversation(t, store, "conv", textEntry(0, "user", "fresh words"))
	require.Len(t, searchEntryIDs(t, store, "conv"), 1)

	// Unchanged content only advances the recorded source version: a marker
	// written behind the indexer's back survives the second pass.
	_, err = store.db.ExecContext(t.Context(), `UPDATE conversation_search_entries SET payload = 'marker words' WHERE conversation_id = 'conv'`)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), record))
	indexSearchConversation(t, store, "conv", textEntry(0, "user", "fresh words"))
	assert.Equal(t, []string{"conv"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "marker"}))
	pending, err := store.PendingSearchIndex(t.Context(), 1)
	require.NoError(t, err)
	assert.Empty(t, pending)

	require.NoError(t, store.Save(t.Context(), record))
	indexSearchConversation(t, store, "conv", textEntry(0, "user", "replacement words"))
	require.Len(t, searchEntryIDs(t, store, "conv"), 1)
	assert.Empty(t, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "marker"}))
	assert.Equal(t, []string{"conv"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "replacement"}))

	document := searchDocument(t, store, "conv", textEntry(0, "user", "after delete"))
	require.NoError(t, store.Delete(t.Context(), "conv"))
	stored, err = store.ReplaceSearchIndex(t.Context(), document)
	require.NoError(t, err)
	assert.False(t, stored)
	assert.Empty(t, searchEntryIDs(t, store, "conv"))
}

func TestDeleteAndPruneRemoveSearchEntries(t *testing.T) {
	store := newSearchTestStore(t)
	for _, id := range []string{"deleted", "orphaned", "kept"} {
		saveSearchConversation(t, store, id, "/workspace")
		indexSearchConversation(t, store, id, textEntry(0, "user", "shared keyword"))
	}

	require.NoError(t, store.Delete(t.Context(), "deleted"))
	assert.Empty(t, searchEntryIDs(t, store, "deleted"))

	// Simulate a deletion by another process that bypassed Store.Delete.
	_, err := store.db.ExecContext(t.Context(), `DELETE FROM conversation_summaries WHERE id = 'orphaned'`)
	require.NoError(t, err)
	pruned, err := store.PruneSearchIndex(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, pruned)
	assert.Empty(t, searchEntryIDs(t, store, "orphaned"))
	assert.Len(t, searchEntryIDs(t, store, "kept"), 1)

	var states int
	require.NoError(t, store.db.GetContext(t.Context(), &states, `SELECT COUNT(*) FROM conversation_search_state`))
	assert.Equal(t, 1, states)
	assert.Equal(t, []string{"kept"}, searchIDs(t, store, conversations.QueryOptions{SearchTerm: "keyword"}))
}
