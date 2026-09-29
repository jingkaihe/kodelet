package searchindex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

const anthropicTranscript = `[
	{"role": "user", "content": [{"type": "text", "text": "Please list the workspace files"}]},
	{"role": "assistant", "content": [
		{"type": "thinking", "thinking": "private reasoning words", "signature": "sig"},
		{"type": "text", "text": "I will run a listing."},
		{"type": "tool_use", "id": "toolu_1", "name": "bash", "input": {"command": "ls -la", "description": "List files", "timeout": 10}}
	]},
	{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": [{"type": "text", "text": "secret tool output"}]}]},
	{"role": "assistant", "content": [{"type": "text", "text": "The workspace has one file."}]}
]`

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	database, err := db.Open(t.Context(), dbPath)
	require.NoError(t, err)
	require.NoError(t, db.NewMigrationRunner(database).Run(t.Context(), migrations.All()))
	require.NoError(t, database.Close())
	store, err := sqlite.NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func saveTranscript(t *testing.T, store *sqlite.Store, id, raw string) {
	t.Helper()
	record := convtypes.NewConversationRecord(id)
	record.Provider = "anthropic"
	record.CWD = "/workspace/project"
	record.RawMessages = json.RawMessage(raw)
	require.NoError(t, store.Save(t.Context(), record))
}

func searchIDs(t *testing.T, store *sqlite.Store, term string) []string {
	t.Helper()
	result, err := store.Query(t.Context(), convtypes.QueryOptions{SearchTerm: term})
	require.NoError(t, err)
	ids := make([]string, 0, len(result.ConversationSummaries))
	for _, summary := range result.ConversationSummaries {
		ids = append(ids, summary.ID)
	}
	return ids
}

func TestBuildDocumentProjectsSearchableEntries(t *testing.T) {
	record := convtypes.NewConversationRecord("conv-1")
	record.Provider = "anthropic"
	record.CWD = "/workspace/project"
	record.Summary = "Listing files"
	record.Metadata = conversations.SetConversationName(nil, "Workspace audit")
	record.RawMessages = json.RawMessage(anthropicTranscript)
	record.CompactionHistory = &convtypes.CompactionHistory{Segments: []convtypes.CompactedSegment{{
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Archived question"}]}]`),
		Marker:      llmtypes.CompactionMarker{ID: "marker", Method: "summary", Summary: "Earlier we fixed the parser."},
	}}}

	document, err := BuildDocument(record)
	require.NoError(t, err)
	assert.Equal(t, "conv-1", document.ConversationID)
	assert.Equal(t, record.UpdatedAt, document.SourceUpdatedAt)
	assert.Equal(t, Version, document.Version)
	assert.Equal(t, []convtypes.SearchEntry{
		{EntryIndex: -1, Kind: "title", Payload: "Workspace audit\nListing files\n/workspace/project\nconv-1"},
		{EntryIndex: 0, Role: "user", Kind: "text", Payload: "Archived question"},
		{EntryIndex: 1, Role: "assistant", Kind: "compaction", Payload: "Earlier we fixed the parser."},
		{EntryIndex: 2, Role: "user", Kind: "text", Payload: "Please list the workspace files"},
		{EntryIndex: 4, Role: "assistant", Kind: "text", Payload: "I will run a listing."},
		{EntryIndex: 5, Role: "assistant", Kind: "tool-use", Payload: "bash\ncommand: ls -la\ndescription: List files\ntimeout: 10"},
		{EntryIndex: 7, Role: "assistant", Kind: "text", Payload: "The workspace has one file."},
	}, document.Entries, "thinking and tool results are excluded")

	again, err := BuildDocument(record)
	require.NoError(t, err)
	assert.Equal(t, document.ContentHash, again.ContentHash)
	record.Summary = "Renamed"
	renamed, err := BuildDocument(record)
	require.NoError(t, err)
	assert.NotEqual(t, document.ContentHash, renamed.ContentHash)
}

