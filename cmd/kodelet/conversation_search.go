package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
)

const conversationSearchTitleLimit = 100

// ConversationSearchOutput is the result of `kodelet conversation search`.
type ConversationSearchOutput struct {
	Conversations []ConversationSearchResultOutput `json:"conversations"`
	Total         int                              `json:"total"`
	SearchPending int                              `json:"search_pending,omitempty"`
}

// ConversationSearchResultOutput describes one matching conversation.
type ConversationSearchResultOutput struct {
	ID           string                          `json:"id"`
	Title        string                          `json:"title"`
	CWD          string                          `json:"cwd,omitempty"`
	CreatedAt    time.Time                       `json:"created_at"`
	UpdatedAt    time.Time                       `json:"updated_at"`
	MessageCount int                             `json:"message_count"`
	MatchCount   int                             `json:"match_count"`
	Matches      []ConversationSearchMatchOutput `json:"matches"`
}

// ConversationSearchMatchOutput is a highlighted excerpt; matched terms are wrapped in "**".
type ConversationSearchMatchOutput struct {
	EntryIndex int    `json:"entry_index"`
	Role       string `json:"role,omitempty"`
	Kind       string `json:"kind"`
	Snippet    string `json:"snippet"`
}

// NewConversationSearchOutput converts a daemon search response for display.
func NewConversationSearchOutput(response conversations.ListConversationsResponse) ConversationSearchOutput {
	output := ConversationSearchOutput{
		Conversations: make([]ConversationSearchResultOutput, 0, len(response.Conversations)),
		Total:         response.Total,
		SearchPending: response.SearchPending,
	}
	for _, summary := range response.Conversations {
		result := ConversationSearchResultOutput{
			ID:           summary.ID,
			Title:        conversationSearchTitle(summary),
			CWD:          summary.CWD,
			CreatedAt:    summary.CreatedAt,
			UpdatedAt:    summary.UpdatedAt,
			MessageCount: summary.MessageCount,
			Matches:      []ConversationSearchMatchOutput{},
		}
		if summary.Search != nil {
			result.MatchCount = summary.Search.MatchCount
			for _, match := range summary.Search.Matches {
				result.Matches = append(result.Matches, ConversationSearchMatchOutput(match))
			}
		}
		output.Conversations = append(output.Conversations, result)
	}
	return output
}

func conversationSearchTitle(summary convtypes.ConversationSummary) string {
	fallback := summary.Summary
	if strings.TrimSpace(fallback) == "" {
		fallback = summary.FirstMessage
	}
	title := []rune(conversations.ResolveConversationName(summary.Metadata, fallback))
	if len(title) > conversationSearchTitleLimit {
		return strings.TrimSpace(string(title[:conversationSearchTitleLimit-1])) + "…"
	}
	return string(title)
}

// Render writes the results as JSON or readable text.
func (o ConversationSearchOutput) Render(w io.Writer, format OutputFormat) error {
	if format == JSONFormat {
		data, err := json.MarshalIndent(o, "", "  ")
		if err != nil {
			return errors.Wrap(err, "error generating JSON output")
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}

	var output strings.Builder
	if len(o.Conversations) == 0 {
		output.WriteString("No conversations match the search.\n")
	}
	for index, result := range o.Conversations {
		if index > 0 {
			output.WriteString("\n")
		}
		fmt.Fprintf(&output, "%s  %s", result.ID, result.UpdatedAt.Format(time.RFC3339))
		if result.CWD != "" {
			fmt.Fprintf(&output, "  %s", result.CWD)
		}
		output.WriteString("\n")
		if result.Title != "" {
			fmt.Fprintf(&output, "  %s\n", result.Title)
		}
		for _, match := range result.Matches {
			fmt.Fprintf(&output, "  - %s: %s\n", conversationSearchMatchLabel(match), match.Snippet)
		}
		if hidden := result.MatchCount - len(result.Matches); hidden > 0 && len(result.Matches) > 0 {
			fmt.Fprintf(&output, "  (%d more %s)\n", hidden, pluralize(hidden, "match", "matches"))
		}
	}
	if shown := len(o.Conversations); shown > 0 && o.Total > shown {
		fmt.Fprintf(&output, "\nShowing %d of %d matching conversations.\n", shown, o.Total)
	}
	if o.SearchPending > 0 {
		fmt.Fprintf(&output, "\nThe search index is still catching up (%d %s pending); results may be incomplete.\n",
			o.SearchPending, pluralize(o.SearchPending, "conversation", "conversations"))
	}
	_, err := io.WriteString(w, output.String())
	return err
}

func conversationSearchMatchLabel(match ConversationSearchMatchOutput) string {
	switch match.Kind {
	case convtypes.SearchEntryKindTitle:
		return "title"
	case convtypes.SearchEntryKindToolUse:
		return "tool input"
	case convtypes.SearchEntryKindCompaction:
		return "compaction summary"
	}
	if match.Role != "" {
		return match.Role
	}
	return match.Kind
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
