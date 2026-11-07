package conversations

import (
	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db"
)

// NewConversationStore returns a conversation store backed by database. The
// caller owns database; closing the store does not close it.
func NewConversationStore(database *db.DB) ConversationStore {
	return sqlite.NewStore(database)
}