func TestBuildDocumentKeepsTitleForUnreadableTranscript(t *testing.T) {
	record := convtypes.NewConversationRecord("broken")
	record.Provider = "anthropic"
	record.RawMessages = json.RawMessage(`{"not": "a transcript"}`)

	document, err := BuildDocument(record)
	require.Error(t, err)
	assert.Equal(t, []convtypes.SearchEntry{{EntryIndex: -1, Kind: "title", Payload: "broken"}}, document.Entries)
	assert.NotEmpty(t, document.ContentHash)
}

func TestFlattenToolInputAndTruncation(t *testing.T) {
	assert.Equal(t, "edits.new: b\nedits.old: a\nedits.new: d\nedits.old: c\npath: main.go",
		flattenToolInput(`{"path": "main.go", "edits": [{"old": "a", "new": "b"}, {"old": "c", "new": "d"}], "unused": null}`))
	assert.Equal(t, "<b> & \"quoted\"", flattenToolInput(`"\u003cb\u003e \u0026 \"quoted\""`))
	assert.Equal(t, "not json {", flattenToolInput(" not json { "))

	payload := strings.Repeat("界", maxToolInputPayloadBytes)
	entry, ok := searchEntry(0, conversations.StreamableMessage{Kind: "tool-use", Role: "assistant", ToolName: "file_write", Input: `{"content":"` + payload + `"}`})
	require.True(t, ok)
	assert.LessOrEqual(t, len(entry.Payload), maxToolInputPayloadBytes)
	assert.True(t, strings.HasPrefix(entry.Payload, "file_write\ncontent: 界"))
	assert.True(t, utf8.ValidString(entry.Payload), "truncation keeps valid UTF-8")

	_, ok = searchEntry(0, conversations.StreamableMessage{Kind: "text", Role: "user", Content: "   "})
	assert.False(t, ok)
}

func TestIndexerRefreshIndexesChangedConversations(t *testing.T) {
	store := newTestStore(t)
	saveTranscript(t, store, "first", anthropicTranscript)
	saveTranscript(t, store, "broken", `{"not": "a transcript"}`)
	indexer := New(store)

	pending, err := indexer.Refresh(t.Context())
	require.NoError(t, err)
	assert.Zero(t, pending)
	assert.Equal(t, []string{"first"}, searchIDs(t, store, "listing"))
	assert.Equal(t, []string{"first"}, searchIDs(t, store, `"ls -la"`), "tool inputs are searchable")
	assert.Empty(t, searchIDs(t, store, "secret"), "tool output is not indexed")
	assert.Empty(t, searchIDs(t, store, "private"), "thinking is not indexed")
	assert.Equal(t, []string{"broken"}, searchIDs(t, store, "broken"), "unreadable transcripts keep their title")

	candidates, err := store.PendingSearchIndex(t.Context(), Version)
	require.NoError(t, err)
	assert.Empty(t, candidates, "unreadable transcripts are not retried until they change")

	record, err := store.Load(t.Context(), "first")
	require.NoError(t, err)
	record.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Replace with a haiku"}]}]`)
	require.NoError(t, store.Save(t.Context(), record))
	pending, err = indexer.Refresh(t.Context())
	require.NoError(t, err)
	assert.Zero(t, pending)
	assert.Equal(t, []string{"first"}, searchIDs(t, store, "haiku"))
	assert.Empty(t, searchIDs(t, store, "listing"))
}

func TestIndexerRefreshHonoursBudget(t *testing.T) {
	store := newTestStore(t)
	saveTranscript(t, store, "one", anthropicTranscript)
	saveTranscript(t, store, "two", anthropicTranscript)

	pending, err := New(store, WithRefreshBudget(0)).Refresh(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, pending)
	assert.Empty(t, searchIDs(t, store, "listing"))

	pending, err = New(store).Refresh(t.Context())
	require.NoError(t, err)
	assert.Zero(t, pending)
	assert.Len(t, searchIDs(t, store, "listing"), 2)
}

