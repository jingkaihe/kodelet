package session

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
)

// SessionStorage defines the interface for ACP session update storage
type SessionStorage interface {
	AppendUpdate(sessionID acptypes.SessionID, update any) error
	ReadUpdates(sessionID acptypes.SessionID) ([]StoredUpdate, error)
	Flush(sessionID acptypes.SessionID) error
	Delete(sessionID acptypes.SessionID) error
	CloseSession(sessionID acptypes.SessionID) error
	Exists(sessionID acptypes.SessionID) bool
	Close() error
}

// GetDefaultStorage returns the default storage implementation (SQLite)
func GetDefaultStorage(ctx context.Context) (SessionStorage, error) {
	return NewStorage(ctx)
}
