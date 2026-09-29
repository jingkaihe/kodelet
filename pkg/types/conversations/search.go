package conversations

import "time"

const (
	SearchEntryKindTitle      = "title"
	SearchEntryKindText       = "text"
	SearchEntryKindToolUse    = "tool-use"
	SearchEntryKindCompaction = "compaction"
)

const MaxSearchMatches = 10

type SearchEntry struct {
	// EntryIndex is the entry's position in the full display transcript; the title uses -1.
	EntryIndex int
	Role       string
	Kind       string
	Payload    string
}

// SearchDocument replaces every search entry of one conversation.
type SearchDocument struct {
	ConversationID string
	// SourceUpdatedAt is the conversation's updated_at value the entries were extracted from.
	SourceUpdatedAt time.Time
	// Version identifies the extraction rules; a different version forces a rebuild.
	Version     int
	ContentHash string
	Entries     []SearchEntry
}

// SearchIndexCandidate identifies a conversation whose search entries are missing or stale.
type SearchIndexCandidate struct {
	ID        string    `db:"id"`
	UpdatedAt time.Time `db:"updated_at"`
}

// SearchMatch snippets wrap matched terms in "**".
type SearchMatch struct {
	EntryIndex int    `json:"entryIndex"`
	Role       string `json:"role,omitempty"`
	Kind       string `json:"kind"`
	Snippet    string `json:"snippet"`
}

type ConversationSearchResult struct {
	MatchCount int           `json:"matchCount"`
	Matches    []SearchMatch `json:"matches,omitempty"`
}
