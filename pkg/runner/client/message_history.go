package client

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

// Bound the encoded result, not raw text: JSON escaping can expand messages.
// Two MiB leaves room for the envelope within the default four-MiB RPC frame.
const workspaceMessageHistoryLimit = 2 * 1024 * 1024

func (s *Service) workspaceMessageHistory(ctx context.Context, params protocol.WorkspaceMessageHistoryParams) (protocol.WorkspaceMessageHistoryResult, error) {
	var result protocol.WorkspaceMessageHistoryResult
	if s == nil {
		return result, errors.New("runner service is required")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return result, errors.New("runner service is closed")
	}
	cwd, err := s.instanceProvider.ResolveWorkingDirectory(ctx, params.CWD)
	if err != nil {
		return result, err
	}
	scope, err := messagehistory.ResolveScopeCWD(cwd)
	if err != nil {
		return result, errors.Wrap(err, "failed to resolve runner message history scope")
	}
	result.CWD, result.ScopeCWD = cwd, scope
	store, err := messagehistory.NewStore()
	if err != nil {
		return result, errors.Wrap(err, "failed to open runner message history")
	}
	if params.Entry != nil {
		entry := *params.Entry
		entry.ScopeCWD, entry.Source = scope, "tui"
		return result, store.Append(ctx, entry)
	}
	entries, err := store.List(ctx, scope, messagehistory.MaxEntriesPerScope)
	if err != nil {
		return result, err
	}
	// These values contain only strings, so JSON encoding cannot fail. Include
	// paths and array syntax in the budget, then prefer newer whole messages.
	encoded, _ := json.Marshal(result)
	remaining := workspaceMessageHistoryLimit - len(encoded) - len(`,"messages":[]`)
	for i := len(entries) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		encoded, _ := json.Marshal(entries[i].Text)
		if size := len(encoded) + 1; size <= remaining {
			result.Messages = append(result.Messages, entries[i].Text)
			remaining -= size
		}
	}
	slices.Reverse(result.Messages)
	return result, nil
}