func TestIndexerRefreshStopsWaitingForBackgroundIndexing(t *testing.T) {
	store := newTestStore(t)
	saveTranscript(t, store, "one", anthropicTranscript)
	saveTranscript(t, store, "two", anthropicTranscript)
	indexer := New(store, WithRefreshBudget(50*time.Millisecond))

	// Simulate the background loop indexing a slow conversation.
	indexer.lock <- struct{}{}
	start := time.Now()
	pending, err := indexer.Refresh(t.Context())
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Equal(t, 2, pending, "nothing is indexed while the lock is busy")
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	assert.Less(t, elapsed, time.Second, "the refresh budget bounds the wait")

	// Cancellation also ends an unbounded wait, as used by the background loop.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := indexer.sync(ctx, time.Time{})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled sync kept waiting for the lock")
	}

	<-indexer.lock
	pending, err = indexer.Refresh(t.Context())
	require.NoError(t, err)
	assert.Zero(t, pending)
	assert.Empty(t, indexer.lock)
}

func TestIndexerAcquireRejectsExpiredDeadline(t *testing.T) {
	indexer := New(newTestStore(t))
	assert.False(t, indexer.acquire(t.Context(), time.Now().Add(-time.Millisecond)))
	assert.Empty(t, indexer.lock)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.False(t, indexer.acquire(ctx, time.Time{}))
	assert.Empty(t, indexer.lock, "a lock won after cancellation is released")
	assert.True(t, indexer.acquire(t.Context(), time.Time{}))
	assert.Len(t, indexer.lock, 1)
}

// changingStore saves the conversation again after it is loaded, as a
// concurrent turn would, so the extracted document is already stale.
type changingStore struct {
	*sqlite.Store
	changes atomic.Int32
}

func (s *changingStore) LoadSearchSource(ctx context.Context, id string) (convtypes.ConversationRecord, error) {
	record, err := s.Store.LoadSearchSource(ctx, id)
	if err == nil && s.changes.Add(-1) >= 0 {
		latest, loadErr := s.Load(ctx, id)
		if loadErr != nil {
			return record, loadErr
		}
		latest.RawMessages = json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Newer message"}]}]`)
		if saveErr := s.Save(ctx, latest); saveErr != nil {
			return record, saveErr
		}
	}
	return record, err
}

func TestIndexerDiscardsDocumentsChangedDuringExtraction(t *testing.T) {
	store := &changingStore{Store: newTestStore(t)}
	store.changes.Store(1)
	saveTranscript(t, store.Store, "busy", anthropicTranscript)
	indexer := New(store)

	pending, err := indexer.Refresh(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, pending, "the stale document stays pending")
	assert.Empty(t, searchIDs(t, store.Store, "listing"))

	pending, err = indexer.Refresh(t.Context())
	require.NoError(t, err)
	assert.Zero(t, pending)
	assert.Equal(t, []string{"busy"}, searchIDs(t, store.Store, "newer"))
	assert.Empty(t, searchIDs(t, store.Store, "listing"))
}

func TestIndexerRunBackfillsAndSweeps(t *testing.T) {
	store := newTestStore(t)
	saveTranscript(t, store, "existing", anthropicTranscript)
	indexer := New(store, WithSweepInterval(10*time.Millisecond))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		indexer.Run(ctx)
	}()

	require.Eventually(t, func() bool { return len(searchIDs(t, store, "listing")) == 1 }, 5*time.Second, 10*time.Millisecond)
	saveTranscript(t, store, "later", `[{"role": "user", "content": [{"type": "text", "text": "Sweep me"}]}]`)
	require.Eventually(t, func() bool { return len(searchIDs(t, store, "sweep")) == 1 }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, store.Delete(t.Context(), "later"))
	assert.Empty(t, searchIDs(t, store, "sweep"))

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}
